package gen

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SeederSpec is one seeder.
//
// It carries the entity and nothing else: a seeder has no columns, and what it
// needs to know -- which repository to call -- is a wiring decision that the
// command prints rather than a flag it takes.
type SeederSpec struct {
	// Entity is the exported entity name, with no Seeder suffix: "Invoice".
	Entity string
}

// Type is the generated type name: "InvoiceSeeder".
func (s SeederSpec) Type() string { return s.Entity + "Seeder" }

// Human is the entity in a sentence: "purchase order".
func (s SeederSpec) Human() string { return strings.ReplaceAll(Normalize(s.Entity), "_", " ") }

// Humans is the plural of Human, for the doc comment.
func (s SeederSpec) Humans() string {
	return strings.ReplaceAll(Module{Name: Normalize(s.Entity)}.Table(), "_", " ")
}

// Plural is the exported plural, which is what the field in Deps is called.
func (s SeederSpec) Plural() string { return Module{Name: Normalize(s.Entity)}.Plural() }

// Receiver is the short local name the commented example uses.
func (s SeederSpec) Receiver() string { return Module{Name: Normalize(s.Entity)}.Receiver() }

// Path is where the file goes.
func (s SeederSpec) Path() string {
	return filepath.Join("database", "seeders", s.Type()+".go")
}

// Validate reports what is wrong before a file is written.
func (s SeederSpec) Validate() error {
	if !IsExportedIdentifier(s.Entity) {
		return fmt.Errorf("%q is not a Go type name: it has to start with a capital letter and hold only letters, digits and underscore", s.Entity)
	}
	return nil
}

// RenderSeeder produces database/seeders/<Entity>Seeder.go.
//
// It depends on no import from the project, and that is what keeps it compiling
// before the wiring exists: a seeder generated with the repository call already
// written would not build until the developer had edited seeders.Deps and
// main.go, and RULE 5 says what comes out of the generator compiles.
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

import "context"

// {{.Type}} seeds the {{.Human}} rows this application needs to exist.
//
// A seeder writes, and a write needs a security.Grant. This is one of the few
// places security.SystemGrant is legitimate -- there is no request and no
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

// Run performs the seeding.
func ({{.Type}}) Run(ctx context.Context, d Deps) error {
	// arandu:begin custom
	// What this looks like once it is wired -- the four lines, in order:
	//
	//	g := security.SystemGrant(policies.{{.Entity}}Create, d.Tenant)
	//	{{.Receiver}} := factories.New{{.Entity}}Factory(d.Tenant).Make(1)
	//	if _, err := d.{{.Plural}}.Create(ctx, g, {{.Receiver}}); err != nil {
	//		return err
	//	}
	//
	// d.Tenant, never a tenant this file picked: a seeder that chooses its own
	// seeds rows nobody can reach (RULE 14).
	//
	// Twice safely: read before writing, or let the UNIQUE refuse the duplicate
	// and treat models.Err{{.Entity}}Conflict as success.
	// arandu:end custom
	return nil
}

// compile-time proof that the seeder honors the contract. A seeder that drifts
// from the interface fails the build rather than failing when someone runs it.
var _ Seeder = {{.Type}}{}
`
