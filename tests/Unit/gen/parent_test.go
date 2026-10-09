package gen_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/gen"
)

// parentModel is a parent's model as make:module writes it, with the key
// field and the table spec handed in.
func parentModel(key, spec string) string {
	return `package models

import "github.com/arandu-io/hesape/database/model"

type Note struct {
	model.Model

	` + key + `
	Title string ` + "`db:\"title\"`" + `
}

var noteTable = model.NewTable(model.TableSpec{
	` + spec + `
	New: func() model.Entity { return new(Note) },
})
`
}

// parentMigration is the migration that creates the notes table, with the
// line that declares its key handed in.
func parentMigration(key string) string {
	return `package migrations

func (CreateNotesTable) Up(ctx context.Context, conn migrations.Connection) error {
	return conn.Schema().Create(ctx, "notes", func(table *schema.Blueprint) {
		` + key + `
		table.String("title")
		table.Timestamps()
	})
}
`
}

// TestTheParentKeyIsReadFromTheParent is the column a nested table stores its
// parent's id in. It used to be a UUID column whatever the parent was, and the
// key every module this generator writes is text: on Postgres the child's
// factory, writing a text key or an empty one, failed on its first row.
func TestTheParentKeyIsReadFromTheParent(t *testing.T) {
	for _, c := range []struct {
		name, model, migration string
		want                   gen.Type
		refusal                string
	}{
		{
			name:      "a text key, as every generated module has",
			model:     parentModel("ID string `db:\"id\"`", `Name: "notes", UniqueIDs: true,`),
			migration: parentMigration(`table.String("id").Primary()`),
			want:      gen.TypeString,
		},
		{
			name:      "a UUID column",
			model:     parentModel("ID string `db:\"id\"`", `Name: "notes",`),
			migration: parentMigration(`table.UUID("id").Primary()`),
			want:      gen.TypeUUID,
		},
		{
			name:  "a text key with no migration to say otherwise",
			model: parentModel("ID string `db:\"id\"`", `Name: "notes",`),
			want:  gen.TypeString,
		},
		{
			name:      "a key under another name, as PrimaryKey declares it",
			model:     parentModel("Code string `db:\"code\"`", `Name: "notes", PrimaryKey: "code",`),
			migration: parentMigration(`table.UUID("code").Primary()`),
			want:      gen.TypeUUID,
		},
		{
			name:    "an integer key",
			model:   parentModel("ID int64 `db:\"id\"`", `Name: "notes",`),
			refusal: "the key of notes is int64",
		},
		{
			name:    "no parent written yet",
			refusal: "write the parent first",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			if c.model != "" {
				writeInto(t, filepath.Join(root, "app", "Models", "Note.go"), []byte(c.model))
			}
			if c.migration != "" {
				writeInto(t, filepath.Join(root, "database", "migrations", "2026_10_01_000001_create_notes_table.go"), []byte(c.migration))
			}
			m := gen.Module{Name: "comment", Parent: "notes", ModulePath: "example.test/project"}

			got, err := gen.ParentKeyOf(root, m)
			if c.refusal != "" {
				if err == nil || !strings.Contains(err.Error(), c.refusal) {
					t.Fatalf("ParentKeyOf = %q, %v: want a refusal saying %q", got, err, c.refusal)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("ParentKeyOf = %q, %v: want %q", got, err, c.want)
			}

			m.Fields = []gen.Field{{Name: "body", Type: gen.TypeText}}
			m.Date = "2026_10_09"
			m.ParentKey = got
			migration, err := gen.RenderMigration(m.MigrationSpec())
			if err != nil {
				t.Fatal(err)
			}
			column := `table.` + map[gen.Type]string{gen.TypeString: "String", gen.TypeUUID: "UUID"}[c.want] + `("note_id")`
			if !strings.Contains(string(migration.Content), column) {
				t.Errorf("the nested migration does not declare %s:\n%s", column, migration.Content)
			}
		})
	}
}

// TestANestedModuleBuildsItsRowsUnderAStoredParent is the factory, the seeder
// and the tenant test of a nested module. None of them may store a row under a
// key that names no parent: the factory's default state leaves the key empty
// and offers the state that fills it, the seeder loads or makes one parent
// through its Grant, and the tenant test stores the parent before the rows.
func TestANestedModuleBuildsItsRowsUnderAStoredParent(t *testing.T) {
	m := spec(true)
	m.Parent = "suppliers"
	files, err := gen.Generate(m)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]string{}
	for _, f := range files {
		byName[filepath.Base(f.Path)] = string(f.Content)
	}

	factory := byName["PurchaseOrderFactory.go"]
	if strings.Contains(factory, "SupplierID:") {
		t.Errorf("the default state names a supplier, which is a key nobody stored:\n%s", factory)
	}
	if !strings.Contains(factory, "func (x *PurchaseOrderFactory) ForSupplier(supplierID string) *PurchaseOrderFactory") {
		t.Errorf("the factory has no state that puts its rows under a supplier:\n%s", factory)
	}

	seeder := byName["PurchaseOrderSeeder.go"]
	for _, want := range []string{
		"models.Suppliers(d.DB).First(ctx, auth.SystemGrant(policies.SupplierList, d.Tenant))",
		"factories.Suppliers(d.DB).CreateOne(ctx, auth.SystemGrant(policies.SupplierCreate, d.Tenant))",
		"factories.PurchaseOrders(d.DB).ForSupplier(parent.ID)",
	} {
		if !strings.Contains(seeder, want) {
			t.Errorf("the seeder does not carry %q:\n%s", want, seeder)
		}
	}

	scope := byName["PurchaseOrderTenantScope_test.go"]
	if !strings.Contains(scope, "factories.PurchaseOrders(db).ForSupplier(parent.ID)") {
		t.Errorf("the tenant test stores rows under no supplier:\n%s", scope)
	}
}

// TestATopLevelModuleNamesNoParent keeps the parent's lines out of a module
// that nests under nothing.
func TestATopLevelModuleNamesNoParent(t *testing.T) {
	files, err := gen.Generate(spec(true))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.Contains(string(f.Content), "parent.ID") || strings.Contains(string(f.Content), "parent, err :=") {
			t.Errorf("%s names a parent the module does not have", f.Path)
		}
	}
}
