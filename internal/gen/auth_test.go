package gen_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/gen"
)

func authSpec() gen.Module {
	return gen.Module{Name: "authui", ModulePath: "example.test/project"}
}

// TestAuthGolden holds the starter kit to the same standard as the generator:
// the same input produces the same bytes, and a change to the templates shows up
// as a diff in review rather than as a surprise in someone's project.
func TestAuthGolden(t *testing.T) {
	files, err := gen.GenerateAuth(authSpec())
	if err != nil {
		t.Fatalf("GenerateAuth: %v", err)
	}
	// Two controllers, HomeController, and the nine views.
	// Three controllers, page.go, and the nine views.
	if len(files) != 13 {
		t.Fatalf("generated %d files, want 13", len(files))
	}

	for _, f := range files {
		golden := filepath.Join("testdata", "auth", filepath.Base(f.Path)+".golden")
		if *update {
			if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(golden, f.Content, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("reading the golden file: %v (run go test ./internal/gen -update)", err)
		}
		if !bytes.Equal(want, f.Content) {
			t.Errorf("%s differs from the golden file", f.Path)
		}
	}
}

// TestTheGeneratedGoParses is the cheap half of the compilation check: it runs
// everywhere, including in CI, where the sibling checkouts do not exist.
func TestTheGeneratedGoParses(t *testing.T) {
	files, err := gen.GenerateAuth(authSpec())
	if err != nil {
		t.Fatalf("GenerateAuth: %v", err)
	}
	for _, f := range files {
		// A .kyse.go is a view, not Go: everything below the package clause is
		// markup, which is exactly why the Go parser refuses it.
		if !strings.HasSuffix(f.Path, ".go") || strings.HasSuffix(f.Path, ".kyse.go") {
			continue
		}
		if _, err := parser.ParseFile(token.NewFileSet(), f.Path, f.Content, parser.AllErrors); err != nil {
			t.Errorf("%s does not parse: %v", f.Path, err)
		}
	}
}

// TestTheGeneratedTemplateIsNotFormatted: a .templ is not Go, and running it
// through gofmt would corrupt it. This is the regression guard for the day
// somebody makes renderRaw call format.Source unconditionally.
func TestTheGeneratedTemplateIsNotFormatted(t *testing.T) {
	files, err := gen.GenerateAuth(authSpec())
	if err != nil {
		t.Fatalf("GenerateAuth: %v", err)
	}
	for _, f := range files {
		if !strings.HasSuffix(filepath.ToSlash(f.Path), "auth/login.kyse.go") {
			continue
		}
		// A view reaches the disk as written. It is not gofmt'd, because
		// everything below the package clause is markup and gofmt would refuse
		// it -- the build tag is what keeps the Go compiler out.
		if !bytes.Contains(f.Content, []byte("//go:build kyse")) {
			t.Error("the view has no build tag: the Go compiler would try to parse the markup")
		}
		if !bytes.Contains(f.Content, []byte("@extends('layouts.app')")) {
			t.Error("the login view does not extend the layout")
		}
		return
	}
	t.Fatal("auth/login.kyse.go was not generated")
}

// TestTheLoginScreenRotatesTheSession: keeping the pre-login session id is
// session fixation. The framework's own handler does this, and a starter kit that
// people copy from has to do it too -- `aru doctor` checks for the call.
func TestTheLoginScreenRotatesTheSession(t *testing.T) {
	handlers := authFile(t, "LoginController_handlers.go")

	if !strings.Contains(handlers, "sessions.Rotate(") {
		t.Error("the login handler does not rotate the session: this is session fixation")
	}
	if !strings.Contains(handlers, "sessions.Destroy(") {
		t.Error("logout does not destroy the session on the server")
	}
}

// TestTheFailureMessageDoesNotEnumerateAccounts: telling the person which half
// was wrong turns the login endpoint into a list of which emails exist.
func TestTheFailureMessageDoesNotEnumerateAccounts(t *testing.T) {
	handlers := authFile(t, "LoginController_handlers.go")

	for _, leak := range []string{"no such user", "user not found", "unknown email", "wrong password"} {
		if strings.Contains(strings.ToLower(handlers), leak) {
			t.Errorf("the rejection message says %q, which tells an attacker whether the email exists", leak)
		}
	}
	if strings.Count(handlers, `"invalid email or password"`) != 1 {
		t.Error("the two failure paths do not share one message")
	}
}

// TestTheFormCarriesAFreshToken: the fragment that comes back after a rejection
// has to bring a usable CSRF token, or the second attempt fails the check for
// reasons nobody can see from the browser.
func TestTheFormCarriesAFreshToken(t *testing.T) {
	handlers := authFile(t, "LoginController_handlers.go")
	views := authFile(t, "auth/login.kyse.go")

	if !strings.Contains(handlers, "csrf.Issue(") {
		t.Error("the rejection path does not issue a token")
	}
	// @csrf is the directive; it compiles to the hidden input with the token.
	if !strings.Contains(views, "@csrf") {
		t.Error("the form has no CSRF field")
	}
	if !strings.Contains(views, `hx-swap="outerHTML"`) || !strings.Contains(views, `hx-target="this"`) {
		t.Error("the form does not replace itself: the swapped-in markup would keep the stale token")
	}
	if strings.Contains(views, "form.Password") {
		t.Error("the password is echoed back into the form")
	}
}

// TestTheScreenDoesNotReachTheDatabase: a handler that imports the data package
// is the shape `aru doctor` rejects, and the starter kit is what people copy.
func TestTheScreenDoesNotReachTheDatabase(t *testing.T) {
	for _, name := range []string{"Auth/LoginController.go", "LoginController_handlers.go", "auth/login.kyse.go"} {
		content := authFile(t, name)
		for _, forbidden := range []string{`"database/sql"`, "framework/data"} {
			if strings.Contains(content, forbidden) {
				t.Errorf("%s imports %s: the screen talks to the service, never to the database", name, forbidden)
			}
		}
	}
}

// TestTheTenantDoesNotComeFromTheRequestBody is RULE 14 applied to the one place
// where the tenant legitimately does not come from a Grant -- login, where there
// is no session yet. It has to come from the resolver the application wired.
func TestTheTenantDoesNotComeFromTheRequestBody(t *testing.T) {
	handlers := authFile(t, "LoginController_handlers.go")

	if !strings.Contains(handlers, "m.tenant(r)") {
		t.Error("the tenant does not come from the resolver")
	}
	for _, bad := range []string{`PostFormValue("tenant")`, `Header.Get("X-Tenant`, `Query().Get("tenant")`} {
		if strings.Contains(handlers, bad) {
			t.Errorf("the tenant is read from the request (%s): anyone could pick which tenant to authenticate against", bad)
		}
	}
}

// TestTheStarterKitDoesNotMigrate: the users table belongs to the framework's
// auth module. Two modules migrating one table is how a schema ends up with two
// owners and a rollout that deadlocks.
func TestTheStarterKitDoesNotMigrate(t *testing.T) {
	if strings.Contains(authFile(t, "Auth/LoginController.go"), "Migrations()") {
		t.Error("the starter kit declares migrations: the users table already has an owner")
	}
	// The starter kit stopped being a module and became controllers in the
	// project's own tree (ADR 0019), so there is no manifest to declare
	// migrations = false. What the rule protects is unchanged and now
	// structural: it emits no migration at all.
	for _, f := range mustGenerateAuth(t) {
		if strings.Contains(filepath.ToSlash(f.Path), "database/migrations") {
			t.Errorf("the starter kit emitted a migration: %s", f.Path)
		}
	}
}

// TestTheKitComposesTheFrameworksAuthModule is the other half of the rule above,
// and the half that was missing.
//
// The kit declaring no migration is right. The kit being registered *in place
// of* the framework's auth module while declaring no migration was not: the
// wiring the command prints takes auth.New out of the list, so the only module
// that owned the users table left with it. `aru migrate` then applied outbox,
// dead_letter and jobs and no users -- no table, no seeded administrator, no
// login, which is the entire point of the kit.
//
// The fix is composition: the generated Module embeds the framework's, so one
// registration keeps one owner for the table and one handler for the path. This
// test reads the declaration rather than the prose, because prose does not
// migrate anything.
func TestTheKitComposesTheFrameworksAuthModule(t *testing.T) {
	source := authFile(t, "Auth/LoginController.go")

	file, err := parser.ParseFile(token.NewFileSet(), "LoginController.go", source, parser.AllErrors)
	if err != nil {
		t.Fatalf("the generated controller does not parse: %v", err)
	}

	declared := typeSpec(t, file, "Module")
	structType, ok := declared.Type.(*ast.StructType)
	if !ok {
		t.Fatalf("Module is not a struct: %T", declared.Type)
	}

	var embedded []string
	for _, field := range structType.Fields.List {
		if len(field.Names) > 0 {
			continue
		}
		embedded = append(embedded, types.ExprString(field.Type))
	}
	if !slices.Contains(embedded, "*auth.Module") {
		t.Errorf("the kit's Module does not embed *auth.Module, so registering it in place of auth.New "+
			"leaves the users table with no owner; it embeds %v", embedded)
	}

	// And the delegation is load-bearing, not incidental: the generated file
	// says so at compile time, in the project, the moment somebody drops it.
	if !strings.Contains(source, "kernel.Migratable = (*Module)(nil)") {
		t.Error("the kit does not prove at compile time that it still carries the framework's schema")
	}
}

// TestTheKitBuildsTheModuleItEmbeds: an embedded pointer left nil is a nil
// dereference at the first migration, which is worse than the missing table it
// replaced.
func TestTheKitBuildsTheModuleItEmbeds(t *testing.T) {
	source := authFile(t, "Auth/LoginController.go")

	file, err := parser.ParseFile(token.NewFileSet(), "LoginController.go", source, parser.AllErrors)
	if err != nil {
		t.Fatalf("the generated controller does not parse: %v", err)
	}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Name.Name != "New" {
			continue
		}
		var built bool
		ast.Inspect(fn, func(n ast.Node) bool {
			kv, ok := n.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || key.Name != "Module" {
				return true
			}
			built = strings.HasPrefix(types.ExprString(kv.Value), "auth.New(")
			return false
		})
		if !built {
			t.Errorf("New does not fill the embedded module with auth.New:\n%s", source)
		}
		return
	}
	t.Fatal("the generated controller declares no New")
}

// typeSpec finds a top-level type declaration by name.
func typeSpec(t *testing.T, file *ast.File, name string) *ast.TypeSpec {
	t.Helper()
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, s := range gd.Specs {
			if ts, ok := s.(*ast.TypeSpec); ok && ts.Name.Name == name {
				return ts
			}
		}
	}
	t.Fatalf("the file declares no type %s", name)
	return nil
}

// TestTheStarterKitLandsInTheLaravelTree: the nine views at the nine paths
// `laravel/ui` uses, and the controller where Laravel puts it.
//
// The command used to write four files into modules/authui/ and declare itself
// with a manifest. It is not a module any more -- it is the project's own code,
// in the project's own tree (ADR 0019), so there is nothing to declare.
func TestTheStarterKitLandsInTheLaravelTree(t *testing.T) {
	var paths []string
	for _, f := range mustGenerateAuth(t) {
		paths = append(paths, filepath.ToSlash(f.Path))
	}
	all := strings.Join(paths, "\n")

	for _, want := range []string{
		"app/Http/Controllers/Auth/LoginController.go",
		// The kit owns the controller that renders home, because home renders
		// with the layout's type and the kit replaced the layout.
		"app/Http/Controllers/HomeController.go",
		"resources/views/layouts/app.kyse.go",
		"resources/views/home.kyse.go",
		"resources/views/welcome.kyse.go",
		"resources/views/auth/login.kyse.go",
		"resources/views/auth/register.kyse.go",
		"resources/views/auth/verify.kyse.go",
		"resources/views/auth/passwords/confirm.kyse.go",
		"resources/views/auth/passwords/email.kyse.go",
		"resources/views/auth/passwords/reset.kyse.go",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("%s was not generated", want)
		}
	}

	// And nothing lands in the old tree.
	if strings.Contains(all, "modules/") {
		t.Errorf("the starter kit still writes into modules/:\n%s", all)
	}
}

// TestTheLandingPageDrawsTheSignedInHalf is the regression test for a landing
// page that offered "Login" to somebody who had just signed in.
//
// The layout the kit installs draws two halves of a navigation bar and picks by
// SignedIn(), and it puts the token in hx-headers on <body>. Both come from
// views.Page, and the controller that renders home is the only thing that fills
// them. It filled neither: the login succeeded, the cookie was set, and the page
// still said Login -- with hx-headers carrying an empty token, so the next write
// from that page was refused with 419 for a reason nothing on screen explained.
//
// The three fields are read off the literal rather than grepped for, because
// what matters is that the page hands them over, not that the words appear.
func TestTheLandingPageDrawsTheSignedInHalf(t *testing.T) {
	source := authFile(t, "HomeController.go")

	file, err := parser.ParseFile(token.NewFileSet(), "HomeController.go", source, parser.AllErrors)
	if err != nil {
		t.Fatalf("the generated controller does not parse: %v", err)
	}

	filled := compositeKeys(t, file, "Index", "views.Page")
	for _, want := range []string{"Token", "Authenticated", "UserName"} {
		if _, ok := filled[want]; !ok {
			t.Errorf("the landing page leaves views.Page.%s at its zero value; it fills %v", want, keysOf(filled))
		}
	}

	// And they come from the session and the issuer, not from a constant: a page
	// that hardcodes Authenticated is a page that is wrong half the time.
	if got := filled["Authenticated"]; got == "true" || got == "false" {
		t.Errorf("Authenticated is the constant %s rather than the state of the session", got)
	}
	for _, want := range []string{"c.sessions.Load(", "c.csrf.Issue("} {
		if !strings.Contains(source, want) {
			t.Errorf("the landing page never calls %s, so it cannot know what it is drawing", want)
		}
	}
}

// TestTheLandingPageIsGivenWhatItReads: the two collaborators arrive through the
// constructor, like every other controller's, rather than being reached for.
func TestTheLandingPageIsGivenWhatItReads(t *testing.T) {
	source := authFile(t, "HomeController.go")

	file, err := parser.ParseFile(token.NewFileSet(), "HomeController.go", source, parser.AllErrors)
	if err != nil {
		t.Fatalf("the generated controller does not parse: %v", err)
	}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Name.Name != "NewHomeController" {
			continue
		}
		var params []string
		for _, field := range fn.Type.Params.List {
			params = append(params, types.ExprString(field.Type))
		}
		for _, want := range []string{"*security.SessionStore", "*security.CSRF"} {
			if !slices.Contains(params, want) {
				t.Errorf("NewHomeController does not take %s, so the page cannot read the session or issue a token; it takes %v", want, params)
			}
		}
		return
	}
	t.Fatal("the generated controller declares no NewHomeController")
}

// TestTheRedirectSurvivesWithoutJavaScript is the regression test for a login
// that answered 200 with an empty body.
//
// The form carries method="post" action="/auth/login" as well as hx-post, and
// that is deliberate: it is the path a browser takes when the scripts have not
// arrived -- both of them are deferred -- or when they never do. HX-Redirect is
// a header only HTMX reads, so a plain form post that succeeded got 200, no
// body, and a blank page.
//
// httpx.Context.Redirect is where the branch lives: HX-Redirect under HTMX, a
// 303 with a Location otherwise. The handler has to go through it, and the two
// exits -- login and logout -- both have to.
func TestTheRedirectSurvivesWithoutJavaScript(t *testing.T) {
	source := authFile(t, "LoginController_handlers.go")

	if strings.Contains(source, `"HX-Redirect"`) {
		t.Error("the handler sets HX-Redirect itself: a form post from a browser without HTMX gets 200 and an empty body")
	}
	if !strings.Contains(source, "httpx.Context{Response: w, Request: r}).Redirect(") {
		t.Error("the redirect does not go through httpx.Context, which is what answers 303 to a request that is not HTMX")
	}

	file, err := parser.ParseFile(token.NewFileSet(), "LoginController_handlers.go", source, parser.AllErrors)
	if err != nil {
		t.Fatalf("the generated handlers do not parse: %v", err)
	}
	for _, handler := range []struct{ name, to string }{
		{"doLogin", `"/"`},
		{"doLogout", `"/auth/login"`},
	} {
		if !callsRedirect(t, file, handler.name, handler.to) {
			t.Errorf("%s does not redirect to %s through the shared helper", handler.name, handler.to)
		}
	}
}

// compositeKeys returns the keys of the first composite literal of the given
// type inside the named function, mapped to the expression each is filled with.
func compositeKeys(t *testing.T, file *ast.File, function, literal string) map[string]string {
	t.Helper()

	out := map[string]string{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != function {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || lit.Type == nil || types.ExprString(lit.Type) != literal {
				return true
			}
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := kv.Key.(*ast.Ident); ok {
					out[key.Name] = types.ExprString(kv.Value)
				}
			}
			return false
		})
		return out
	}
	t.Fatalf("the file declares no %s", function)
	return nil
}

// keysOf is what a failure message shows: the fields the page did fill.
func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// callsRedirect reports whether the named handler calls redirect(w, r, to).
func callsRedirect(t *testing.T, file *ast.File, function, to string) bool {
	t.Helper()

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != function {
			continue
		}
		var found bool
		ast.Inspect(fn, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok || ident.Name != "redirect" || len(call.Args) != 3 {
				return true
			}
			found = types.ExprString(call.Args[2]) == to
			return !found
		})
		return found
	}
	t.Fatalf("the file declares no %s", function)
	return false
}

// mustGenerateAuth returns the generated files or fails.
func mustGenerateAuth(t *testing.T) []gen.File {
	t.Helper()
	files, err := gen.GenerateAuth(authSpec())
	if err != nil {
		t.Fatalf("GenerateAuth: %v", err)
	}
	return files
}

// TestTheStarterKitIsRegenerable: without the custom markers, regenerating eats
// whatever the project added -- and a generator people are afraid to rerun is a
// one-time scaffold.
func TestTheStarterKitIsRegenerable(t *testing.T) {
	for _, name := range []string{"Auth/LoginController.go", "LoginController_handlers.go"} {
		if !strings.Contains(authFile(t, name), "arandu:begin custom") {
			t.Errorf("%s has no custom block: a regeneration would discard the project's additions", name)
		}
	}
}

func TestAuthNeedsTheModulePath(t *testing.T) {
	if _, err := gen.GenerateAuth(gen.Module{Name: "authui"}); err == nil {
		t.Fatal("generated without a module path")
	}
}

func authFile(t *testing.T, name string) string {
	t.Helper()
	files, err := gen.GenerateAuth(authSpec())
	if err != nil {
		t.Fatalf("GenerateAuth: %v", err)
	}
	// Matched by suffix, not by exact base name. The tree moved to Laravel's
	// shape and the file names moved with it; a test pinned to one spelling
	// breaks on a rename that changed nothing it was testing.
	for _, f := range files {
		if strings.HasSuffix(filepath.ToSlash(f.Path), name) {
			return string(f.Content)
		}
	}
	t.Fatalf("%s was not generated", name)
	return ""
}
