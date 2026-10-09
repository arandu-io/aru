package gen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// FactoryField is one field of the entity, as the factory sees it.
//
// It carries the Go type and not the DSL type, and that is the decision rather
// than an oversight: `aru make:model --factory` knows money and date, and
// `aru make:factory` reads the model and only ever sees int64 and time.Time. A
// default that depended on the DSL would make the same file come out two ways
// depending on which command wrote it, which is the definition of two shapes of
// one thing.
type FactoryField struct {
	GoName string
	GoType string
}

// Factory is how a specification field describes itself to a factory.
func (f Field) Factory() FactoryField {
	return FactoryField{GoName: f.GoName(), GoType: f.GoType()}
}

// Fake is the expression the definition fills the field with, from the faker
// it is handed, or empty for a type it has no value for -- that field keeps its
// zero value, and a state sets it.
//
// It reads the Go type and, for text, the name: an address is an address, an
// identifier is a UUID, and any other text is a handle rather than a word,
// because a text column may be unique and a list of words runs out.
func (f FactoryField) Fake() string {
	switch f.GoType {
	case "string":
		switch {
		case strings.Contains(f.GoName, "Email"):
			return "f.Email()"
		case strings.HasSuffix(f.GoName, "ID"):
			return "f.UUID()"
		default:
			return "f.UserName()"
		}
	case "int64":
		return "int64(f.Int(1, 1000))"
	case "float64":
		return "f.Float(0, 1000, 2)"
	case "bool":
		return "f.Bool()"
	case "time.Time":
		return "f.Time(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))"
	default:
		return ""
	}
}

// FactorySpec is what a factory needs to know, and it is deliberately less than
// a Module: the Go type of each field, and whether the entity has a tenant.
type FactorySpec struct {
	Entity       string
	Tenant       bool
	Fields       []FactoryField
	ModelsImport string
}

// Constructor is the function the factory is reached by, in database/factories,
// and the query it builds through, in app/Models: Invoices.
func (s FactorySpec) Constructor() string { return Constructor(s.Entity) }

// Header is the first line of the file, which is also how model:build tells a
// factory it rendered from one somebody wrote: it re-renders only the first kind.
func (s FactorySpec) Header() string { return FactoryHeaderPrefix + s.Entity + FactoryHeaderSuffix }

// Receiver is the short name the example state in the doc comment uses.
func (s FactorySpec) Receiver() string { return Module{Name: Normalize(s.Entity)}.Receiver() }

// Type is the generated type name.
func (s FactorySpec) Type() string { return s.Entity + "Factory" }

// Humans is the plural, for the doc comment.
func (s FactorySpec) Humans() string {
	return strings.ReplaceAll(Module{Name: Normalize(s.Entity)}.Table(), "_", " ")
}

// NeedsTime reports whether a generated value names the time package.
func (s FactorySpec) NeedsTime() bool {
	for _, f := range s.Fields {
		if f.GoType == "time.Time" {
			return true
		}
	}
	return false
}

// Path is where the file goes.
func (s FactorySpec) Path() string {
	return filepath.Join("database", "factories", s.Type()+".go")
}

// Validate reports what is wrong before a file is written.
func (s FactorySpec) Validate() error {
	if !IsExportedIdentifier(s.Entity) {
		return fmt.Errorf("%q is not a Go type name", s.Entity)
	}
	if s.ModelsImport == "" {
		return errModulePath
	}
	return nil
}

// RenderFactory produces database/factories/<Entity>Factory.go.
func RenderFactory(s FactorySpec) (File, error) {
	if err := s.Validate(); err != nil {
		return File{}, err
	}
	content, err := render(s.Type()+".go", factoryTemplate, s)
	if err != nil {
		return File{}, err
	}
	return File{Path: s.Path(), Content: content}, nil
}

// FieldsFromModel reads the entity's fields off app/Models/<Entity>.go.
//
// The model is the schema here -- it is a struct, not a class that discovers its
// columns at runtime -- so the factory is derived from it rather than declared
// again. Two sources of truth about one set of columns is how they drift, which
// is the same reason make:policy reads the tenant off this function instead of
// asking for it a second time.
//
// ID, TenantID, CreatedAt, UpdatedAt and DeletedAt are skipped: the first is
// drawn by the model on insert, the second comes from the Grant, and the rest
// from the clock. The embedded model.Model has no name, so it is never a field.
func FieldsFromModel(path, entity string) (fields []FactoryField, tenant bool, err error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, false, err
	}

	var found *ast.StructType
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok || spec.Name.Name != entity {
			return true
		}
		if st, ok := spec.Type.(*ast.StructType); ok {
			found = st
		}
		return false
	})
	if found == nil {
		return nil, false, fmt.Errorf("%s declares no type %s", path, entity)
	}

	for _, f := range found.Fields.List {
		goType := typeExpr(f.Type)
		for _, name := range f.Names {
			switch name.Name {
			case "ID", "CreatedAt", "UpdatedAt", "DeletedAt":
				continue
			case "TenantID":
				tenant = true
				continue
			}
			if !name.IsExported() {
				continue
			}
			fields = append(fields, FactoryField{GoName: name.Name, GoType: goType})
		}
	}
	return fields, tenant, nil
}

// typeExpr renders a field's type back to source, for the subset a generated
// model uses. Anything else comes back as written, and the compiler is what
// reports it -- a factory that guessed would emit a default of the wrong type.
func typeExpr(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return typeExpr(t.X) + "." + t.Sel.Name
	case *ast.StarExpr:
		return "*" + typeExpr(t.X)
	case *ast.ArrayType:
		return "[]" + typeExpr(t.Elt)
	default:
		return ""
	}
}

// FactoryHeaderPrefix and FactoryHeaderSuffix frame the first line of a factory
// file, around the entity: "// Rendered by aru model:build for models.Invoice.".
//
// It is not the standard generated-code marker, because the file has a part
// that is somebody's: the custom block holds the default state and the named
// states, and an editor that refused to edit the file would refuse that too.
const (
	FactoryHeaderPrefix = "// Rendered by aru model:build for models."
	FactoryHeaderSuffix = ". Everything outside the custom block is rewritten on the next build."
)

// FactoryEntity reports whether content is a factory model:build rendered, and
// for which entity.
func FactoryEntity(content []byte) (string, bool) {
	line, _, _ := strings.Cut(string(content), "\n")
	rest, found := strings.CutPrefix(line, FactoryHeaderPrefix)
	if !found {
		return "", false
	}
	entity, found := strings.CutSuffix(rest, FactoryHeaderSuffix)
	return entity, found && IsExportedIdentifier(entity)
}

// factoryTemplate is the typed factory of one entity over the core factory.
//
// Everything above the custom block is a forward that converts at the boundary,
// and is rewritten on every build. The default state is the one thing a person
// writes, so it is the first thing in the custom block: rendered once from the
// fields, and after that it is theirs.
const factoryTemplate = `{{.Header}}

package factories

import (
	"context"
{{- if .NeedsTime}}
	"time"
{{- end}}

	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/database/model"
	factory "github.com/arandu-io/hesape/database/model/factories"
	"github.com/arandu-io/hesape/faker"

	models "{{.ModelsImport}}"
)

// {{.Type}} builds rows of {{.Humans}}, for tests and for seeding.
//
// Make builds rows and stores nothing. Create stores them, and takes the Grant
// every write takes: {{if .Tenant}}the tenant comes off it, and {{end}}a factory is no way around the
// policy that guards the table.
//
// Every method returns a new factory, so a factory kept in a variable is never
// changed by a caller that adds a state to it.
type {{.Type}} struct{ f *factory.Factory }

// {{.Constructor}} returns the factory of {{.Humans}} over db, with define{{.Entity}} as
// its default state.
//
//	rows, err := factories.{{.Constructor}}(db).Count(10).Create(ctx, g)
//	one, err := factories.{{.Constructor}}(db).State(func({{.Receiver}} *models.{{.Entity}}) { ... }).MakeOne()
//
// The values come from a seeded faker -- the same rows on every run, so a
// failure reproduces -- and Seed asks for others. The key is left empty: the
// model draws a fresh one for every row it stores, so two batches never share
// one, and Make builds rows that have none yet.
func {{.Constructor}}(db model.DB) *{{.Type}} {
	return &{{.Type}}{f: factory.New(models.{{.Constructor}}(db).Base(), func(f faker.Faker, row model.Entity) {
		*row.(*models.{{.Entity}}) = define{{.Entity}}(f)
	})}
}

// Count returns a factory that makes n rows.
func (x *{{.Type}}) Count(n int) *{{.Type}} { return &{{.Type}}{f: x.f.Count(n)} }

// Seed returns a factory whose values start from seed.
func (x *{{.Type}}) Seed(seed int64) *{{.Type}} { return &{{.Type}}{f: x.f.Seed(seed)} }

// State returns a factory that applies fn to every row after the default state.
func (x *{{.Type}}) State(fn func(*models.{{.Entity}})) *{{.Type}} {
	return &{{.Type}}{f: x.f.State(func(row model.Entity) { fn(row.(*models.{{.Entity}})) })}
}

// Sequence returns a factory that cycles through states, one per row.
func (x *{{.Type}}) Sequence(states ...func(*models.{{.Entity}})) *{{.Type}} {
	adapted := make([]func(model.Entity), len(states))
	for i, state := range states {
		adapted[i] = func(row model.Entity) { state(row.(*models.{{.Entity}})) }
	}
	return &{{.Type}}{f: x.f.Sequence(adapted...)}
}

// AfterMaking returns a factory that runs fn on each row once it is built.
func (x *{{.Type}}) AfterMaking(fn func(*models.{{.Entity}})) *{{.Type}} {
	return &{{.Type}}{f: x.f.AfterMaking(func(row model.Entity) { fn(row.(*models.{{.Entity}})) })}
}

// AfterCreating returns a factory that runs fn on each row once it is stored.
func (x *{{.Type}}) AfterCreating(fn func(context.Context, auth.Grant, *models.{{.Entity}}) error) *{{.Type}} {
	return &{{.Type}}{f: x.f.AfterCreating(func(ctx context.Context, g auth.Grant, row model.Entity) error {
		return fn(ctx, g, row.(*models.{{.Entity}}))
	})}
}

// Make returns the rows without storing any of them.
func (x *{{.Type}}) Make() (models.{{.Entity}}Collection, error) {
	rows, err := x.f.Make()
	return x.collection(rows), err
}

// MakeOne returns one row without storing it, whatever Count says.
func (x *{{.Type}}) MakeOne() (*models.{{.Entity}}, error) {
	e, err := x.f.MakeOne()
	row, _ := e.(*models.{{.Entity}})
	return row, err
}

// Create stores the rows and returns them.
func (x *{{.Type}}) Create(ctx context.Context, g auth.Grant) (models.{{.Entity}}Collection, error) {
	rows, err := x.f.Create(ctx, g)
	return x.collection(rows), err
}

// CreateOne stores one row and returns it, whatever Count says.
func (x *{{.Type}}) CreateOne(ctx context.Context, g auth.Grant) (*models.{{.Entity}}, error) {
	e, err := x.f.CreateOne(ctx, g)
	row, _ := e.(*models.{{.Entity}})
	return row, err
}

// collection converts the rows the core factory returns. It is a method of
// its own so that Create, which spends a Grant, calls nothing but the core.
func (x *{{.Type}}) collection(rows model.Rows) models.{{.Entity}}Collection {
	return models.{{.Entity}}CollectionOf(rows)
}

// arandu:begin custom
// define{{.Entity}} is the default state: every row the factory makes starts here,
// and a State changes the part a test cares about. Named states go here too.
func define{{.Entity}}(f faker.Faker) models.{{.Entity}} {
	return models.{{.Entity}}{
{{- range .Fields}}
{{- if .Fake}}
		{{.GoName}}: {{.Fake}},
{{- end}}
{{- end}}
	}
}
// arandu:end custom
`
