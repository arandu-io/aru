package gen

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SeederSpec is one seeder.
//
// It carries the entity, and whether the entity's factory is there to seed
// through: a seeder has no columns of its own, and a factory is what knows how
// to make a row.
type SeederSpec struct {
	// Entity is the exported entity name, with no Seeder suffix: "Invoice".
	Entity string
	// ModulePath is the project's module path, for the imports a seeder that
	// goes through the factory names.
	ModulePath string
	// Factory says the entity has a model, a policy and a factory, so the
	// seeder creates its rows through the factory. Without them it is the
	// empty seeder, which compiles in any project and seeds nothing until it
	// is written.
	Factory bool
}

// ModelsImport, PoliciesImport and FactoriesImport are the project packages a
// seeder that goes through the factory imports.
func (s SeederSpec) ModelsImport() string { return s.ModulePath + "/app/Models" }

// PoliciesImport is the package the action constant comes from.
func (s SeederSpec) PoliciesImport() string { return s.ModulePath + "/app/Policies" }

// FactoriesImport is the package the factory is declared in.
func (s SeederSpec) FactoriesImport() string { return s.ModulePath + "/database/factories" }

// Type is the generated type name: "InvoiceSeeder".
func (s SeederSpec) Type() string { return s.Entity + "Seeder" }

// Human is the entity in a sentence: "purchase order".
func (s SeederSpec) Human() string { return strings.ReplaceAll(Normalize(s.Entity), "_", " ") }

// Humans is the plural of Human, for the doc comment.
func (s SeederSpec) Humans() string {
	return strings.ReplaceAll(Module{Name: Normalize(s.Entity)}.Table(), "_", " ")
}

// Constructor is where the query and the factory of the entity start: Invoices.
func (s SeederSpec) Constructor() string { return Constructor(s.Entity) }

// Path is where the file goes.
func (s SeederSpec) Path() string {
	return filepath.Join("database", "seeders", s.Type()+".go")
}

// Validate reports what is wrong before a file is written.
func (s SeederSpec) Validate() error {
	if !IsExportedIdentifier(s.Entity) {
		return fmt.Errorf("%q is not a Go type name: it has to start with a capital letter and hold only letters, digits and underscore", s.Entity)
	}
	if s.Factory && s.ModulePath == "" {
		return errModulePath
	}
	return nil
}

// RenderSeeder produces database/seeders/<Entity>Seeder.go.
//
// With a factory to go through, the seeder creates its rows with it, and reads
// the database through Deps.DB -- the one line of wiring it needs. Without one
// it depends on no import from the project, and compiles before anything else
// of the entity exists.
func RenderSeeder(s SeederSpec) (File, error) {
	if err := s.Validate(); err != nil {
		return File{}, err
	}
	content, err := render(s.Type()+".go", seederTemplate, s)
	if err != nil {
		return File{}, err
	}
	return File{Path: s.Path(), Content: content}, nil
}

const seederTemplate = `package seeders

{{if .Factory}}import (
	"context"

	"github.com/arandu-io/hesape/auth"

	models "{{.ModelsImport}}"
	policies "{{.PoliciesImport}}"
	factories "{{.FactoriesImport}}"
){{else}}import "context"{{end}}

// {{.Type}} seeds the {{.Human}} rows this application needs to exist.
//
// A seeder writes, and a write needs an auth.Grant. This is one of the few
// places auth.SystemGrant is legitimate -- there is no request and no
// actor behind it -- and ` + "`" + `aru doctor` + "`" + ` allows it here
// because of the directory this file is in, not because of what the function
// is called. Anywhere else it is a warning that has to be answered with
// //arandu:system-grant <reason>.
//
// Run must be safe to run twice. A seeder that fails on the second run cannot
// be part of a deploy, and this one runs on every deploy that runs the first.
type {{.Type}} struct{}

// Name is how the seeder is addressed on the command line.
func ({{.Type}}) Name() string { return "{{.Type}}" }
{{if .Factory}}
// Run creates the rows through the factory, in d.Tenant -- never a tenant this
// file picked: a seeder that chooses its own seeds rows nobody can reach. A
// table that already has rows is left as it is, which is what makes a second
// run safe.
func ({{.Type}}) Run(ctx context.Context, d Deps) error {
	seeded, err := models.{{.Constructor}}(d.DB).Exists(ctx, auth.SystemGrant(policies.{{.Entity}}List, d.Tenant))
	if err != nil || seeded {
		return err
	}
	if _, err := factories.{{.Constructor}}(d.DB).Count(10).Create(ctx, auth.SystemGrant(policies.{{.Entity}}Create, d.Tenant)); err != nil {
		return err
	}

	// arandu:begin custom
	// Rows the factory's defaults do not describe go here: a fixed record, a
	// state applied to some of them.
	// arandu:end custom
	return nil
}
{{else}}
// Run performs the seeding.
func ({{.Type}}) Run(ctx context.Context, d Deps) error {
	// arandu:begin custom
	// Once the entity has its factory -- ` + "`" + `aru make:factory {{.Entity}}` + "`" + ` -- this is
	// two calls, in d.Tenant and never a tenant this file picked:
	//
	//	g := auth.SystemGrant(policies.{{.Entity}}Create, d.Tenant)
	//	_, err := factories.{{.Constructor}}(d.DB).Count(10).Create(ctx, g)
	//
	// Twice safely: ask whether the table already has rows before writing.
	// arandu:end custom
	return nil
}
{{end}}
// compile-time proof that the seeder honors the contract. A seeder that drifts
// from the interface fails the build rather than failing when someone runs it.
var _ Seeder = {{.Type}}{}
`
