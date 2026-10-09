package gen_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/doctor"
	"github.com/arandu-io/aru/tests"
)

// TestTheDoctorIsSilentOnTheGeneratedProject runs `aru doctor` over a project
// made of every generator's output, with its wiring pasted, and requires no
// error and no structural warning.
//
// The generated tree is the answer to "what does correct code look like here",
// and the doctor is the question. A generator whose output the doctor
// reports teaches the shape the next warning is about -- and the structural
// rules exist because the applications copied a shape from somewhere.
//
// The wiring is what pasting the printed lines produces, reduced to what the
// doctor reads: bootstrap/app.go names every constructor the generators wrote,
// main.go links the migrations and the connector, and routes/web.go is the
// route table. The structural rules are read off internal/doctor/structure.go,
// so a rule added there is a rule this test holds the generators to.
func TestTheDoctorIsSilentOnTheGeneratedProject(t *testing.T) {
	if testing.Short() {
		t.Skip("generates the whole corpus: skipped under -short")
	}
	root, _ := generatedProject(t)
	writeWiring(t, root)

	findings, err := doctor.Run(root, "")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	structural := structuralRules(t)

	var reported []string
	for _, f := range findings {
		if f.Severity == doctor.Error || structural[f.Rule] {
			reported = append(reported, f.Rule+" "+f.File+": "+f.Message)
			continue
		}
		t.Logf("not held here: [%s] %s: %s", f.Rule, f.File, f.Message)
	}
	sort.Strings(reported)
	if len(reported) > 0 {
		t.Errorf("the doctor reports the generated project:\n  %s", strings.Join(reported, "\n  "))
	}
}

// structuralRules are the rule names internal/doctor/structure.go declares.
func structuralRules(t *testing.T) map[string]bool {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(tests.Root(t), "internal", "doctor", "structure.go"))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, m := range regexp.MustCompile(`Rule: *"([a-z0-9-]+)"`).FindAllStringSubmatch(string(body), -1) {
		names[m[1]] = true
	}
	if len(names) == 0 {
		t.Fatal("structure.go declares no rule, so nothing would be checked")
	}
	return names
}

// writeWiring writes the files the generators print lines for and never
// write: main.go, the project's arandu.toml, bootstrap/app.go naming every
// constructor of a controller and a service, and routes/web.go.
func writeWiring(t *testing.T, root string) {
	t.Helper()

	var refs []string
	imports := map[string]string{}
	for dir, alias := range map[string]string{
		"app/Http/Controllers": "controllers",
		"app/Services":         "services",
	} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, dir, e.Name()), nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range file.Decls {
				if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "New") {
					refs = append(refs, alias+"."+fn.Name.Name)
					imports[alias] = generatedModulePath + "/" + dir
				}
			}
		}
	}
	sort.Strings(refs)
	aliases := make([]string, 0, len(imports))
	for alias, path := range imports {
		aliases = append(aliases, "\t"+alias+" \""+path+"\"")
	}
	sort.Strings(aliases)

	writeInto(t, filepath.Join(root, "bootstrap", "app.go"), []byte(`// Package bootstrap is where the printed wiring is pasted.
package bootstrap

import (
`+strings.Join(aliases, "\n")+`
)

// Constructors is every constructor the generators wrote, named where the
// printed lines put them.
var Constructors = []any{
	`+strings.Join(refs, ",\n\t")+`,
}
`))
	writeInto(t, filepath.Join(root, "routes", "web.go"), []byte(`// Package routes is the route table.
package routes

import "github.com/arandu-io/framework/http"

// Web registers the routes the printed lines name.
func Web(r *http.Router) {}
`))
	writeInto(t, filepath.Join(root, "main.go"), []byte(`package main

import (
	_ "`+generatedModulePath+`/bootstrap"
	_ "`+generatedModulePath+`/database/migrations"
	_ "github.com/arandu-io/hesape/database/connectors/sqlite"
)

func main() {}
`))
	writeInto(t, filepath.Join(root, "arandu.toml"), []byte("name = \"project\"\n"))
}
