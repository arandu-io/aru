package gen_test

import (
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/gen"
)

// updated_at, in every place that has to agree about it.
//
// It used to appear nowhere in this generator. The model had no field, so the
// migration created no column, so the repository wrote no value -- and the
// model layer, which defaults UpdatedAtColumn to "updated_at" and guards the
// stamp on hasColumn, found no field and skipped the stamp without a word.
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

	if !strings.Contains(model, "UpdatedAt") && strings.Contains(model, "`db:\"updated_at\"`") {
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

// TestTheRepositoryWritesUpdatedAtOnBothPaths is the half that was wrong twice:
// Create never wrote the column, and Update stamped no timestamp at all.
func TestTheRepositoryWritesUpdatedAtOnBothPaths(t *testing.T) {
	repo := rendered(t, "app/Repositories/PurchaseOrderRepository.go")

	if !strings.Contains(repo, "updated_at") {
		t.Fatalf("the repository never names updated_at:\n%s", repo)
	}
	if !strings.Contains(repo, "p.UpdatedAt = p.CreatedAt") {
		t.Error("Create does not stamp updated_at; a row that was never updated has no value in a NOT NULL column")
	}
	if !strings.Contains(repo, "updated_at = ? WHERE id = ?") {
		t.Error("Update does not stamp updated_at, which makes the column a lie")
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
