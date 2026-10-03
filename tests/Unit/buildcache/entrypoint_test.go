package buildcache_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/arandu-io/aru/tests"
)

// TestTheToolchainIsStartedInOnePlace keeps every build this command makes in
// the cache it trims.
//
// A call that starts `go` itself inherits the machine's cache, so what it
// compiles lands where nothing here measures it and no trim reaches it -- and
// it does so silently, because the build succeeds. Packaging did exactly that
// on every platform until it was routed through the one function. This walks
// the source that ships and refuses a second way in.
//
// Tests are left out on purpose: a suite compiling a throwaway project is the
// developer's build, in the developer's cache.
func TestTheToolchainIsStartedInOnePlace(t *testing.T) {
	root := tests.Root(t)
	allowed := filepath.Join("internal", "buildcache", "buildcache.go")

	examined := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || (strings.HasPrefix(d.Name(), ".") && path != root) {
				return filepath.SkipDir
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
		if rel == allowed {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Errorf("%s does not parse, so nothing is known about what it starts: %v", rel, err)
			return nil
		}
		examined++
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !startsGo(call) {
				return true
			}
			t.Errorf("%s starts the go command itself; use buildcache.Command, or buildcache.Env for a library "+
				"that starts it, so the build lands in the cache this command trims", rel)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if examined == 0 {
		t.Fatal("no source file was examined, so the walk looked in the wrong place")
	}
}

// startsGo reports whether call is exec.Command("go", ...) or
// exec.CommandContext(ctx, "go", ...).
func startsGo(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	if !ok || pkg.Name != "exec" {
		return false
	}
	program := 0
	switch selector.Sel.Name {
	case "Command":
	case "CommandContext":
		program = 1
	default:
		return false
	}
	if len(call.Args) <= program {
		return false
	}
	literal, ok := call.Args[program].(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return false
	}
	name, err := strconv.Unquote(literal.Value)
	return err == nil && name == "go"
}
