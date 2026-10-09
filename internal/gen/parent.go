package gen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

// ParentKeyOf reads the type of the parent's key off the project rooted at
// root, for a module nested under m.Parent.
//
// The model is where the key is declared: app/Models/<Parent>.go holds the
// struct, the field tagged with the table's primary key, and the table's name.
// A field typed other than string is refused, because the nested module reads
// the parent from the path as text and loads it through the parent's service.
// A string key is a text column unless the migration that creates the parent's
// table declares it a UUID column, in which case the child's column is one too:
// the model says it is text, and only the migration says which text.
//
// The parent's model has to exist. The nested service loads the parent through
// its service and the seeder makes one through its factory, so a child written
// before its parent is a project that does not compile.
func ParentKeyOf(root string, m Module) (Type, error) {
	entity := m.ParentEntity()
	rel := filepath.Join("app", "Models", entity+".go")
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, rel), nil, parser.SkipObjectResolution)
	if os.IsNotExist(unwrapPathError(err)) {
		return "", fmt.Errorf("%s nests under %s, and %s does not exist: write the parent first, "+
			"with `aru make:module %s`", m.Resource(), m.Parent, rel, m.ParentParam())
	}
	if err != nil {
		return "", err
	}

	table, key := tableSpecOf(file)
	if table == "" {
		table = strings.ReplaceAll(m.Parent, "-", "_")
	}
	if key == "" {
		key = "id"
	}

	goType := ""
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok || spec.Name.Name != entity {
			return true
		}
		st, ok := spec.Type.(*ast.StructType)
		if !ok {
			return false
		}
		for _, f := range st.Fields.List {
			if f.Tag == nil {
				continue
			}
			tag, err := strconv.Unquote(f.Tag.Value)
			if err != nil {
				continue
			}
			if column, _, _ := strings.Cut(reflect.StructTag(tag).Get("db"), ","); column == key {
				goType = typeExpr(f.Type)
			}
		}
		return false
	})
	switch goType {
	case "":
		return "", fmt.Errorf("%s declares no field of %s tagged db:%q, the key of %s", rel, entity, key, table)
	case "string":
	default:
		return "", fmt.Errorf("the key of %s is %s in %s: a nested module reads the parent from the path as text "+
			"and loads it through %s.Get, so it nests under a parent keyed by text", m.Parent, goType, rel, m.ParentServiceType())
	}

	if migrationKeyMethod(root, table, key) == types[TypeUUID].Blueprint {
		return TypeUUID, nil
	}
	return TypeString, nil
}

// unwrapPathError answers the error a *os.PathError carries, so a missing file
// reads as os.ErrNotExist whichever layer reported it.
func unwrapPathError(err error) error {
	if pe, ok := err.(*os.PathError); ok {
		return pe.Err
	}
	return err
}

// tableSpecOf reads the Name and the PrimaryKey of the model.TableSpec literal
// in a model file, each empty when it is not written as a string literal.
func tableSpecOf(file *ast.File) (name, key string) {
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || typeExpr(lit.Type) != "model.TableSpec" {
			return true
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			field, ok := kv.Key.(*ast.Ident)
			value, literal := kv.Value.(*ast.BasicLit)
			if !ok || !literal || value.Kind != token.STRING {
				continue
			}
			text, err := strconv.Unquote(value.Value)
			if err != nil {
				continue
			}
			switch field.Name {
			case "Name":
				name = text
			case "PrimaryKey":
				key = text
			}
		}
		return false
	})
	return name, key
}

// migrationKeyMethod answers the Blueprint method that declares the key column
// of a table, read from the migration under database/migrations that creates
// it: "String" for table.String("id").Primary(). It is empty when no migration
// there creates the table with a literal name, which reads as text.
func migrationKeyMethod(root, table, key string) string {
	paths, _ := filepath.Glob(filepath.Join(root, "database", "migrations", "*.go"))
	method := ""
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || method != "" {
				return method == ""
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Create" || len(call.Args) != 3 || stringArg(call.Args[1]) != table {
				return true
			}
			body, ok := call.Args[2].(*ast.FuncLit)
			if !ok {
				return true
			}
			ast.Inspect(body.Body, func(n ast.Node) bool {
				column, ok := n.(*ast.CallExpr)
				if !ok || len(column.Args) == 0 || stringArg(column.Args[0]) != key {
					return true
				}
				if s, ok := column.Fun.(*ast.SelectorExpr); ok {
					if _, onBlueprint := s.X.(*ast.Ident); onBlueprint && method == "" {
						method = s.Sel.Name
					}
				}
				return true
			})
			return false
		})
		if method != "" {
			return method
		}
	}
	return ""
}

// stringArg answers the value of a string literal argument, or empty.
func stringArg(e ast.Expr) string {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	text, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return text
}
