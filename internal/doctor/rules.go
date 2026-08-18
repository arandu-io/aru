package doctor

import (
	"fmt"
	"go/ast"
	"go/token"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/arandu-io/aru/internal/kyse"
	"github.com/arandu-io/aru/internal/manifest"
)

// rules is the whole check surface. Each one exists because something real can
// go wrong that the compiler cannot see -- and each message says what breaks,
// not which rule was violated.
//
// Adding a rule that rejects existing code is a breaking change: it enters as a
// warning in a minor and becomes an error in the next major.
//
// What belongs in this slice is a check about the application aru generates:
// the tree with app/, routes/ and resources/views/ in it. A check about aru's
// own source belongs in .golangci.yml at the root instead, and the two cannot
// be confused by accident -- doctor refuses a directory that is not an Arandu
// project, and the linter never sees one.
//
// The tree decides that, not the topic. Two rules below find SQL assembled by
// hand, which an off-the-shelf linter also finds, and they stay here: whoever
// runs `aru doctor` is not required to run anything else, so a report that
// verified authorization and left injection to a second tool would be half an
// answer.
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
	tenantMustScopeTheSQL,
	theOutboxTableTravelsWithWhatWritesToIt,
	resourceNotReauthorized,
	rawOutputIsAComponent,
}

// 0. A file doctor could not read makes every other rule unreliable.
//
// Skipping an unparsable file silently would leave the syntax error to the
// compiler, which reports it better -- and that is beside the point. Every rule
// here reasons over the whole file set, so a project whose InvoicePolicy.go does
// not parse looks exactly like a project with no policy for Invoice: an invented
// finding, pointing at the wrong file, telling the author to write a policy they
// had already written.
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
// app/Policies/ is not a convention an organized team chooses to keep: it is
// skeleton, and this is the rule that makes it so.
func repositoryNeedsPolicy(p *project) []Finding {
	repositories, policies := repositoriesAndPolicies(p)

	// One finding per entity, and every entity -- not the first one and then
	// stop. Breaking out after the first would make a project with three
	// unprotected entities report one, and the author would find the next only
	// after fixing that one and running again.
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
//
// The suffix, not the substring. Asking whether the receiver CONTAINS "Repo"
// makes ReportPolicy, Reporter and Reposition repositories: a read model that
// takes a Grant and hands it to the repository below is reported for not
// checking it, and the code is correct. A rule that fires on correct code is how
// a tool teaches people to ignore it.
func isRepository(f *file, fn *ast.FuncDecl) bool {
	if f.category == "Repositories" {
		return true
	}
	// Suffix, not substring, and both spellings.
	//
	// strings.Contains(t, "Repo") classified ReportPolicy, Reporter and
	// Reposition as repositories and produced a false positive on each. Narrowing
	// it to HasSuffix("Repository") fixed those three and lost every type ending
	// in Repo -- and this predicate gates all four authorization rules at once,
	// so a type it does not see is a type where none of them apply. Trading a
	// noisy rule for a blind one is the worse half of the trade.
	//
	// Both suffixes exclude the three names above, because none of them ends in
	// Repo or Repository.
	return looksLikeRepository(receiverType(fn))
}

// looksLikeRepository is the naming half of isRepository, split out so the two
// spellings and the three near-misses can be pinned by name in a test.
func looksLikeRepository(t string) bool {
	return strings.HasSuffix(t, "Repository") || strings.HasSuffix(t, "Repo")
}

// databaseCall reports whether a call goes through a database handle.
//
// It is the database/sql surface, by method name: doctor never type-checks, so
// what it can see is that something called QueryContext on something. A method
// of a repository that calls one of these has reached the table.
func databaseCall(name string) bool {
	switch {
	case strings.HasSuffix(name, ".Query"),
		strings.HasSuffix(name, ".QueryContext"),
		strings.HasSuffix(name, ".QueryRow"),
		strings.HasSuffix(name, ".QueryRowContext"),
		strings.HasSuffix(name, ".Exec"),
		strings.HasSuffix(name, ".ExecContext"),
		strings.HasSuffix(name, ".Prepare"),
		strings.HasSuffix(name, ".PrepareContext"),
		strings.HasSuffix(name, ".Begin"),
		strings.HasSuffix(name, ".BeginTx"):
		return true
	}
	return false
}

// reachesTheDatabase reports whether the body of a method touches the handle.
func reachesTheDatabase(fn *ast.FuncDecl) bool {
	return funcBodyContains(fn, databaseCall)
}

// grantChecks counts the calls to Check in a body, and how many of them have
// their answer used.
//
// Asking only whether a call exists is what let `_ = g.Check(x)` through: the
// method asks the question, throws the answer away, and reads like one that
// authorizes. That is worse than leaving the check out, because a reader stops
// looking.
//
// Used means the value goes somewhere: returned, tested in an if, or assigned to
// a name -- which is then almost always the err of the line below. Discarded
// means the two shapes that provably drop it: assignment to the blank identifier
// and a bare call statement.
func grantChecks(fn *ast.FuncDecl) (total, used int) {
	if fn.Body == nil {
		return 0, 0
	}

	isCheck := func(call *ast.CallExpr) bool {
		return strings.HasSuffix(callName(call), ".Check")
	}
	all := map[*ast.CallExpr]bool{}
	discarded := map[*ast.CallExpr]bool{}

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if isCheck(x) {
				all[x] = true
			}
		case *ast.ExprStmt:
			if call, ok := x.X.(*ast.CallExpr); ok && isCheck(call) {
				discarded[call] = true
			}
		case *ast.AssignStmt:
			if len(x.Lhs) != len(x.Rhs) {
				return true
			}
			for i, rhs := range x.Rhs {
				call, ok := rhs.(*ast.CallExpr)
				if !ok || !isCheck(call) {
					continue
				}
				if id, ok := x.Lhs[i].(*ast.Ident); ok && id.Name == "_" {
					discarded[call] = true
				}
			}
		}
		return true
	})
	return len(all), len(all) - len(discarded)
}

// 2. Every exported repository method that touches the handle must take a Grant
// and check it.
//
// Three ways to lose the policy, one rule, because they are one mistake seen
// from three sides:
//
//   - the method never declares the Grant, so there is no signature to satisfy;
//   - it declares it and never checks it, so a Grant issued for another action
//     passes;
//   - it checks it into the blank identifier, so the answer is thrown away.
//
// The first one matters most, and it is the one a signature check misses:
// skipping every method that does not take a Grant assumes the signature is the
// enforcement. It applies to List and Find exactly as it applies to Create -- a
// read model, a report, a projection or an export with no policy is a tenant
// leak with a technical name.
//
// Only exported methods. An unexported helper that runs the query for the
// exported method above it is the shape people write, and the exported one is
// already audited: reporting the helper as well would be a second finding for
// one fact, and that is how a report becomes noise.
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
			file, line := f.at(fn)

			if !takesGrant(fn) {
				if !reachesTheDatabase(fn) {
					continue
				}
				out = append(out, Finding{
					Rule: "grant-not-received", Severity: Error,
					File: file, Line: line,
					Message: fn.Name.Name + " reaches the database and receives no Grant",
					Why:     "reading is authorized exactly like writing: a query with no policy is a tenant leak with a technical name. A read model, a report, a projection and an export are not exceptions. Take a security.Grant and start the method with: if err := g.Check(Action...); err != nil { return err }",
				})
				continue
			}

			total, used := grantChecks(fn)
			switch {
			case total == 0:
				out = append(out, Finding{
					Rule: "grant-not-checked", Severity: Error,
					File: file, Line: line,
					Message: fn.Name.Name + " receives a Grant and never checks it",
					Why:     "a Grant issued for another action would pass. Start the method with: if err := g.Check(Action...); err != nil { return err }",
				})
			case used == 0:
				out = append(out, Finding{
					Rule: "grant-check-discarded", Severity: Error,
					File: file, Line: line,
					Message: fn.Name.Name + " calls Check and discards the answer",
					Why:     "`_ = g.Check(...)` asks the question and throws it away, so the method is unauthorized while reading like one that is not -- which is worse than leaving the check out, because the next reader stops looking. Return the error: if err := g.Check(Action...); err != nil { return err }",
				})
			}
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

// requestPath names the kind of file when it sits on the path of a request, and
// reports false when it does not.
//
// The two rules below cannot ask for the Controllers category alone, because the
// request does not arrive only there. A handler written inline in the custom
// block of routes/web.go -- which the skeleton invites people to write in -- and
// a middleware in app/Http/Middleware are the same request, one layer earlier,
// with the same database under them.
func requestPath(f *file) (string, bool) {
	switch {
	case f.category == "Controllers":
		return "controller", true
	case f.category == "Middleware":
		return "middleware", true
	case strings.HasPrefix(f.rel, "routes/"):
		return "route file", true
	}
	return "", false
}

// 4. A handler that reaches the data package is a handler that skipped the
// service, and therefore the policy.
func controllerMustNotReachData(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		kind, onTheRequestPath := requestPath(f)
		if f.isTest || !onTheRequestPath {
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
				Message: "this " + kind + " uses the data package beyond data.Query",
				Why:     "a handler that reaches the database skipped the service, and therefore the policy -- whether it is a controller, a middleware, or written inline in the route table. Move the call into app/Services, where the Grant is issued.",
			})
			break
		}
	}
	return out
}

// 5. A handler that imports app/Repositories skips the service the same way, and
// the compiler cannot see it: the repository method it calls does require a
// Grant, and a handler can produce one with SystemGrant.
//
// Service and Repository are not a convention an organized team chooses to keep:
// they are skeleton, and the direction of the arrow between them is checked.
func controllerMustNotReachTheRepository(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		kind, onTheRequestPath := requestPath(f)
		if f.isTest || !onTheRequestPath {
			continue
		}
		for imported := range f.imports {
			if !strings.Contains(strings.ToLower(imported), "/app/repositories") {
				continue
			}
			out = append(out, Finding{
				Rule: "controller-reaches-repository", Severity: Error,
				File: f.rel, Line: 1,
				Message: "this " + kind + " imports " + imported,
				Why:     "the Grant a repository requires would have to be issued here, which is the request authorizing itself. Call app/Services instead: it validates, calls security.Authorize, and only then reaches the repository.",
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

// grantConstructors are the exported functions that hand back a security.Grant
// without a Policy having answered for it.
//
// security.Authorize is not here, and that is the point: it IS the policy check,
// so a Grant that came from it was authorized by construction. What is listed is
// the escape hatch and everything that wraps it.
//
// It is a list rather than one name because wrapping is what got past the rules
// below. They matched the literal `security.SystemGrant`, and jobs.GrantFor
// delegates to it with the action and the tenant read off a Job -- an ordinary
// exported struct that any package can fill in:
//
//	jobs.GrantFor(jobs.Job{Action: "customer.delete", TenantID: ctx.Query("org")})
//
// That reaches the database with permissions nobody granted, under a tenant the
// caller chose, and a rule matching one literal name produces no finding at all.
//
// A new function returning a security.Grant has to be added here. Nothing makes
// that automatic -- the doctor reads one package at a time and does not resolve
// types across modules -- so TestEveryGrantConstructorIsGuarded compares this
// list against what the framework actually exports.
var grantConstructors = []string{
	"security.SystemGrant",
	"jobs.GrantFor",
}

// isGrantConstructor reports whether a call hands back an unauthorized Grant.
func isGrantConstructor(name string) bool {
	for _, c := range grantConstructors {
		if name == c {
			return true
		}
	}
	return false
}

// tenantArg returns the expression that supplies the tenant to a grant
// constructor.
//
// The two spell it differently: security.SystemGrant takes it as the second
// positional argument, and jobs.GrantFor takes a Job whose TenantID field
// carries it. Reading only the positional form is what let the wrapper through.
func tenantArg(call *ast.CallExpr, name string) (ast.Expr, bool) {
	switch name {
	case "security.SystemGrant":
		if len(call.Args) >= 2 {
			return call.Args[1], true
		}
	case "jobs.GrantFor":
		if len(call.Args) != 1 {
			return nil, false
		}
		lit, ok := call.Args[0].(*ast.CompositeLit)
		if !ok {
			return nil, false
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "TenantID" {
				return kv.Value, true
			}
		}
	}
	return nil, false
}

// requestValuesReachingAGrant follows the value instead of reading the name.
//
// The two checks above ask whether a header or a parameter is CALLED something
// with "tenant" in it, and that is the wrong question:
//
//	org := ctx.Query("org")
//	g := security.SystemGrant(ActionView, org)
//
// passes both of them, and it is exactly the hole that matters. The name of a
// header is chosen by whoever wrote the client; what makes it a tenant is where
// the value ends up.
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
		// No early return on an empty `tainted`: the embedded form assigns
		// nothing, and skipping pass two here is what hid it.

		// Pass two: the tenant argument of a grant constructor, either a name
		// pass one tainted or a request read written inline.
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := callName(call)
			if !isGrantConstructor(name) {
				return true
			}
			arg, ok := tenantArg(call, name)
			if !ok {
				return true
			}

			// The embedded form, with no variable in between:
			//
			//	security.SystemGrant(ActionView, ctx.Query("org"))
			//
			// Pass one never sees it, because nothing was assigned. It is the
			// shortest way to write the hole this rule exists to close, and it
			// went unreported while the longer version was caught.
			if inner, isCall := arg.(*ast.CallExpr); isCall {
				if readsTheRequest(callName(inner)) {
					file, line := f.at(call)
					out = append(out, Finding{
						Rule: "tenant-from-request", Severity: Error,
						File: file, Line: line,
						Message: fmt.Sprintf("the tenant of this Grant is read straight off the request, by %s", callName(inner)),
						Why:     "whoever sent the request picks the tenant, and reads every row of it. It comes from the Grant.",
					})
				}
				return true
			}

			id, ok := arg.(*ast.Ident)
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
		// The http.Context accessors: the same values, one layer up.
		strings.HasSuffix(name, "ctx.Param"),
		strings.HasSuffix(name, "ctx.Query"),
		strings.HasSuffix(name, "ctx.Input"):
		return true
	}
	return false
}

// 7. SystemGrant is the one way past the policy. Its call sites are the audit.
//
// Two things can excuse a call, and the name of the enclosing function is not
// one of them. Letting through any function whose name contains ensure, seed,
// job, worker, migrate or backfill passes `ensureGrant` and fires on
// `issueGrant`, identical in every other character. A check a rename defeats is
// a spelling convention.
//
// What excuses a call:
//
//   - the file is one of the places the skeleton puts work with no request
//     behind it -- database/seeders, app/Jobs, app/Console, cmd, main.go. That
//     is a directory the framework decided, not a word somebody chose;
//   - the line carries `//arandu:system-grant <reason>`. An escape has to be
//     deliberate and visible: it stays in the diff, it is read in review, and
//     the reason is written down next to the thing it excuses. A marker with no
//     reason excuses nothing.
func systemGrantIsAudited(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if f.isTest {
			continue
		}
		systemScope := inASystemScope(f.rel)
		escapes := systemGrantEscapes(f)
		enclosing := enclosingFuncs(f)

		f.calls(func(call *ast.CallExpr, name string) {
			if !isGrantConstructor(name) {
				return
			}
			file, line := f.at(call)

			// An empty tenant is refused by the framework at runtime; catching it
			// here says so before anyone waits for the failure.
			if arg, found := tenantArg(call, name); found {
				if lit, ok := arg.(*ast.BasicLit); ok && (lit.Value == `""` || lit.Value == "``") {
					out = append(out, Finding{
						Rule: "system-grant-without-tenant", Severity: Error,
						File: file, Line: line,
						Message: "SystemGrant with an empty tenant",
						Why:     "it returns the zero Grant, which fails Check -- so this call site is dead code. A system grant with no tenant would read across every customer, which is why it does not exist.",
					})
					return
				}
			}
			if systemScope || escapes[line] || escapes[line-1] {
				return
			}

			where := "SystemGrant"
			if fn := enclosing[line]; fn != "" {
				where = "SystemGrant in " + fn
			}
			out = append(out, Finding{
				Rule: "system-grant-outside-scope", Severity: Warning,
				File: file, Line: line,
				Message: where + " is outside a seeder, a job and a command",
				Why:     "it is the one way past a policy, and its call sites are the audit. If this is a request path, the Grant should come from security.Authorize instead. If the escape is deliberate, write `//arandu:system-grant <reason>` on the line, so the reason is read in review rather than inferred from the function name.",
			})
		})
	}
	return out
}

// inASystemScope reports whether the file is one of the places the skeleton puts
// work that has no request behind it.
//
// By path segment, not by substring. "seeder" anywhere in a path matched
// app/Services/SeederService.go, which is the same rename hole one level up.
func inASystemScope(rel string) bool {
	lower := strings.ToLower(filepath.ToSlash(rel))
	if lower == "main.go" || lower == "routes/console.go" {
		return true
	}
	for _, segment := range strings.Split(path.Dir(lower), "/") {
		switch segment {
		case "seeders", "jobs", "commands", "console", "cmd":
			return true
		}
	}
	return false
}

// systemGrantEscapes returns the lines carrying a deliberate escape.
//
// The marker is `//arandu:system-grant <reason>`, on the line of the call or on
// the line above it, and the reason is required: a bare marker is a suppression
// with nothing to review, which is what this rule exists to prevent.
func systemGrantEscapes(f *file) map[int]bool {
	out := map[int]bool{}
	for _, group := range f.ast.Comments {
		for _, c := range group.List {
			text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
			reason, marked := strings.CutPrefix(text, "arandu:system-grant")
			if !marked || strings.TrimSpace(reason) == "" {
				continue
			}
			out[f.fset.Position(c.Pos()).Line] = true
		}
	}
	return out
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

		out = append(out, concatenatedSQL(f)...)
	}
	return out
}

// concatenatedSQL is the other half of the check.
//
// Reading fmt.Sprintf alone leaves a hole in the one barrier against hand-built
// SQL:
//
//	"SELECT id FROM invoices WHERE reference LIKE '%" + term + "%'"
//
// passes clean, with term coming straight off the request.
//
// # What separates it from the concatenation the generator writes on purpose
//
// The generated repository concatenates too, and correctly: a package-level
// const for the column list, and a `column` local that was chosen from an
// allowlist. Neither can carry a value from outside.
//
// The signal that tells them apart, with no type information, is where the
// operand comes from: a PARAMETER of the enclosing function, or a field of one,
// is a value the caller supplied. A const or a local is not.
//
// # The limit, stated rather than implied
//
// Assigning a parameter to a local and concatenating the local is not caught --
// separating that from the allowlist pattern needs dataflow, and the allowlist
// pattern is what the generator emits. A rule that flagged it would fire on the
// code the generator writes, and a rule that fires on correct code is how a tool
// teaches people to ignore it.
func concatenatedSQL(f *file) []Finding {
	var out []Finding

	f.functions(func(fn *ast.FuncDecl) {
		params := parameterNames(fn)
		if len(params) == 0 || fn.Body == nil {
			return
		}

		ast.Inspect(fn.Body, func(n ast.Node) bool {
			bin, ok := n.(*ast.BinaryExpr)
			if !ok || bin.Op != token.ADD {
				return true
			}

			var sawSQL bool
			var offender string
			var walk func(ast.Expr)
			walk = func(e ast.Expr) {
				switch v := e.(type) {
				case *ast.BinaryExpr:
					if v.Op == token.ADD {
						walk(v.X)
						walk(v.Y)
					}
				case *ast.BasicLit:
					if v.Kind == token.STRING && looksLikeSQL(v.Value) {
						sawSQL = true
					}
				case *ast.Ident:
					if params[v.Name] {
						offender = v.Name
					}
				case *ast.SelectorExpr:
					// q.Sort, in.Term: a field of a parameter is still the
					// caller's value.
					if root, ok := v.X.(*ast.Ident); ok && params[root.Name] {
						offender = root.Name + "." + v.Sel.Name
					}
				}
			}
			walk(bin)

			if sawSQL && offender != "" {
				file, line := f.at(bin)
				out = append(out, Finding{
					Rule: "sql-built-by-concatenation", Severity: Error,
					File: file, Line: line,
					Message: "SQL assembled by concatenating " + offender,
					Why: "a value the caller supplied reaches the statement as text, which is injection with one more step than fmt.Sprintf. Use a ? placeholder and pass it as an argument. " +
						"A column or table NAME cannot be a placeholder -- pick it from an allowlist first, and concatenate the allowlisted value, which is what `aru make:module` emits.",
				})
				return false
			}
			return true
		})
	})
	return out
}

// parameterNames is the set of names the function's signature binds.
func parameterNames(fn *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	if fn.Type == nil || fn.Type.Params == nil {
		return out
	}
	for _, field := range fn.Type.Params.List {
		for _, name := range field.Names {
			if name.Name != "_" {
				out[name.Name] = true
			}
		}
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
// what makes the check right in this tree, where app/Models and
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
// data)` has three, and the method names come from http.Context. A method
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
// module, so the file belongs at the root of a repository that is published --
// and an application is not published. Demanding it from every
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

		// database/migrations is where the schema lives, so
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
//
// Event handlers and both quote styles, because that is where the mistake lives.
// Matching x-data, x-init and x-effect with double quotes alone would let
// `x-on:click="fetch(...)"`, `@click="fetch(...)"` and every single-quoted form
// through untouched -- and an event handler is precisely where somebody writes a
// network call.
var alpineAttribute = regexp.MustCompile(`(?s)(?:^|[^\w:@-])(x-data|x-init|x-effect|x-on:[\w.:-]+|@[\w.:-]+)\s*=\s*("[^"]*"|'[^']*')`)

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
// Alpine is allowed when the state is client-only, ephemeral, and invisible to
// the server -- a dropdown, a tab, an input mask. The moment a directive talks
// to the server, the component should have been an HTMX fragment, and the
// application now has two ways to fetch data with two sets of error handling,
// two loading states and two places CSRF can be forgotten.
//
// Without this check that line is opinion, and opinion does not survive a code
// review at 6pm.
func alpineHoldsClientStateOnly(p *project) []Finding {
	var out []Finding

	for _, v := range p.views {
		for _, match := range alpineAttribute.FindAllStringSubmatchIndex(v.body, -1) {
			directive := v.body[match[2]:match[3]]
			// The quotes are part of the capture, so that one alternative can
			// hold both styles; the content is what sits between them.
			quoted := v.body[match[4]:match[5]]
			content := quoted[1 : len(quoted)-1]

			for _, forbidden := range networkInAlpine {
				if !strings.Contains(content, forbidden.token) {
					continue
				}
				out = append(out, Finding{
					Rule: "alpine-reaches-the-server", Severity: Error,
					// The directive, not the whole match: the match starts one
					// character earlier, and that character can be the newline
					// that ends the line above.
					File: v.rel, Line: lineOf(v.body, match[2]),
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

// 15. A multi-tenant repository whose SQL lost its tenant predicate reads and
// writes every customer's rows.
//
// It is the leak in its most direct form, and the reason it survives review is
// that everything else about the method is right: the Grant is taken, the Grant
// is checked, the policy exists -- and the `AND tenant_id = ?` somebody deleted
// while debugging never came back. The tenant comes from the Grant, and it has
// to reach the WHERE.
//
// INSERT is not checked here. The tenant of a row being written is a value in
// the column list, not a predicate, and it comes from data.Tenant(g) -- a
// different mistake, with a different shape.
func tenantMustScopeTheSQL(p *project) []Finding {
	multiTenant := multiTenantRepositories(p)
	if len(multiTenant) == 0 {
		return nil
	}

	var out []Finding
	for _, f := range p.files {
		if f.isTest {
			continue
		}
		for _, decl := range f.ast.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !isRepository(f, fn) {
				continue
			}
			if !multiTenant[receiverType(fn)] {
				continue
			}
			for _, sql := range sqlStatements(fn) {
				if tenantIsInThePredicate(sql.text) {
					continue
				}
				file, line := f.at(sql.node)
				out = append(out, Finding{
					Rule: "sql-without-tenant-scope", Severity: Error,
					File: file, Line: line,
					Message: "the " + sql.verb + " in " + fn.Name.Name + " does not filter by tenant_id",
					Why:     "this repository scopes its other queries by tenant, so the table has the column and this statement reaches every customer's rows -- reading them, or writing them. Add `AND tenant_id = ?` to the WHERE and pass data.Tenant(g), which is the only source of a tenant for SQL.",
				})
			}
		}
	}
	return out
}

// multiTenantRepositories reports which repository types hold a tenant column,
// keyed by receiver type.
//
// Nothing in the AST says an entity is multi-tenant -- there is no type to
// inspect and doctor never type-checks -- so it is inferred from three things
// the code does say, any one of which is enough:
//
//   - a method of the type calls data.Tenant, which is the only source of a
//     tenant for SQL;
//   - a query of the type names tenant_id, which is the column;
//   - the entity the repository is named after has a TenantID field.
//
// The third is what catches the worst case: a repository whose every query lost
// the predicate, where the first two signals are gone with it.
func multiTenantRepositories(p *project) map[string]bool {
	entities := map[string]bool{}
	for _, f := range p.files {
		if f.isTest {
			continue
		}
		f.types(func(ts *ast.TypeSpec) {
			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				return
			}
			for _, field := range st.Fields.List {
				for _, n := range field.Names {
					if strings.EqualFold(n.Name, "TenantID") {
						entities[ts.Name.Name] = true
					}
				}
			}
		})
	}

	out := map[string]bool{}
	for _, f := range p.files {
		if f.isTest {
			continue
		}
		for _, decl := range f.ast.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !isRepository(f, fn) {
				continue
			}
			receiver := receiverType(fn)
			if receiver == "" {
				continue
			}
			entity := strings.TrimSuffix(receiver, "Repository")
			if entities[entity] || funcBodyContains(fn, func(n string) bool { return n == "data.Tenant" }) {
				out[receiver] = true
				continue
			}
			for _, sql := range sqlStatements(fn) {
				if strings.Contains(strings.ToLower(sql.text), "tenant_id") {
					out[receiver] = true
				}
			}
		}
	}
	return out
}

// outboxWriters are the calls that put a domain event in the outbox, and
// therefore cannot commit without the table.
//
// The value is the sentence that says what stops working, because the two are
// not the same failure to the person reading the report: one is a module of the
// framework that publishes on its own, and the other is the application's own
// code choosing to.
// auth.NewService is in the list and auth.New is too, and they are not ranked
// the same -- see outboxWriterRank. NewService is where the Outbox is actually
// constructed, from the repository's handle; New is the registration and builds
// nothing, taking a service somebody else made.
//
// The rule began with the registration alone, and on the reference application
// that put the finding in the wrong file: examples/ wraps auth.New inside
// app/Http/Controllers/Auth/LoginController.go, so the report pointed at a
// controller while its own sentence said to add a line to bootstrap/app.go. A
// finding that names one file and asks for a change in another is one the reader
// has to re-derive, and the file it named is not even wrong -- it is just not
// where anything is missing.
var outboxWriters = map[string]string{
	"auth.NewService": "the auth module publishes auth.user.registered inside the transaction that creates the account, so every sign-up fails",
	"auth.New":        "the auth module publishes auth.user.registered inside the transaction that creates the account, so every sign-up fails",
	// events.NewOutbox is what auth.NewService builds internally and what a
	// module written by hand builds directly. Reaching for it is the same
	// commitment.
	"events.NewOutbox": "this code stores domain events in the same transaction as the write that produced them, so that write fails",
}

// outboxWriterRank decides which call a single finding points at.
//
// A construction beats a registration, because construction is where the
// commitment is made and it sits in the bootstrap, next to the line the reader
// has to add. A registration is kept in the list so that a project handed its
// service from another package is still covered, and ranked below so it is only
// used when there is nothing better.
func outboxWriterRank(call string) int {
	if call == "auth.New" {
		return 1
	}
	return 0
}

// outboxProviders are the registrations that bring the table.
//
// Two, because the relay is registered instead of the plain module when
// something publishes: events.WithRelay carries the same Migrations. A rule that
// knew only NewModule would fire on the more advanced of the two correct
// wirings, which is how a tool teaches people to ignore it.
var outboxProviders = map[string]bool{
	"events.NewModule": true,
	"events.WithRelay": true,
}

// 16. A module whose writes need another module's table.
//
// The compiler cannot see this and neither can any test the application runs
// against an empty database that it also migrated: auth.Register succeeds in
// development and fails in production the first time somebody signs up, with
// "no such table: outbox", on the screen where a 500 costs the most and where
// the person has no way to route around it. The table travels with
// events.NewModule() -- that is the whole reason the events module exists rather
// than the schema being copied into each project's migrations -- and both
// shipped bootstraps register it. An application that deletes the line while
// tidying gets no warning from anything else.
//
// It is an Error rather than the Warning a new rule normally enters as. That
// policy is about rules that reject code which works, and this one cannot: the
// only project it fires on is one where creating an account already fails a
// hundred percent of the time. Reporting that as a warning would mean CI passes
// on an application whose sign-up is broken.
//
// It concludes from an absence -- no registration anywhere in the project -- so
// it says nothing when a file did not parse, for the reason project.blind
// exists: the missing line may be in the file nobody could read.
//
// # The limit, stated rather than implied
//
// It sees the calls written in this project. A project whose modules are
// registered by a helper living in another repository is one this cannot answer
// about, and it reports the writer as unprovided -- the finding names the line to
// add, so the cost of being wrong there is one line read and dismissed. The
// alternative, resolving registrations across module boundaries, is a type
// checker, and doctor deliberately runs on a project that does not compile.
func theOutboxTableTravelsWithWhatWritesToIt(p *project) []Finding {
	type writer struct {
		file   string
		line   int
		call   string
		breaks string
	}

	var writers []writer
	provided := false

	for _, f := range p.files {
		if f.isTest {
			continue
		}
		f.calls(func(call *ast.CallExpr, name string) {
			if outboxProviders[name] {
				provided = true
				return
			}
			breaks, writes := outboxWriters[name]
			if !writes {
				return
			}
			file, line := f.at(call)
			writers = append(writers, writer{file: file, line: line, call: name, breaks: breaks})
		})
	}

	if provided || len(writers) == 0 || len(p.unreadable) > 0 {
		return nil
	}

	// One finding, whatever the project touches the outbox from. The fact is
	// about the project -- there is no outbox table anywhere in it -- and the fix
	// is one line in one file; a report that states it three times, once per call
	// site, is three entries somebody has to read to learn they all say the same
	// thing. The call site chosen is only where the report points, so it is the
	// most useful one: see outboxWriterRank.
	sort.SliceStable(writers, func(i, j int) bool {
		if a, b := outboxWriterRank(writers[i].call), outboxWriterRank(writers[j].call); a != b {
			return a < b
		}
		if writers[i].file != writers[j].file {
			return writers[i].file < writers[j].file
		}
		return writers[i].line < writers[j].line
	})

	w := writers[0]
	return []Finding{{
		Rule: "outbox-not-registered", Severity: Error,
		File: w.file, Line: w.line,
		Message: w.call + " stores domain events and this project registers no outbox table",
		Why: w.breaks + " -- at runtime, with `no such table: outbox`, in front of whoever was using it. " +
			"The table belongs to the events module so that it travels rather than being copied into every project: add events.NewModule() to the k.Register(...) list in bootstrap/app.go, or events.WithRelay(relay) if something already publishes them.",
	}}
}

// sqlStatement is one SQL statement written in a method, as far as doctor can
// read it.
type sqlStatement struct {
	// verb is SELECT, UPDATE or DELETE: the three that carry a predicate.
	verb string
	text string
	node ast.Node
}

// sqlStatements reads the SQL literals of a body.
//
// A query is usually a literal concatenated with a constant holding the column
// list, so the concatenation is flattened and the parts doctor cannot see are
// dropped. That is sound for the question being asked: a column list does not
// contain a WHERE clause, and if the tenant predicate were hidden inside a
// constant this would report a query that is in fact scoped -- which is why the
// message says what it saw.
//
// A statement written entirely in a package-level constant is outside what this
// reads, and saying so is better than implying coverage it does not have.
func sqlStatements(fn *ast.FuncDecl) []sqlStatement {
	if fn.Body == nil {
		return nil
	}

	var out []sqlStatement
	record := func(text string, n ast.Node) {
		if verb, ok := scopedVerb(text); ok {
			out = append(out, sqlStatement{verb: verb, text: text, node: n})
		}
	}

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BinaryExpr:
			if x.Op != token.ADD {
				return true
			}
			text := concatenatedString(x)
			if text == "" {
				return true
			}
			record(text, x)
			return false
		case *ast.BasicLit:
			if text, ok := stringLiteral(x); ok {
				record(text, x)
			}
		}
		return true
	})
	return out
}

// concatenatedString joins the string literals of a `+` chain, separated by a
// space so that two halves never glue into one token. Anything that is not a
// literal contributes nothing: doctor does not resolve constants.
func concatenatedString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.BasicLit:
		if text, ok := stringLiteral(x); ok {
			return text
		}
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			return strings.TrimSpace(concatenatedString(x.X) + " " + concatenatedString(x.Y))
		}
	case *ast.ParenExpr:
		return concatenatedString(x.X)
	}
	return ""
}

// scopedVerb reports which of the three statements that carry a predicate this
// text is.
func scopedVerb(text string) (string, bool) {
	upper := strings.ToUpper(text)
	found, at := "", -1
	for _, verb := range []string{"SELECT ", "UPDATE ", "DELETE "} {
		i := strings.Index(upper, verb)
		if i < 0 || (at >= 0 && i >= at) {
			continue
		}
		found, at = strings.TrimSpace(verb), i
	}
	return found, found != ""
}

// tenantIsInThePredicate reports whether the tenant column is in the WHERE.
//
// After the WHERE, not anywhere in the statement: `SELECT id, tenant_id FROM
// invoices WHERE id = ?` names the column and reads every tenant's rows, which
// is the exact statement this rule exists to find.
func tenantIsInThePredicate(text string) bool {
	lower := strings.ToLower(text)
	where := strings.Index(lower, "where")
	if where < 0 {
		return false
	}
	return strings.Contains(lower[where:], "tenant_id")
}

// resourceNotReauthorized checks that the row read from the database is
// re-authorized before being returned.
//
// The first Authorize produces a Grant from a zero value, and the repository
// call uses it. The second Authorize, with the row that was read, is the
// object-level authorization. A method that skips it compiles and passes all
// other rules -- the Grant was received, checked, and the policy exists -- but
// returns data any user of the same tenant may read.
//
// The rule looks for methods that call Authorize and a repository data method
// (Find, Get, List, Paginate) in the same body, and warns if the Authorize
// calls all come before the last repository call.
func resourceNotReauthorized(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if f.isTest {
			continue
		}
		for _, decl := range f.ast.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Body == nil {
				continue
			}
			file, line := f.at(fn)

			callsAuthorize := funcBodyContains(fn, func(name string) bool {
				return name == "security.Authorize"
			})
			if !callsAuthorize {
				continue
			}

			callsRepoData := funcBodyContains(fn, func(name string) bool {
				return strings.HasSuffix(name, ".Find") ||
					strings.HasSuffix(name, ".Get")
			})
			if !callsRepoData {
				continue
			}

			lastAuthorize, lastRepo := -1, -1
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				name := callName(call)
				pos := f.fset.Position(call.Pos()).Offset
				if name == "security.Authorize" {
					lastAuthorize = pos
				}
				if strings.HasSuffix(name, ".Find") ||
					strings.HasSuffix(name, ".Get") {
					lastRepo = pos
				}
				return true
			})

			if lastAuthorize < lastRepo {
				out = append(out, Finding{
					Rule: "resource-not-reauthorized", Severity: Warning,
					File: file, Line: line,
					Message: fn.Name.Name + " reads a row and does not re-authorize it",
					Why:     "the first Authorize tells whether the caller may look at all. The second, with the row that was read, is the object-level decision: the caller may read this row, and nobody else's. Skipping it means any user of the same tenant sees the row. Call security.Authorize with the entity after the read.",
				})
			}
		}
	}
	return out
}

// 17. What a raw interpolation writes goes to the page as markup.
//
// A view has two interpolation forms and they look almost the same. `{{ }}`
// escapes; `{!! !!}` does not, and is the one place in a view where a string
// becomes HTML. The difference is three characters, and nothing in the source
// says which of the two a given line is entitled to.
//
// A component call is entitled to it. A component is an exported Go function
// that returns template.HTML, and every interpolation inside it was escaped when
// it was generated -- so the markup it returns is markup somebody wrote, not
// markup somebody typed into a form.
//
// An expression that is not a call is a value, and a value here is stored
// cross-site scripting the first time one of them comes from a person. The shape
// is not hypothetical: rendering Markdown produces a string with raw HTML left
// in it, and the line that puts it on the page is `{!! .Body !!}`.
//
// What it cannot see, and it is worth knowing before trusting the report: this
// reads the markup, never the types. A call whose function returns a plain
// string -- Markdown rendered in the view itself -- has the shape of a component
// and passes. A field that already holds template.HTML is a value and does not.
// Separating those two needs the type of the expression, which is why this
// warns rather than fails: it is the shape that is wrong, and the shape is
// evidence, not proof.
func rawOutputIsAComponent(p *project) []Finding {
	var out []Finding
	for _, v := range p.views {
		parsed, err := kyse.Parse(v.rel, v.body)
		if err != nil {
			// A view that does not parse is `aru view:build`'s report, and it
			// makes a better one. Reading the markup of a broken view here would
			// put this rule's name on somebody's syntax error.
			continue
		}
		for _, n := range rawInterpolations(parsed) {
			if isNamedCall(n.Body) {
				continue
			}
			out = append(out, Finding{
				Rule: "raw-output-is-not-a-component", Severity: Warning,
				File: v.rel, Line: n.Line,
				Message: "{!! " + n.Body + " !!} writes a value to the page, not a component",
				Why:     "the raw form escapes nothing, so whatever the expression holds arrives as markup. A component earns that: it is a function returning template.HTML, and what it interpolated was escaped when it was generated. A value has not been through anything -- a bio, a comment or a rendered Markdown body written here is stored cross-site scripting, and it runs for every reader of the page. Write it as {{ " + n.Body + " }}, which escapes, or return it from a component.",
			})
		}
	}
	return out
}

// rawInterpolations collects every `{!! !!}` in a view, wherever it sits.
//
// Sections and the bodies of blocks are walked too: a raw interpolation inside
// @foreach is the one most likely to be a row of somebody's data.
func rawInterpolations(f *kyse.File) []kyse.Node {
	var out []kyse.Node

	var walk func(nodes []kyse.Node)
	walk = func(nodes []kyse.Node) {
		for _, n := range nodes {
			if n.Kind == kyse.Raw {
				out = append(out, n)
			}
			walk(n.Children)
		}
	}

	walk(f.Body)
	for _, s := range f.Sections {
		walk(s.Nodes)
	}
	return out
}

// isNamedCall reports whether the whole expression is a call to a named
// function: a name, optionally qualified by a package, whose argument list opens
// right after it and closes at the end of the expression.
//
// Closing at the END is what makes it the whole expression rather than the start
// of one. `components.Alert(x) + .Body` opens a call and does not end with it,
// and the half that would reach the page unescaped is the half after the plus.
func isNamedCall(expr string) bool {
	expr = strings.TrimSpace(expr)

	i := 0
	for {
		start := i
		for i < len(expr) && isNameByte(expr[i], i > start) {
			i++
		}
		if i == start {
			return false
		}
		if i < len(expr) && expr[i] == '.' {
			i++
			continue
		}
		break
	}
	if i >= len(expr) || expr[i] != '(' {
		return false
	}
	return callClosesAtEnd(expr, i)
}

// isNameByte reports whether a byte may appear in a Go name. A digit is allowed
// everywhere except the first position.
func isNameByte(c byte, inside bool) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		return true
	case c >= '0' && c <= '9':
		return inside
	}
	return false
}

// callClosesAtEnd reports whether the parenthesis at open is closed by the last
// byte of the expression.
//
// String and rune literals are skipped rather than scanned, because a component
// is very often given a label with a parenthesis in it -- "Close (esc)" -- and
// counting that one would read the call as ending three characters early.
func callClosesAtEnd(expr string, open int) bool {
	depth := 0
	for i := open; i < len(expr); i++ {
		switch c := expr[i]; c {
		case '"', '\'', '`':
			end := endOfLiteral(expr, i)
			if end < 0 {
				return false
			}
			i = end
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i == len(expr)-1
			}
		}
	}
	return false
}

// endOfLiteral returns the index of the byte that closes the literal opening at
// start, or -1 when nothing does.
func endOfLiteral(s string, start int) int {
	quote := s[start]
	for i := start + 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			// A raw string has no escapes: the backslash in it is a backslash.
			if quote != '`' {
				i++
			}
		case quote:
			return i
		}
	}
	return -1
}
