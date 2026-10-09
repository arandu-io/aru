package doctor

import (
	"fmt"
	"go/ast"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// The rules in this file are about where code lives, not about what it lets
// through. Each one reports a shape the application tree has an owner for --
// the conversion of input, the answer to a rejected form, the call to another
// system -- written somewhere else, by hand.
//
// They are all warnings. The code they report compiles, runs and is often
// correct today; what it costs is a second way to do something that already
// has one, found by the next person to change it. A warning is the size of
// that claim, and none of them becomes an error without a report from the
// applications that shows it is never wrong.
//
// Each rule says in its own comment what it reads and how far: the function
// (one body, nothing it calls), the file, or the project (every parsed file,
// by name, never by type). Where the honest answer needs a call graph the rule
// says so and reports nothing rather than guessing. A file name or a line
// count tells somebody where to look; neither is evidence on its own, and the
// two rules that count lines or methods say so in their message.
//
// There is no suppression. A real technical exception is a condition of the
// rule, written in the rule.

// controllerFile reports whether a file holds controllers: under
// app/Http/Controllers, at any depth, and not a test.
func controllerFile(f *file) bool {
	return !f.isTest && f.category == "Controllers" && strings.HasPrefix(f.rel, "app/Http/Controllers/")
}

// serviceFile reports whether a file is under app/Services, at any depth, and
// not a test.
func serviceFile(f *file) bool {
	return !f.isTest && strings.HasPrefix(f.rel, "app/Services/")
}

// applicationFile reports whether a file is code the application wrote and
// runs: app/, bootstrap/, routes/, config/, cmd/ and main.go, never a test.
//
// The compiled views under storage/ are generated, the fixtures under tests/
// are tests, and database/ holds migrations and seeders, which each rule that
// reads it names on its own.
func applicationFile(f *file) bool {
	if f.isTest {
		return false
	}
	if f.rel == "main.go" {
		return true
	}
	for _, dir := range []string{"app/", "bootstrap/", "routes/", "config/", "cmd/"} {
		if strings.HasPrefix(f.rel, dir) {
			return true
		}
	}
	return false
}

// callUse is one call of interest inside a function, kept for the message.
type callUse struct {
	node ast.Node
	what string
}

// summarize renders the calls a finding names, in the order they first
// appear, with a count when one repeats: "ctx.Input (x3), ctx.Request.FormValue".
func summarize(uses []callUse) string {
	var order []string
	count := map[string]int{}
	for _, u := range uses {
		if count[u.what] == 0 {
			order = append(order, u.what)
		}
		count[u.what]++
	}
	parts := make([]string, 0, len(order))
	for _, what := range order {
		if n := count[what]; n > 1 {
			parts = append(parts, fmt.Sprintf("%s (x%d)", what, n))
			continue
		}
		parts = append(parts, what)
	}
	return strings.Join(parts, ", ")
}

// funcLabel names a declared function for a message: Store, or
// NoteController.Store when it has a receiver.
func funcLabel(fn *ast.FuncDecl) string {
	if recv := receiverType(fn); recv != "" {
		return recv + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// callsIn collects, in source order, the calls in a function body that pick
// answers with a description.
func callsIn(fn *ast.FuncDecl, pick func(call *ast.CallExpr, name string) (string, bool)) []callUse {
	if fn.Body == nil {
		return nil
	}
	var out []callUse
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if what, ok := pick(call, callName(call)); ok {
			out = append(out, callUse{node: call, what: what})
		}
		return true
	})
	return out
}

// importedName reports whether expr is a selector on an import of one of
// the paths, and answers the path and the selected name.
func (f *file) importedName(expr ast.Expr, match func(path string) bool) (string, string, bool) {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return "", "", false
	}
	x, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", "", false
	}
	imported, ok := f.importPath(x.Name)
	if !ok || !match(imported) {
		return "", "", false
	}
	return imported, sel.Sel.Name, true
}

// isPath answers a matcher for exactly one import path.
func isPath(want string) func(string) bool {
	return func(p string) bool { return p == want }
}

// 20. The controller converts the request with ctx.Bind, and only with it.
//
// Reason. Binding fills the request struct the service validates, and an
// error in conversion comes back as validation.Errors the router already
// answers. A controller that reads field by field builds a second request
// type in its body, one the service, a job and a command cannot share, and
// whose missing field is an empty string rather than a message.
//
// Scope. Every function declared in a file under app/Http/Controllers,
// tests excluded, the published authentication screens included.
//
// Severity. A warning: the code it reports works.
//
// Positive: `ctx.Input("name")`, `ctx.Request.FormValue("name")`,
// `r.PostFormValue("x")`, `ctx.Request.ParseForm()`, `r.ParseMultipartForm(n)`
// and `json.NewDecoder(...)` from encoding/json. One finding per function, at
// the first read, naming every one.
//
// Negative: `ctx.Bind(&in)`; `ctx.Param("id")`, which is the route and not
// the form; `ctx.Query`, which reads one query parameter and is left to the
// pagination and filter code that needs it.
//
// Known false positive: a method called Input or FormValue on a type of the
// application's own with one argument reads the same.
//
// Limit. Local to the function: it reads the calls in one body by name and
// not by type. A helper in another package that reads the form for the
// controller is not followed. `ctx.Request.PostForm` read as a field is not
// reported.
//
// Correction: declare the fields on the request struct with `form:"..."`
// tags and call ctx.Bind(&in).
func inputIsBoundNotRead(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if !controllerFile(f) {
			continue
		}
		f.functions(func(fn *ast.FuncDecl) {
			uses := callsIn(fn, func(call *ast.CallExpr, name string) (string, bool) {
				switch {
				case strings.HasSuffix(name, ".Input") && len(call.Args) == 1,
					strings.HasSuffix(name, ".FormValue") && len(call.Args) == 1,
					strings.HasSuffix(name, ".PostFormValue") && len(call.Args) == 1,
					strings.HasSuffix(name, ".ParseForm") && len(call.Args) == 0,
					strings.HasSuffix(name, ".ParseMultipartForm") && len(call.Args) == 1:
					return name, true
				}
				if _, sym, ok := f.importedName(call.Fun, isPath("encoding/json")); ok && sym == "NewDecoder" {
					return name, true
				}
				return "", false
			})
			if len(uses) == 0 {
				return
			}
			out = append(out, Finding{
				Rule: "input-read-by-hand", Severity: Warning,
				File: f.rel, Line: f.line(uses[0].node),
				Message: funcLabel(fn) + " reads the request by hand: " + summarize(uses),
				Why: "a field read here is a second request type that the service, a job and a command cannot share, " +
					"and a field that is missing arrives as an empty string instead of a message. " +
					"Declare the fields on the struct in app/Http/Requests with form tags and call ctx.Bind(&in).",
			})
		})
	}
	return out
}

// 21. The service validates the request, not the controller.
//
// Reason. A job or a command that calls the same service has no controller in
// front of it. Validation called by the controller is validation that path
// skips, and the second caller writes it again or goes without it.
//
// Scope. Every function declared in a file under app/Http/Controllers, tests
// excluded.
//
// Severity. A warning.
//
// Positive: `in.Validate()`, `form.Validate()` -- a method called Validate
// with no argument -- and `validation.Validate(x)` from the validation
// package of the framework or of hesape.
//
// Negative: the same call in app/Services; a method named Validated or
// ValidateToken.
//
// Known false positive: a Validate() with no argument on something that is
// not a request -- a query value, a token -- is read the same, because the
// doctor does not resolve types.
//
// Limit. Local to the function, by name.
//
// Correction: pass the bound request to the service and call Validate() first
// thing in the service method; return validation.Errors from there.
func theServiceValidates(p *project) []Finding {
	isValidation := func(p string) bool {
		return p == "github.com/arandu-io/framework/validation" || p == "github.com/arandu-io/hesape/validation"
	}
	var out []Finding
	for _, f := range p.files {
		if !controllerFile(f) {
			continue
		}
		f.functions(func(fn *ast.FuncDecl) {
			uses := callsIn(fn, func(call *ast.CallExpr, name string) (string, bool) {
				if _, sym, ok := f.importedName(call.Fun, isValidation); ok {
					return name, sym == "Validate"
				}
				return name, strings.HasSuffix(name, ".Validate") && len(call.Args) == 0
			})
			if len(uses) == 0 {
				return
			}
			out = append(out, Finding{
				Rule: "validate-called-by-controller", Severity: Warning,
				File: f.rel, Line: f.line(uses[0].node),
				Message: funcLabel(fn) + " validates the request itself: " + summarize(uses),
				Why: "a job or a command that calls the same service has no controller in front of it, so it skips " +
					"this validation or repeats it. Pass the bound request to the service and call Validate() at the " +
					"start of the service method; the router answers the validation.Errors it returns.",
			})
		})
	}
	return out
}

// 22. JSON goes out through a resource and errors through problem+json, never
// by hand.
//
// Reason. A controller that encodes onto the ResponseWriter or writes its own
// status picks a shape for the body and for the error that the router and
// every other endpoint do not share, and a client learns one format per
// handler.
//
// Scope. Every function declared in a file under app/Http/Controllers, tests
// excluded.
//
// Severity. A warning.
//
// Positive: `json.NewEncoder(...)` from encoding/json, and a
// `.WriteHeader(code)` call in a function that also calls json.Marshal or
// names a JSON media type. One finding per function.
//
// Negative: `ctx.JSON(status, resource)`; json.Marshal into a value that is
// then handed to a view; WriteHeader in a handler that streams a file, with no
// JSON in it.
//
// Known false positive: an encoder writing to a buffer that never reaches the
// response is read the same.
//
// Limit. Local to the function, by name.
//
// Correction: return ctx.JSON with a JsonResource for domain data, and return
// the error: the router writes problem+json for a client that asked for JSON.
func jsonIsWrittenByTheFramework(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if !controllerFile(f) {
			continue
		}
		f.functions(func(fn *ast.FuncDecl) {
			json := writesJSON(f, fn)
			uses := callsIn(fn, func(call *ast.CallExpr, name string) (string, bool) {
				if _, sym, ok := f.importedName(call.Fun, isPath("encoding/json")); ok {
					return name, sym == "NewEncoder"
				}
				return name, json && strings.HasSuffix(name, ".WriteHeader") && len(call.Args) == 1
			})
			if len(uses) == 0 {
				return
			}
			out = append(out, Finding{
				Rule: "json-written-by-hand", Severity: Warning,
				File: f.rel, Line: f.line(uses[0].node),
				Message: funcLabel(fn) + " writes the response by hand: " + summarize(uses),
				Why: "the body and the error format chosen here are this handler's alone, so a client learns one shape per " +
					"endpoint and an error escapes the problem+json the router writes. Return ctx.JSON with a JsonResource, " +
					"and return the error instead of writing it.",
			})
		})
	}
	return out
}

// writesJSON reports whether a function produces JSON by itself: it calls
// json.Marshal or json.MarshalIndent from encoding/json, or names a JSON media
// type in a string literal.
func writesJSON(f *file, fn *ast.FuncDecl) bool {
	if fn.Body == nil {
		return false
	}
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if _, sym, ok := f.importedName(x.Fun, isPath("encoding/json")); ok && (sym == "Marshal" || sym == "MarshalIndent") {
				found = true
			}
		case *ast.BasicLit:
			if text, ok := stringLiteral(x); ok && strings.Contains(strings.ToLower(text), "json") {
				found = true
			}
		}
		return !found
	})
	return found
}

// 23. A rejected form is answered by the router, with a redirect.
//
// Reason. The router turns validation.Errors into a redirect back with what
// was typed and the messages in the flash. A controller that answers 422
// itself is a second path: htmx discards a 422 swap by default, and a reload
// of that answer submits the form again.
//
// Scope. Every function declared in a file under app/Http/Controllers, tests
// excluded.
//
// Severity. A warning.
//
// Positive: http.StatusUnprocessableEntity from net/http, or the literal 422,
// anywhere in the body except a comparison or a case. One finding per
// function, at the first.
//
// Negative: `if status == http.StatusUnprocessableEntity`; a function that
// receives the status as a parameter -- the caller that passes 422 is the one
// reported.
//
// Known false positive: 422 written for a client that asked for JSON, which
// the router already answers with problem+json.
//
// Limit. Local to the function. A constant of the application's own holding
// 422 is not resolved.
//
// Correction: return the validation.Errors and let the router answer; the
// form page reads what was typed and the messages from the flash.
func aRejectedFormIsARedirect(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if !controllerFile(f) {
			continue
		}
		f.functions(func(fn *ast.FuncDecl) {
			if fn.Body == nil {
				return
			}
			compared := map[ast.Node]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.BinaryExpr:
					if x.Op == token.EQL || x.Op == token.NEQ {
						compared[x.X], compared[x.Y] = true, true
					}
				case *ast.CaseClause:
					for _, e := range x.List {
						compared[e] = true
					}
				}
				return true
			})
			var first ast.Node
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if first != nil {
					return false
				}
				e, ok := n.(ast.Expr)
				if !ok || compared[n] {
					return true
				}
				if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.INT && lit.Value == "422" {
					first = n
					return false
				}
				if _, sym, ok := f.importedName(e, isPath("net/http")); ok && sym == "StatusUnprocessableEntity" {
					first = n
					return false
				}
				return true
			})
			if first == nil {
				return
			}
			out = append(out, Finding{
				Rule: "invalid-form-answered-by-hand", Severity: Warning,
				File: f.rel, Line: f.line(first),
				Message: funcLabel(fn) + " answers a rejected form with 422 itself",
				Why: "htmx discards a 422 swap by default, so the person sees the form unchanged, and a reload of the " +
					"answer submits the form again. Return the validation.Errors: the router redirects back with what " +
					"was typed and the messages in the flash.",
			})
		})
	}
	return out
}

// 24. The route guard loads the session; the controller reads ctx.User().
//
// Reason. RequireAuth loads the session once and puts the subject on the
// request. A controller that loads it again keeps a session store of its own,
// decides by itself what an expired session answers, and is the place a
// second notion of who is signed in starts.
//
// Scope. Every function declared in a file under app/Http/Controllers,
// except app/Http/Controllers/Auth: signing in, out and the second factor
// are where a session is created and read before any guard can.
//
// Severity. A warning.
//
// Positive: `c.sessions.Load(ctx.Ctx(), ctx.Request)`, `sessions.Load(...)`
// -- a call named Load whose receiver is named after a session -- and a Load
// from the hesape session package.
//
// Negative: `ctx.User()`; a Load on something else, `c.cache.Load(key)`.
//
// Known false positive: a field named sessions that is not a session store.
//
// Limit. Local to the function, by the name of the receiver: a store held in
// a field called store is not seen.
//
// Correction: put the route behind the auth guard and read who is asking with
// ctx.User().
func theSessionIsLoadedByTheGuard(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if !controllerFile(f) || strings.HasPrefix(f.rel, "app/Http/Controllers/Auth/") {
			continue
		}
		f.functions(func(fn *ast.FuncDecl) {
			uses := callsIn(fn, func(call *ast.CallExpr, name string) (string, bool) {
				if _, sym, ok := f.importedName(call.Fun, isPath("github.com/arandu-io/hesape/session")); ok {
					return name, sym == "Load"
				}
				receiver, method, found := cutLast(name)
				if !found || method != "Load" {
					return "", false
				}
				_, last, _ := cutLast(receiver)
				if last == "" {
					last = receiver
				}
				return name, strings.Contains(strings.ToLower(last), "session")
			})
			if len(uses) == 0 {
				return
			}
			out = append(out, Finding{
				Rule: "session-loaded-in-controller", Severity: Warning,
				File: f.rel, Line: f.line(uses[0].node),
				Message: funcLabel(fn) + " loads the session itself: " + summarize(uses),
				Why: "the route guard already loaded the session and put the subject on the request, so this is a second " +
					"place that decides who is signed in and what an expired session answers. Put the route behind the " +
					"auth guard and read the subject with ctx.User().",
			})
		})
	}
	return out
}

// cutLast splits a rendered call at its last dot: "c.sessions.Load" is
// "c.sessions" and "Load".
func cutLast(name string) (string, string, bool) {
	i := strings.LastIndex(name, ".")
	if i < 0 {
		return "", name, false
	}
	return name[:i], name[i+1:], true
}

// 25. A redirect names a route.
//
// Reason. A path written in a controller is a second copy of the route table:
// the day the route moves or gains a prefix, the redirect still compiles and
// sends the person to a 404.
//
// Scope. Every call in a file under app/Http/Controllers, tests excluded.
//
// Severity. A warning.
//
// Positive: `ctx.Redirect("/notes")`, `http.Redirect(w, r, "/login", 303)`,
// and a concatenation that starts with a literal path,
// `ctx.Redirect("/notes/" + id)`. One finding per call.
//
// Negative: `ctx.RedirectRoute("notes.show", id)`; `ctx.Redirect(next)` with
// a value; an absolute URL to another site.
//
// Known false positive: a path that is not a route of this application, such
// as a static file.
//
// Limit. Local to the call: a path held in a variable or a constant is not
// followed.
//
// Correction: ctx.RedirectRoute with the route's name, or ctx.URL for a link.
func redirectsNameARoute(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if !controllerFile(f) {
			continue
		}
		f.calls(func(call *ast.CallExpr, name string) {
			if !strings.HasSuffix(name, "Redirect") && name != "Redirect" {
				return
			}
			for _, arg := range call.Args {
				text := concatenatedString(arg)
				if bin, ok := arg.(*ast.BinaryExpr); ok {
					text = leadingLiteral(bin)
				}
				if !strings.HasPrefix(text, "/") || strings.HasPrefix(text, "//") {
					continue
				}
				out = append(out, Finding{
					Rule: "redirect-to-literal-path", Severity: Warning,
					File: f.rel, Line: f.line(call),
					Message: "this redirect is written as the path " + strconv.Quote(text),
					Why: "a path written here is a second copy of the route table: when the route moves or gains a prefix, " +
						"this still compiles and sends the person to a 404. Use ctx.RedirectRoute with the route's name.",
				})
				return
			}
		})
	}
	return out
}

// leadingLiteral is the literal a `+` chain opens with, or empty.
func leadingLiteral(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			return leadingLiteral(x.X)
		}
	case *ast.ParenExpr:
		return leadingLiteral(x.X)
	case *ast.BasicLit:
		if text, ok := stringLiteral(x); ok {
			return text
		}
	}
	return ""
}

// 26. Markup is a view.
//
// Reason. A page is a .kyse.go view with typed data and the escaping the view
// compiler writes. html/template under app/ is a second template engine
// beside it, and template.HTML is the value that skips escaping.
//
// Scope. Every file under app/, tests excluded.
//
// Severity. A warning.
//
// Positive: `import "html/template"`. One finding per file, at the import.
//
// Negative: text/template, which renders text and not markup; html/template
// imported outside app/.
//
// Known false positive: none known; a file that imports it to name a type in
// a signature it does not otherwise use is still reported.
//
// Limit. Local to the file: the import is the finding.
//
// Correction: render a view, or a component from kyse for a piece of markup.
func markupIsAView(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if f.isTest || !strings.HasPrefix(f.rel, "app/") {
			continue
		}
		for _, imp := range f.ast.Imports {
			if strings.Trim(imp.Path.Value, `"`) != "html/template" {
				continue
			}
			out = append(out, Finding{
				Rule: "html-template-in-app", Severity: Warning,
				File: f.rel, Line: f.line(imp),
				Message: "this file imports html/template",
				Why: "a page is a view with typed data and the escaping the view compiler writes; html/template here is " +
					"a second template engine, and template.HTML is the value that skips escaping. Render a view, or a " +
					"kyse component for a piece of markup.",
			})
		}
	}
	return out
}

// httpPackage reports whether an import path carries the HTTP request, the
// response, the session or the HTTP context: net/http and its subpackages,
// framework/http, hesape/http except its outgoing client, and hesape/session.
func httpPackage(p string) bool {
	switch {
	case p == "net/http", strings.HasPrefix(p, "net/http/"):
		return true
	case p == "github.com/arandu-io/framework/http", strings.HasPrefix(p, "github.com/arandu-io/framework/http/"):
		return true
	case p == "github.com/arandu-io/hesape/http/client", strings.HasPrefix(p, "github.com/arandu-io/hesape/http/client/"):
		return false
	case p == "github.com/arandu-io/hesape/http", strings.HasPrefix(p, "github.com/arandu-io/hesape/http/"):
		return true
	case p == "github.com/arandu-io/hesape/session":
		return true
	}
	return false
}

// pureHTTPNames are the names of net/http that read no request and write no
// response: functions of bytes and strings.
var pureHTTPNames = map[string]bool{
	"DetectContentType": true, "CanonicalHeaderKey": true, "TimeFormat": true, "ParseTime": true,
}

// outgoingHTTPNames are the names of net/http a call to another system is
// written with. In a file that calls out, a Request or a Header is the one it
// sends, which is client-outside-clients' to report.
var outgoingHTTPNames = map[string]bool{
	"Client": true, "DefaultClient": true, "Get": true, "Post": true, "Head": true, "PostForm": true,
	"NewRequest": true, "NewRequestWithContext": true, "Request": true, "Response": true, "Header": true,
	"Transport": true, "DefaultTransport": true, "RoundTripper": true, "NoBody": true, "ErrUseLastResponse": true,
}

// 27. A service speaks requests and models, never HTTP.
//
// Reason. A job, a command, a listener and a tool call the same service with
// no request in hand. A service that takes the request, the response writer,
// the HTTP context, the session or a cookie can only be called from a
// controller, and the rule it holds is out of reach of everything else.
//
// Scope. Every file under app/Services, tests excluded.
//
// Severity. A warning.
//
// Positive: any name from net/http, framework/http, hesape/http or
// hesape/session other than a status or method constant -- *http.Request,
// http.ResponseWriter, *http.Context, http.Cookie -- and template.HTML from
// html/template. One finding per file and import, at the first use, naming
// every one.
//
// Negative: `http.StatusTooManyRequests` returned by the HTTPStatus method of
// a domain error, which is how a domain error says its status;
// `http.MethodPost`; http.DetectContentType and the other functions of bytes
// and strings; hesape/http/client, which belongs to a client; and, in a file
// that calls another system, the request it sends and the response it reads
// -- client-outside-clients reports that file, and one finding is enough.
//
// Known false positive: an HTTP type named in a function that is not a
// service method -- a resolver type kept in the directory -- is reported the
// same, because it is the directory and not the method that decides.
//
// Limit. Local to the file, by import and selector.
//
// Correction: take the request struct and the subject as arguments; return a
// model, a collection or an error with HTTPStatus() and let the router map it.
func servicesSpeakNoHTTP(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if !serviceFile(f) {
			continue
		}
		type use struct {
			first ast.Node
			names []string
			seen  map[string]bool
		}
		uses := map[string]*use{}
		var order []string
		callsOut := len(outgoingCalls(f)) > 0
		ast.Inspect(f.ast, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			imported, name, ok := f.importedName(sel, func(p string) bool { return httpPackage(p) || p == "html/template" })
			if !ok {
				return true
			}
			if imported == "html/template" && name != "HTML" {
				return true
			}
			if strings.HasPrefix(name, "Status") || strings.HasPrefix(name, "Method") || pureHTTPNames[name] {
				return true
			}
			if callsOut && imported == "net/http" && outgoingHTTPNames[name] {
				return true
			}
			u := uses[imported]
			if u == nil {
				u = &use{first: sel, seen: map[string]bool{}}
				uses[imported] = u
				order = append(order, imported)
			}
			if !u.seen[name] {
				u.seen[name] = true
				u.names = append(u.names, path.Base(imported)+"."+name)
			}
			return true
		})
		for _, imported := range order {
			u := uses[imported]
			out = append(out, Finding{
				Rule: "service-takes-http", Severity: Warning,
				File: f.rel, Line: f.line(u.first),
				Message: "this service names " + strings.Join(u.names, ", ") + " from " + imported,
				Why: "a job, a command, a listener or a tool calls a service with no request in hand, so a service " +
					"that takes the request, the response, the session or a cookie can only be reached from a controller. " +
					"Take the request struct and the subject as arguments, and return an error with HTTPStatus() for the router to map.",
			})
		}
	}
	return out
}

// 28. app/Services is one directory.
//
// Reason. A service is one per aggregate or family of use cases, beside the
// others. A subpackage is a second tree of ownership inside the first --
// engines, ports, workflows -- whose rules nobody looking in app/Services
// finds, and which each application invents differently.
//
// Scope. Every directory under app/Services that holds a Go file.
//
// Severity. A warning, and a pointer rather than a proof: the directory says
// where to look.
//
// Positive: app/Services/Billing/billing.go. One finding per directory, at
// its first file.
//
// Negative: app/Services/BillingService.go.
//
// Known false positive: none; a directory of data files with no Go in it is
// not read.
//
// Limit. Local to the file tree.
//
// Correction: an external system goes to app/Clients; an engine that wraps
// another technology, or a client useful to more than one project, is a
// module; a state machine is the entity's rule plus jobs; what remains is
// one service file per aggregate in app/Services.
func servicesAreFlat(p *project) []Finding {
	first := map[string]*file{}
	for _, f := range p.files {
		if !strings.HasPrefix(f.rel, "app/Services/") || f.dir == "app/Services" {
			continue
		}
		if seen, ok := first[f.dir]; !ok || f.rel < seen.rel {
			first[f.dir] = f
		}
	}
	dirs := make([]string, 0, len(first))
	for dir := range first {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	var out []Finding
	for _, dir := range dirs {
		out = append(out, Finding{
			Rule: "service-subpackage", Severity: Warning,
			File: first[dir].rel, Line: 1,
			Message: dir + " is a package inside app/Services",
			Why: "a service is one file per aggregate beside the others; a subpackage is a second tree of ownership whose " +
				"rules nobody looking in app/Services finds. An external system goes to app/Clients, an engine or a reusable " +
				"client is a module, and a state machine is the entity's rule driven by jobs.",
		})
	}
	return out
}

// serviceFileLimit is the length past which a service file is reported. It is
// a starting point measured on the skeleton and the applications, revised
// with their reports.
const serviceFileLimit = 600

// 29. A service file a person can read.
//
// Reason. A service of two thousand lines is several services sharing a
// file: one per aggregate is the shape, and length is where the second
// aggregate shows first.
//
// Scope. Every file under app/Services, tests excluded.
//
// Severity. A warning of priority, never more: a line count says where to
// look and proves nothing on its own.
//
// Positive: a file of more than 600 lines. One finding per file, at line 1.
//
// Negative: a file of 600 lines or fewer, however much it does.
//
// Known false positive: a long file that is one aggregate with long
// comments.
//
// Limit. Local to the file: the count is the parser's line count.
//
// Correction: split by aggregate or family of use cases; move the entity's
// rules into the model's custom block and an external call into a client.
func serviceFilesStayReadable(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if !serviceFile(f) {
			continue
		}
		tf := f.fset.File(f.ast.Pos())
		if tf == nil || tf.LineCount() <= serviceFileLimit {
			continue
		}
		out = append(out, Finding{
			Rule: "service-file-too-large", Severity: Warning,
			File: f.rel, Line: 1,
			Message: fmt.Sprintf("this service file has %d lines, past the %d this report starts at", tf.LineCount(), serviceFileLimit),
			Why: "a line count is where to look, not proof of a problem: a service this long is usually several aggregates " +
				"sharing one file. Split it by aggregate, move the entity's rules into the model's custom block, and move " +
				"calls to another system into a client.",
		})
	}
	return out
}

// controllerActionLimit is the number of actions past which a controller is
// reported: the seven of a resource, and room for named actions beside them.
const controllerActionLimit = 12

// isAction reports whether a method has the shape of a route handler: one
// *Context and an error back, or a ResponseWriter and a *Request.
func isAction(fn *ast.FuncDecl) bool {
	if fn.Recv == nil || !fn.Name.IsExported() || fn.Type.Params == nil {
		return false
	}
	var params []ast.Expr
	for _, field := range fn.Type.Params.List {
		n := len(field.Names)
		if n == 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			params = append(params, field.Type)
		}
	}
	switch len(params) {
	case 1:
		star, ok := params[0].(*ast.StarExpr)
		if !ok {
			return false
		}
		sel, ok := star.X.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Context" {
			return false
		}
		results := fn.Type.Results
		if results == nil || len(results.List) != 1 {
			return false
		}
		id, ok := results.List[0].Type.(*ast.Ident)
		return ok && id.Name == "error"
	case 2:
		w, ok := params[0].(*ast.SelectorExpr)
		if !ok || w.Sel.Name != "ResponseWriter" {
			return false
		}
		star, ok := params[1].(*ast.StarExpr)
		if !ok {
			return false
		}
		r, ok := star.X.(*ast.SelectorExpr)
		return ok && r.Sel.Name == "Request"
	}
	return false
}

// 30. A controller is one resource.
//
// Reason. The seven actions of a resource and a few named ones fit in one
// controller. Past that, the controller is several resources registered by
// hand, and the routes, the policies and the views of each are found only by
// reading all of it.
//
// Scope. Every controller type under app/Http/Controllers: its exported
// methods with the shape of a handler, across every file of its package.
//
// Severity. A warning of priority: the count says where to look.
//
// Positive: a type with more than 12 such methods. One finding per type, at
// its declaration.
//
// Negative: 12 or fewer; an unexported helper; an exported method that is not
// a handler.
//
// Known false positive: a method of the handler shape that is never routed.
//
// Limit. Local to the package, by declaration: a handler built by a method
// that returns a func is not counted.
//
// Correction: split by resource -- nested resources, a singleton, an
// invokable controller for an action that stands alone.
func controllersStaySmall(p *project) []Finding {
	type key struct{ dir, typ string }
	count := map[key]int{}
	where := map[key]Finding{}
	for _, f := range p.files {
		if !controllerFile(f) {
			continue
		}
		f.functions(func(fn *ast.FuncDecl) {
			if !isAction(fn) {
				return
			}
			k := key{f.dir, receiverType(fn)}
			count[k]++
			if _, ok := where[k]; !ok {
				where[k] = Finding{File: f.rel, Line: f.line(fn)}
			}
		})
	}
	for _, f := range p.files {
		if !controllerFile(f) {
			continue
		}
		f.types(func(ts *ast.TypeSpec) {
			k := key{f.dir, ts.Name.Name}
			if _, ok := count[k]; ok {
				where[k] = Finding{File: f.rel, Line: f.line(ts)}
			}
		})
	}
	keys := make([]key, 0, len(count))
	for k, n := range count {
		if n > controllerActionLimit {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].dir != keys[j].dir {
			return keys[i].dir < keys[j].dir
		}
		return keys[i].typ < keys[j].typ
	})
	var out []Finding
	for _, k := range keys {
		at := where[k]
		out = append(out, Finding{
			Rule: "controller-too-many-actions", Severity: Warning,
			File: at.File, Line: at.Line,
			Message: fmt.Sprintf("%s has %d actions, past the %d this report starts at", k.typ, count[k], controllerActionLimit),
			Why: "a count is where to look, not proof: a controller this size is usually several resources registered by " +
				"hand, whose routes, policies and views are found only by reading all of it. Split it by resource -- " +
				"a nested resource, a singleton, or an invokable controller for an action that stands alone.",
		})
	}
	return out
}

// readsAFormField reports whether a call reads one field of the request by
// name: ctx.Input, FormValue, PostFormValue, or Form.Get and PostForm.Get.
func readsAFormField(call *ast.CallExpr, name string) bool {
	if len(call.Args) != 1 {
		return false
	}
	for _, suffix := range []string{".Input", ".FormValue", ".PostFormValue", ".PostForm.Get", ".Form.Get"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// 31. A form field does not choose the operation.
//
// Reason. A POST whose handler switches on a submitted field to call one
// service method or another is several actions behind one route: one policy
// check per branch written by hand, one name in the route table for all of
// them, and an operation a client picks by editing a hidden input.
//
// Scope. Every function declared in a file under app/Http/Controllers, tests
// excluded.
//
// Severity. A warning.
//
// Positive: `switch ctx.Input("op") { case "publish": c.svc.Publish(...); case
// "archive": c.svc.Archive(...) }`, and the same switch on a variable the
// function assigned from such a read. One finding per switch.
//
// Negative: a switch on a route parameter or an argument; a switch on a form
// field whose branches call one method, or none.
//
// Known false positive: a switch on a field that selects a presentation and
// calls two read methods.
//
// Limit. Local to the function. Telling a switch that picks an operation from
// one that picks a filter would need the call graph of every branch, so the
// rule asks only for two different methods on fields of the receiver --
// c.svc.Publish and c.svc.Archive -- in two branches, and stays silent
// otherwise. An if-else chain on the field is not read.
//
// Correction: one named action per operation, r.ResourceAction(http.MethodPost,
// "notes", "publish", c.Publish), each with its own route and policy action.
func operationsAreRoutes(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if !controllerFile(f) {
			continue
		}
		f.functions(func(fn *ast.FuncDecl) {
			if fn.Body == nil {
				return
			}
			recv := receiverName(fn)
			fromForm := map[string]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.AssignStmt:
					for i, rhs := range x.Rhs {
						call, ok := rhs.(*ast.CallExpr)
						if !ok || i >= len(x.Lhs) || !readsAFormField(call, callName(call)) {
							continue
						}
						if id, ok := x.Lhs[i].(*ast.Ident); ok {
							fromForm[id.Name] = true
						}
					}
				case *ast.ValueSpec:
					for i, v := range x.Values {
						call, ok := v.(*ast.CallExpr)
						if ok && i < len(x.Names) && readsAFormField(call, callName(call)) {
							fromForm[x.Names[i].Name] = true
						}
					}
				}
				return true
			})
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sw, ok := n.(*ast.SwitchStmt)
				if !ok || sw.Tag == nil {
					return true
				}
				chosen := false
				switch tag := sw.Tag.(type) {
				case *ast.CallExpr:
					chosen = readsAFormField(tag, callName(tag))
				case *ast.Ident:
					chosen = fromForm[tag.Name]
				}
				if !chosen || recv == "" {
					return true
				}
				methods := map[string]bool{}
				branches := 0
				for _, stmt := range sw.Body.List {
					clause, ok := stmt.(*ast.CaseClause)
					if !ok {
						continue
					}
					found := false
					for _, s := range clause.Body {
						ast.Inspect(s, func(n ast.Node) bool {
							call, ok := n.(*ast.CallExpr)
							if !ok {
								return true
							}
							parts := strings.Split(callName(call), ".")
							if len(parts) == 3 && parts[0] == recv {
								methods[parts[1]+"."+parts[2]] = true
								found = true
							}
							return true
						})
					}
					if found {
						branches++
					}
				}
				if branches < 2 || len(methods) < 2 {
					return true
				}
				names := make([]string, 0, len(methods))
				for m := range methods {
					names = append(names, m)
				}
				sort.Strings(names)
				out = append(out, Finding{
					Rule: "operation-chosen-by-form-field", Severity: Warning,
					File: f.rel, Line: f.line(sw),
					Message: funcLabel(fn) + " picks the operation from a form field: " + strings.Join(names, ", "),
					Why: "each branch is an action without a route of its own: one name in the route table for all of them, " +
						"the authorization of each written by hand, and an operation the client picks by editing a hidden " +
						"input. Give each operation its own named action with r.ResourceAction.",
				})
				return true
			})
		})
	}
	return out
}

// 32. Another system is reached through a client.
//
// Reason. A call to a payment gateway, a registry or a messaging API is a
// contract with someone else: its configuration, its retries, its errors and
// the fake the tests use. Written inside a service or a controller, each one
// invents that contract again, and a test reaches the network.
//
// Scope. Every file under app/ except app/Clients, tests excluded.
//
// Severity. A warning.
//
// Positive: an http.Client built from net/http, http.DefaultClient,
// http.Get, http.Post, http.Head, http.PostForm, http.NewRequest and
// http.NewRequestWithContext, and any import of hesape/http/client. One
// finding per file, at the first, naming every one.
//
// Negative: the same in app/Clients/<Vendor>Client.go; bootstrap building
// the http.Client a client receives.
//
// Known false positive: a request built to be served in-process, not sent.
//
// Limit. Local to the file. A client that a module hands over is outside the
// project and not read.
//
// Correction: app/Clients/<Vendor>Client.go with a constructor taking typed
// config, a small interface for the service and a fake beside it; a client
// useful to more than one project is a module.
func externalCallsGoThroughAClient(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if f.isTest || !strings.HasPrefix(f.rel, "app/") || strings.HasPrefix(f.rel, "app/Clients/") {
			continue
		}
		uses := outgoingCalls(f)
		if len(uses) == 0 {
			continue
		}
		out = append(out, Finding{
			Rule: "client-outside-clients", Severity: Warning,
			File: f.rel, Line: f.line(uses[0].node),
			Message: "this file calls out over HTTP outside app/Clients: " + summarize(uses),
			Why: "a call to another system carries a contract -- configuration, retries, errors, and the fake the tests " +
				"use -- and written here it is invented again and a test reaches the network. Move it to " +
				"app/Clients/<Vendor>Client.go with an interface and a fake, or to a module when more than one project needs it.",
		})
	}
	return out
}

// outgoingCalls are the places a file calls another system over HTTP, in
// source order: an import of hesape/http/client, and the names of net/http a
// request is sent with.
func outgoingCalls(f *file) []callUse {
	outgoing := map[string]bool{
		"Client": true, "DefaultClient": true, "Get": true, "Post": true, "Head": true,
		"PostForm": true, "NewRequest": true, "NewRequestWithContext": true,
	}
	var uses []callUse
	for _, imp := range f.ast.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		if p == "github.com/arandu-io/hesape/http/client" || strings.HasPrefix(p, "github.com/arandu-io/hesape/http/client/") {
			uses = append(uses, callUse{node: imp, what: p})
		}
	}
	ast.Inspect(f.ast, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if _, name, ok := f.importedName(sel, isPath("net/http")); ok && outgoing[name] {
			uses = append(uses, callUse{node: sel, what: exprName(sel)})
		}
		return true
	})
	sort.SliceStable(uses, func(i, j int) bool { return uses[i].node.Pos() < uses[j].node.Pos() })
	return uses
}

// customRegions are the line ranges between `arandu:begin custom` and
// `arandu:end custom` in a file.
func customRegions(f *file) [][2]int {
	var out [][2]int
	open := -1
	for _, group := range f.ast.Comments {
		for _, c := range group.List {
			text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
			line := f.fset.Position(c.Pos()).Line
			switch {
			case strings.HasPrefix(text, "arandu:begin custom"):
				open = line
			case strings.HasPrefix(text, "arandu:end custom") && open >= 0:
				out = append(out, [2]int{open, line})
				open = -1
			}
		}
	}
	return out
}

// ioPackage reports whether an import path reaches a database or a network.
func ioPackage(p string) bool {
	switch {
	case p == "database/sql", strings.HasPrefix(p, "database/sql/"),
		p == "net", p == "net/http", strings.HasPrefix(p, "net/http/"),
		p == "github.com/arandu-io/framework/data",
		p == "github.com/arandu-io/hesape/database", strings.HasPrefix(p, "github.com/arandu-io/hesape/database/"),
		p == "github.com/arandu-io/hesape/http", strings.HasPrefix(p, "github.com/arandu-io/hesape/http/"):
		return true
	}
	return false
}

// 33. The entity's own rules are pure.
//
// Reason. The custom block of a model holds the entity's invariants, derived
// values and transitions: methods that change only the fields of the row.
// One that reaches the database or the network is a service hiding in the
// model, and one that reads the clock is a rule no test can pin to a date.
//
// Scope. Every function declared inside an `arandu:begin custom` block of a
// file under app/Models, tests excluded, except init -- which registers the
// relations -- and the methods of a <Entity>Query type, which are scopes and
// are the query by design.
//
// Severity. A warning.
//
// Positive: a method that names database/sql, net, net/http, framework/data,
// hesape/database or hesape/http, or calls time.Now, time.Since or
// time.Until. One finding per function, at the first.
//
// Negative: `func (n Note) CanPublish(now time.Time) bool`, with the time as
// a parameter; a scope on *NoteQuery.
//
// Known false positive: a type from one of those packages named only to be
// returned unchanged.
//
// Limit. Local to the function: a call to a helper of the package that reads
// the clock is not followed.
//
// Correction: take the time as a parameter, and move the read or the write to
// the service, which calls the rule.
func entityRulesArePure(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if f.isTest || f.category != "Models" {
			continue
		}
		regions := customRegions(f)
		if len(regions) == 0 {
			continue
		}
		f.functions(func(fn *ast.FuncDecl) {
			if fn.Name.Name == "init" && fn.Recv == nil {
				return
			}
			if strings.HasSuffix(receiverType(fn), "Query") {
				return
			}
			line := f.line(fn)
			inside := false
			for _, r := range regions {
				inside = inside || (line > r[0] && line < r[1])
			}
			if !inside {
				return
			}
			var first ast.Node
			var what string
			ast.Inspect(fn, func(n ast.Node) bool {
				if first != nil {
					return false
				}
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if imported, name, ok := f.importedName(sel, ioPackage); ok {
					first, what = sel, path.Base(imported)+"."+name+" from "+imported
					return false
				}
				if _, name, ok := f.importedName(sel, isPath("time")); ok && (name == "Now" || name == "Since" || name == "Until") {
					first, what = sel, "time."+name
					return false
				}
				return true
			})
			if first == nil {
				return
			}
			out = append(out, Finding{
				Rule: "model-rule-touches-io", Severity: Warning,
				File: f.rel, Line: f.line(first),
				Message: funcLabel(fn) + " is an entity rule and reaches " + what,
				Why: "a rule of the entity changes only the fields of the row; one that reaches the database or the network " +
					"is a service hiding in the model, and one that reads the clock cannot be tested against a date. Take the " +
					"time as a parameter and let the service read, write and call.",
			})
		})
	}
	return out
}

// 34. A fragment is a partial.
//
// Reason. A fragment is a piece of a page answered to an element that asked
// for it. Kept in resources/views/partials and included by the page, the
// page and the fragment render the same template; a page view answered as a
// fragment is a whole document swapped into an element, or a page whose
// status was chosen by hand.
//
// Scope. Every call in the application's Go, tests excluded.
//
// Severity. A warning.
//
// Positive: `ctx.Fragment(http.StatusOK, "notes.index", data)`. One finding
// per call.
//
// Negative: `ctx.Fragment(http.StatusOK, "partials.note-row", data)`; a view
// name held in a variable.
//
// Known false positive: a view outside partials/ that has no @extends and is
// only ever a fragment, which still belongs in partials/.
//
// Limit. Local to the call: only a literal name is read.
//
// Correction: move the fragment to resources/views/partials/, include it from
// the page with @include, and answer pages with ctx.View.
func fragmentsArePartials(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if !applicationFile(f) {
			continue
		}
		f.calls(func(call *ast.CallExpr, name string) {
			if !strings.HasSuffix(name, ".Fragment") || len(call.Args) != 3 {
				return
			}
			view, ok := stringLiteral(call.Args[1])
			if !ok || strings.HasPrefix(view, "partials.") {
				return
			}
			out = append(out, Finding{
				Rule: "fragment-without-partial", Severity: Warning,
				File: f.rel, Line: f.line(call),
				Message: "ctx.Fragment answers " + strconv.Quote(view) + ", which is not in resources/views/partials",
				Why: "a fragment is a piece a page includes, so page and fragment render one template; a view outside " +
					"partials/ answered as a fragment is a whole page swapped into an element, or a status chosen by hand. " +
					"Move it to resources/views/partials/ and @include it, and answer pages with ctx.View.",
			})
		})
	}
	return out
}

// camelWords splits an identifier into its words, keeping an acronym whole:
// NormalizeBRLInput is Normalize, BRL, Input.
func camelWords(name string) []string {
	runes := []rune(name)
	var out []string
	start := 0
	for i := 1; i < len(runes); i++ {
		prev, cur := runes[i-1], runes[i]
		next := rune(0)
		if i+1 < len(runes) {
			next = runes[i+1]
		}
		switch {
		case cur == '_':
			if start < i {
				out = append(out, string(runes[start:i]))
			}
			start = i + 1
		case unicode.IsLower(prev) && unicode.IsUpper(cur),
			unicode.IsUpper(prev) && unicode.IsUpper(cur) && unicode.IsLower(next),
			unicode.IsLetter(prev) && unicode.IsDigit(cur):
			if start < i {
				out = append(out, string(runes[start:i]))
			}
			start = i
		}
	}
	if start < len(runes) {
		out = append(out, string(runes[start:]))
	}
	return out
}

// documentVerbs are the words that make a function about CPF or CNPJ a
// validator or a formatter.
var documentVerbs = map[string]bool{
	"valid": true, "validate": true, "validation": true, "validator": true, "is": true,
	"verify": true, "check": true, "checksum": true, "format": true, "formatted": true,
	"mask": true, "masked": true, "normalize": true, "normalise": true, "normalized": true,
	"clean": true, "strip": true, "parse": true, "digit": true, "digits": true,
}

// reimplementedHelper answers what a function's name says it reimplements,
// or empty.
func reimplementedHelper(fn *ast.FuncDecl) string {
	name := fn.Name.Name
	switch strings.ToLower(name) {
	case "slug", "slugify", "toslug", "makeslug", "generateslug":
		if fn.Recv == nil {
			return "a slug"
		}
	}
	words := camelWords(name)
	lower := make([]string, len(words))
	for i, w := range words {
		lower[i] = strings.ToLower(w)
	}
	for _, w := range lower {
		if w == "brl" {
			return "a BRL amount"
		}
	}
	for _, w := range lower {
		if w != "cpf" && w != "cnpj" {
			continue
		}
		if len(lower) == 1 && fn.Recv == nil {
			return "a " + strings.ToUpper(w)
		}
		for _, v := range lower {
			if documentVerbs[v] {
				return "a " + strings.ToUpper(w)
			}
		}
	}
	return ""
}

// 35. A helper comes from the catalog.
//
// Reason. A slug is str.Slug in hesape, and CPF, CNPJ and BRL are the
// Brazilian module's. A copy in the application is one more implementation
// whose edge cases -- the accent, the check digit, the rounding of a cent --
// are fixed in one place and wrong in the others.
//
// Scope. Every function and method declared under app/, tests excluded.
//
// Severity. A warning.
//
// Positive: a function named Slugify, Slug, ToSlug, MakeSlug or GenerateSlug;
// any name with the word BRL; a name with the word CPF or CNPJ alone or beside
// a validating or formatting word -- ValidCPF, FormatCNPJ, cpfCheckDigit,
// NormalizeCPF. One finding per function.
//
// Negative: a method Slug() or CPF() on an entity, which answers its own
// field; PublishedBySlug; establishmentByCNPJ, which looks a record up.
//
// Known false positive: a function about BRL that is not a format or a parse.
//
// Limit. Local to the declaration: the name is what is read, never the body,
// so a slug written under another name is not seen.
//
// Correction: call str.Slug, number.Currency, or the hyz-is/arandu-br module
// for CPF, CNPJ and BRL, and delete the copy.
func helpersComeFromTheCatalog(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if f.isTest || !strings.HasPrefix(f.rel, "app/") {
			continue
		}
		f.functions(func(fn *ast.FuncDecl) {
			what := reimplementedHelper(fn)
			if what == "" {
				return
			}
			out = append(out, Finding{
				Rule: "helper-reimplemented", Severity: Warning,
				File: f.rel, Line: f.line(fn),
				Message: funcLabel(fn) + " reimplements " + what + " in the application",
				Why: "the catalog already has this -- str.Slug in hesape, and the hyz-is/arandu-br module for CPF, CNPJ and " +
					"BRL -- and a copy is one more place where the accent, the check digit or the rounding of a cent is " +
					"fixed in one implementation and wrong in the other. Call the catalog's and delete this one.",
			})
		})
	}
	return out
}

// 36. SQL lives in a repository.
//
// Reason. A statement written in a service, a controller or bootstrap is a
// query nobody looking for the reads of a table finds, and the tenant
// predicate, the Grant on the read and the pagination are each written again
// by hand next to it.
//
// Scope. Every function body in the application's Go except under
// app/Repositories and database/, tests excluded.
//
// Severity. A warning.
//
// Positive: a body that calls Query, QueryRow, Exec or Prepare (or the
// Context form of each) and holds a SELECT, INSERT, UPDATE or DELETE literal.
// One finding per body, at the statement.
//
// Negative: the same body in app/Repositories; a migration under database/;
// a body that runs DDL, which the migration rules read.
//
// Known false positive: a method named Exec or Query on a type of the
// application's own, next to a SQL literal it never runs.
//
// Limit. Local to the body: a statement in a package-level constant, or
// assembled in another function, is not seen.
//
// Correction: the generated Model and Builder for CRUD, and
// app/Repositories/<Name>Repository.go, with the Grant, for anything the
// model does not express.
func sqlLivesInRepositories(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if !applicationFile(f) || strings.HasPrefix(f.rel, "app/Repositories/") {
			continue
		}
		f.functionBodies(func(body *ast.BlockStmt) {
			runs := false
			ast.Inspect(body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || runs {
					return !runs
				}
				name := callName(call)
				if databaseCall(name) && len(call.Args) > 0 && !strings.HasSuffix(name, ".Begin") && !strings.HasSuffix(name, ".BeginTx") {
					runs = true
				}
				return !runs
			})
			if !runs {
				return
			}
			statements := sqlStatementsIn(body, statementVerb)
			if len(statements) == 0 {
				return
			}
			out = append(out, Finding{
				Rule: "raw-sql-outside-repository", Severity: Warning,
				File: f.rel, Line: f.line(statements[0].node),
				Message: "a " + statements[0].verb + " statement runs outside app/Repositories",
				Why: "a statement here is a read or a write of the table that nobody looking in app/Repositories finds, and " +
					"its tenant predicate and Grant are written again by hand beside it. Use the generated model, or " +
					"app/Repositories/<Name>Repository.go with the Grant for what the model does not express.",
			})
		})
	}
	return out
}

// 37. What a generator wrote is wired.
//
// Reason. A controller or a service that nothing constructs is code the
// router never reaches: its tests pass, it shows up in every search, and a
// change to it changes nothing the person sees.
//
// Scope. Every exported function named New* declared in a file under
// app/Http/Controllers or app/Services, tests excluded; references read in
// every other parsed file, tests excluded.
//
// Severity. A warning.
//
// Positive: NewInvoiceService, declared and named nowhere else.
//
// Negative: a constructor bootstrap/app.go or routes/web.go calls; one
// another constructor calls, which is wiring the rule cannot judge; a test
// double -- Fake, Stub, Mock, Spy or Dummy is a word of the constructor, of
// the type it returns or of its file -- that a _test.go file constructs. A
// double exists for the tests, and a test calling it is the wiring it has.
//
// Formerly false: NewCieloFakeAdapter in app/Services, constructed only by
// tests/Unit/FakeAdapters_test.go, was reported as unwired.
//
// Known false positive: a constructor kept for a caller in another module.
//
// Limit. The project, by name and not by type: a different function with the
// same name anywhere counts as a reference, so a collision hides a finding
// rather than inventing one. A test double is recognised by its name; one
// named like production code and called only from tests is still reported,
// which is the case the rule exists for: a service whose only caller is its
// own test.
//
// Correction: paste the wiring make:module printed into bootstrap/app.go and
// routes/web.go, or delete the code nothing reaches.
func constructorsAreWired(p *project) []Finding {
	type ctor struct {
		f  *file
		fn *ast.FuncDecl
	}
	var ctors []ctor
	for _, f := range p.files {
		if f.isTest || !(strings.HasPrefix(f.rel, "app/Http/Controllers/") || strings.HasPrefix(f.rel, "app/Services/")) {
			continue
		}
		f.functions(func(fn *ast.FuncDecl) {
			if fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "New") && fn.Name.IsExported() {
				ctors = append(ctors, ctor{f, fn})
			}
		})
	}
	if len(ctors) == 0 {
		return nil
	}
	referenced := map[string]bool{}
	tested := map[string]bool{}
	declared := map[*ast.Ident]bool{}
	for _, c := range ctors {
		declared[c.fn.Name] = true
	}
	for _, f := range p.files {
		into := referenced
		if f.isTest {
			into = tested
		}
		ast.Inspect(f.ast, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && !declared[id] {
				into[id.Name] = true
			}
			return true
		})
	}
	var out []Finding
	for _, c := range ctors {
		if referenced[c.fn.Name.Name] || len(p.unreadable) > 0 {
			continue
		}
		if tested[c.fn.Name.Name] && isTestDouble(c.f, c.fn) {
			continue
		}
		out = append(out, Finding{
			Rule: "generated-not-wired", Severity: Warning,
			File: c.f.rel, Line: c.f.line(c.fn),
			Message: c.fn.Name.Name + " is never called: bootstrap and routes do not construct it, and nothing else does",
			Why: "code nothing constructs is code the router never reaches: its tests pass and a change to it changes " +
				"nothing anybody sees. Paste the wiring make:module printed into bootstrap/app.go and routes/web.go, or " +
				"delete what nothing reaches.",
		})
	}
	return out
}

// testDoubleWords are the words that name a stand-in a test constructs in
// place of the real thing.
// They are compared in lower case, so stubGateway, an unexported type, counts.
var testDoubleWords = map[string]bool{"fake": true, "stub": true, "mock": true, "spy": true, "dummy": true}

// isTestDouble reports whether a constructor builds a test double: one of
// testDoubleWords is a word of its name, of the type its first result names,
// or of the file it is declared in.
func isTestDouble(f *file, fn *ast.FuncDecl) bool {
	names := []string{fn.Name.Name, strings.TrimSuffix(path.Base(f.rel), ".go")}
	if fn.Type.Results != nil && len(fn.Type.Results.List) > 0 {
		result := fn.Type.Results.List[0].Type
		if star, ok := result.(*ast.StarExpr); ok {
			result = star.X
		}
		switch t := result.(type) {
		case *ast.Ident:
			names = append(names, t.Name)
		case *ast.SelectorExpr:
			names = append(names, t.Sel.Name)
		}
	}
	for _, name := range names {
		for _, word := range camelWords(name) {
			if testDoubleWords[strings.ToLower(word)] {
				return true
			}
		}
	}
	return false
}

// chosenByTheCode reports whether the value of Roles or Actions names roles
// or actions the code picked: a literal, a non-empty list, or a name from the
// application's policies or from the permission module.
func (f *file) chosenByTheCode(e ast.Expr) bool {
	chosen := false
	ast.Inspect(e, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BasicLit:
			chosen = true
		case *ast.CompositeLit:
			if len(x.Elts) > 0 {
				chosen = true
			}
		case *ast.SelectorExpr:
			if _, _, ok := f.importedName(x, func(p string) bool {
				return strings.HasSuffix(strings.ToLower(p), "/app/policies") ||
					strings.HasPrefix(p, "github.com/hyz-is/arandu-permission")
			}); ok {
				chosen = true
			}
		}
		return !chosen
	})
	return chosen
}

// 38. A subject carries the access that was stored for it.
//
// Reason. What a subject may do comes from the roles stored for the account,
// filled by the permission module, and is decided by the policies. A Subject
// literal that writes its own roles or actions is access granted by the
// line of code that builds it, which no screen shows and no administrator can
// revoke.
//
// Scope. Every composite literal of Subject from framework/security or
// hesape/auth in app/, bootstrap/, routes/, config/, cmd/ and main.go. Tests
// and database/ -- seeders and factories, which build fixtures -- are not
// read.
//
// Severity. A warning.
//
// Positive: `security.Subject{ID: "system", Roles: []string{"admin"}}`,
// `auth.Subject{Actions: policies.AllActions()}`. One finding per literal.
//
// Negative: `security.Subject{ID: u.ID, Roles: append([]string(nil), u.Roles...)}`
// in the model that derives the subject from the stored account; a literal
// with no Roles and no Actions.
//
// Known false positive: a system actor whose roles are themselves stored and
// read back through a constant.
//
// Limit. Local to the literal: a Roles field assigned after the literal is
// built, and a value held in a variable, are not followed.
//
// Correction: derive the subject from the stored account -- User.Subject() --
// and let arandu-permission fill its roles; a job runs with the Grant the
// service issued, not with a subject of its own.
func subjectsComeFromStoredAccess(p *project) []Finding {
	isSubjectPackage := func(p string) bool {
		return p == "github.com/arandu-io/framework/security" || p == "github.com/arandu-io/hesape/auth"
	}
	var out []Finding
	for _, f := range p.files {
		if !applicationFile(f) {
			continue
		}
		ast.Inspect(f.ast, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if _, name, ok := f.importedName(lit.Type, isSubjectPackage); !ok || name != "Subject" {
				return true
			}
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || (key.Name != "Roles" && key.Name != "Actions") || !f.chosenByTheCode(kv.Value) {
					continue
				}
				out = append(out, Finding{
					Rule: "subject-built-by-hand", Severity: Warning,
					File: f.rel, Line: f.line(lit),
					Message: "this Subject is given its " + key.Name + " by the code that builds it",
					Why: "access written into a literal is granted by this line: no permission screen shows it and no " +
						"administrator can revoke it. Derive the subject from the stored account and let arandu-permission " +
						"fill its roles; a background task runs with the Grant its service issued.",
				})
				return true
			}
			return true
		})
	}
	return out
}
