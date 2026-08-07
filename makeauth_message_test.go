package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// projectModule is the module path the throwaway project below declares.
const projectModule = "example.test/project"

// TestTheInstructionNamesSymbolsTheGeneratorEmits is the check that was missing.
//
// make:auth used to print `routes.Deps{Login: controllers.NewLoginController(…)}`
// while writing `package authui` with `func New(…) *Module`. Neither symbol
// existed, routes.Deps had no such field, and the one Go file the command wrote
// stayed orphaned -- nothing registered it, and `aru doctor` walked past it.
//
// So the message is not compared against a fixed string here. It is compared
// against the package the command just wrote: every qualified name it prints,
// whose package is one this command generated, has to resolve to a declaration
// in that package.
func TestTheInstructionNamesSymbolsTheGeneratorEmits(t *testing.T) {
	project := throwawayProject(t)

	var out, errOut strings.Builder
	if err := makeAuth(nil, &out, &errOut); err != nil {
		t.Fatalf("make:auth: %v\n%s", err, errOut.String())
	}
	message := out.String()

	declared := declaredSymbols(t, project)

	// The kit's own module is the point of the whole instruction, so the
	// message referring to nothing at all must not pass.
	if _, ok := declared["authui"]["New"]; !ok {
		t.Fatal("the generator no longer emits authui.New; this test is measuring the wrong thing")
	}
	if !strings.Contains(message, "authui.New(") {
		t.Errorf("the instruction never names the constructor the command wrote:\n%s", message)
	}

	for _, ref := range qualifiedNames(message) {
		symbols, generated := declared[ref.pkg]
		if !generated {
			// A package this command does not write -- the framework's, or the
			// skeleton's. Nothing here can verify those.
			continue
		}
		if _, ok := symbols[ref.name]; !ok {
			t.Errorf("the instruction says %s.%s, and the %s this command writes has no such declaration:\n%s",
				ref.pkg, ref.name, ref.pkg, message)
		}
	}
}

// TestTheInstructionImportsWhereTheFileLanded: the import path is derived from
// the project's module path and the directory the command wrote to, so moving
// the file without moving the instruction fails here rather than at `go build`.
func TestTheInstructionImportsWhereTheFileLanded(t *testing.T) {
	project := throwawayProject(t)

	var out, errOut strings.Builder
	if err := makeAuth(nil, &out, &errOut); err != nil {
		t.Fatalf("make:auth: %v\n%s", err, errOut.String())
	}

	dir := filepath.Dir(filepath.Join("app", "Http", "Controllers", "Auth", "LoginController.go"))
	want := `"` + projectModule + "/" + filepath.ToSlash(dir) + `"`
	if !strings.Contains(out.String(), want) {
		t.Errorf("the instruction does not import %s:\n%s", want, out.String())
	}
	_ = project
}

// TestTheInstructionCallsTheConstructorWithItsArity: an example missing an
// argument is an example that does not compile, which is the failure this whole
// test file exists to stop.
func TestTheInstructionCallsTheConstructorWithItsArity(t *testing.T) {
	project := throwawayProject(t)

	var out, errOut strings.Builder
	if err := makeAuth(nil, &out, &errOut); err != nil {
		t.Fatalf("make:auth: %v\n%s", err, errOut.String())
	}

	want := constructorArity(t, project)
	got, ok := callArity(out.String(), "authui.New(")
	if !ok {
		t.Fatalf("the instruction has no authui.New( call:\n%s", out.String())
	}
	if got != want {
		t.Errorf("the instruction calls authui.New with %d arguments, and it takes %d:\n%s",
			got, want, out.String())
	}
}

// TestTheKitSaysItsScreensHaveNoRoutes: nine screens land and three paths
// answer. Somebody who generates nine and finds six 404s concludes the command
// is broken, so the command has to say so itself -- the restriction is in ADR
// 0022, and a decision nobody reads is not a warning.
func TestTheKitSaysItsScreensHaveNoRoutes(t *testing.T) {
	throwawayProject(t)

	var out, errOut strings.Builder
	if err := makeAuth(nil, &out, &errOut); err != nil {
		t.Fatalf("make:auth: %v\n%s", err, errOut.String())
	}
	message := out.String()

	for _, want := range []string{
		"/auth/register",
		"/auth/verify",
		"/welcome",
		"404",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("the output does not warn about %q:\n%s", want, message)
		}
	}
}

// throwawayProject builds the smallest tree make:auth accepts and chdirs into
// it. It writes no framework: nothing here compiles the result, only reads it.
func throwawayProject(t *testing.T) string {
	t.Helper()

	project := t.TempDir()
	writeFile(t, filepath.Join(project, "go.mod"), "module "+projectModule+"\n\ngo 1.25\n")
	writeFile(t, filepath.Join(project, "main.go"), "package main\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(project, "arandu.toml"), "name = \"test\"\n")
	writeFile(t, filepath.Join(project, "app", "Http", "Controllers", "Controller.go"),
		"package controllers\n\n// Controller is what every controller embeds.\ntype Controller struct{}\n")

	chdir(t, project)
	return project
}

// declaredSymbols reads back every Go file the command wrote and returns, per
// package, the top-level names it declares.
//
// Reading them rather than listing them is the point: the test cannot drift from
// the generator, because it has no copy of what the generator emits.
func declaredSymbols(t *testing.T, project string) map[string]map[string]bool {
	t.Helper()

	out := map[string]map[string]bool{}
	fset := token.NewFileSet()

	err := filepath.WalkDir(project, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		// A .kyse.go is markup behind a build tag; the Go parser cannot read it,
		// and the generated file beside it is what the project compiles.
		if strings.HasSuffix(path, viewSuffix) {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("the command wrote Go that does not parse: %s: %v", path, err)
		}
		pkg := file.Name.Name
		if out[pkg] == nil {
			out[pkg] = map[string]bool{}
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					out[pkg][d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						out[pkg][s.Name.Name] = true
					case *ast.ValueSpec:
						for _, n := range s.Names {
							out[pkg][n.Name] = true
						}
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// constructorArity counts the parameters of authui.New as written.
func constructorArity(t *testing.T, project string) int {
	t.Helper()

	path := filepath.Join(project, "app", "Http", "Controllers", "Auth", "LoginController.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Name.Name != "New" {
			continue
		}
		n := 0
		for _, field := range fn.Type.Params.List {
			// `a, b int` is one field with two names.
			n += max(len(field.Names), 1)
		}
		return n
	}
	t.Fatalf("%s declares no New", path)
	return 0
}

// qualifiedName is one `pkg.Name` the message printed.
type qualifiedName struct{ pkg, name string }

var qualifiedNamePattern = regexp.MustCompile(`\b([a-z][a-z0-9]*)\.([A-Z][A-Za-z0-9_]*)`)

func qualifiedNames(message string) []qualifiedName {
	var out []qualifiedName
	for _, m := range qualifiedNamePattern.FindAllStringSubmatch(message, -1) {
		out = append(out, qualifiedName{pkg: m[1], name: m[2]})
	}
	return out
}

// callArity counts the top-level arguments of the first call to prefix.
func callArity(message, prefix string) (int, bool) {
	at := strings.Index(message, prefix)
	if at < 0 {
		return 0, false
	}
	rest := message[at+len(prefix):]

	depth, args := 0, 1
	for _, r := range rest {
		switch r {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth == 0 {
				return args, true
			}
			depth--
		case ',':
			if depth == 0 {
				args++
			}
		case '\n':
			// A call that never closes on its line is a message that was
			// reformatted; counting past it would invent an answer.
			return 0, false
		}
	}
	return 0, false
}
