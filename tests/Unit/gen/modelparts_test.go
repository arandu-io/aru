package gen_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/gen"
)

// TestEverythingWritesTheEntityAndThePathToIt.
//
// --all is the entity and the path a request takes to it: the migration that
// creates its table, the factory that builds it, the seeder that fills it, the
// policy that decides who may reach it, the request that validates the input,
// the service that joins them, and a resource controller built with that
// service whose seven actions answer 501. It is still not make:module: no
// screen, no action written, no skill -- and no Repository, which the CRUD
// path does not have whichever command writes it.
func TestEverythingWritesTheEntityAndThePathToIt(t *testing.T) {
	files, err := gen.GenerateModel(invoiceModule(), gen.Everything())
	if err != nil {
		t.Fatalf("GenerateModel: %v", err)
	}

	got := make([]string, 0, len(files))
	for _, f := range files {
		got = append(got, filepath.ToSlash(f.Path))
	}
	slices.Sort(got)

	want := []string{
		"app/Http/Controllers/InvoiceController.go",
		"app/Http/Requests/InvoiceRequest.go",
		"app/Models/Invoice.go",
		"app/Models/InvoiceQuery.go",
		"app/Policies/InvoicePolicy.go",
		"app/Services/InvoiceService.go",
		"database/factories/InvoiceFactory.go",
		"database/migrations/2026_08_07_000001_create_invoices_table.go",
		"database/seeders/InvoiceSeeder.go",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("--all wrote\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	for _, f := range got {
		if strings.Contains(f, "Repositories") || strings.Contains(f, "resources/views") || strings.Contains(f, "SKILL.md") {
			t.Errorf("--all wrote %s, which belongs to make:module or to nobody", f)
		}
	}

	// The service is make:module's, byte for byte: one template, so the
	// mandatory path cannot be written two ways depending on the command.
	fromModule, err := gen.Generate(invoiceModule())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	find := func(files []gen.File, path string) string {
		for _, f := range files {
			if filepath.ToSlash(f.Path) == path {
				return string(f.Content)
			}
		}
		return ""
	}
	if find(files, "app/Services/InvoiceService.go") != find(fromModule, "app/Services/InvoiceService.go") {
		t.Error("make:model --all and make:module write different services for one entity")
	}
}

// TestAServiceIsNotWrittenWithoutWhatItJoins: the service calls the policy
// and takes the request, so a part set that asks for it without both is
// refused rather than written as a file that does not compile.
func TestAServiceIsNotWrittenWithoutWhatItJoins(t *testing.T) {
	if _, err := gen.GenerateModel(invoiceModule(), gen.ModelParts{Service: true, Policy: true}); err == nil {
		t.Error("a service was written without the request it takes")
	}
}

// TestEachPartIsOneFile: a part asked for on its own writes its file and no
// other, so --all is the parts and not a path of its own. The model is two
// files whatever was asked for -- the entity and the query model:build writes
// beside it -- so the part is the third.
func TestEachPartIsOneFile(t *testing.T) {
	for _, c := range []struct {
		name  string
		parts gen.ModelParts
		want  string
	}{
		{"migration", gen.ModelParts{Migration: true}, "database/migrations/2026_08_07_000001_create_invoices_table.go"},
		{"factory", gen.ModelParts{Factory: true}, "database/factories/InvoiceFactory.go"},
		{"seeder", gen.ModelParts{Seeder: true}, "database/seeders/InvoiceSeeder.go"},
		{"policy", gen.ModelParts{Policy: true}, "app/Policies/InvoicePolicy.go"},
		{"request", gen.ModelParts{Request: true}, "app/Http/Requests/InvoiceRequest.go"},
		{"controller", gen.ModelParts{Controller: true}, "app/Http/Controllers/InvoiceController.go"},
	} {
		t.Run(c.name, func(t *testing.T) {
			files, err := gen.GenerateModel(invoiceModule(), c.parts)
			if err != nil {
				t.Fatalf("GenerateModel: %v", err)
			}
			if len(files) != 3 {
				t.Fatalf("--%s wrote %d files, want the model, its query and one part", c.name, len(files))
			}
			if got := filepath.ToSlash(files[2].Path); got != c.want {
				t.Errorf("--%s wrote %s, want %s", c.name, got, c.want)
			}
		})
	}
}

// TestThePolicyIsTheSameFileMakeModuleWrites.
//
// Rendered from one template through one call, so the policy an entity gets from
// make:model and the policy it gets from make:module cannot describe different
// rules. Two templates would diverge at the first change, and the one nobody
// noticed would be the one in the project.
func TestThePolicyIsTheSameFileMakeModuleWrites(t *testing.T) {
	fromModel, err := gen.GenerateModel(invoiceModule(), gen.ModelParts{Policy: true})
	if err != nil {
		t.Fatalf("GenerateModel: %v", err)
	}
	fromModule, err := gen.Generate(invoiceModule())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	policy := func(files []gen.File) string {
		t.Helper()
		for _, f := range files {
			if strings.Contains(filepath.ToSlash(f.Path), "app/Policies/") {
				return string(f.Content)
			}
		}
		t.Fatal("no policy was written")
		return ""
	}

	if policy(fromModel) != policy(fromModule) {
		t.Error("make:model --policy and make:module write different policies for one entity")
	}
}

func invoiceModule() gen.Module {
	return gen.Module{
		Name:       "invoice",
		Fields:     []gen.Field{{Name: "reference", Type: gen.TypeString, Unique: true}},
		Tenant:     true,
		ModulePath: "example.test/project",
		Date:       "2026_08_07",
		Sequence:   1,
	}
}
