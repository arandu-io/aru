package doctor

import (
	"errors"
	"go/ast"
	"go/token"
	"go/types"
)

// fileObjects says which declaration each identifier of one file denotes,
// read from that file alone: the variable a router or a controller was
// assigned to, told apart from another of the same name in another scope.
//
// The type checker answers it, run over the one file with every import left
// unresolved. Nothing here needs a type: what it needs is scope, and the
// checker resolves a local name to its declaration whether or not the type of
// that declaration could be known. A name declared in another file of the
// package resolves to nothing, as it would for any reader of this file only.
type fileObjects struct {
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

// resolveFile answers the objects of f.
func resolveFile(f *file) *fileObjects {
	info := &types.Info{
		Defs: map[*ast.Ident]types.Object{},
		Uses: map[*ast.Ident]types.Object{},
	}
	// The errors are expected -- every imported name is one -- and the
	// checker keeps going past each when it has somewhere to report them.
	conf := types.Config{Importer: unresolvedImports{}, Error: func(error) {}}
	_, _ = conf.Check(f.pkg, f.fset, []*ast.File{f.ast}, info)

	out := &fileObjects{info: info, decls: map[types.Object]ast.Node{}}
	ast.Inspect(f.ast, func(n ast.Node) bool {
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

func (o *fileObjects) declare(name *ast.Ident, decl ast.Node) {
	if object := o.info.Defs[name]; object != nil {
		o.decls[object] = decl
	}
}

// object answers the declaration id denotes, or nil when the file does not
// declare it: a name from another file, an imported package, or a predeclared
// one such as nil.
func (o *fileObjects) object(id *ast.Ident) types.Object {
	object := o.info.ObjectOf(id)
	if object == nil || object.Parent() == types.Universe {
		return nil
	}
	if _, imported := object.(*types.PkgName); imported {
		return nil
	}
	return object
}

// objectsOf answers the objects of f, resolved once per file.
func (s *mapState) objectsOf(f *file) *fileObjects {
	if s.objects == nil {
		s.objects = map[*file]*fileObjects{}
	}
	if o := s.objects[f]; o != nil {
		return o
	}
	o := resolveFile(f)
	s.objects[f] = o
	return o
}
