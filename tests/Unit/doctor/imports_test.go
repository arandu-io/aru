package doctor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/doctor"
	"github.com/arandu-io/aru/tests"
)

// importsProject writes a project whose go.mod requires the framework and
// replaces it with the catalog's fixture module, plus the files given. The
// replace is what puts a framework on disk without a module cache, so every
// case below reads the same declarations.
func importsProject(t *testing.T, requires string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	stub := tests.Fixture(t, "catalog", "framework")
	if requires == "" {
		requires = "require github.com/arandu-io/framework v0.50.2\n\nreplace github.com/arandu-io/framework => " + stub + "\n"
	}
	files["go.mod"] = "module example.test/project\n\ngo 1.26\n\n" + requires
	files["main.go"] = "package main\n\nfunc main() {}\n"
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// canonicalFindings runs the doctor and keeps the import-not-canonical ones.
func canonicalFindings(t *testing.T, root string) []doctor.Finding {
	t.Helper()
	findings, err := doctor.Run(root, doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var out []doctor.Finding
	for _, f := range findings {
		if f.Rule == "import-not-canonical" {
			out = append(out, f)
		}
	}
	return out
}

// TestAnAliasNamedThroughItsBridgeIsReported is the positive case: a service
// that names hesape/auth's Grant and Authorize through framework/security.
func TestAnAliasNamedThroughItsBridgeIsReported(t *testing.T) {
	root := importsProject(t, "", map[string]string{
		"app/Services/LedgerService.go": `package services

import (
	"context"

	"github.com/arandu-io/framework/security"
)

func Settle(ctx context.Context, g security.Grant, p security.Policy[int], s security.Subject) error {
	_, err := security.Authorize(ctx, p, s, "ledger.settle", 1)
	return err
}
`,
	})
	found := canonicalFindings(t, root)
	if len(found) != 1 {
		t.Fatalf("got %d findings, want one per file and import: %v", len(found), found)
	}
	f := found[0]
	if f.Severity != doctor.Warning {
		t.Errorf("severity is %s: a rule about the shape of an import is a warning", f.Severity)
	}
	if f.File != "app/Services/LedgerService.go" || f.Line != 6 {
		t.Errorf("finding at %s:%d, want the import at app/Services/LedgerService.go:6", f.File, f.Line)
	}
	for _, want := range []string{"Grant, Policy, Subject, Authorize from github.com/arandu-io/hesape/auth", "github.com/arandu-io/framework/security"} {
		if !strings.Contains(f.Message, want) {
			t.Errorf("the message does not say %q: %s", want, f.Message)
		}
	}
	if !strings.Contains(f.Why, "the import goes") {
		t.Errorf("nothing else is used from the import, and the finding does not say it can go: %s", f.Why)
	}
}

// TestWhatTheFrameworkDeclaresStaysWhereItIs is the negative case, and the
// reason the decision is per symbol: the router and the session store are the
// framework's own, in packages whose other names are aliases.
func TestWhatTheFrameworkDeclaresStaysWhereItIs(t *testing.T) {
	root := importsProject(t, "", map[string]string{
		"routes/web.go": `package routes

import fhttp "github.com/arandu-io/framework/http"

func Web(r *fhttp.Router) *fhttp.Router { return r }
`,
		"app/Http/Middleware/Session.go": `package middleware

import "github.com/arandu-io/framework/security"

func Load(store *security.SessionStore) error { return store.Load() }
`,
		"app/Services/AuditService.go": `package services

import "github.com/arandu-io/hesape/auth"

func Allowed(g auth.Grant) bool { return g != auth.Grant{} }
`,
		// A parameter that shadows the import is a value, and its Grant field
		// is not the package's.
		"app/Services/ShadowService.go": `package services

import "github.com/arandu-io/framework/security"

var _ *security.SessionStore

func Count(security struct{ Grant int }) int { return security.Grant }
`,
	})
	if found := canonicalFindings(t, root); len(found) != 0 {
		t.Errorf("code that names each symbol by its own path was reported: %v", found)
	}
}

// TestAMixedImportKeepsWhatTheFrameworkDeclares reports the alias and says the
// import stays for the envelope beside it.
func TestAMixedImportKeepsWhatTheFrameworkDeclares(t *testing.T) {
	root := importsProject(t, "", map[string]string{
		"app/Http/Controllers/InvoiceController.go": `package controllers

import fhttp "github.com/arandu-io/framework/http"

type InvoiceController struct{}

func (c *InvoiceController) Index(ctx *fhttp.Context) error { return nil }

func (c *InvoiceController) Routes(r *fhttp.Router) {}
`,
	})
	found := canonicalFindings(t, root)
	if len(found) != 1 {
		t.Fatalf("got %d findings, want one: %v", len(found), found)
	}
	if !strings.Contains(found[0].Message, "Context from github.com/arandu-io/hesape/http") {
		t.Errorf("the message does not name Context and its path: %s", found[0].Message)
	}
	if strings.Contains(found[0].Message, "Router") || !strings.Contains(found[0].Why, "Keep it for Router") {
		t.Errorf("the router is the framework's, and the finding has to keep the import for it: %s / %s",
			found[0].Message, found[0].Why)
	}
}

// TestARenamedForwardNamesWhatItIsCalledThere: HashPassword is a call through
// to hesape/hashing.Make, and the finding has to say both names, or the person
// changes the import and the code stops compiling.
func TestARenamedForwardNamesWhatItIsCalledThere(t *testing.T) {
	root := importsProject(t, "", map[string]string{
		"app/Services/PasswordService.go": `package services

import "github.com/arandu-io/framework/security"

func Hash(p string) (string, error) { return security.HashPassword(p) }
`,
	})
	found := canonicalFindings(t, root)
	if len(found) != 1 || !strings.Contains(found[0].Message, "HashPassword (Make there) from github.com/arandu-io/hesape/hashing") {
		t.Errorf("the renamed forward is not reported with both names: %v", found)
	}
}

// TestWhatTheRuleCannotSeeItDoesNotReport pins the two limits its doc comment
// declares: a dot import has no selector to read, and a framework that is not
// on disk has no catalog.
func TestWhatTheRuleCannotSeeItDoesNotReport(t *testing.T) {
	dot := importsProject(t, "", map[string]string{
		"app/Services/DotService.go": `package services

import . "github.com/arandu-io/framework/security"

func Keep(g Grant) Grant { return g }
`,
	})
	if found := canonicalFindings(t, dot); len(found) != 0 {
		t.Errorf("a dot import was reported, and the rule says it cannot read one: %v", found)
	}

	t.Setenv("GOMODCACHE", t.TempDir())
	missing := importsProject(t, "require github.com/arandu-io/framework v0.50.2\n", map[string]string{
		"app/Services/LedgerService.go": `package services

import "github.com/arandu-io/framework/security"

func Keep(g security.Grant) security.Grant { return g }
`,
	})
	if found := canonicalFindings(t, missing); len(found) != 0 {
		t.Errorf("a framework that is on no disk produced findings, so they were guessed: %v", found)
	}
}
