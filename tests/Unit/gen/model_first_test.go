package gen_test

import (
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/gen"
)

// TestGeneratedModulesUseTheModelFirstPath fixes the public shape promised by
// make:module: the application model owns the Hesape model, the service owns
// the database handle, and CRUD does not grow a repository layer of its own.
func TestGeneratedModulesUseTheModelFirstPath(t *testing.T) {
	files, err := gen.Generate(spec(true))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	byPath := make(map[string]string, len(files))
	for _, file := range files {
		byPath[file.Path] = string(file.Content)
		if strings.HasPrefix(file.Path, "app/Repositories/") {
			t.Errorf("make:module emitted the CRUD repository %s", file.Path)
		}
	}

	model := byPath["app/Models/PurchaseOrder.go"]
	for _, want := range []string{
		`"github.com/arandu-io/hesape/database/model"`,
		"model.Model[PurchaseOrder]",
		"func PurchaseOrders(db *data.DB) *model.Model[PurchaseOrder]",
	} {
		if !strings.Contains(model, want) {
			t.Errorf("the generated model does not contain %q:\n%s", want, model)
		}
	}

	service := byPath["app/Services/PurchaseOrderService.go"]
	for _, want := range []string{
		"db     *data.DB",
		"func NewPurchaseOrderService(db *data.DB) *PurchaseOrderService",
		"models.PurchaseOrders(s.db)",
	} {
		if !strings.Contains(service, want) {
			t.Errorf("the generated service does not contain %q:\n%s", want, service)
		}
	}
	if strings.Contains(service, "/app/Repositories") || strings.Contains(service, "repositories.") {
		t.Errorf("the generated service still reaches the CRUD repository:\n%s", service)
	}

	controller := byPath["app/Http/Controllers/PurchaseOrderController.go"]
	for _, want := range []string{
		"row(ctx *fhttp.Context, p *models.PurchaseOrder)",
		"form(p *models.PurchaseOrder)",
	} {
		if !strings.Contains(controller, want) {
			t.Errorf("the controller does not retain the Model-backed entity pointer in %q:\n%s", want, controller)
		}
	}
}

// TestGeneratedViewsImportTheirNativeComponent keeps generated code on the
// component package instead of growing a new dependency on the Framework
// compatibility bridge.
func TestGeneratedViewsImportTheirNativeComponent(t *testing.T) {
	files, err := gen.Generate(spec(false))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	examined := 0
	for _, file := range files {
		if !strings.HasSuffix(file.Path, ".kyse.go") && !strings.HasSuffix(file.Path, "Controller.go") {
			continue
		}
		examined++
		body := string(file.Content)
		if strings.Contains(body, `"github.com/arandu-io/framework/view"`) {
			t.Errorf("%s imports the Framework view bridge:\n%s", file.Path, body)
		}
		if !strings.Contains(body, `"github.com/arandu-io/hesape/view"`) {
			t.Errorf("%s does not import the native view component:\n%s", file.Path, body)
		}
	}
	if examined != 5 {
		t.Fatalf("examined %d generated view consumers, want the controller and four screens", examined)
	}
}

// TestGeneratedUnitTestsExerciseTheModelFirstBoundary keeps make:test aligned
// with make:module. A test generated later must not revive the CRUD repository
// that the module generator deliberately omitted.
func TestGeneratedUnitTestsExerciseTheModelFirstBoundary(t *testing.T) {
	file, err := gen.RenderTest(spec(true))
	if err != nil {
		t.Fatalf("RenderTest: %v", err)
	}
	body := string(file.Content)
	for _, want := range []string{
		`services "example.test/project/app/Services"`,
		"services.NewPurchaseOrderService(nil)",
		"model.Builder[models.PurchaseOrder]",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the generated test does not contain %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "/app/Repositories") || strings.Contains(body, "repositories.") {
		t.Errorf("make:test still generates a test against the CRUD repository:\n%s", body)
	}
}

// TestTheGeneratorNeverWritesTheHumanTenantClaim keeps the deliberate red/green
// handoff intact. The migration is mechanical; claiming that every query was
// reviewed is not, so Generate may print guidance through its caller but may
// never manufacture or edit the feature-test registry.
func TestTheGeneratorNeverWritesTheHumanTenantClaim(t *testing.T) {
	files, err := gen.Generate(spec(true))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, file := range files {
		if strings.HasSuffix(file.Path, "tests/Feature/TenantScope_test.go") {
			t.Fatalf("the generator fabricated the human tenant claim in %s", file.Path)
		}
		if strings.Contains(string(file.Content), "scopedByTenant") {
			t.Fatalf("the generator smuggled the human tenant claim into %s", file.Path)
		}
	}
}
