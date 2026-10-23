package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/doctor"
	"github.com/arandu-io/aru/internal/gen"
)

// kitProject writes a project whose tests package declares Serve and Open over
// stand-ins, and whose go.mod replaces the framework with a directory holding
// stand-ins of the kit's browser and openapi packages, so a generated test is
// compiled against the shapes it calls without a network.
func kitProject(t *testing.T) string {
	t.Helper()
	root := probeProjectRunning(t, "package main\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(root, "go.mod"), "module example.test/probe\n\ngo 1.26\n\nrequire github.com/arandu-io/framework v0.0.0\n\nreplace github.com/arandu-io/framework => ./fw\n")
	writeFile(t, filepath.Join(root, "fw", "go.mod"), "module github.com/arandu-io/framework\n\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "fw", "test", "browser", "browser.go"), `package browser

type Locator struct{}

func Role(role, name string) Locator { return Locator{} }

type Page struct{}

func (*Page) Visit(target string, params ...any) {}
func (*Page) Click(Locator)                      {}
`)
	writeFile(t, filepath.Join(root, "fw", "test", "openapi", "openapi.go"), `package openapi

import (
	"net/http"
	"testing"
)

type Target interface{}

type Call struct {
	Operation string
	Params    map[string]string
	Body      any
	Status    int
}

type Document struct{}

func Load(t testing.TB, path string) *Document                 { return &Document{} }
func URL(base string, client *http.Client) Target               { return nil }
func (*Document) Check(t testing.TB, target Target, call Call) {}
`)
	writeFile(t, filepath.Join(root, "tests", "TestCase.go"), `package tests

import (
	"testing"

	"github.com/arandu-io/framework/test/browser"
)

type Server struct{ URL string }

func Serve(t *testing.T) *Server { return &Server{} }

func Open(t *testing.T, server *Server) *browser.Page { return &browser.Page{} }
`)
	writeFile(t, filepath.Join(root, "docs", "contracts", "app.openapi.json"), `{"openapi": "3.1.0", "paths": {
  "/invoices/{invoice}": {"get": {"operationId": "invoices.show", "responses": {"200": {}, "404": {}}}},
  "/invoices": {"post": {"requestBody": {}, "responses": {"201": {}}}}
}}`)
	return root
}

// vetted compiles the project with the e2e tag, which is what proves a
// generated file is Go that calls what it names.
func vetted(t *testing.T, root string) {
	t.Helper()
	cmd := exec.Command(goTool(t), "vet", "-tags", "e2e", "./...")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the generated test does not compile: %v\n%s", err, out)
	}
}

// TestMakeE2EWritesAJourneyThatCompiles generates a journey and a smoke
// journey and compiles them behind the e2e tag.
func TestMakeE2EWritesAJourneyThatCompiles(t *testing.T) {
	root := kitProject(t)

	code, stdout, stderr := exercise(t, "make:e2e", "pay an invoice", "--route=/invoices")
	if code != 0 {
		t.Fatalf("make:e2e exited %d: %s %s", code, stdout, stderr)
	}
	body, err := os.ReadFile(filepath.Join(root, "tests", "E2E", "PayAnInvoice_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"//go:build e2e", "package e2e_test", "func TestPayAnInvoice(t *testing.T) {",
		"server := tests.Serve(t)", "page := tests.Open(t, server)", `page.Visit("/invoices")`, "// arandu:begin custom"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the journey does not carry %q:\n%s", want, body)
		}
	}
	if code, _, stderr := exercise(t, "make:e2e", "Home", "--smoke"); code != 0 {
		t.Fatalf("make:e2e --smoke: %s", stderr)
	}
	smoke, _ := os.ReadFile(filepath.Join(root, "tests", "E2E", "Home_test.go"))
	if !strings.Contains(string(smoke), "func TestSmokeHome(t *testing.T) {") || !strings.Contains(string(smoke), `page.Visit("/")`) {
		t.Errorf("--smoke did not name the test TestSmokeHome:\n%s", smoke)
	}
	vetted(t, root)

	if code, _, stderr := exercise(t, "make:e2e", "Home"); code == 0 || !strings.Contains(stderr, "already exists") {
		t.Errorf("an existing journey was overwritten without --force: %s", stderr)
	}
	if code, _, stderr := exercise(t, "make:e2e", "Other", "--route=no.such.route"); code == 0 || !strings.Contains(stderr, "not the name of a GET route") {
		t.Errorf("an unknown route was not refused: %s", stderr)
	}
}

// TestMakeE2ERefusesWithoutTheHelpers requires the helpers to paste when the
// tests package cannot open a page.
func TestMakeE2ERefusesWithoutTheHelpers(t *testing.T) {
	probeProjectRunning(t, "package main\n\nfunc main() {}\n")
	code, _, stderr := exercise(t, "make:e2e", "Checkout")
	if code == 0 || !strings.Contains(stderr, "func Open(t *testing.T, server *test.Server) *browser.Page") {
		t.Errorf("make:e2e did not show the helpers to paste: %s", stderr)
	}
}

// TestMakeContractWritesTheTestOfAnOperation generates the contract test of
// one operation named by its id and of one named by its method and path, and
// compiles both.
func TestMakeContractWritesTheTestOfAnOperation(t *testing.T) {
	root := kitProject(t)

	if code, stdout, stderr := exercise(t, "make:contract", "invoices.show"); code != 0 {
		t.Fatalf("make:contract exited %d: %s %s", code, stdout, stderr)
	}
	body, err := os.ReadFile(filepath.Join(root, "tests", "Contract", "InvoicesShow_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"package contract_test", `"github.com/arandu-io/framework/test/openapi"`,
		"func TestInvoicesShowAnswersItsContract(t *testing.T) {", `openapi.Load(t, "../../docs/contracts/app.openapi.json")`,
		`Operation: "GET /invoices/{invoice}"`, `"invoice": "",`, "Status:    http.StatusOK,"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the contract test does not carry %q:\n%s", want, body)
		}
	}
	if code, _, stderr := exercise(t, "make:contract", "POST", "/invoices"); code != 0 {
		t.Fatalf("make:contract POST /invoices: %s", stderr)
	}
	post, _ := os.ReadFile(filepath.Join(root, "tests", "Contract", "PostInvoices_test.go"))
	if !strings.Contains(string(post), "Body:      body,") || !strings.Contains(string(post), "http.StatusCreated") {
		t.Errorf("the POST contract test does not send a body or expect 201:\n%s", post)
	}
	vetted(t, root)

	if code, _, stderr := exercise(t, "make:contract", "invoices.delete"); code == 0 || !strings.Contains(stderr, "GET /invoices/{invoice} (invoices.show)") {
		t.Errorf("an undocumented operation was not refused with the list: %s", stderr)
	}
}

// TestWhatTheGeneratorsWriteIsQuietOnTheSuiteRules generates a module with
// its policy test -- which builds a Subject to ask the Policy's Can -- a
// tenant-scoped feature test, a journey and a contract test, and requires the
// doctor's suite rules to find nothing in them but the pipeline the project
// has not written yet.
func TestWhatTheGeneratorsWriteIsQuietOnTheSuiteRules(t *testing.T) {
	root := kitProject(t)
	m := moduleUnderTest("purchase_order")
	m.ModulePath = "example.test/probe"
	files, err := gen.Generate(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := gen.Write(root, files, false); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"make:e2e", "Checkout", "--smoke"}, {"make:contract", "invoices.show"}} {
		if code, _, stderr := exercise(t, args...); code != 0 {
			t.Fatalf("%v: %s", args, stderr)
		}
	}
	findings, err := doctor.Run(root, doctor.Conventional)
	if err != nil {
		t.Fatal(err)
	}
	suite := map[string]bool{"test-sleep": true, "test-forged-subject": true, "test-css-locator": true,
		"test-copied-conformance": true, "skip-without-reason": true, "driver-specific-api-in-test": true}
	sawCI := false
	for _, f := range findings {
		if suite[f.Rule] {
			t.Errorf("generated code is reported: %s", f)
		}
		sawCI = sawCI || f.Rule == "e2e-not-run-in-ci"
	}
	if !sawCI {
		t.Error("a project with a journey and no pipeline was not told to run it in CI")
	}
	writeFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"), "jobs:\n  e2e:\n    steps:\n      - run: aru test --smoke\n")
	findings, _ = doctor.Run(root, doctor.Conventional)
	for _, f := range findings {
		if f.Rule == "e2e-not-run-in-ci" {
			t.Errorf("a pipeline running aru test --smoke was not read: %s", f)
		}
	}
}
