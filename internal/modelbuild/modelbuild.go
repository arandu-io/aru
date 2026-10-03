// Package modelbuild finds the entities of a Go module and says which generated
// files they need: the query beside each one, and the typed factory a project
// keeps for it.
//
// An entity is a struct that embeds model.Model from hesape's database/model,
// with a package-level model.NewTable whose spec allocates it. Nothing is
// registered anywhere: the struct and its table are the declaration, and this
// package reads them off the source the way the compiler would, without
// running anything.
//
// Plan only reads. What it returns is what `aru model:build` writes, what
// `aru model:build --check` refuses and what `aru doctor` reports as stale, so
// the three cannot disagree about whether a project is up to date.
package modelbuild

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/arandu-io/aru/internal/gen"
)

// ModelPath is the import path of the non-generic model core.
const ModelPath = "github.com/arandu-io/hesape/database/model"

// Entity is one struct embedding model.Model, with the table that allocates it.
type Entity struct {
	// Dir is the package directory, relative to the module root and slashed.
	Dir string
	// Package is the package clause.
	Package string
	// Name is the struct.
	Name string
	// Source is the base name of the file declaring the struct.
	Source string
	// Line is where the struct is declared.
	Line int
	// TableVar is the variable holding its *model.Table.
	TableVar string
	// SoftDeletes is the SoftDeletes field of the table's spec.
	SoftDeletes bool
}

// Query is the spec of the entity's generated query file.
func (e Entity) Query() gen.QuerySpec {
	return gen.QuerySpec{
		Package:     e.Package,
		Entity:      e.Name,
		Source:      e.Source,
		Constructor: gen.Constructor(e.Name),
		TableVar:    e.TableVar,
		SoftDeletes: e.SoftDeletes,
	}
}

// Change is one file a build would write or remove.
type Change struct {
	// Path is relative to the module root, slashed.
	Path string
	// Content is what the file should hold; nil means the file goes.
	Content []byte
	// Why is "missing", "stale" or "orphaned", for a report.
	Why string
}

// Error is a declaration Plan cannot generate from, at the line that says so.
type Error struct {
	File    string
	Line    int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("%s:%d: %s", e.File, e.Line, e.Message) }

// parsed is one file of the module.
type parsed struct {
	rel   string // slashed, relative to the root
	dir   string
	ast   *ast.File
	fset  *token.FileSet
	model string // the name the model core is imported under, or ""
	body  []byte
}

// Find returns every entity of the module rooted at root, sorted by directory
// and name.
func Find(root string) ([]Entity, error) {
	files, err := parseModule(root)
	if err != nil {
		return nil, err
	}
	return entities(files)
}

// Plan returns the changes that would bring the generated files of the module
// rooted at root up to date, sorted by path. No change means it is current, and
// a module with no entity has none.
func Plan(root string) ([]Change, error) {
	files, err := parseModule(root)
	if err != nil {
		return nil, err
	}
	found, err := entities(files)
	if err != nil {
		return nil, err
	}

	var changes []Change
	planned := map[string]bool{}
	for _, e := range found {
		spec := e.Query()
		path := joinRel(e.Dir, spec.Path())
		planned[path] = true

		want, err := gen.RenderQuery(spec)
		if err != nil {
			return nil, &Error{File: joinRel(e.Dir, e.Source), Line: e.Line, Message: err.Error()}
		}
		have, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		switch {
		case readErr != nil:
			changes = append(changes, Change{Path: path, Content: want, Why: "missing"})
		case !isGeneratedQuery(have):
			return nil, &Error{File: path, Line: 1, Message: fmt.Sprintf(
				"%s is where the query of %s goes, and model:build did not write it: rename the file, "+
					"since a build that replaced it would delete code somebody wrote", path, e.Name)}
		case !bytes.Equal(have, want):
			changes = append(changes, Change{Path: path, Content: want, Why: "stale"})
		}
	}

	// A query file whose entity is gone is removed, and only when its header
	// says model:build wrote it.
	for _, f := range files {
		if planned[f.rel] || !isGeneratedQuery(f.body) {
			continue
		}
		changes = append(changes, Change{Path: f.rel, Why: "orphaned"})
	}

	factories, err := planFactories(root, found)
	if err != nil {
		return nil, err
	}
	changes = append(changes, factories...)

	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}

// Apply writes and removes what Plan returned.
func Apply(root string, changes []Change) error {
	for _, c := range changes {
		path := filepath.Join(root, filepath.FromSlash(c.Path))
		if c.Content == nil {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, c.Content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func isGeneratedQuery(content []byte) bool {
	_, ok := gen.GeneratedQuerySource(content)
	return ok
}

// planFactories re-renders the factories model:build rendered before, for the
// entities of app/Models.
//
// A factory is re-rendered only when its first line says model:build wrote it.
// One written against the generic model has its default state outside any
// custom block, and replacing it would delete that state: the upgrade tool
// converts it first, and until then it is left alone.
func planFactories(root string, found []Entity) ([]Change, error) {
	modulePath := readModulePath(root)
	if modulePath == "" {
		return nil, nil
	}
	var changes []Change
	for _, e := range found {
		if e.Dir != "app/Models" {
			continue
		}
		rel := "database/factories/" + e.Name + "Factory.go"
		path := filepath.Join(root, filepath.FromSlash(rel))
		have, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if entity, ours := gen.FactoryEntity(have); !ours || entity != e.Name {
			continue
		}

		source := filepath.Join(root, "app", "Models", e.Source)
		fields, tenant, err := gen.FieldsFromModel(source, e.Name)
		if err != nil {
			return nil, &Error{File: joinRel(e.Dir, e.Source), Line: e.Line, Message: err.Error()}
		}
		file, err := gen.RenderFactory(gen.FactorySpec{
			Entity: e.Name, Tenant: tenant, Fields: fields,
			ModelsImport: modulePath + "/app/Models",
		})
		if err != nil {
			return nil, &Error{File: rel, Line: 1, Message: err.Error()}
		}
		want := gen.Merge(rel, have, file.Content)
		if !bytes.Equal(have, want) {
			changes = append(changes, Change{Path: rel, Content: want, Why: "stale"})
		}
	}
	return changes, nil
}

// parseModule parses every Go source of the module that is not a test, a view
// or under a directory the go tool ignores. A file that does not parse stops
// the plan, naming its line: it could declare an entity, and a plan that
// skipped it would remove that entity's query.
func parseModule(root string) ([]*parsed, error) {
	var out []*parsed
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (name == "vendor" || name == "testdata" || name == "node_modules" ||
				strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			// A directory with a go.mod of its own is another module.
			if path != root {
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".kyse.go") {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, rel, body, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("model:build cannot read %v", err)
		}
		f := &parsed{rel: rel, dir: filepath.ToSlash(filepath.Dir(rel)), ast: file, fset: fset, body: body}
		for _, imp := range file.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == ModelPath {
				f.model = "model"
				if imp.Name != nil {
					f.model = imp.Name.Name
				}
			}
		}
		out = append(out, f)
		return nil
	})
	return out, err
}

// table is one package-level model.NewTable with the entity its spec allocates.
type table struct {
	file        *parsed
	line        int
	name        string
	entity      string
	softDeletes bool
}

// entities pairs every struct embedding model.Model with the table whose spec
// allocates it, package by package.
func entities(files []*parsed) ([]Entity, error) {
	byDir := map[string][]*parsed{}
	var dirs []string
	for _, f := range files {
		if _, seen := byDir[f.dir]; !seen {
			dirs = append(dirs, f.dir)
		}
		byDir[f.dir] = append(byDir[f.dir], f)
	}
	sort.Strings(dirs)

	var out []Entity
	for _, dir := range dirs {
		var structs []Entity
		var tables []table
		declared := map[string]bool{}
		for _, f := range byDir[dir] {
			generated := isGeneratedQuery(f.body)
			for _, decl := range f.ast.Decls {
				for _, name := range topLevelNames(decl) {
					if !generated {
						declared[name] = true
					}
				}
			}
			if f.model == "" || generated {
				continue
			}
			found, err := structsIn(f)
			if err != nil {
				return nil, err
			}
			structs = append(structs, found...)
			declaredTables, err := tablesIn(f)
			if err != nil {
				return nil, err
			}
			tables = append(tables, declaredTables...)
		}

		for _, e := range structs {
			var matched []table
			for _, t := range tables {
				if t.entity == e.Name {
					matched = append(matched, t)
				}
			}
			at := joinRel(e.Dir, e.Source)
			switch len(matched) {
			case 0:
				return nil, &Error{File: at, Line: e.Line, Message: fmt.Sprintf(
					"%[1]s embeds model.Model and no model.NewTable in its package allocates it. Declare its table:\n\n"+
						"\tvar %[2]sTable = model.NewTable(model.TableSpec{\n"+
						"\t\tName: \"...\",\n"+
						"\t\tNew:  func() model.Entity { return new(%[1]s) },\n"+
						"\t})", e.Name, lowerFirst(e.Name))}
			case 1:
				e.TableVar = matched[0].name
				e.SoftDeletes = matched[0].softDeletes
			default:
				return nil, &Error{File: matched[1].file.rel, Line: matched[1].line, Message: fmt.Sprintf(
					"%s is allocated by two tables, %s and %s: a row has one table", e.Name, matched[0].name, matched[1].name)}
			}

			spec := e.Query()
			for _, name := range []string{
				spec.Constructor, spec.Query(), spec.Collection(), spec.Collection() + "Of", spec.Of(), spec.Column(),
			} {
				if declared[name] {
					return nil, &Error{File: at, Line: e.Line, Message: fmt.Sprintf(
						"%s's generated query declares %s, and the package already declares it: rename the one written by hand",
						e.Name, name)}
				}
			}
			out = append(out, e)
		}
	}
	return out, nil
}

// structsIn returns the structs of f that embed model.Model, by value and
// without a type argument.
func structsIn(f *parsed) ([]Entity, error) {
	var out []Entity
	for _, decl := range f.ast.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts := spec.(*ast.TypeSpec)
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range st.Fields.List {
				if len(field.Names) != 0 || !isModelSelector(field.Type, f.model, "Model") {
					continue
				}
				if ts.TypeParams != nil {
					return nil, &Error{File: f.rel, Line: f.fset.Position(ts.Pos()).Line, Message: fmt.Sprintf(
						"%s embeds model.Model and has type parameters: an entity is one concrete row type", ts.Name.Name)}
				}
				out = append(out, Entity{
					Dir: f.dir, Package: f.ast.Name.Name, Name: ts.Name.Name,
					Source: filepath.Base(f.rel), Line: f.fset.Position(ts.Pos()).Line,
				})
			}
		}
	}
	return out, nil
}

// tablesIn returns the package-level model.NewTable calls of f whose spec
// names the row it allocates.
func tablesIn(f *parsed) ([]table, error) {
	var out []table
	for _, decl := range f.ast.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, value := range vs.Values {
				if i >= len(vs.Names) {
					break
				}
				call, ok := value.(*ast.CallExpr)
				if !ok || !isModelSelector(call.Fun, f.model, "NewTable") || len(call.Args) != 1 {
					continue
				}
				lit, ok := call.Args[0].(*ast.CompositeLit)
				if !ok {
					continue
				}
				t := table{file: f, line: f.fset.Position(vs.Names[i].Pos()).Line, name: vs.Names[i].Name}
				for _, elt := range lit.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, _ := kv.Key.(*ast.Ident)
					if key == nil {
						continue
					}
					switch key.Name {
					case "New":
						t.entity = allocated(kv.Value)
					case "SoftDeletes":
						id, isIdent := kv.Value.(*ast.Ident)
						if !isIdent || (id.Name != "true" && id.Name != "false") {
							return nil, &Error{File: f.rel, Line: f.fset.Position(kv.Pos()).Line, Message: fmt.Sprintf(
								"the SoftDeletes of %s is not the word true or false, and model:build reads it to decide "+
									"which methods the query has: write it as a literal", t.name)}
						}
						t.softDeletes = id.Name == "true"
					}
				}
				if t.entity != "" {
					out = append(out, t)
				}
			}
		}
	}
	return out, nil
}

// allocated answers the type a spec's New allocates, when it is written the one
// way a table is declared: func() model.Entity { return new(T) }, or &T{}.
func allocated(e ast.Expr) string {
	fn, ok := e.(*ast.FuncLit)
	if !ok || len(fn.Body.List) != 1 {
		return ""
	}
	ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return ""
	}
	switch r := ret.Results[0].(type) {
	case *ast.CallExpr:
		if id, ok := r.Fun.(*ast.Ident); ok && id.Name == "new" && len(r.Args) == 1 {
			if t, ok := r.Args[0].(*ast.Ident); ok {
				return t.Name
			}
		}
	case *ast.UnaryExpr:
		if lit, ok := r.X.(*ast.CompositeLit); ok && r.Op == token.AND {
			if t, ok := lit.Type.(*ast.Ident); ok {
				return t.Name
			}
		}
	}
	return ""
}

func isModelSelector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || pkg == "" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg && sel.Sel.Name == name
}

func topLevelNames(decl ast.Decl) []string {
	var out []string
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if d.Recv == nil {
			out = append(out, d.Name.Name)
		}
	case *ast.GenDecl:
		for _, spec := range d.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				out = append(out, s.Name.Name)
			case *ast.ValueSpec:
				for _, n := range s.Names {
					out = append(out, n.Name)
				}
			}
		}
	}
	return out
}

func joinRel(dir, name string) string {
	if dir == "." || dir == "" {
		return name
	}
	return dir + "/" + name
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// readModulePath answers the module line of go.mod, or empty.
func readModulePath(root string) string {
	body, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(body), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}
