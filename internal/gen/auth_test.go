package gen_test

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
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
	if len(files) != 12 {
		t.Fatalf("generated %d files, want 12", len(files))
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
