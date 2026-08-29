package gen_test

import (
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/gen"
)

// updated_at, in every place that has to agree about it.
//
// It used to appear nowhere in this generator. The model had no field, so the
// migration created no column, so the persistence layer wrote no value -- and
// the model, which defaults UpdatedAtColumn to "updated_at" and guards the stamp
// on hasColumn, found no field and skipped the stamp without a word.
//
// That is the shape of the defect worth a test of its own: four files that have
// to agree, none of which fails when they do not. Every assertion below is one
// of the four.

func rendered(t *testing.T, name string) string {
	t.Helper()
	files, err := gen.Generate(spec(true))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, f := range files {
		if strings.HasSuffix(f.Path, name) {
			return string(f.Content)
		}
	}
	t.Fatalf("%s was not generated", name)
	return ""
}

// TestTheModelDeclaresUpdatedAt, with the tag the model layer reads.
func TestTheModelDeclaresUpdatedAt(t *testing.T) {
	model := rendered(t, "app/Models/PurchaseOrder.go")

	if !strings.Contains(model, "UpdatedAt") || !strings.Contains(model, "`db:\"updated_at\"`") {
		t.Errorf("the model has no UpdatedAt with its column tag:\n%s", model)
	}
	// Every column carries a tag, so that renaming a Go field is a compile
	// question rather than a silent rename of the column.
	if !strings.Contains(model, "`db:\"created_at\"`") || !strings.Contains(model, "`db:\"reference\"`") {
		t.Errorf("the columns are not tagged:\n%s", model)
	}
}

// TestTheMigrationCreatesUpdatedAt.
func TestTheMigrationCreatesUpdatedAt(t *testing.T) {
	migration := rendered(t, "create_purchase_orders_table.go")

	// table.Timestamps() declares created_at and updated_at together, which is
	// the point of it having a name of its own: two columns that always travel
	// as a pair are one decision, and a migration that declared one of them
	// would be the defect this test was written for.
	if !strings.Contains(migration, "table.Timestamps()") {
		t.Errorf("the migration does not declare the timestamps:\n%s", migration)
	}
}

// TestTheModelSavesOnBothWritePaths keeps timestamp ownership on the embedded
// Model. Save stamps created_at/updated_at for a new instance and updated_at for
// an existing one, so Create and Update must both use it.
func TestTheModelSavesOnBothWritePaths(t *testing.T) {
	service := rendered(t, "app/Services/PurchaseOrderService.go")

	if !strings.Contains(service, "candidate.Save(ctx, g)") {
		t.Errorf("Create does not save through the timestamp-aware Model:\n%s", service)
	}
	if !strings.Contains(service, "stored.Save(ctx, g)") {
		t.Errorf("Update does not save through the timestamp-aware Model:\n%s", service)
	}
}

// TestUpdatedAtCannotBeDeclaredTwice: the name is reserved, because --fields
// accepting it produced a second column of the same name.
func TestUpdatedAtCannotBeDeclaredTwice(t *testing.T) {
	fields, err := gen.ParseFields("updated_at:timestamp")
	if err != nil {
		t.Fatalf("ParseFields: %v", err)
	}
	module := spec(true)
	module.Fields = fields

	if err := module.Validate(); err == nil {
		t.Error("updated_at was accepted as a field; the generator now emits one of its own, and the table would have two")
	} else if !strings.Contains(err.Error(), "updated_at") {
		t.Errorf("the error was %v, want it to name the field", err)
	}
}
