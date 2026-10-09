package gen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Answered is one field a JSON Resource answers with: the Go field it reads and
// the key it is answered under, which is the column.
type Answered struct {
	GoName string
	Key    string
}

// ResourceSpec is one JSON Resource to write: the entity it wraps and the
// fields it lets leave.
type ResourceSpec struct {
	// Entity is the model's type: Invoice.
	Entity string
	// Table is the plural the collection answers its rows under: invoices.
	Table string
	// Tenant reports whether the model carries a tenant, which the test sets so
	// that it can prove the tenant is not answered.
	Tenant bool
	// Fields are the allow-list, in the model's order.
	Fields []Answered
	// ModulePath is the project's, so the generated imports resolve.
	ModulePath string
}

// Type is the resource's type name: InvoiceResource.
func (s ResourceSpec) Type() string { return s.Entity + "Resource" }

// Collection is the collection's type name: InvoiceCollection.
func (s ResourceSpec) Collection() string { return s.Entity + "Collection" }

// Keys are the allow-list's keys, sorted, for the test that compares them
// against what was answered.
func (s ResourceSpec) Keys() []string {
	keys := make([]string, 0, len(s.Fields))
	for _, f := range s.Fields {
		keys = append(keys, f.Key)
	}
	sort.Strings(keys)
	return keys
}

// ModelsImport and ResourcesImport are where the two packages live.
func (s ResourceSpec) ModelsImport() string    { return s.ModulePath + "/app/Models" }
func (s ResourceSpec) ResourcesImport() string { return s.ModulePath + "/app/Http/Resources" }

// RenderResource produces app/Http/Resources/<Entity>Resource.go and the test
// that proves ctx.JSON answers with the listed fields and nothing else.
func RenderResource(s ResourceSpec) ([]File, error) {
	if !IsExportedIdentifier(s.Entity) || s.ModulePath == "" || s.Table == "" {
		return nil, fmt.Errorf("a resource needs an entity, its table and the project module path")
	}
	resource, err := render(s.Type()+".go", resourceTemplate, s)
	if err != nil {
		return nil, err
	}
	test, err := render(s.Type()+"_test.go", resourceTestTemplate, s)
	if err != nil {
		return nil, err
	}
	return []File{
		{Path: filepath.Join("app", "Http", "Resources", s.Type()+".go"), Content: resource},
		{Path: filepath.Join("tests", "Unit", s.Type()+"_test.go"), Content: test},
	}, nil
}

// ColumnsFromModel reads what a JSON Resource may answer with off
// app/Models/<Entity>.go: every field with a db tag, under that tag, except the
// tenant -- which says whose row it is, and is never a client's to read -- and
// the soft-delete stamp. It also reports whether the model has a tenant.
func ColumnsFromModel(path, entity string) (fields []Answered, tenant bool, err error) {
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
		found, _ = spec.Type.(*ast.StructType)
		return false
	})
	if found == nil {
		return nil, false, fmt.Errorf("%s declares no type %s", path, entity)
	}
	for _, f := range found.Fields.List {
		if f.Tag == nil {
			continue
		}
		raw, err := strconv.Unquote(f.Tag.Value)
		if err != nil {
			continue
		}
		column, _, _ := strings.Cut(reflect.StructTag(raw).Get("db"), ",")
		if column == "" || column == "-" {
			continue
		}
		for _, name := range f.Names {
			switch {
			case column == "tenant_id":
				tenant = true
			case column == "deleted_at", !name.IsExported():
			default:
				fields = append(fields, Answered{GoName: name.Name, Key: column})
			}
		}
	}
	return fields, tenant, nil
}

const resourceTemplate = `package resources

import (
	hhttp "github.com/arandu-io/hesape/http"

	models "{{.ModelsImport}}"
)

// {{.Type}} is what one {{.Entity}} answers with as JSON: the fields ToArray
// lists, and nothing else.
//
// It exists because an encoder handed the entity answers with whatever fields
// the entity has, including the ones somebody adds to it later without reading
// this file: a password hash, an internal note, the tenant the row belongs to.
// A resource answers with a list somebody wrote. ctx.JSON takes one and no
// other value, and so does mcp.JSON, so a controller and a tool over the same
// service answer the same document.
type {{.Type}} struct {
	record *models.{{.Entity}}
}

// New{{.Type}} wraps one {{.Entity}}.
func New{{.Type}}(record *models.{{.Entity}}) {{.Type}} {
	return {{.Type}}{record: record}
}

// Compile-time proof that ctx.JSON takes it.
var _ hhttp.JsonResource = {{.Type}}{}

// ToArray returns the fields that may leave, by name. The tenant is not among
// them, and a field added to the model is not either until somebody adds it
// here: delete a line for a field this answer should not carry.
func (r {{.Type}}) ToArray() map[string]any {
	return map[string]any{
{{- range .Fields}}
		{{quote .Key}}: r.record.{{.GoName}},
{{- end}}
		// arandu:begin custom
		// A computed field, or one that leaves only for some readers, goes
		// here; a value that reports itself missing is left out of the answer.
		// arandu:end custom
	}
}

// With returns what goes beside "data" at the top of the answer: metadata about
// the answer rather than about the {{.Entity}}. There is none yet.
func (r {{.Type}}) With() map[string]any { return nil }

// {{.Collection}} is what a list of {{.Table}} answers with: each row through
// {{.Type}}, under {{quote .Table}}.
type {{.Collection}} struct {
	rows models.{{.Entity}}Collection
}

// New{{.Collection}} wraps a page of rows, as the service's List returns them.
func New{{.Collection}}(rows models.{{.Entity}}Collection) {{.Collection}} {
	return {{.Collection}}{rows: rows}
}

// Compile-time proof that ctx.JSON takes it.
var _ hhttp.JsonResource = {{.Collection}}{}

// ToArray returns every row through {{.Type}}, so a list answers with the same
// fields one record does.
func (c {{.Collection}}) ToArray() map[string]any {
	items := make([]map[string]any, 0, len(c.rows))
	for _, row := range c.rows {
		items = append(items, New{{.Type}}(row).ToArray())
	}
	return map[string]any{ {{- quote .Table}}: items}
}

// With returns what goes beside "data": the links of the next and previous
// page belong here, once the controller hands them over.
func (c {{.Collection}}) With() map[string]any { return nil }

// arandu:begin custom
// arandu:end custom
`

const resourceTestTemplate = `package unit_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	hhttp "github.com/arandu-io/hesape/http"

	resources "{{.ResourcesImport}}"
	models "{{.ModelsImport}}"
)

// TestThe{{.Type}}AnswersOnlyWhatItLists writes one {{.Entity}} through
// ctx.JSON, as a controller does, and reads the keys of what was answered.
// They have to be the allow-list and nothing more{{if .Tenant}} -- the tenant
// in particular, which is set on the record and must not leave{{end}}.
func TestThe{{.Type}}AnswersOnlyWhatItLists(t *testing.T) {
	record := &models.{{.Entity}}{ID: "record-1"{{if .Tenant}}, TenantID: "tenant-a"{{end}} }

	// No server and no router: ctx.JSON is called on a context over a
	// recorder, the way the router would call it, with a plain request.
	request, err := http.NewRequest(http.MethodGet, "/", nil)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	ctx := hhttp.NewContext(w, request, nil, nil)
	if err := ctx.JSON(http.StatusOK, resources.New{{.Type}}(record)); err != nil {
		t.Fatalf("ctx.JSON: %v", err)
	}

	var answer struct {
		Data map[string]any ` + "`" + `json:"data"` + "`" + `
	}
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
		t.Fatalf("the answer is not JSON: %v\n%s", err, w.Body.String())
	}
	answered := make([]string, 0, len(answer.Data))
	for key := range answer.Data {
		answered = append(answered, key)
	}
	sort.Strings(answered)

	// The allow-list, as ToArray writes it. A field added there is added here,
	// on purpose: what leaves is decided twice, in two files, by somebody
	// reading both.
	listed := []string{ {{- range $i, $k := .Keys}}{{if $i}}, {{end}}{{quote $k}}{{end -}} }
	if strings.Join(answered, ",") != strings.Join(listed, ",") {
		t.Errorf("answered %v, want exactly %v", answered, listed)
	}
	if answer.Data["id"] != "record-1" {
		t.Errorf("id = %v, want the record's", answer.Data["id"])
	}
}
// arandu:begin custom
// arandu:end custom
`
