package doctor

import (
	"go/ast"
	"strings"
)

// rules is the whole check surface. Each one exists because something real can
// go wrong that the compiler cannot see -- and each message says what breaks,
// not which rule was violated.
//
// Adding a rule that rejects existing code is a breaking change (doc 23): it
// enters as a warning in a minor and becomes an error in the next major.
var rules = []func([]*file) []Finding{
	repositoryNeedsPolicy,
	repositoryMethodNeedsGrant,
	policyMustBeOpened,
	handlerMustNotReachData,
	tenantMustComeFromTheGrant,
	systemGrantIsAudited,
	noBuiltSQL,
	sensitiveFieldNeedsRedaction,
	moduleMustNotImportModule,
	sessionMustRotateOnLogin,
}

// 1. A repository without a policy in the same module means the entity is
// reachable and nobody decided who may reach it.
func repositoryNeedsPolicy(files []*file) []Finding {
	hasPolicy := map[string]bool{}
	for _, f := range files {
		if f.module == "" || f.isTest {
			continue
		}
		for _, decl := range f.ast.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gen.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok && strings.HasSuffix(ts.Name.Name, "Policy") {
					hasPolicy[f.module] = true
				}
			}
		}
	}

	var out []Finding
	for _, f := range files {
		if f.module == "" || f.isTest || hasPolicy[f.module] {
			continue
		}
		if !strings.HasSuffix(f.rel, ".repo.go") && !strings.Contains(f.rel, "repo") {
			continue
		}
		out = append(out, Finding{
			Rule: "repository-without-policy", Severity: Error,
			File: f.rel, Line: 1,
			Message: "module " + f.module + " has a repository and no policy",
			Why:     "the entity is reachable and nobody decided who may reach it. Run `aru make:policy` or write the Policy type in this module.",
		})
		break
	}
	return out
}

// 2. Every repository method must call g.Check before touching the handle. The
// signature forces the Grant to be passed; only this checks it was verified.
func repositoryMethodNeedsGrant(files []*file) []Finding {
	var out []Finding
	for _, f := range files {
		if f.isTest {
			continue
		}
		for _, decl := range f.ast.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil {
				continue
			}
			if !strings.Contains(receiverType(fn), "Repo") || !ast.IsExported(fn.Name.Name) {
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

// 3. A generated policy denies everything. Left that way, the module is dead
// code -- and worse, it looks finished.
func policyMustBeOpened(files []*file) []Finding {
	var out []Finding
	for _, f := range files {
		if f.isTest || f.module == "" {
			continue
		}
		for _, decl := range f.ast.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "Can" || !strings.Contains(receiverType(fn), "Policy") {
				continue
			}
			if returnsNil(fn) {
				continue
			}
			file, line := f.at(fn)
			out = append(out, Finding{
				Rule: "policy-never-opened", Severity: Warning,
				File: file, Line: line,
				Message: "the policy of " + f.module + " denies every action",
				Why:     "this is how `aru make:module` generates it, on purpose. Open what the module needs inside the custom block -- until then every request to it is refused.",
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

// 4. A handler that reaches the data package is a handler that skipped the
// service, and therefore the policy.
func handlerMustNotReachData(files []*file) []Finding {
	var out []Finding
	for _, f := range files {
		if f.isTest || !strings.HasSuffix(f.rel, "handlers.go") {
			continue
		}
		for path := range f.imports {
			if !strings.HasSuffix(path, "/framework/data") {
				continue
			}
			// data.Query is the pagination type and belongs in a handler; the
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
				Message: "this handler uses the data package beyond data.Query",
				Why:     "a handler that reaches the database skipped the service, and therefore the policy. Move the call into the service, where the Grant is issued.",
			})
			break
		}
	}
	return out
}

// 5. A tenant taken from the request is the client choosing which data to read.
func tenantMustComeFromTheGrant(files []*file) []Finding {
	var out []Finding
	for _, f := range files {
		if f.isTest {
			continue
		}
		f.calls(func(call *ast.CallExpr, name string) {
			switch name {
			case "r.PathValue", "r.FormValue", "r.PostFormValue", "req.PathValue", "req.FormValue":
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
	}
	return out
}

// 6. SystemGrant is the one way past the policy. Its call sites are the audit.
func systemGrantIsAudited(files []*file) []Finding {
	var out []Finding
	for _, f := range files {
		if f.isTest {
			continue
		}
		// Seeders, jobs and commands are where it legitimately belongs, and so is
		// a method whose name says it is seeding -- EnsureAdmin, SeedDemo. The
		// file path alone would flag every module that offers a seeding entry
		// point, and a warning that fires on correct code gets muted.
		legitimateFile := strings.Contains(f.rel, "seeder") ||
			strings.Contains(f.rel, "/jobs/") ||
			strings.Contains(f.rel, "cmd/")

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

// 7. SQL built with Sprintf or concatenation of a variable is injection, whatever
// the intent was.
func noBuiltSQL(files []*file) []Finding {
	var out []Finding
	for _, f := range files {
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

// 8. An entity with a secret in it needs to refuse to serialize itself, or the
// first Dump publishes it on the debug page.
func sensitiveFieldNeedsRedaction(files []*file) []Finding {
	sensitive := []string{"password", "secret", "token", "document", "apikey", "api_key", "creditcard", "cpf", "cnpj"}

	var out []Finding
	for _, f := range files {
		if f.isTest || f.module == "" {
			continue
		}
		for _, decl := range f.ast.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok || st.Fields == nil {
					continue
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
				if found == "" || hasRedaction(files, f.module, ts.Name.Name) {
					continue
				}
				file, line := f.at(ts)
				out = append(out, Finding{
					Rule: "sensitive-field-not-redacted", Severity: Warning,
					File: file, Line: line,
					Message: ts.Name.Name + " holds " + found + " and does not redact itself",
					Why:     "one observability.Dump or one log line publishes it on the debug page. Add LogValue() slog.Value and MarshalJSON to the type, so no caller has to remember.",
				})
			}
		}
	}
	return out
}

func hasRedaction(files []*file, module, typeName string) bool {
	for _, f := range files {
		if f.module != module {
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

// 9. A module importing another module directly is the coupling that makes a
// module stop being publishable.
func moduleMustNotImportModule(files []*file) []Finding {
	var out []Finding
	for _, f := range files {
		if f.module == "" || f.isTest {
			continue
		}
		for path := range f.imports {
			i := strings.Index(path, "/modules/")
			if i < 0 {
				continue
			}
			other := strings.TrimPrefix(path[i+len("/modules/"):], "/")
			if other == f.module || other == "" {
				continue
			}
			// The auth module is the framework's own, and being able to read the
			// subject is what every module needs.
			if strings.Contains(path, "/framework/modules/") {
				continue
			}
			out = append(out, Finding{
				Rule: "module-imports-module", Severity: Warning,
				File: f.rel, Line: 1,
				Message: "module " + f.module + " imports module " + other + " directly",
				Why:     "a module is a unit of distribution, and this one no longer travels alone. Talk to it through an interface this module declares, or move the shared type out of both.",
			})
			break
		}
	}
	return out
}

// 10. Login without rotating the session id is session fixation: an attacker
// plants a known id and inherits the session after the victim signs in.
func sessionMustRotateOnLogin(files []*file) []Finding {
	var out []Finding
	for _, f := range files {
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
