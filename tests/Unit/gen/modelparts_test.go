package gen_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/gen"
)

// TestEverythingWritesTheDataSideAndNothingElse.
//
// --all is the entity: the migration that creates its table, the factory that
// builds it, the seeder that fills it, the policy that decides who may reach it,
// and the request that validates the input. It is not a smaller make:module --
// the controller, the service, the repository, the views and the route wiring
// are the feature, and a --all that wrote them would be a second spelling of a
// command that already exists.
func TestEverythingWritesTheDataSideAndNothingElse(t *testing.T) {
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
		"app/Http/Requests/InvoiceRequest.go",
		"app/Models/Invoice.go",
		"app/Policies/InvoicePolicy.go",
		"database/factories/InvoiceFactory.go",
		"database/migrations/2026_08_07_000001_create_invoices_table.go",
		"database/seeders/InvoiceSeeder.go",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("--all wrote\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}

	// The repository is the one thing it must not write, and the reason is in
	// gen.GenerateModel: a repository pulls a policy and a service with it, and
	// the mandatory path is indivisible.
	for _, f := range got {
		if strings.Contains(f, "Repositories") || strings.Contains(f, "Controllers") || strings.Contains(f, "Services") {
			t.Errorf("--all wrote %s, which belongs to make:module", f)
		}
	}
}

// TestEachPartIsOneFile: a part asked for on its own writes its file and no
// other, so --all is the parts and not a path of its own.
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
	} {
		t.Run(c.name, func(t *testing.T) {
			files, err := gen.GenerateModel(invoiceModule(), c.parts)
			if err != nil {
				t.Fatalf("GenerateModel: %v", err)
			}
			if len(files) != 2 {
				t.Fatalf("--%s wrote %d files, want the model and one part", c.name, len(files))
			}
			if got := filepath.ToSlash(files[1].Path); got != c.want {
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
