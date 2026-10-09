package doctor_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/doctor"
)

// structuralRules are the warnings about where code lives. Every one of them
// is a warning, and each has a positive and a negative case below.
var structuralRules = []string{
	"input-read-by-hand",
	"validate-called-by-controller",
	"json-written-by-hand",
	"invalid-form-answered-by-hand",
	"session-loaded-in-controller",
	"redirect-to-literal-path",
	"html-template-in-app",
	"service-takes-http",
	"service-subpackage",
	"service-file-too-large",
	"controller-too-many-actions",
	"operation-chosen-by-form-field",
	"client-outside-clients",
	"model-rule-touches-io",
	"fragment-without-partial",
	"helper-reimplemented",
	"raw-sql-outside-repository",
	"generated-not-wired",
	"subject-built-by-hand",
}

// structureProject writes a project with the files given, plus a go.mod and a
// main.go, and answers its root.
func structureProject(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	all := map[string]string{
		"go.mod":  "module example.test/shape\n\ngo 1.26\n",
		"main.go": "package main\n\nfunc main() {}\n",
	}
	for name, body := range files {
		all[name] = body
	}
	for name, body := range all {
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

// findingsOf runs the doctor and keeps the findings of one rule.
func findingsOf(t *testing.T, root, rule string) []doctor.Finding {
	t.Helper()
	findings, err := doctor.Run(root, doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var out []doctor.Finding
	for _, f := range findings {
		if f.Rule == rule {
			out = append(out, f)
		}
	}
	return out
}

const controllerHead = `package controllers

import (
	"context"
	"encoding/json"
	"net/http"

	hhttp "github.com/arandu-io/hesape/http"
	"github.com/arandu-io/hesape/validation"
)

var (
	_ = context.Background
	_ = json.Marshal
	_ = http.StatusOK
	_ = validation.Errors{}
)

type C struct {
	svc      interface{ Publish(context.Context) error; Archive(context.Context) error; Get(context.Context) error }
	sessions interface{ Load(context.Context, *http.Request) (string, error) }
	cache    interface{ Load(string) (string, bool) }
}
`

// controller wraps the bodies of methods into a controller file.
func controller(methods string) map[string]string {
	return map[string]string{"app/Http/Controllers/C.go": controllerHead + methods}
}

// structureCase is one shape and what one rule should say about it.
type structureCase struct {
	name  string
	rule  string
	files map[string]string
	want  int
}

func structureCases() []structureCase {
	many := func(n int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "func (c *C) A%d(ctx *hhttp.Context) error { return nil }\n", i)
		}
		return b.String()
	}
	long := func(n int) string {
		var b strings.Builder
		b.WriteString("package services\n")
		for b.Len() == 0 || strings.Count(b.String(), "\n") < n {
			b.WriteString("\n// a line of a long service\n")
		}
		return b.String()
	}
	return []structureCase{
		// input-read-by-hand
		{"every by-hand read in one function is one finding", "input-read-by-hand", controller(`
func (c *C) Store(ctx *hhttp.Context) error {
	_ = ctx.Input("a")
	_ = ctx.Request.FormValue("b")
	_ = ctx.Request.PostFormValue("c")
	_ = ctx.Request.ParseForm()
	_ = ctx.Request.ParseMultipartForm(1)
	var v any
	return json.NewDecoder(ctx.Request.Body).Decode(&v)
}`), 1},
		{"a decoder alone is reading the body by hand", "input-read-by-hand", controller(`
func (c *C) Store(ctx *hhttp.Context) error {
	var v any
	return json.NewDecoder(ctx.Request.Body).Decode(&v)
}`), 1},
		{"Bind, Param and Query are not reads by hand", "input-read-by-hand", controller(`
func (c *C) Store(ctx *hhttp.Context) error {
	var in struct{ A string }
	_ = ctx.Param("id")
	_ = ctx.Query("page")
	return ctx.Bind(&in)
}`), 0},
		{"the same read in a service is not a controller's", "input-read-by-hand", map[string]string{
			"app/Services/S.go": "package services\n\nfunc read(ctx interface{ Input(string) string }) string { return ctx.Input(\"a\") }\n",
		}, 0},

		// validate-called-by-controller
		{"a controller calling Validate", "validate-called-by-controller", controller(`
func (c *C) Store(ctx *hhttp.Context) error {
	var in interface{ Validate() validation.Errors }
	return in.Validate()
}`), 1},
		{"the package function is the same call", "validate-called-by-controller", map[string]string{
			"app/Http/Controllers/C.go": "package controllers\n\nimport v \"github.com/arandu-io/framework/validation\"\n\nfunc store(x any) error { return v.Validate(x) }\n",
		}, 1},
		{"Validate with an argument, and Validated, are not the request's", "validate-called-by-controller", controller(`
func (c *C) Store(ctx *hhttp.Context) error {
	var in interface{ Validate(string) error; Validated() bool }
	_ = in.Validated()
	return in.Validate("x")
}`), 0},

		// json-written-by-hand
		{"an encoder on the response", "json-written-by-hand", controller(`
func (c *C) Export(ctx *hhttp.Context) error {
	return json.NewEncoder(ctx.Response).Encode(1)
}`), 1},
		{"a status written beside a JSON body", "json-written-by-hand", controller(`
func (c *C) Export(ctx *hhttp.Context) error {
	body, _ := json.Marshal(1)
	ctx.Response.WriteHeader(http.StatusOK)
	_, err := ctx.Response.Write(body)
	return err
}`), 1},
		{"a status written for a file is not JSON", "json-written-by-hand", controller(`
func (c *C) Download(ctx *hhttp.Context) error {
	ctx.Response.Header().Set("Content-Type", "text/csv")
	ctx.Response.WriteHeader(http.StatusOK)
	return nil
}`), 0},

		// invalid-form-answered-by-hand
		{"the constant, written into the answer", "invalid-form-answered-by-hand", controller(`
func (c *C) Store(ctx *hhttp.Context) error {
	return ctx.Fragment(http.StatusUnprocessableEntity, "partials.form", struct{}{})
}`), 1},
		{"the literal is the same status", "invalid-form-answered-by-hand", controller(`
func (c *C) Store(ctx *hhttp.Context) error { return ctx.Status(422) }`), 1},
		{"a comparison and a case answer nothing", "invalid-form-answered-by-hand", controller(`
func (c *C) Store(ctx *hhttp.Context, status int) error {
	if status == http.StatusUnprocessableEntity {
		return nil
	}
	switch status {
	case 422:
		return nil
	}
	return nil
}`), 0},

		// session-loaded-in-controller
		{"a controller loading the session", "session-loaded-in-controller", controller(`
func (c *C) Show(ctx *hhttp.Context) error {
	_, err := c.sessions.Load(ctx.Ctx(), ctx.Request)
	return err
}`), 1},
		{"a Load on a cache is not a session", "session-loaded-in-controller", controller(`
func (c *C) Show(ctx *hhttp.Context) error {
	_, _ = c.cache.Load("k")
	return nil
}`), 0},
		{"signing in is where a session is read before any guard", "session-loaded-in-controller", map[string]string{
			"app/Http/Controllers/Auth/Login.go": "package auth\n\nimport \"context\"\n\ntype M struct{ sessions interface{ Load(context.Context) error } }\n\nfunc (m M) Login(ctx context.Context) error { return m.sessions.Load(ctx) }\n",
		}, 0},

		// redirect-to-literal-path
		{"a path, and a path with a value glued on", "redirect-to-literal-path", controller(`
func (c *C) Done(ctx *hhttp.Context) error {
	if ctx.Param("id") == "" {
		return ctx.Redirect("/notes")
	}
	return ctx.Redirect("/notes/" + ctx.Param("id"))
}`), 2},
		{"a route name, a value and another site", "redirect-to-literal-path", controller(`
func (c *C) Done(ctx *hhttp.Context) error {
	next := ctx.URL("notes.index")
	if next == "" {
		return ctx.Redirect("https://example.com/notes")
	}
	if next == "x" {
		return ctx.Redirect("//cdn.example.com/x")
	}
	if next == "y" {
		return ctx.Redirect(next)
	}
	return ctx.RedirectRoute("notes.show", "1")
}`), 0},

		// html-template-in-app
		{"html/template under app", "html-template-in-app", map[string]string{
			"app/Presentation/P.go": "package presentation\n\nimport \"html/template\"\n\nvar _ template.HTML\n",
		}, 1},
		{"text/template, and html/template outside app", "html-template-in-app", map[string]string{
			"app/Presentation/P.go": "package presentation\n\nimport \"text/template\"\n\nvar _ = template.New\n",
			"cmd/tool/main.go":      "package main\n\nimport \"html/template\"\n\nvar _ template.HTML\n\nfunc main() {}\n",
		}, 0},

		// service-takes-http
		{"a service taking the request and the context", "service-takes-http", map[string]string{
			"app/Services/S.go": `package services

import (
	"net/http"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/hesape/session"
)

func (s *S) Start(r *http.Request, ctx *fhttp.Context, st *session.Store) {}

type S struct{}
`,
		}, 3},
		{"template.HTML is markup in a service", "service-takes-http", map[string]string{
			"app/Services/S.go": "package services\n\nimport \"html/template\"\n\nfunc render() template.HTML { return \"\" }\n",
		}, 1},
		{"a status, a method, a sniff, and the outgoing call a client rule reports", "service-takes-http", map[string]string{
			"app/Services/S.go": `package services

import (
	"context"
	"net/http"
)

type Gone struct{}

func (Gone) HTTPStatus() int { return http.StatusGone }

func kind(b []byte) string { return http.DetectContentType(b) }

func fetch(ctx context.Context, c *http.Client) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com", nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}
`,
		}, 0},

		// service-subpackage
		{"two packages inside app/Services", "service-subpackage", map[string]string{
			"app/Services/Billing/billing.go":      "package billing\n",
			"app/Services/Billing/billing_more.go": "package billing\n",
			"app/Services/Engines/Vision/v.go":     "package vision\n",
		}, 2},
		{"a flat app/Services", "service-subpackage", map[string]string{
			"app/Services/BillingService.go": "package services\n",
		}, 0},

		// service-file-too-large
		{"a service past six hundred lines", "service-file-too-large", map[string]string{
			"app/Services/Big.go": long(601),
		}, 1},
		{"six hundred lines is the limit, not past it", "service-file-too-large", map[string]string{
			"app/Services/Edge.go":     long(599),
			"app/Repositories/Big.go":  strings.Replace(long(900), "package services", "package repositories", 1),
			"app/Services/Big_test.go": long(900),
		}, 0},

		// controller-too-many-actions
		{"thirteen actions across two files", "controller-too-many-actions", map[string]string{
			"app/Http/Controllers/C.go":      controllerHead + many(7),
			"app/Http/Controllers/C_more.go": "package controllers\n\nimport hhttp \"github.com/arandu-io/hesape/http\"\n\nfunc (c *C) B0(ctx *hhttp.Context) error { return nil }\nfunc (c *C) B1(ctx *hhttp.Context) error { return nil }\nfunc (c *C) B2(ctx *hhttp.Context) error { return nil }\nfunc (c *C) B3(ctx *hhttp.Context) error { return nil }\nfunc (c *C) B4(ctx *hhttp.Context) error { return nil }\nfunc (c *C) B5(w http.ResponseWriter, r *http.Request) {}\n",
		}, 1},
		{"twelve actions and helpers that are not actions", "controller-too-many-actions", controller(many(12) + `
func (c *C) helper(ctx *hhttp.Context) error { return nil }
func (c *C) Label(id string) string { return id }
`), 0},

		// operation-chosen-by-form-field
		{"a switch on a form field calling two methods", "operation-chosen-by-form-field", controller(`
func (c *C) Act(ctx *hhttp.Context) error {
	switch ctx.Input("op") {
	case "publish":
		return c.svc.Publish(ctx.Ctx())
	case "archive":
		return c.svc.Archive(ctx.Ctx())
	}
	return nil
}`), 1},
		{"the same switch through a variable", "operation-chosen-by-form-field", controller(`
func (c *C) Act(ctx *hhttp.Context) error {
	op := ctx.Request.PostFormValue("op")
	switch op {
	case "publish":
		return c.svc.Publish(ctx.Ctx())
	default:
		return c.svc.Archive(ctx.Ctx())
	}
}`), 1},
		{"a route parameter, and a form field that calls one method", "operation-chosen-by-form-field", controller(`
func (c *C) Act(ctx *hhttp.Context) error {
	switch ctx.Param("op") {
	case "publish":
		return c.svc.Publish(ctx.Ctx())
	case "archive":
		return c.svc.Archive(ctx.Ctx())
	}
	switch ctx.Input("view") {
	case "a", "b":
		return c.svc.Get(ctx.Ctx())
	}
	return nil
}`), 0},

		// client-outside-clients
		{"a service calling out", "client-outside-clients", map[string]string{
			"app/Services/S.go": "package services\n\nimport \"net/http\"\n\nfunc ping() { _, _ = http.Get(\"https://example.com\") }\n",
			"app/Services/T.go": "package services\n\nimport _ \"github.com/arandu-io/hesape/http/client\"\n",
		}, 2},
		{"a client in app/Clients, and bootstrap building its http.Client", "client-outside-clients", map[string]string{
			"app/Clients/PayClient.go": "package clients\n\nimport \"net/http\"\n\ntype PayClient struct{ c *http.Client }\n\nfunc (p PayClient) Ping() { _, _ = p.c.Get(\"https://example.com\") }\n",
			"bootstrap/app.go":         "package bootstrap\n\nimport \"net/http\"\n\nvar Client = &http.Client{}\n",
		}, 0},

		// model-rule-touches-io
		{"an entity rule reading the clock and the database", "model-rule-touches-io", map[string]string{
			"app/Models/Note.go": `package models

import (
	"database/sql"
	"time"
)

type Note struct{ Due time.Time }

// arandu:begin custom

func (n Note) Overdue() bool { return time.Now().After(n.Due) }

func (n Note) Load(db *sql.DB) error { return nil }

// arandu:end custom
`,
		}, 2},
		{"the time as a parameter, a scope, init, and code outside the block", "model-rule-touches-io", map[string]string{
			"app/Models/Note.go": `package models

import (
	"database/sql"
	"time"
)

type Note struct{ Due time.Time }

type NoteQuery struct{ since time.Time }

func generated(db *sql.DB) time.Time { return time.Now() }

// arandu:begin custom

func (n Note) Overdue(now time.Time) bool { return now.After(n.Due) }

func (q *NoteQuery) Recent() *NoteQuery { q.since = time.Now(); return q }

func init() { _ = time.Now() }

// arandu:end custom
`,
		}, 0},

		// fragment-without-partial
		{"a page answered as a fragment", "fragment-without-partial", controller(`
func (c *C) Row(ctx *hhttp.Context) error { return ctx.Fragment(200, "notes.index", struct{}{}) }`), 1},
		{"a partial, and a name held in a value", "fragment-without-partial", controller(`
func (c *C) Row(ctx *hhttp.Context) error {
	name := "notes.index"
	if name == "" {
		return ctx.Fragment(200, name, struct{}{})
	}
	return ctx.Fragment(200, "partials.note-row", struct{}{})
}`), 0},

		// helper-reimplemented
		{"a slug, a BRL formatter and a CPF validator", "helper-reimplemented", map[string]string{
			"app/Support/Text.go": "package support\n\nfunc Slugify(s string) string { return s }\n\nfunc formatBRLCents(c int64) string { return \"\" }\n\nfunc cpfCheckDigit(s string) int { return 0 }\n\nfunc CNPJ(s string) string { return s }\n",
		}, 4},
		{"an entity's Slug and CPF, and lookups by slug and CNPJ", "helper-reimplemented", map[string]string{
			"app/Models/Person.go": "package models\n\ntype Person struct{ slug, cpf string }\n\nfunc (p Person) Slug() string { return p.slug }\n\nfunc (p Person) CPF() string { return p.cpf }\n\nfunc PublishedBySlug(s string) string { return s }\n\nfunc establishmentByCNPJ(s string) string { return s }\n",
			"tests/Unit/x_test.go": "package unit\n\nfunc Slugify(s string) string { return s }\n",
		}, 0},

		// raw-sql-outside-repository
		{"a statement run from a service", "raw-sql-outside-repository", map[string]string{
			"app/Services/S.go": "package services\n\nimport \"database/sql\"\n\nfunc count(db *sql.DB) error {\n\t_, err := db.Exec(\"DELETE FROM notes WHERE id = ?\", 1)\n\treturn err\n}\n",
		}, 1},
		{"a repository, a migration, and a literal never run", "raw-sql-outside-repository", map[string]string{
			"app/Repositories/NoteRepository.go": "package repositories\n\nimport \"database/sql\"\n\nfunc count(db *sql.DB) error {\n\t_, err := db.Exec(\"DELETE FROM notes WHERE id = ?\", 1)\n\treturn err\n}\n",
			"database/migrations/0001_create.go": "package migrations\n\nimport \"database/sql\"\n\nfunc up(db *sql.DB) error {\n\t_, err := db.Exec(\"UPDATE notes SET pinned = 0 WHERE pinned IS NULL\")\n\treturn err\n}\n",
			"app/Services/Doc.go":                "package services\n\n// Example is documentation.\nconst Example = \"SELECT id FROM notes WHERE id = ?\"\n\nfunc describe() string { return \"SELECT id FROM notes WHERE id = ?\" }\n",
		}, 0},

		// generated-not-wired
		{"a controller and a service nothing constructs", "generated-not-wired", map[string]string{
			"app/Http/Controllers/NoteController.go": "package controllers\n\ntype NoteController struct{}\n\nfunc NewNoteController() *NoteController { return nil }\n",
			"app/Services/NoteService.go":            "package services\n\ntype NoteService struct{}\n\nfunc NewNoteService() *NoteService { return nil }\n",
			"tests/Feature/note_test.go":             "package feature\n\nvar _ = NewNoteService\n",
		}, 2},
		{"constructors bootstrap and another constructor call", "generated-not-wired", map[string]string{
			"app/Http/Controllers/NoteController.go": "package controllers\n\nimport services \"example.test/shape/app/Services\"\n\ntype NoteController struct{}\n\nfunc NewNoteController() *NoteController { _ = services.NewAuditService(); return nil }\n",
			"app/Services/AuditService.go":           "package services\n\nfunc NewAuditService() int { return 0 }\n",
			"bootstrap/app.go":                       "package bootstrap\n\nimport controllers \"example.test/shape/app/Http/Controllers\"\n\nvar _ = controllers.NewNoteController\n",
		}, 0},

		// subject-built-by-hand
		{"roles and actions written into a literal", "subject-built-by-hand", map[string]string{
			"app/Services/S.go": `package services

import (
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/auth"

	policies "example.test/shape/app/Policies"
)

var system = security.Subject{ID: "system", Roles: []string{"admin"}}

var all = auth.Subject{ID: "sync", Actions: policies.All()}
`,
		}, 2},
		{"stored roles, no roles, and a seeder", "subject-built-by-hand", map[string]string{
			"app/Models/User.go": `package models

import "github.com/arandu-io/framework/security"

type User struct{ ID string; Roles []string }

func (u User) Subject() security.Subject {
	return security.Subject{ID: u.ID, Roles: append([]string(nil), u.Roles...)}
}

func guest() security.Subject { return security.Subject{ID: "guest"} }
`,
			"database/seeders/Seeder.go": "package seeders\n\nimport \"github.com/arandu-io/framework/security\"\n\nvar seed = security.Subject{ID: \"seed\", Roles: []string{\"admin\"}}\n",
		}, 0},
	}
}

// TestEachStructuralRuleSeesItsShapeAndNotItsNearMiss runs every rule on the
// shape it exists for and on the shapes it must stay quiet on.
func TestEachStructuralRuleSeesItsShapeAndNotItsNearMiss(t *testing.T) {
	covered := map[string][2]bool{}
	for _, c := range structureCases() {
		t.Run(c.rule+"/"+c.name, func(t *testing.T) {
			got := findingsOf(t, structureProject(t, c.files), c.rule)
			if len(got) != c.want {
				var lines []string
				for _, f := range got {
					lines = append(lines, f.String())
				}
				t.Errorf("%d finding(s), want %d:\n%s", len(got), c.want, strings.Join(lines, "\n"))
			}
			for _, f := range got {
				if f.Severity != doctor.Warning {
					t.Errorf("%s reported at %s: a structural rule is a warning", f.Rule, f.Severity)
				}
			}
		})
		seen := covered[c.rule]
		if c.want > 0 {
			seen[0] = true
		} else {
			seen[1] = true
		}
		covered[c.rule] = seen
	}
	for _, rule := range structuralRules {
		if seen := covered[rule]; !seen[0] || !seen[1] {
			t.Errorf("%s has no positive or no negative case here", rule)
		}
	}
}

// TestTheStructuralRulesAreWarningsInTheList holds the list to the decision:
// every structural rule is listed, and listed as a warning only.
func TestTheStructuralRulesAreWarningsInTheList(t *testing.T) {
	listed := map[string]string{}
	for _, r := range doctor.List() {
		listed[r.Name] = r.Severity()
	}
	for _, rule := range structuralRules {
		if got, ok := listed[rule]; !ok || got != "warning" {
			t.Errorf("%s is listed as %q, want warning", rule, got)
		}
	}
}

// TestTheCleanFixtureHasNoStructuralFinding keeps the shape the generator emits
// on the right side of every structural rule.
func TestTheCleanFixtureHasNoStructuralFinding(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "clean"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	structural := map[string]bool{}
	for _, rule := range structuralRules {
		structural[rule] = true
	}
	for _, f := range findings {
		if structural[f.Rule] {
			t.Errorf("the clean fixture reports %s", f)
		}
	}
}

// TestEveryStructuralRuleFiresOnTheViolationsFixture names each rule on the
// fixture where its mistake is planted, so a rule that stopped seeing it is
// named here rather than only counted.
func TestEveryStructuralRuleFiresOnTheViolationsFixture(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "violations"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	fired := map[string]bool{}
	for _, f := range findings {
		fired[f.Rule] = true
	}
	for _, rule := range structuralRules {
		if !fired[rule] {
			t.Errorf("%s fires on nothing in the violations fixture", rule)
		}
	}
}
