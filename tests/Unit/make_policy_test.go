package unit_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/tests"
)

// tenantCheck is the line the policy template writes for an entity that
// belongs to a tenant, and the only line that keeps one tenant's row out of
// another tenant's hands at the policy.
const tenantCheck = "TenantID != s.Tenant"

// TestAPolicyRegeneratedForATenantModelKeepsTheTenantCheck runs the commands
// somebody runs, in the order they run them, on a module that has no
// repository.
//
// make:policy decided whether the entity belongs to a tenant by reading
// app/Repositories/<Entity>Repository.go. A module the generator writes today
// has no repository -- the Model is the data entry point -- so the answer was
// always no, and `make:policy note --force` replaced a policy that compared
// the row's tenant with the subject's by one that compared nothing. Every
// action the custom block opened from then on was open across tenants, and the
// file still read like the one the generator writes.
//
// The global entity is the other half: a model with no TenantID has nothing
// to compare, and a policy that compared it would not compile.
func TestAPolicyRegeneratedForATenantModelKeepsTheTenantCheck(t *testing.T) {
	binary := buildPublicContractCLI(t, tests.Root(t))

	project := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":      "module example.test/project\n\ngo 1.26\n",
		"main.go":     "package main\n\nfunc main() {}\n",
		"arandu.toml": "name = \"project\"\n",
	} {
		if err := os.WriteFile(filepath.Join(project, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	policy := func(entity string) string {
		t.Helper()
		body, err := os.ReadFile(filepath.Join(project, "app", "Policies", entity+"Policy.go"))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}

	runPublicContractCLI(t, project, binary, "make:module", "note", "--tenant", "--fields", "title:string!")
	if !strings.Contains(policy("Note"), tenantCheck) {
		t.Fatalf("make:module --tenant wrote a policy without the tenant check:\n%s", policy("Note"))
	}
	if _, err := os.Stat(filepath.Join(project, "app", "Repositories")); !os.IsNotExist(err) {
		t.Fatalf("the module has a repository, so this test no longer describes a model-first module: %v", err)
	}

	runPublicContractCLI(t, project, binary, "make:policy", "note", "--force")
	if !strings.Contains(policy("Note"), tenantCheck) {
		t.Errorf("make:policy --force dropped the tenant check of a model that carries TenantID:\n%s", policy("Note"))
	}

	runPublicContractCLI(t, project, binary, "make:model", "Memo", "--fields", "title:string")
	runPublicContractCLI(t, project, binary, "make:policy", "memo")
	if strings.Contains(policy("Memo"), tenantCheck) {
		t.Errorf("make:policy wrote a tenant check for a model with no TenantID, which does not compile:\n%s", policy("Memo"))
	}

	runPublicContractCLI(t, project, binary, "make:model", "Ticket", "--tenant", "--fields", "title:string")
	runPublicContractCLI(t, project, binary, "make:policy", "ticket")
	if !strings.Contains(policy("Ticket"), tenantCheck) {
		t.Errorf("make:policy wrote no tenant check for a model that carries TenantID:\n%s", policy("Ticket"))
	}
}
