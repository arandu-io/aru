// Package resolve says which declaration each identifier of one Go file
// denotes, read from that file alone.
//
// The type checker answers it, run over the one file with every import left
// unresolved. Nothing here needs a type: what a caller needs is scope -- that
// `security` in `security.Grant` is the import and not a local variable that
// shadows it, or that two identifiers name the same variable -- and the checker
// resolves a local name to its declaration whether or not the type of that
// declaration could be known. A name declared in another file of the package
// resolves to nothing, as it would for any reader of this file only.
//
// It takes the place of ast.Ident.Obj, which the parser fills without type
// information and go/ast deprecates for that reason, so a file read here can be
// parsed with parser.SkipObjectResolution.
package resolve

import (
	"errors"
	"go/ast"
	"go/token"
	"go/types"
)

// File is what the identifiers of one file denote.
type File struct {
	file *ast.File
	pkg  *types.Package
	info *types.Info
	// decls maps an object declared in the file to the node declaring it: the
	// field of a parameter, receiver or struct, a var or const spec, or a
	// short variable declaration.
	decls map[types.Object]ast.Node
}

// errImportsUnresolved is what the checker is told for every import. The
// packages are left as names with nothing behind them, and every use of them
// is an error the checker records and moves past.
var errImportsUnresolved = errors.New("imports are not resolved")

type unresolvedImports struct{}

func (unresolvedImports) Import(string) (*types.Package, error) {
	return nil, errImportsUnresolved
}

// Check resolves the identifiers of f, whose positions are in fset.
//
// It never fails. The errors the checker reports are expected -- every
// imported name is one -- and it keeps going past each, so a file that does not
// type-check on its own, which is most files, is still resolved.
func Check(fset *token.FileSet, f *ast.File) *File {
	info := &types.Info{
		Defs: map[*ast.Ident]types.Object{},
		Uses: map[*ast.Ident]types.Object{},
	}
	conf := types.Config{Importer: unresolvedImports{}, Error: func(error) {}}
	pkg, _ := conf.Check(f.Name.Name, fset, []*ast.File{f}, info)

	out := &File{file: f, pkg: pkg, info: info, decls: map[types.Object]ast.Node{}}
	ast.Inspect(f, func(n ast.Node) bool {
		switch decl := n.(type) {
		case *ast.Field:
			for _, name := range decl.Names {
				out.declare(name, decl)
			}
		case *ast.ValueSpec:
			for _, name := range decl.Names {
				out.declare(name, decl)
			}
		case *ast.AssignStmt:
			if decl.Tok != token.DEFINE {
				return true
			}
			for _, left := range decl.Lhs {
				if name, ok := left.(*ast.Ident); ok {
					out.declare(name, decl)
				}
			}
		}
		return true
	})
	return out
}

func (r *File) declare(name *ast.Ident, decl ast.Node) {
	if object := r.info.Defs[name]; object != nil {
		r.decls[object] = decl
	}
}

// Object answers the declaration id denotes or declares, or nil when the file
// does not declare it: a name from another file, an imported package, or a
// predeclared one such as nil.
//
// An embedded field is answered by the type it names rather than the field it
// declares, so `struct{ Store }` denotes the Store of another file -- nothing
// here -- and not a field of this one.
func (r *File) Object(id *ast.Ident) types.Object {
	object := r.info.ObjectOf(id)
	if field, ok := object.(*types.Var); ok && field.Embedded() {
		object = r.info.Uses[id]
	}
	if object == nil || object.Parent() == types.Universe {
		return nil
	}
	if _, imported := object.(*types.PkgName); imported {
		return nil
	}
	return object
}

// Declaration answers the node that declares object: an *ast.Field, an
// *ast.ValueSpec or an *ast.AssignStmt, the last for a short variable
// declaration. It answers nil for any other declaration, and for an object the
// file does not declare.
func (r *File) Declaration(object types.Object) ast.Node {
	return r.decls[object]
}

// Package is the package the file was checked as, holding the scopes of its
// declarations: Package().Scope().Innermost(pos) is the scope at pos.
func (r *File) Package() *types.Package {
	return r.pkg
}

// Local reports whether id is resolved within the file: it denotes one of the
// file's declarations, or is a declaration itself. The variable a type switch
// declares in its header is one, though the checker gives it no object of its
// own. An imported package's name is never local, nor is a predeclared name,
// nor one declared in another file, nor the name of the package clause.
func (r *File) Local(id *ast.Ident) bool {
	if object, defined := r.info.Defs[id]; defined && object == nil {
		return id != r.file.Name
	}
	return r.Object(id) != nil
}
