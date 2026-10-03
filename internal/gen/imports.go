package gen

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ast/astutil"

	"github.com/arandu-io/hesape/publish"
)

// Merge carries the custom blocks of the file on disk into the regenerated one,
// and, for Go, settles the imports the result needs.
//
// The custom block is somebody's code, and it may use a package the template
// never imports: a named state that formats a date, a scope that trims a
// string. The template's import list is written for the template's code, so a
// merge alone would drop that import on every regeneration and the file would
// stop compiling. And the reverse: the template may import a package for code
// it seeds into the custom block once -- a default state that draws a time --
// which the person has since rewritten without it.
//
// So the import list of a merged Go file is what it uses, taken from the two
// lists it could have come from. Anything it cannot read is returned merged and
// untouched: a file that does not parse is the compiler's to report.
func Merge(file string, existing, generated []byte) []byte {
	merged := publish.Merge(file, existing, generated)
	if !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, ".kyse.go") || bytes.Equal(merged, generated) {
		return merged
	}
	tidy, err := tidyImports(merged, existing, generated)
	if err != nil {
		return merged
	}
	return tidy
}

// tidyImports drops the imports src does not use and adds the ones it uses
// from the candidate files' import lists.
func tidyImports(src []byte, candidates ...[]byte) ([]byte, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	used := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Obj == nil {
				used[id.Name] = true
			}
		}
		return true
	})

	present := map[string]bool{}
	for _, imp := range file.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		present[p] = true
		name, explicit := importName(imp)
		if name == "_" || name == "." || used[name] {
			continue
		}
		if explicit {
			astutil.DeleteNamedImport(fset, file, name, p)
		} else {
			astutil.DeleteImport(fset, file, p)
		}
	}

	for _, candidate := range candidates {
		other, err := parser.ParseFile(token.NewFileSet(), "", candidate, parser.ImportsOnly)
		if err != nil {
			continue
		}
		for _, imp := range other.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			name, explicit := importName(imp)
			if present[p] || !used[name] {
				continue
			}
			present[p] = true
			if explicit {
				astutil.AddNamedImport(fset, file, name, p)
			} else {
				astutil.AddImport(fset, file, p)
			}
		}
	}

	var buf bytes.Buffer
	if err := format.Node(&buf, fset, file); err != nil {
		return nil, err
	}
	return format.Source(buf.Bytes())
}

// importName is the name an import binds, and whether it was written out. An
// unnamed import binds the last element of its path, which is what every
// package this generator writes an import for is called.
func importName(imp *ast.ImportSpec) (string, bool) {
	if imp.Name != nil {
		return imp.Name.Name, true
	}
	p, _ := strconv.Unquote(imp.Path.Value)
	return path.Base(p), false
}
