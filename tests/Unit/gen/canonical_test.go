package gen_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/catalog"
	"github.com/arandu-io/aru/internal/gen"
)

// TestTheGeneratorNamesEverySymbolByItsCanonicalPath reads every golden file --
// which is every byte a template emits -- and fails on a framework symbol named
// through a path that is not its own.
//
// The catalog is the one `aru imports:catalog` prints for the framework version
// a new project requires, so what is checked is the generator against the
// release a person receives: a symbol the framework only aliases is written
// with the component's path, and one it declares -- the router, the middleware
// type, the mailable -- with the framework's.
func TestTheGeneratorNamesEverySymbolByItsCanonicalPath(t *testing.T) {
	c := publishedCatalog(t)

	goldens, err := filepath.Glob(filepath.Join("..", "..", "..", "internal", "gen", "testdata", "*", "*.go.golden"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(goldens)

	read, framework := 0, 0
	for _, path := range goldens {
		if strings.HasSuffix(path, ".kyse.go.golden") {
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, body, 0)
		if err != nil {
			t.Fatalf("%s does not parse: %v", path, err)
		}
		read++

		for _, imp := range file.Imports {
			pkg, _ := strconv.Unquote(imp.Path.Value)
			if !strings.HasPrefix(pkg, catalog.Framework+"/") {
				continue
			}
			framework++
			if !c.Covers(pkg) {
				t.Errorf("%s imports %s, which framework %s does not have", path, pkg, c.Version)
				continue
			}
			local := gen.AssumedImportName(pkg)
			if imp.Name != nil {
				local = imp.Name.Name
			}
			ast.Inspect(file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if id, ok := sel.X.(*ast.Ident); !ok || id.Name != local {
					return true
				}
				symbol, known := c.Lookup(pkg, sel.Sel.Name)
				if known && symbol.Moved() {
					t.Errorf("%s names %s.%s, which is %s.%s: write the canonical path in the template",
						path, pkg, sel.Sel.Name, symbol.Canonical, symbol.CanonicalName)
				}
				return true
			})
		}
	}
	if read == 0 || framework == 0 {
		t.Fatalf("read %d golden files and %d framework imports: the walk looked in the wrong place", read, framework)
	}
}

// publishedCatalog is the catalog of the framework version the compile
// harness pins, fetched the way `aru imports:catalog` fetches it.
func publishedCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	root := t.TempDir()
	mod := "module example.test/catalog\n\ngo 1.26\n\nrequire " + catalog.Framework + " " + published[catalog.Framework] + "\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "-mod=mod")
	c, err := catalog.Fetch(root)
	if err != nil {
		say := t.Skipf
		if os.Getenv("CI") != "" {
			say = t.Fatalf
		}
		say("the catalog of framework %s could not be read, so nothing was checked: %v", published[catalog.Framework], err)
	}
	return c
}
