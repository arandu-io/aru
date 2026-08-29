package kyse_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/kyse"
)

// An `@go` block is the second place a view names a package, and it is the
// place the markup does not show.
//
// The body of a view is markup with Go in the gaps, and what it calls a package
// for is small: an asset in a link, a helper inside an interpolation. A
// declaration is not written there at all -- it is written in the `@go` block,
// which is Go the person types and this generator copies through untouched. So
// the block is where `template.HTML` appears on the field of a props struct,
// where `io.Writer` appears in a helper's signature, and where `errors.New`
// appears beside them.
//
// Every one of those resolved through an import the generated file used to
// carry under its plain name. Renaming the imports into the reserved namespace
// withdrew all six names at once, and republishing only `view` left the other
// five withdrawn: a component that declared `Icon template.HTML` compiled to a
// file whose only html/template answers to `kyse__template`, and the person was
// told `undefined: template` against the line of their own declaration.
//
// One source per package would prove less than one source naming all six,
// because the failure is per name and a corpus that names five leaves the sixth
// exactly as broken as it was.
const (
	goBlockComponent = `//go:build kyse

package components

@go
type PanelProps struct {
	view.Page

	Icon  template.HTML
	Label string
}

var errPanel = errors.New("panel")

func panelNote(p PanelProps, w io.Writer) error {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s", p.Label))
	if _, err := io.WriteString(w, b.String()); err != nil {
		return errors.Join(errPanel, err)
	}
	return nil
}
@endgo

<div class="panel">{!! .Icon !!}<span>{{ .Label }}</span></div>
`

	goBlockPage = `//go:build kyse

package views

@go
type DashboardData struct {
	view.Page

	Icon  template.HTML
	Title string
}

var errDashboard = errors.New("dashboard")

func dashboardNote(d DashboardData, w io.Writer) error {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s", d.Title))
	if _, err := io.WriteString(w, b.String()); err != nil {
		return errors.Join(errDashboard, err)
	}
	return nil
}
@endgo

@extends('layouts.app')

@section('content')
<h1>{{ .Title }}</h1>
{!! .Icon !!}
@endsection
`
)

// The import line each republished package produces, written the way the
// generated file writes it.
//
// Building is the assertion that matters, and these are here so a failure names
// the package that is missing instead of leaving the reader to find it in a
// compiler's output. The alias is spelled even where it repeats the path,
// because gofmt keeps an explicit one and a test that expected the shorter form
// would fail on a file that compiles.
//
// Each is matched anchored to the start of its line, and that is not tidiness:
// the reserved alias of the same package is `kyse__template "html/template"`,
// which contains `template "html/template"` whole. An unanchored search passed
// on four of the six against a file that binds none of them.
var goBlockImports = map[string]string{
	"view":     `view "github.com/arandu-io/hesape/view"`,
	"template": `template "html/template"`,
	"io":       `io "io"`,
	"fmt":      `fmt "fmt"`,
	"strings":  `strings "strings"`,
	"errors":   `errors "errors"`,
}

// TestAGoBlockThatNamesAPackageCompiles is the promise the rename broke in the
// half of a view that is not markup.
//
// It is built rather than read, for the reason the tests beside it are: a
// withdrawn name costs a type error, and only a type checker sees one. The
// corpus is generated into a module of its own -- the native view package
// beside it is the same stub -- and handed to the Go compiler.
func TestAGoBlockThatNamesAPackageCompiles(t *testing.T) {
	tool := goTool(t)
	root := t.TempDir()
	writeStubModule(t, root)

	for _, v := range []struct {
		dir      string
		view     string
		dataType string
		source   string
	}{
		{dir: "components", view: "components.panel", dataType: "PanelProps", source: goBlockComponent},
		{dir: "views", view: "dashboard", dataType: "DashboardData", source: goBlockPage},
	} {
		path := "resources/views/" + v.dir + "/" + v.dir + ".kyse.go"
		file, err := kyse.Parse(path, v.source)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		out, err := kyse.Generate(file, v.view, v.dataType, v.dir+"/"+v.dir+".go")
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		generated := string(out)

		for _, name := range collidingPackages {
			if !strings.Contains(generated, "\n\t"+goBlockImports[name]+"\n") {
				t.Errorf("the @go block of %s calls %s, and the generated file does not bind that name:\n%s",
					v.view, name, generated)
			}
		}
		// Publishing a name is an addition and never a swap. The calls this
		// generator writes go on going through the reserved namespace, because
		// a generated call written through the view's name would be a call a
		// loop binding could still take.
		for _, reserved := range []string{"kyse__template.", "kyse__io.", "kyse__view."} {
			if !strings.Contains(generated, reserved) {
				t.Errorf("the generated Go stopped calling %s under the reserved name:\n%s", reserved, generated)
			}
		}
		writeFile(t, filepath.Join(root, v.dir, v.dir+".go"), generated)
	}

	build := exec.Command(tool, "build", "./...")
	build.Dir = root
	// The module is the stub and the standard library, so nothing is fetched.
	build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off", "GOTOOLCHAIN=local")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("a view whose @go block names a package this generator renames does not build: %v\n%s", err, out)
	}
}

// The call in the block and the binding in the loop, which is the collision
// giving the name back creates.
//
// The `@go` block is outside every loop, so a package named there is named in
// the file's own scope -- and a loop that binds the same word renames it over
// the whole of its body. The binding is on line 11 of every one of these, so a
// refusal that carries the generated file's position instead of the view's is
// visible as a wrong line rather than passing quietly.
const goBlockConflict = `//go:build kyse

package views

@go
type D struct{ Items []string }

var kept = PKG.SYMBOL
@endgo

@foreach(.Items as PKG)
<li>{{ PKG }}</li>
@endforeach
`

// TestAGoBlockCallAndALoopBindingCollide is the cost of giving the names back,
// paid where it can be seen, for every name that was given back.
//
// A view that never names one of these may bind it in a loop and reach nothing
// but its own row -- that is what the rename was for, and TestTheOrdinaryNames
// StayLegal holds it. A view that does both has written a collision between two
// things it wrote, and it is told which line, rather than being handed a package
// renamed to a string for the length of a loop body.
func TestAGoBlockCallAndALoopBindingCollide(t *testing.T) {
	symbols := map[string]string{
		"view":     "Text",
		"template": "HTMLEscapeString",
		"io":       "WriteString",
		"fmt":      "Fprintf",
		"strings":  "NewReader",
		"errors":   "New",
	}

	const path = "resources/views/home.kyse.go"
	for _, name := range collidingPackages {
		t.Run(name, func(t *testing.T) {
			source := strings.ReplaceAll(goBlockConflict, "SYMBOL", symbols[name])
			source = strings.ReplaceAll(source, "PKG", name)

			file, err := kyse.Parse(path, source)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			_, err = kyse.Generate(file, "home", "D", "storage/framework/views/home.go")
			if err == nil {
				t.Fatalf("a view called %s in its @go block and bound %q in a loop, and the build went on", name, name)
			}
			message := err.Error()
			for _, want := range []string{path + ":11", `"` + name + `"`, "as a package", "as item"} {
				if !strings.Contains(message, want) {
					t.Errorf("the refusal of %q does not say %q:\n%v", name, want, err)
				}
			}
		})
	}
}

// TestAGoBlockThatImportsForItselfIsNotBoundTwice is the boundary on the other
// side.
//
// A component whose block calls `strings.Fields` may write `import "strings"`
// in its own import block, which is where a package a person calls belongs.
// That name is then already bound in the generated file, and binding it a
// second time is the duplicate Go refuses -- so the view's own import wins and
// nothing is added beside it.
func TestAGoBlockThatImportsForItselfIsNotBoundTwice(t *testing.T) {
	const source = `//go:build kyse

package components

import "strings"

@go
type TagProps struct {
	Label string
}

func tagWords(p TagProps) []string {
	return strings.Fields(p.Label)
}
@endgo

<span>{{ .Label }}</span>
`

	file, err := kyse.Parse("resources/views/components/tag.kyse.go", source)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out, err := kyse.Generate(file, "components.tag", "TagProps", "components/tag.go")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if n := strings.Count(string(out), `"strings"`+"\n"); n != 2 {
		// Two: the reserved alias this generator always writes for a component,
		// and the view's own line. A third is the republished one, added on top
		// of a name the file already binds.
		t.Errorf("the view's own import of strings was bound %d times rather than 2:\n%s", n, out)
	}

	tool := goTool(t)
	root := t.TempDir()
	writeStubModule(t, root)
	writeFile(t, filepath.Join(root, "components", "tag.go"), string(out))

	build := exec.Command(tool, "build", "./...")
	build.Dir = root
	build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off", "GOTOOLCHAIN=local")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("a view that imports a republished package for itself does not build: %v\n%s", err, out)
	}
}
