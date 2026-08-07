package doctor

import (
	"fmt"
	"go/ast"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/arandu-io/aru/internal/manifest"
)

// rules is the whole check surface. Each one exists because something real can
// go wrong that the compiler cannot see -- and each message says what breaks,
// not which rule was violated.
//
// Adding a rule that rejects existing code is a breaking change (doc 23): it
// enters as a warning in a minor and becomes an error in the next major.
//
// module-imports-module is gone with the tree it policed: ADR 0019 removed
// modules/ as a structure of the framework, and the boundary that mattered --
// the controller reaching data directly -- is covered by the two rules named
// after it below.
var rules = []func(*project) []Finding{
	unreadableFiles,
	repositoryNeedsPolicy,
	repositoryMethodNeedsGrant,
	policyMustBeOpened,
	controllerMustNotReachData,
	controllerMustNotReachTheRepository,
	tenantMustComeFromTheGrant,
	systemGrantIsAudited,
	noBuiltSQL,
	sensitiveFieldNeedsRedaction,
	sessionMustRotateOnLogin,
	viewDataMustBeAStruct,
	viewMustExist,
	declaredPermissionsMatchTheCode,
	alpineHoldsClientStateOnly,
}

// 0. A file doctor could not read makes every other rule unreliable.
//
// The parser used to skip an unparsable file silently, on the reasoning that the
// compiler reports a syntax error better -- which is true, and beside the point.
// Every rule here reasons over the whole file set, so a project whose
// InvoicePolicy.go does not parse looks exactly like a project with no policy for
// Invoice, and doctor reported that: an invented finding, pointing at the wrong
// file, telling the author to write a policy they had already written. Found by
// audit.
//
// Reporting it first and as an error is the honest answer: what follows is
// incomplete, and here is why.
func unreadableFiles(p *project) []Finding {
	var out []Finding
	for _, u := range p.unreadable {
		out = append(out, Finding{
			Rule: "file-does-not-parse", Severity: Error,
			File: u.rel, Line: u.line,
			Message: "this file does not parse: " + u.reason,
			Why:     "doctor reads the whole project to answer questions about it, so a file it cannot read makes the rest of this report incomplete -- and can make it wrong: an entity whose policy does not parse looks like an entity with no policy. Fix the syntax and run again.",
		})
	}
	return out
}

// entityPlace is where an entity was found, for a finding that names it.
type entityPlace struct {
	rel  string
	line int
}

// repositoriesAndPolicies collects, per entity, where its repository is and
// whether it has a policy.
//
// Both the path and the type name are read. The path is the convention --
// app/Repositories/InvoiceRepository.go -- and it keeps working on a file that
// does not parse. The type name is the truth: an InvoicePolicy declared inside a
// file called Policies.go is still the policy of Invoice, and a rule that only
// looked at file names would demand a second one.
func repositoriesAndPolicies(p *project) (map[string]entityPlace, map[string]bool) {
	repositories := map[string]entityPlace{}
	policies := map[string]bool{}

	record := func(entity string, at entityPlace) {
		if entity == "" {
			return
		}
		if _, seen := repositories[entity]; !seen {
			repositories[entity] = at
		}
	}

	for _, f := range p.files {
		if f.isTest {
			continue
		}
		switch f.category {
		case "Repositories":
			record(f.entity, entityPlace{f.rel, 1})
		case "Policies":
			if f.entity != "" {
				policies[f.entity] = true
			}
		}

		f.types(func(ts *ast.TypeSpec) {
			name := ts.Name.Name
			switch {
			case strings.HasSuffix(name, "Repository"):
				record(strings.TrimSuffix(name, "Repository"), entityPlace{f.rel, f.line(ts)})
			case strings.HasSuffix(name, "Policy"):
				if entity := strings.TrimSuffix(name, "Policy"); entity != "" {
					policies[entity] = true
				}
			}
		})
	}
	return repositories, policies
}

// 1. A repository without a policy for the same entity means the entity is
// reachable and nobody decided who may reach it.
//
// app/Policies/ is not a convention of an organized team here, the way it is in
// Laravel: it is skeleton, and this is the rule that makes it so (ADR 0019).
func repositoryNeedsPolicy(p *project) []Finding {
	repositories, policies := repositoriesAndPolicies(p)

	// One finding per entity, and every entity -- not the first one and then
	// stop. It used to break out of the loop after the first, so a project with
	// three unprotected entities reported one, and the author found the next only
	// after fixing that one and running again. Found by audit; the spec
	// validator's own doc comment says why that is the wrong shape.
	entities := make([]string, 0, len(repositories))
	for entity := range repositories {
		entities = append(entities, entity)
	}
	sort.Strings(entities)

	var out []Finding
	for _, entity := range entities {
		if policies[entity] {
			continue
		}
		// This rule concludes from an absence, so it has to know whether the
		// absence is real. See project.blind.
		if p.blind(entity) {
			continue
		}
		at := repositories[entity]
		out = append(out, Finding{
			Rule: "repository-without-policy", Severity: Error,
			File: at.rel, Line: at.line,
			Message: entity + " has a repository and no policy",
			Why:     "the entity is reachable and nobody decided who may reach it. Run `aru make:policy` or write app/Policies/" + entity + "Policy.go.",
		})
	}
	return out
}

// isRepository reports whether a method belongs to a repository.
//
// The directory answers for a project that follows the tree, and the receiver
// name answers for the one that does not -- a type called InvoiceRepository is a
// repository wherever somebody filed it.
func isRepository(f *file, fn *ast.FuncDecl) bool {
	return f.category == "Repositories" || strings.Contains(receiverType(fn), "Repo")
}

// 2. Every repository method must call g.Check before touching the handle. The
// signature forces the Grant to be passed; only this checks it was verified.
//
// It applies to List and Find exactly as it applies to Create: a read path with
// no policy is a tenant leak with a technical name (RULE 17).
func repositoryMethodNeedsGrant(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if f.isTest {
			continue
		}
		for _, decl := range f.ast.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil {
				continue
			}
			if !isRepository(f, fn) || !ast.IsExported(fn.Name.Name) {
				continue
			}
			if !takesGrant(fn) {
				continue
			}
			if funcBodyContains(fn, func(n string) bool { return strings.HasSuffix(n, ".Check") }) {
				continue
			}
			file, line := f.at(fn)
			out = append(out, Finding{
				Rule: "grant-not-checked", Severity: Error,
				File: file, Line: line,
				Message: fn.Name.Name + " receives a Grant and never checks it",
				Why:     "a Grant issued for another action would pass. Start the method with: if err := g.Check(Action...); err != nil { return err }",
			})
		}
	}
	return out
}

func takesGrant(fn *ast.FuncDecl) bool {
	if fn.Type.Params == nil {
		return false
	}
	for _, p := range fn.Type.Params.List {
		if sel, ok := p.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "Grant" {
			return true
		}
	}
	return false
}

// 3. A generated policy denies everything. Left that way, the entity is
// unreachable -- and worse, it looks finished.
func policyMustBeOpened(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if f.isTest {
			continue
		}
		for _, decl := range f.ast.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "Can" {
				continue
			}
			receiver := receiverType(fn)
			if f.category != "Policies" && !strings.Contains(receiver, "Policy") {
				continue
			}
			if returnsNil(fn) {
				continue
			}

			entity := strings.TrimSuffix(receiver, "Policy")
			if entity == "" {
				entity = f.entity
			}
			if entity == "" {
				entity = "this entity"
			}
			file, line := f.at(fn)
			out = append(out, Finding{
				Rule: "policy-never-opened", Severity: Warning,
				File: file, Line: line,
				Message: "the policy of " + entity + " denies every action",
				Why:     "this is how `aru make:policy` generates it, on purpose. Open what the application needs inside the custom block -- until then every request that reaches this entity is refused.",
			})
		}
	}
	return out
}

func returnsNil(fn *ast.FuncDecl) bool {
	if fn.Body == nil {
		return false
	}
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 {
			return true
		}
		if id, ok := ret.Results[0].(*ast.Ident); ok && id.Name == "nil" {
			found = true
			return false
		}
		return true
	})
	return found
}

// 4. A controller that reaches the data package is a controller that skipped the
// service, and therefore the policy.
func controllerMustNotReachData(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if f.isTest || f.category != "Controllers" {
			continue
		}
		for path := range f.imports {
			if !strings.HasSuffix(path, "/framework/data") {
				continue
			}
			// data.Query is the pagination type and belongs in a controller; the
			// rest of the package does not.
			onlyQuery := true
			ast.Inspect(f.ast, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if x, ok := sel.X.(*ast.Ident); ok && x.Name == "data" && sel.Sel.Name != "Query" {
					onlyQuery = false
					return false
				}
				return true
			})
			if onlyQuery {
				continue
			}
			out = append(out, Finding{
				Rule: "handler-reaches-data", Severity: Error,
				File: f.rel, Line: 1,
				Message: "this controller uses the data package beyond data.Query",
				Why:     "a controller that reaches the database skipped the service, and therefore the policy. Move the call into app/Services, where the Grant is issued.",
			})
			break
		}
	}
	return out
}

// 5. A controller that imports app/Repositories skips the service the same way,
// and the compiler cannot see it: the repository method it calls does require a
// Grant, and a controller can produce one with SystemGrant.
//
// This is the boundary ADR 0019 calls the other 20%: in Laravel, Service and
// Repository are a convention of an organized team; here they are skeleton, and
// the direction of the arrow is checked.
func controllerMustNotReachTheRepository(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if f.isTest || f.category != "Controllers" {
			continue
		}
		for path := range f.imports {
			if !strings.Contains(strings.ToLower(path), "/app/repositories") {
				continue
			}
			out = append(out, Finding{
				Rule: "controller-reaches-repository", Severity: Error,
				File: f.rel, Line: 1,
				Message: "this controller imports " + path,
				Why:     "the Grant a repository requires would have to be issued here, which is the controller authorizing itself. Call app/Services instead: it validates, calls security.Authorize, and only then reaches the repository.",
			})
			break
		}
	}
	return out
}

// 6. A tenant taken from the request is the client choosing which data to read.
func tenantMustComeFromTheGrant(p *project) []Finding {
	files := p.files
	var out []Finding
	for _, f := range files {
		if f.isTest {
			continue
		}
		f.calls(func(call *ast.CallExpr, name string) {
			switch name {
			case "r.PathValue", "r.FormValue", "r.PostFormValue", "req.PathValue", "req.FormValue",
				"ctx.Param", "ctx.Query", "ctx.Input", "c.Param", "c.Query", "c.Input":
			default:
				return
			}
			if len(call.Args) == 0 {
				return
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || !strings.Contains(strings.ToLower(lit.Value), "tenant") {
				return
			}
			file, line := f.at(call)
			out = append(out, Finding{
				Rule: "tenant-from-request", Severity: Error,
				File: file, Line: line,
				Message: "the tenant is being read from the request",
				Why:     "a tenant the client controls is the client choosing whose data to read. It comes from data.Tenant(g), and the Grant comes from the session.",
			})
		})

		// The header form, which is the one that looks harmless.
		f.calls(func(call *ast.CallExpr, name string) {
			if !strings.HasSuffix(name, "Header.Get") && name != "r.Header.Get" {
				return
			}
			if len(call.Args) == 0 {
				return
			}
			if lit, ok := call.Args[0].(*ast.BasicLit); ok && strings.Contains(strings.ToLower(lit.Value), "tenant") {
				file, line := f.at(call)
				out = append(out, Finding{
					Rule: "tenant-from-header", Severity: Error,
					File: file, Line: line,
					Message: "the tenant is being read from a header",
					Why:     "any client can send that header. Resolve the tenant from the host name or from a constant, and let it reach SQL only through the Grant.",
				})
			}
		})

		out = append(out, requestValuesReachingAGrant(f)...)
	}
	return out
}

// requestValuesReachingAGrant follows the value instead of reading the name.
//
// The two checks above ask whether a header or a parameter is CALLED something
// with "tenant" in it, and that is the wrong question:
//
//	org := ctx.Query("org")
//	g := security.SystemGrant(ActionView, org)
//
// passes both of them and is exactly the hole RULE 14 exists to close. The name
// of a header is chosen by whoever wrote the client; what makes it a tenant is
// where the value ends up. Found by audit.
//
// The analysis is deliberately small: within one function, a variable assigned
// from something the request controls is tainted, and a tainted variable
// reaching the tenant argument of SystemGrant is the finding. It does not follow
// values across functions, and it says so -- a doctor that claims more coverage
// than it has is worse than one that claims less.
func requestValuesReachingAGrant(f *file) []Finding {
	var out []Finding

	ast.Inspect(f.ast, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}

		// Pass one: every local name assigned from a request-controlled read.
		tainted := map[string]*ast.CallExpr{}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			var lhs []ast.Expr
			var rhs []ast.Expr
			switch a := n.(type) {
			case *ast.AssignStmt:
				lhs, rhs = a.Lhs, a.Rhs
			default:
				return true
			}
			if len(lhs) != len(rhs) {
				return true
			}
			for i, r := range rhs {
				call, ok := r.(*ast.CallExpr)
				if !ok || !readsTheRequest(callName(call)) {
					continue
				}
				if id, ok := lhs[i].(*ast.Ident); ok && id.Name != "_" {
					tainted[id.Name] = call
				}
			}
			return true
		})
		if len(tainted) == 0 {
			return true
		}

		// Pass two: a tainted name in the tenant argument of SystemGrant.
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || callName(call) != "security.SystemGrant" || len(call.Args) < 2 {
				return true
			}
			id, ok := call.Args[1].(*ast.Ident)
			if !ok {
				return true
			}
			source, isTainted := tainted[id.Name]
			if !isTainted {
				return true
			}
			file, line := f.at(call)
			_, sourceLine := f.at(source)
			out = append(out, Finding{
				Rule: "tenant-from-request", Severity: Error,
				File: file, Line: line,
				Message: fmt.Sprintf("the tenant of this Grant is %s, which line %d read from the request", id.Name, sourceLine),
				Why:     "the name of a header or a parameter is chosen by whoever wrote the client, so it proves nothing. What makes a value a tenant is that it scopes SQL -- and a client that picks its own scope reads whichever customer it names. Resolve the tenant from the session, the host name or a constant.",
			})
			return true
		})
		return true
	})
	return out
}

// readsTheRequest reports whether a call returns something the client controls.
func readsTheRequest(name string) bool {
	switch {
	case strings.HasSuffix(name, "Header.Get"),
		strings.HasSuffix(name, "Query.Get"),
		strings.HasSuffix(name, ".PathValue"),
		strings.HasSuffix(name, ".FormValue"),
		strings.HasSuffix(name, ".PostFormValue"),
		strings.HasSuffix(name, ".Cookie"),
		// The httpx.Context accessors: the same values, one layer up.
		strings.HasSuffix(name, "ctx.Param"),
		strings.HasSuffix(name, "ctx.Query"),
		strings.HasSuffix(name, "ctx.Input"):
		return true
	}
	return false
}

// 7. SystemGrant is the one way past the policy. Its call sites are the audit.
func systemGrantIsAudited(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if f.isTest {
			continue
		}
		// Seeders, jobs and commands are where it legitimately belongs, and so is
		// a method whose name says it is seeding -- EnsureAdmin, SeedDemo. The
		// file path alone would flag every entity that offers a seeding entry
		// point, and a warning that fires on correct code gets muted.
		lower := strings.ToLower(f.rel)
		legitimateFile := strings.Contains(lower, "seeder") ||
			strings.Contains(lower, "/jobs/") ||
			strings.Contains(lower, "/commands/") ||
			strings.Contains(lower, "/console/") ||
			strings.Contains(lower, "routes/console.go") ||
			strings.Contains(lower, "cmd/") ||
			lower == "main.go"

		enclosing := enclosingFuncs(f)

		f.calls(func(call *ast.CallExpr, name string) {
			if name != "security.SystemGrant" {
				return
			}
			file, line := f.at(call)
			legitimate := legitimateFile || seedingName(enclosing[line])

			// An empty tenant is refused by the framework at runtime; catching it
			// here says so before anyone waits for the failure.
			if len(call.Args) >= 2 {
				if lit, ok := call.Args[1].(*ast.BasicLit); ok && (lit.Value == `""` || lit.Value == "``") {
					out = append(out, Finding{
						Rule: "system-grant-without-tenant", Severity: Error,
						File: file, Line: line,
						Message: "SystemGrant with an empty tenant",
						Why:     "it returns the zero Grant, which fails Check -- so this call site is dead code. A system grant with no tenant would read across every customer, which is why it does not exist.",
					})
					return
				}
			}
			if legitimate {
				return
			}
			out = append(out, Finding{
				Rule: "system-grant-outside-scope", Severity: Warning,
				File: file, Line: line,
				Message: "SystemGrant outside a seeder, job or command",
				Why:     "it is the one way past a policy, and its call sites are the audit. If this is a request path, the Grant should come from security.Authorize instead.",
			})
		})
	}
	return out
}

// seedingName reports whether the function name says it is seeding or running a
// job, which are the two places a system grant belongs.
func seedingName(name string) bool {
	lower := strings.ToLower(name)
	for _, p := range []string{"seed", "ensure", "job", "worker", "migrate", "backfill"} {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// enclosingFuncs maps every line of the file to the function that contains it,
// so a finding can be judged by where it sits.
func enclosingFuncs(f *file) map[int]string {
	out := map[int]string{}
	for _, decl := range f.ast.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		from := f.fset.Position(fn.Pos()).Line
		to := f.fset.Position(fn.End()).Line
		for l := from; l <= to; l++ {
			out[l] = fn.Name.Name
		}
	}
	return out
}

// 8. SQL built with Sprintf or concatenation of a variable is injection, whatever
// the intent was.
func noBuiltSQL(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if f.isTest {
			continue
		}
		f.calls(func(call *ast.CallExpr, name string) {
			if name != "fmt.Sprintf" || len(call.Args) == 0 {
				return
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || !looksLikeSQL(lit.Value) {
				return
			}
			file, line := f.at(call)
			out = append(out, Finding{
				Rule: "sql-built-with-sprintf", Severity: Error,
				File: file, Line: line,
				Message: "SQL assembled with fmt.Sprintf",
				Why:     "every value interpolated into SQL is injection waiting for the right input. Use ? placeholders and pass the values as arguments -- the dialect rebinds them.",
			})
		})
	}
	return out
}

func looksLikeSQL(s string) bool {
	upper := strings.ToUpper(s)
	for _, kw := range []string{"SELECT ", "INSERT ", "UPDATE ", "DELETE ", " FROM ", " WHERE "} {
		if strings.Contains(upper, kw) {
			return true
		}
	}
	return false
}

// 9. A type with a secret in it needs to refuse to serialize itself, or the
// first Dump publishes it on the debug page.
func sensitiveFieldNeedsRedaction(p *project) []Finding {
	sensitive := []string{"password", "secret", "token", "document", "apikey", "api_key", "creditcard", "cpf", "cnpj"}

	var out []Finding
	for _, f := range p.files {
		// Anything under app/ -- a model, a request, a DTO. Outside it the
		// project is configuration and wiring, where a field called Token is the
		// credential being read rather than a record being stored.
		if f.isTest || f.category == "" {
			continue
		}
		f.types(func(ts *ast.TypeSpec) {
			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				return
			}

			var found string
			for _, field := range st.Fields.List {
				for _, n := range field.Names {
					lower := strings.ToLower(n.Name)
					for _, s := range sensitive {
						if strings.Contains(lower, s) {
							found = n.Name
						}
					}
				}
			}
			if found == "" || hasRedaction(p.files, f.dir, ts.Name.Name) {
				return
			}
			file, line := f.at(ts)
			out = append(out, Finding{
				Rule: "sensitive-field-not-redacted", Severity: Warning,
				File: file, Line: line,
				Message: ts.Name.Name + " holds " + found + " and does not redact itself",
				Why:     "one observability.Dump or one log line publishes it on the debug page. Add LogValue() slog.Value and MarshalJSON to the type, so no caller has to remember.",
			})
		})
	}
	return out
}

// hasRedaction looks for the methods in the directory the type lives in.
//
// The directory, not the whole project: a method has to be declared in the
// package of its receiver, so anywhere else is not the same type. That is also
// what makes the check right in the Laravel tree, where app/Models and
// app/Requests are two packages that both declare a type called User.
func hasRedaction(files []*file, dir, typeName string) bool {
	for _, f := range files {
		if f.dir != dir {
			continue
		}
		for _, decl := range f.ast.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || receiverType(fn) != typeName {
				continue
			}
			if fn.Name.Name == "LogValue" || fn.Name.Name == "MarshalJSON" {
				return true
			}
		}
	}
	return false
}

// 10. Login without rotating the session id is session fixation: an attacker
// plants a known id and inherits the session after the victim signs in.
func sessionMustRotateOnLogin(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if f.isTest {
			continue
		}
		for _, decl := range f.ast.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			name := strings.ToLower(fn.Name.Name)
			if !strings.Contains(name, "login") && !strings.Contains(name, "signin") {
				continue
			}
			if !funcBodyContains(fn, func(n string) bool { return strings.Contains(n, "Authenticate") }) {
				continue
			}
			if funcBodyContains(fn, func(n string) bool { return strings.HasSuffix(n, ".Rotate") || strings.HasSuffix(n, ".Start") }) {
				continue
			}
			file, line := f.at(fn)
			out = append(out, Finding{
				Rule: "session-not-rotated", Severity: Error,
				File: file, Line: line,
				Message: fn.Name.Name + " authenticates and does not rotate the session",
				Why:     "keeping the pre-login id is session fixation: an attacker plants a known id and inherits the session once the victim signs in. Call SessionStore.Rotate after Authenticate.",
			})
		}
	}
	return out
}

// renderCall is a ctx.View or ctx.Fragment call, with the two arguments the view
// rules care about.
type renderCall struct {
	call *ast.CallExpr
	// name is the view name argument, whatever it is written as.
	name ast.Expr
	// data is the argument that reaches the template.
	data ast.Expr
}

// renderCalls finds the calls that render a view.
//
// It matches by shape rather than by type, because doctor never type-checks:
// `ctx.View(name, data)` has two arguments and `ctx.Fragment(status, name,
// data)` has three, and the method names come from httpx.Context. A method
// called View with two arguments on something else is a false positive this
// accepts -- and the alternative, type resolution, would mean doctor could only
// run on a project that already compiles.
func renderCalls(f *file) []renderCall {
	var out []renderCall
	f.calls(func(call *ast.CallExpr, name string) {
		switch {
		case strings.HasSuffix(name, ".View") && len(call.Args) == 2:
			out = append(out, renderCall{call: call, name: call.Args[0], data: call.Args[1]})
		case strings.HasSuffix(name, ".Fragment") && len(call.Args) == 3:
			out = append(out, renderCall{call: call, name: call.Args[1], data: call.Args[2]})
		}
	})
	return out
}

// 11. The data of a view is a typed struct, never a map.
//
// Doc 14 makes this the reason the view layer exists at all: with a struct, a
// renamed field is a compile error and the page cannot ship broken. With
// map[string]any, a typo in a key is a nil the template renders as nothing --
// the page comes up, the field is blank, and nobody finds out until a customer
// says the invoice has no total.
//
// It follows the value one step, because the map is usually built on the line
// above the render rather than inside the call.
func viewDataMustBeAStruct(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if f.isTest {
			continue
		}
		maps := mapVariables(f)

		for _, r := range renderCalls(f) {
			source, why := "", ""
			switch {
			case isMapExpression(r.data):
				why = "this call passes a map"
			default:
				id, ok := r.data.(*ast.Ident)
				if !ok {
					continue
				}
				at, isMap := maps[id.Name]
				if !isMap {
					continue
				}
				source = id.Name
				why = fmt.Sprintf("%s, built on line %d, is a map", source, f.line(at))
			}

			file, line := f.at(r.call)
			out = append(out, Finding{
				Rule: "view-data-is-a-map", Severity: Error,
				File: file, Line: line,
				Message: "the data of " + viewNameOf(r) + " is a map: " + why,
				Why:     "a struct is what makes a typo a compile error. With a map, a renamed or misspelled key renders as an empty string, the page comes up, and the missing field is found in production. Declare the type in the view's @go block and pass it.",
			})
		}
	}
	return out
}

// viewNameOf renders the view being rendered, for a message.
func viewNameOf(r renderCall) string {
	if name, ok := stringLiteral(r.name); ok {
		return "view " + name
	}
	return "this view"
}

// isMapExpression reports whether an expression is plainly a map.
func isMapExpression(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.CompositeLit:
		_, ok := x.Type.(*ast.MapType)
		return ok
	case *ast.CallExpr:
		// make(map[string]any)
		if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "make" && len(x.Args) > 0 {
			_, isMap := x.Args[0].(*ast.MapType)
			return isMap
		}
	case *ast.UnaryExpr:
		return isMapExpression(x.X)
	}
	return false
}

// mapVariables collects the local names that hold a map, per file.
//
// One step of assignment, no more: the shape people write is a literal on the
// line before the render, and an analysis that promised to follow a value
// through three functions without a type checker would be promising something it
// cannot deliver.
func mapVariables(f *file) map[string]ast.Node {
	out := map[string]ast.Node{}

	ast.Inspect(f.ast, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			if len(x.Lhs) != len(x.Rhs) {
				return true
			}
			for i, rhs := range x.Rhs {
				if !isMapExpression(rhs) {
					continue
				}
				if id, ok := x.Lhs[i].(*ast.Ident); ok && id.Name != "_" {
					out[id.Name] = rhs
				}
			}
		case *ast.ValueSpec:
			if _, isMap := x.Type.(*ast.MapType); isMap {
				for _, id := range x.Names {
					out[id.Name] = x
				}
				return true
			}
			for i, value := range x.Values {
				if i < len(x.Names) && isMapExpression(value) {
					out[x.Names[i].Name] = value
				}
			}
		}
		return true
	})
	return out
}

// 12. A view that is rendered has to exist.
//
// The name is a string, so this is the one place in the view path the compiler
// cannot reach: `ctx.View("invoices.idex", data)` compiles, deploys, and returns
// 500 the first time somebody opens the page. Nothing else in the request would
// have failed.
//
// It stays quiet on a project with no views at all -- a library, or an
// application whose views have not been written yet -- because a rule that fires
// on every call in an empty project teaches people to ignore it.
func viewMustExist(p *project) []Finding {
	if len(p.views) == 0 {
		return nil
	}

	var out []Finding
	for _, f := range p.files {
		if f.isTest {
			continue
		}
		for _, r := range renderCalls(f) {
			name, ok := stringLiteral(r.name)
			if !ok || name == "" {
				// A name computed at runtime is outside what this can check, and
				// saying so is better than guessing.
				continue
			}
			if p.hasView(name) {
				continue
			}
			file, line := f.at(r.call)
			out = append(out, Finding{
				Rule: "view-does-not-exist", Severity: Error,
				File: file, Line: line,
				Message: "there is no view named " + name,
				Why:     "the name of a view is a string, so this is the one thing in the view path the compiler cannot check: it builds, it deploys, and the page answers 500 the first time somebody opens it. Create " + viewsDir + "/" + strings.ReplaceAll(name, ".", "/") + ".kyse.go, or fix the name.",
			})
		}
	}
	return out
}

// 13. What the project declares in arandu.mod.toml has to match what it does.
//
// The declaration is the only thing anyone reads before installing a module from
// the registry, and a declaration nobody verifies is worse than none: it is a
// promise with the weight of a check and the reliability of a comment.
//
// Used and not declared is an error -- that is the code doing something its
// installer did not agree to. Declared and not used is a warning, because asking
// for more than you need is how a permission model erodes into everyone
// declaring everything.
//
// A project with no manifest is silent. The unit of distribution is the Go
// module (doc 18), so the file belongs at the root of a repository that is
// published -- and an application is not published. Demanding it from every
// `aru new` would be a warning that fires on correct code, which is how a tool
// teaches people to stop reading it.
func declaredPermissionsMatchTheCode(p *project) []Finding {
	declared := p.manifest
	if declared == nil {
		return nil
	}

	name := declared.Name
	if name == "" {
		name = "this project"
	}
	used := usedPermissions(p)

	var out []Finding
	for _, c := range []struct {
		name        string
		declared    bool
		used        bool
		consequence string
	}{
		{"network", declared.Permissions.Network, used.Network,
			"the code makes calls that leave the process, and whoever installed it agreed to a module that does not"},
		{"filesystem", declared.Permissions.Filesystem, used.Filesystem,
			"the code reads or writes files outside the database, which is not visible from its API"},
		{"exec", declared.Permissions.Exec, used.Exec,
			"the code runs another program, which is the widest capability there is"},
		{"migrations", declared.Permissions.Migrations, used.Migrations,
			"the code owns tables, and an installer who did not expect that will not know to run aru migrate"},
	} {
		switch {
		case c.used && !c.declared:
			out = append(out, Finding{
				Rule: "permission-not-declared", Severity: Error,
				File: relativeTo(p.root, declared.Path), Line: 1,
				Message: name + " uses " + c.name + " and declares " + c.name + " = false",
				Why:     c.consequence + ". Set " + c.name + " = true under [permissions], or remove the code that needs it.",
			})
		case c.declared && !c.used && len(p.unreadable) == 0:
			// Absence, so it needs the guard, and here the guard is the whole
			// file set: a project whose only network call sits in a file that
			// does not parse would be told to declare network = false, which is
			// the opposite of true.
			out = append(out, Finding{
				Rule: "permission-not-used", Severity: Warning,
				File: relativeTo(p.root, declared.Path), Line: 1,
				Message: name + " declares " + c.name + " = true and does not use it",
				Why:     "asking for more than you need is how a permission model erodes into everyone declaring everything. Set " + c.name + " = false.",
			})
		}
	}
	return out
}

// usedPermissions is the capability audit, by AST.
//
// It looks at what the code calls rather than at what it imports, because
// net/http is imported by every project that has a controller and says nothing
// about whether it calls out.
func usedPermissions(p *project) manifest.Permissions {
	var used manifest.Permissions

	for _, f := range p.files {
		if f.isTest {
			continue
		}

		// database/migrations is where the schema lives in the Laravel tree, so
		// its existence is the declaration -- no method name to remember.
		if strings.HasPrefix(f.rel, "database/migrations/") {
			used.Migrations = true
		}

		for path := range f.imports {
			switch path {
			case "os/exec":
				used.Exec = true
			case "net/smtp", "net/rpc":
				used.Network = true
			case "io/ioutil":
				used.Filesystem = true
			}
		}

		f.calls(func(_ *ast.CallExpr, name string) {
			switch {
			// The client half of net/http. The server half -- Handler,
			// ResponseWriter, StatusOK -- is every project with a route.
			case name == "http.Get", name == "http.Post", name == "http.Head",
				name == "http.PostForm", name == "http.NewRequest",
				name == "http.NewRequestWithContext",
				strings.HasSuffix(name, ".Do"),
				name == "net.Dial", name == "net.DialTimeout":
				used.Network = true

			case name == "os.Open", name == "os.OpenFile", name == "os.Create",
				name == "os.ReadFile", name == "os.WriteFile", name == "os.Remove",
				name == "os.RemoveAll", name == "os.Mkdir", name == "os.MkdirAll",
				name == "os.Rename", name == "os.ReadDir",
				name == "filepath.Walk", name == "filepath.WalkDir":
				used.Filesystem = true

			case name == "exec.Command", name == "exec.CommandContext", name == "syscall.Exec":
				used.Exec = true
			}
		})

		for _, decl := range f.ast.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv != nil && fn.Name.Name == "Migrations" {
				used.Migrations = true
			}
		}
	}
	return used
}

func relativeTo(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return rel
	}
	return path
}

// alpineAttribute captures what an Alpine directive contains.
//
// Alpine is written in HTML attributes, so this is text matching rather than
// parsing. That is a real limitation: a directive split across lines by a
// formatter still matches, and one built by string concatenation in Go does not.
// It catches the shape people actually write.
var alpineAttribute = regexp.MustCompile(`(?s)(x-data|x-init|x-effect)\s*=\s*"([^"]*)"`)

// networkInAlpine is the set that means this state is not client-only.
var networkInAlpine = []struct {
	token string
	what  string
}{
	{"fetch(", "a fetch call"},
	{"axios", "an axios call"},
	{"XMLHttpRequest", "an XMLHttpRequest"},
	{"$store", "a global Alpine store"},
	{"navigator.sendBeacon", "a beacon"},
	{"EventSource(", "a server-sent events subscription"},
	{"new WebSocket", "a WebSocket"},
}

// 14. Alpine holds client state, and nothing else.
//
// Doc 14 draws the line: Alpine is allowed when the state is client-only,
// ephemeral, and invisible to the server -- a dropdown, a tab, an input mask.
// The moment a directive talks to the server, the component should have been an
// HTMX fragment, and the application now has two ways to fetch data with two
// sets of error handling, two loading states and two places CSRF can be
// forgotten.
//
// Without this check, RULE 9 is opinion, and opinion does not survive a code
// review at 6pm.
func alpineHoldsClientStateOnly(p *project) []Finding {
	var out []Finding

	for _, v := range p.views {
		for _, match := range alpineAttribute.FindAllStringSubmatchIndex(v.body, -1) {
			directive := v.body[match[2]:match[3]]
			content := v.body[match[4]:match[5]]

			for _, forbidden := range networkInAlpine {
				if !strings.Contains(content, forbidden.token) {
					continue
				}
				out = append(out, Finding{
					Rule: "alpine-reaches-the-server", Severity: Error,
					File: v.rel, Line: lineOf(v.body, match[0]),
					Message: directive + " contains " + forbidden.what,
					Why: "Alpine holds state that is client-only, ephemeral and invisible to the server. " +
						"A directive that talks to the server should be an HTMX fragment instead -- otherwise the " +
						"application has two ways to fetch data, with two loading states and two places to forget the CSRF token.",
				})
				break
			}
		}
	}
	return out
}

// lineOf returns the 1-indexed line containing the byte offset.
func lineOf(body string, offset int) int {
	if offset > len(body) {
		offset = len(body)
	}
	return strings.Count(body[:offset], "\n") + 1
}
