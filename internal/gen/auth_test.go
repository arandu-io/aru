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
	if len(files) != 4 {
		t.Fatalf("generated %d files, want 4", len(files))
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
		if !strings.HasSuffix(f.Path, ".go") {
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
		if filepath.Base(f.Path) != "views.templ" {
			continue
		}
		if !bytes.Contains(f.Content, []byte("templ LoginPage(")) {
			t.Error("the templ declarations are missing")
		}
		return
	}
	t.Fatal("views.templ was not generated")
}

// TestTheLoginScreenRotatesTheSession: keeping the pre-login session id is
// session fixation. The framework's own handler does this, and a starter kit that
// people copy from has to do it too -- `aru doctor` checks for the call.
func TestTheLoginScreenRotatesTheSession(t *testing.T) {
	handlers := authFile(t, "handlers.go")

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
	handlers := authFile(t, "handlers.go")

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
	handlers := authFile(t, "handlers.go")
	views := authFile(t, "views.templ")

	if !strings.Contains(handlers, "csrf.Issue(") {
		t.Error("the rejection path does not issue a token")
	}
	if !strings.Contains(views, `name="_csrf"`) {
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
	for _, name := range []string{"module.go", "handlers.go", "views.templ"} {
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
	handlers := authFile(t, "handlers.go")

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
	if strings.Contains(authFile(t, "module.go"), "Migrations()") {
		t.Error("the starter kit declares migrations: the users table already has an owner")
	}
	if !strings.Contains(authFile(t, "arandu.mod.toml"), "migrations = false") {
		t.Error("the manifest claims tables the module does not own")
	}
}

// TestTheStarterKitDeclaresItself: without the manifest, the first thing anyone
// sees after running make:auth is a doctor warning about the files they just
// generated.
func TestTheStarterKitDeclaresItself(t *testing.T) {
	toml := authFile(t, "arandu.mod.toml")

	for _, want := range []string{"name = ", "[permissions]", "network = false", "exec = false"} {
		if !strings.Contains(toml, want) {
			t.Errorf("the manifest has no %q", want)
		}
	}
}

// TestTheStarterKitIsRegenerable: without the custom markers, regenerating eats
// whatever the project added -- and a generator people are afraid to rerun is a
// one-time scaffold.
func TestTheStarterKitIsRegenerable(t *testing.T) {
	for _, name := range []string{"module.go", "handlers.go"} {
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
	for _, f := range files {
		if filepath.Base(f.Path) == name {
			return string(f.Content)
		}
	}
	t.Fatalf("%s was not generated", name)
	return ""
}
