package gen_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/gen"
)

// The tests here each lock one defect an audit found in the generated code.
// They read the generated source rather than run it, because what went wrong
// was what got written -- and a golden file records the whole output without
// saying which line is the one that matters.

// TestTheCursorFollowsTheSortColumn: keyset pagination compared created_at
// while ordering by whatever q.Sort named. Any sort other than the default
// computed the page boundary on one ordering and returned the rows in another,
// so pages skipped rows and repeated rows, silently and with no error.
func TestTheCursorFollowsTheSortColumn(t *testing.T) {
	for _, tenant := range []bool{true, false} {
		repo := byName(t, spec(tenant))["PurchaseOrderRepository.go"]

		if strings.Contains(repo, "SELECT created_at FROM") {
			t.Errorf("tenant=%v: the cursor still reads created_at instead of the sorted column", tenant)
		}
		if !strings.Contains(repo, "` + column + `") {
			t.Errorf("tenant=%v: the cursor predicate does not name the sorted column", tenant)
		}
		// The ordering and the predicate have to be the same column, so both
		// sides of the comparison come from the allowlist and neither from the
		// request.
		if !strings.Contains(repo, "ORDER BY ` + column + `") {
			t.Errorf("tenant=%v: the ORDER BY no longer uses the allowlisted column", tenant)
		}
	}
}

// TestListingIsItsOwnPermission: ActionList was declared, granted in the
// specification, and never checked. List authorized ActionView, so whoever
// could open one record could page through every record there is.
func TestListingIsItsOwnPermission(t *testing.T) {
	files := byName(t, spec(true))

	repo := files["PurchaseOrderRepository.go"]
	// The action constant carries the entity now: PurchaseOrderList, not
	// ActionList. app/Policies/ is one package for every entity.
	if !strings.Contains(repo, "PurchaseOrderList") {
		t.Error("Repo.List does not check the list permission")
	}

	service := files["PurchaseOrderService.go"]
	if !strings.Contains(service, "PurchaseOrderList") {
		t.Error("Service.List does not authorize the list permission")
	}
}

// TestARequiredNumberCanBeFilledIn: Required takes a string, and the generator
// handed it a literal "" for every non-text field. So a required int, date,
// amount or decimal failed validation with "is required" no matter what was
// sent, and the generated create endpoint could not be used at all.
func TestARequiredNumberCanBeFilledIn(t *testing.T) {
	request := byName(t, spec(true))["PurchaseOrderRequest.go"]

	if strings.Contains(request, `validation.Required(e, "total", "")`) {
		t.Fatal("a required amount is still validated against a literal empty string")
	}
	if !strings.Contains(request, `validation.NotZero(e, "total", r.Total)`) {
		t.Error("a required amount is not validated against the value that was sent")
	}
	// Text still goes through Required, which trims: "   " is not an answer.
	if !strings.Contains(request, `validation.Required(e, "reference", r.Reference)`) {
		t.Error("required text no longer goes through Required")
	}
}

// TestDecimalKeepsItsDigits: REAL is four bytes in PostgreSQL, so a float64
// written through it came back rounded to about seven digits -- on read,
// silently, with no error anywhere.
func TestDecimalKeepsItsDigits(t *testing.T) {
	m := spec(true)
	m.Fields = append(m.Fields, gen.Field{Name: "rate", Type: gen.TypeDecimal})

	// Collapsed, because the DDL aligns its columns and the alignment is not
	// what this test is about.
	migration := collapse(migrationOf(t, m))
	if strings.Contains(migration, "rate REAL") {
		t.Fatal("decimal is still REAL, which is four bytes in PostgreSQL")
	}
	if !strings.Contains(migration, "rate DOUBLE PRECISION") {
		t.Errorf("decimal is not DOUBLE PRECISION:\n%s", migration)
	}
}

// byName generates a module and returns its files keyed by base name.
func byName(t *testing.T, m gen.Module) map[string]string {
	t.Helper()
	files, err := gen.Generate(m)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := map[string]string{}
	for _, f := range files {
		out[filepath.Base(f.Path)] = string(f.Content)
	}
	return out
}

// collapse squeezes runs of whitespace to one space, so a test about content
// does not fail on alignment.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

// TestNothingKeyedIsDeclaredTEXT is the defect that made MySQL unusable, and it
// is checked by shape rather than by example because it repeated in five places.
//
// MySQL stores TEXT off-page and refuses it in a key without a prefix length, so
// `id TEXT PRIMARY KEY` fails with "BLOB/TEXT column used in key specification
// without a key length". That is the first statement of the first migration --
// MySQL never got past creating the table. Found by audit.
func TestNothingKeyedIsDeclaredTEXT(t *testing.T) {
	m := spec(true)
	migration := collapse(migrationOf(t, m))

	for _, forbidden := range []string{
		"id TEXT PRIMARY KEY",
		"tenant_id TEXT",
		// reference is the unique field of the fixture.
		"reference TEXT",
	} {
		if strings.Contains(migration, forbidden) {
			t.Errorf("a keyed column is still TEXT: %q", forbidden)
		}
	}
	for _, wanted := range []string{
		"id VARCHAR(255) PRIMARY KEY",
		"tenant_id VARCHAR(255) NOT NULL",
		"reference VARCHAR(255) NOT NULL",
	} {
		if !strings.Contains(migration, wanted) {
			t.Errorf("missing %q:\n%s", wanted, migration)
		}
	}

	// The long kind stays TEXT: it is never indexed, and capping it would make
	// "long text" mean 255 characters.
	if !strings.Contains(migration, "notes TEXT") {
		t.Error("a text field is no longer TEXT")
	}
}

// TestValidationAgreesWithTheColumn: a value that passes validation has to fit
// the column, or the rejection arrives from the database driver and names a
// column rather than the field somebody filled in.
func TestValidationAgreesWithTheColumn(t *testing.T) {
	request := byName(t, spec(true))["PurchaseOrderRequest.go"]

	// reference is a string: VARCHAR(255), so 255.
	if !strings.Contains(request, `validation.MaxLen(e, "reference", r.Reference, 255)`) {
		t.Error("a string field is not capped at the width of its column")
	}
	// supplier_email is an address: the addr-spec limit, under the column.
	if !strings.Contains(request, `validation.MaxLen(e, "supplier_email", r.SupplierEmail, 254)`) {
		t.Error("an email field is not capped at 254")
	}
	// notes is long text: TEXT, so the cap is the generous one.
	if !strings.Contains(request, `validation.MaxLen(e, "notes", r.Notes, 5000)`) {
		t.Error("a text field is capped as if it were a string")
	}
}

// migrationOf devolve a migration gerada, seja qual for o nome do arquivo.
//
// O nome carrega a data, e prender o teste a ela o quebra na virada do dia.
func migrationOf(t *testing.T, m gen.Module) string {
	t.Helper()
	for name, body := range byName(t, m) {
		if strings.Contains(name, "create_") && strings.HasSuffix(name, "_table.go") {
			return body
		}
	}
	t.Fatal("nenhuma migration foi gerada")
	return ""
}
