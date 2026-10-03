package doctor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/doctor"
	"github.com/arandu-io/aru/internal/gen"
)

// The two rules about the generated model layer, on a project the generator
// wrote: clean as generated, and dirty in each way the rules exist to catch.

// TestAGeneratedModuleIsCleanForTheModelRules: the query file make:module
// writes is the one model:build would, the factory hands Base() to the core
// factory without calling anything on it, and a service that uses the typed
// query reaches nothing of the core. None of it is a finding -- of these two
// rules or of any other rule, on the files the generator owns.
func TestAGeneratedModuleIsCleanForTheModelRules(t *testing.T) {
	root := generatedProject(t)

	findings, err := doctor.Run(root, doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range findings {
		generated := strings.HasPrefix(f.File, "app/Models/") || strings.HasPrefix(f.File, "database/factories/")
		if f.Rule == "model-query-stale" || f.Rule == "model-core-outside-models" || generated {
			t.Errorf("a generated module was reported: %s", f)
		}
	}
}

// TestAStaleOrUngeneratableQueryIsReported: a query file edited by hand, one
// whose entity is gone, and an entity model:build cannot write a query for are
// all the same finding, each at its own place.
func TestAStaleOrUngeneratableQueryIsReported(t *testing.T) {
	root := generatedProject(t)
	query := filepath.Join(root, "app", "Models", "PurchaseOrderQuery.go")
	body, err := os.ReadFile(query)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, query, string(body)+"\n// edited by hand\n")
	writeFile(t, filepath.Join(root, "app", "Models", "RefundQuery.go"),
		gen.QueryHeader("Refund.go")+"\n\npackage models\n")

	got := modelFindings(t, root)
	for _, want := range []string{
		"app/Models/PurchaseOrderQuery.go:1: app/Models/PurchaseOrderQuery.go is stale",
		"app/Models/RefundQuery.go:1: app/Models/RefundQuery.go is orphaned",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}

	// An entity with no table cannot have a query at all, and the finding is at
	// the struct rather than at a file that does not exist.
	writeFile(t, filepath.Join(root, "app", "Models", "Refund.go"), `package models

import "github.com/arandu-io/hesape/database/model"

type Refund struct {
	model.Model
	ID string
}
`)
	if got := modelFindings(t, root); !strings.Contains(got, "app/Models/Refund.go:5: model:build cannot write the query of this entity") {
		t.Errorf("an entity with no table is not reported at its struct:\n%s", got)
	}
}

// TestTheCoreReachedFromOutsideTheModelsIsReported: from a service, a test or
// any package that declares no entity, a model.NewTable, a method called
// through Base() and one called on a held *model.Builder are each a finding.
// The same calls inside app/Models are what a local scope is, and are not.
func TestTheCoreReachedFromOutsideTheModelsIsReported(t *testing.T) {
	root := generatedProject(t)
	writeFile(t, filepath.Join(root, "app", "Models", "scopes.go"), `package models

// Approved is a local scope: the core builder is reached inside the package of
// the entity, which is where the typed query lives.
func (q *PurchaseOrderQuery) Approved() *PurchaseOrderQuery {
	q.Base().Where("approved", true)
	return q
}
`)
	writeFile(t, filepath.Join(root, "tests", "Unit", "Shortcut_test.go"), `package unit_test

import (
	"github.com/arandu-io/hesape/database/model"

	models "example.test/project/app/Models"
)

var shadow = model.NewTable(model.TableSpec{Name: "purchase_orders"})

func approved(db model.DB) {
	models.PurchaseOrders(db).Base().Where("approved", true)
	b := models.PurchaseOrders(db).Base()
	b.OrderBy("id")
	var t *model.Table = shadow
	t.Query(db)
}
`)

	got := modelFindings(t, root)
	for _, want := range []string{
		"tests/Unit/Shortcut_test.go:9: model.NewTable is called",
		"tests/Unit/Shortcut_test.go:12: the core builder's Where is called through Base()",
		"tests/Unit/Shortcut_test.go:14: the core Builder's OrderBy is called on b",
		"tests/Unit/Shortcut_test.go:16: the core Table's Query is called on t",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "scopes.go") {
		t.Errorf("a local scope inside app/Models was reported:\n%s", got)
	}
}

// generatedProject is a project holding one module as make:module and
// make:factory write it, plus a service that uses the typed query.
func generatedProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.test/project\n\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(root, "arandu.toml"), "name = \"project\"\n")

	module := gen.Module{
		Name:       "purchase_order",
		Fields:     []gen.Field{{Name: "reference", Type: gen.TypeString, Required: true}},
		Tenant:     true,
		ModulePath: "example.test/project",
		Date:       "2026_07_31",
	}
	files, err := gen.GenerateModel(module, gen.ModelParts{Factory: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := gen.Write(root, files, false); err != nil {
		t.Fatal(err)
	}
	return root
}

// modelFindings is the two rules' findings, one per line.
func modelFindings(t *testing.T, root string) string {
	t.Helper()
	findings, err := doctor.Run(root, doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var lines []string
	for _, f := range findings {
		if f.Rule == "model-query-stale" || f.Rule == "model-core-outside-models" {
			lines = append(lines, f.File+":"+itoa(f.Line)+": "+f.Message)
		}
	}
	return strings.Join(lines, "\n")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for ; n > 0; n /= 10 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
	}
	return string(digits)
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
