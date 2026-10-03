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
	tidy, err := TidyImports(merged, existing, generated)
	if err != nil {
		return merged
	}
	return tidy
}

// TidyImports drops the imports src does not use and adds the ones it uses
// from the candidate files' import lists, then formats it.
//
// A candidate is Go source read only for its imports, so a caller that knows
// which path a name it wrote stands for passes a file that is nothing but that
// import. A blank or a dot import is never dropped: what it is for is not a
// name in the file.
func TidyImports(src []byte, candidates ...[]byte) ([]byte, error) {
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

	// Collected first and deleted after: deleting an import edits the slice
	// the loop would be reading.
	present := map[string]bool{}
	var unused []*ast.ImportSpec
	for _, imp := range file.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		present[p] = true
		name, _, certain := importName(imp)
		if certain && name != "_" && name != "." && !used[name] {
			unused = append(unused, imp)
		}
	}
	for _, imp := range unused {
		p, _ := strconv.Unquote(imp.Path.Value)
		if name, explicit, _ := importName(imp); explicit {
			astutil.DeleteNamedImport(fset, file, name, p)
		} else {
			astutil.DeleteImport(fset, file, p)
		}
		delete(present, p)
	}

	for _, candidate := range candidates {
		other, err := parser.ParseFile(token.NewFileSet(), "", candidate, parser.ImportsOnly)
		if err != nil {
			continue
		}
		for _, imp := range other.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			name, explicit, certain := importName(imp)
			if present[p] || !certain || !used[name] {
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

// importName is the name an import binds, whether it was written out, and
// whether it is known.
//
// An unnamed import binds the package clause of what it imports, which only
// the package says. The last element of the path is that name by convention,
// and the convention is trusted only where it can hold: a lowercase
// identifier. ".../app/Models" binds models, "gopkg.in/yaml.v3" binds yaml,
// and an import whose name is not known is neither dropped nor added -- a
// guess that removed it would delete an import the file uses.
func importName(imp *ast.ImportSpec) (name string, explicit, certain bool) {
	if imp.Name != nil {
		return imp.Name.Name, true, true
	}
	p, _ := strconv.Unquote(imp.Path.Value)
	base := path.Base(p)
	for i, r := range base {
		lower := r >= 'a' && r <= 'z'
		digit := i > 0 && r >= '0' && r <= '9'
		if !lower && !digit {
			return base, false, false
		}
	}
	return base, false, true
}
