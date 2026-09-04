package kyse_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/kyse"
)

// The names the generated Go used to write into the same scope as the loop
// binding, one for each way a view could take one away from it.
//
// `@foreach(.Steps as s)` was the one that found it: the URL escape writes its
// result into a temporary, the temporary was called `s`, and the view was
// reported for `s.URL undefined (type string has no field or method URL)` --
// against a line of a file whose header says DO NOT EDIT, for code the person
// never wrote. Every name below was reachable the same way, and a compiler that
// reserves a common letter is a compiler whose failures nobody can predict.
var collidingVariables = []string{"s", "err", "w", "d", "sections", "item", "data", "v", "ok", "props"}

// The same failure, one indirection out: the names of the packages the
// generated file calls.
//
// A package name is an ordinary identifier in the file that imports it, so
// `@foreach(.Steps as view)` renamed the package every escape goes through and
// the view was reported for `view.TextURL undefined (type Step has no field or
// method TextURL)` -- the same message shape, the same DO NOT EDIT file, and
// the same nothing telling the person that `io`, `fmt` or `template` were
// spoken for. Fixing only the variables left this half open, and it is one list
// rather than two because a view cannot tell them apart.
//
// One entry per import the generator writes. A name missing from here is a name
// a view can still take.
var collidingPackages = []string{"view", "io", "fmt", "template", "strings", "errors"}

// collidingBindings is both halves, which is what a view sees: one namespace of
// names it may bind, and no reason to expect any of them to be reserved.
var collidingBindings = append(append([]string{}, collidingVariables...), collidingPackages...)

// The three shapes a view compiles to, because they do not share a scope: a
// page threads an error through a writer it was handed, a layout is also handed
// the sections it yields, and a component builds a string and returns it.
const (
	pageTemplate = `//go:build kyse

package views

@go
type PageData struct {
	Title string
	Rows  []Row
	Tags  []string
}

type Row struct {
	URL   string
	Label string
	Attr  string
	Color string
}
@endgo

@extends('layouts.app')

@section('content')
<h1>{{ .Title }}</h1>
@foreach(.Rows as BINDING)
<a href="{{ BINDING.URL }}">{{ BINDING.Label }}</a>
<span {{ BINDING.Attr }}="1">{{ .Title }}</span>
<p style="color: {{ BINDING.Color }}">{{ BINDING.Label }}</p>
<script>var label = "{{ BINDING.Label }}";</script>
@include('partials.footer')
@csrf
@foreach(.Tags)
<i>{{ BINDING.Label }}</i>
@endforeach
@endforeach
@endsection
`

	layoutTemplate = `//go:build kyse

package views

@go
type LayoutData struct {
	Title string
	Rows  []string
}
@endgo

<!doctype html>
<html>
<head><title>{{ .Title }}</title></head>
<body>
@foreach(.Rows as BINDING)
<p>{{ BINDING }} {{ .Title }}</p>
@yield('content')
@endforeach
</body>
</html>
`

	// The layout that declares no interface of its own, which is the ordinary
	// one: it renders the contract the framework publishes, so the type it
	// asserts the data to is named out of a package rather than declared here.
	// That name is the one thing in a generated file that comes from neither
	// this generator nor the view's own source, and it is spelled the way a
	// person writing Go spells it.
	contractLayoutTemplate = `//go:build kyse

package views

<!doctype html>
<html>
<head><title>{{ .Title() }}</title></head>
<body>
@foreach(.Nav() as BINDING)
<a href="{{ BINDING }}">{{ BINDING }}</a>
@endforeach
@yield('content')
</body>
</html>
`

	componentTemplate = `//go:build kyse

package components

@go
type ButtonProps struct {
	Label string
	Rows  []string
}
@endgo

<button>
@foreach(.Rows as BINDING)
<span>{{ BINDING }} {{ .Label }}</span>
@endforeach
</button>
`
)

// shape is one of the three, with the calls that have to keep reading the
// generator's own names whatever the view binds.
//
// Building is the assertion for most of the names, and it is not enough for all
// of them: `data` and `sections` reach the framework as `any` and as a map the
// view never mentions, so a view that took one of those names over used to
// compile and hand the wrong value across. The calls below are checked as text
// for that reason -- what they read must not depend on what the view called its
// loop variable.
type shape struct {
	name     string
	source   string
	view     string
	dataType string
	reads    []string
}

var shapes = []shape{
	{
		name:     "page",
		source:   pageTemplate,
		view:     "home",
		dataType: "PageData",
		reads: []string{
			`kyse__view.RenderInto(kyse__w, "layouts.app", kyse__data, kyse__sections)`,
			`kyse__view.Include(kyse__w, "partials.footer", kyse__data)`,
			`kyse__view.CSRF(kyse__w, kyse__data)`,
			`kyse__d, kyse__ok := kyse__data.(PageData)`,
		},
	},
	{
		name:     "layout",
		source:   layoutTemplate,
		view:     "layouts.app",
		dataType: "LayoutData",
		reads:    []string{`kyse__view.Yield(kyse__w, kyse__sections, "content")`},
	},
	{
		// The type asserted to is the framework's, and the framework is imported
		// under a name of this generator's choosing -- so the assertion has to
		// be written in that name and the error a person reads has to keep the
		// other one.
		name:     "contract-layout",
		source:   contractLayoutTemplate,
		view:     "layouts.app",
		dataType: "view.Layout",
		reads: []string{
			"kyse__d, kyse__ok := kyse__data.(kyse__view.Layout)",
			`kyse__view.WrongData("layouts.app", "view.Layout", kyse__data)`,
		},
	},
	{
		name:     "component",
		source:   componentTemplate,
		view:     "components.button",
		dataType: "ButtonProps",
		reads:    []string{"func Button(kyse__props ButtonProps) kyse__template.HTML {"},
	},
}

// TestALoopBindingCannotTakeTheCompilersOwnNames compiles the same three views
// once per name the generated Go used to write for itself, and builds all of
// them.
//
// Reading the generated Go and looking for the shape it should have would prove
// less: the failure this is about is a type error, and only a type checker sees
// one. So the whole corpus is written into a module of its own -- the framework
// beside it is a stub, because what is under test is the naming and not the
// framework -- and handed to the Go compiler.
func TestALoopBindingCannotTakeTheCompilersOwnNames(t *testing.T) {
	tool := goTool(t)
	root := t.TempDir()
	writeStubModule(t, root)

	for _, binding := range collidingBindings {
		t.Run(binding, func(t *testing.T) {
			for _, s := range shapes {
				dir := binding + "_" + s.name
				// The path of the view is what the //line directives carry, so
				// a build failure names the binding that caused it rather than
				// one of thirty files called the same thing.
				path := "resources/views/" + dir + "/" + s.name + ".kyse.go"

				file, err := kyse.Parse(path, strings.ReplaceAll(s.source, "BINDING", binding))
				if err != nil {
					t.Fatalf("Parse: %v", err)
				}
				out, err := kyse.Generate(file, s.view, s.dataType, dir+"/"+s.name+".go")
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				for _, want := range s.reads {
					if !strings.Contains(string(out), want) {
						t.Errorf("a view that binds %q moved what the generated Go reads: %q is not in\n%s",
							binding, want, out)
					}
				}
				writeFile(t, filepath.Join(root, dir, s.name+".go"), string(out))
			}
		})
	}

	build := exec.Command(tool, "build", "./...")
	build.Dir = root
	// The module is the stub and the standard library, so nothing is fetched:
	// a proxy that is off proves it rather than trusting it.
	build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off", "GOTOOLCHAIN=local")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("the generated Go does not build: %v\n%s", err, out)
	}
}

// TestABindingInTheCompilersNamespaceIsRefused is the other half of the same
// promise.
//
// Every name is free -- `s`, `err`, `w`, a single letter, whichever letter it
// is -- and exactly one namespace is not, because that is what lets every other
// one be free. A view that walks into it is told so at build time, at the line
// it wrote, instead of being handed a renamed temporary.
func TestABindingInTheCompilersNamespaceIsRefused(t *testing.T) {
	source := "//go:build kyse\n\npackage views\n\n@go\ntype D struct{ Items []string }\n@endgo\n\n" +
		"@foreach(.Items as kyse__w)\n<li>{{ kyse__w }}</li>\n@endforeach\n"

	file, err := kyse.Parse("resources/views/home.kyse.go", source)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, err = kyse.Generate(file, "home", "D", "storage/framework/views/home.go")
	if err == nil {
		t.Fatal("a view bound a name the generated file writes for itself, and the build went on")
	}

	if message := err.Error(); !strings.Contains(message, "kyse__w") ||
		!strings.Contains(message, "resources/views/home.kyse.go:9") {
		t.Errorf("the refusal does not name the binding and the line it is on: %v", err)
	}
}

// The two shapes that call the native view package from the view's own
// source: an asset in a link, and the embedded struct a page is drawn with.
//
// Neither is written by this generator. They are written by the person, they
// resolved through the import the generated file already carried, and renaming
// every import into the reserved namespace withdrew the name they resolved
// through -- so a project whose layout had asked for its stylesheet since the
// day it was generated stopped building, with `undefined: view` against the
// line the person wrote.
const (
	publishedPackageLayout = `//go:build kyse

package layouts

<!doctype html>
<html>
<head>
<title>{{ .Title() }}</title>
<link rel="stylesheet" href="{{ view.AssetURL("app.css") }}">
</head>
<body>
@yield('content')
</body>
</html>
`

	publishedPackagePage = `//go:build kyse

package views

@go
type HomeData struct {
	view.Page

	Name string
}
@endgo

@extends('layouts.app')

@section('content')
<h1>{{ .Name }}</h1>
@endsection
`
)

const explicitlyImportedNativeView = `//go:build kyse

package views

import "github.com/arandu-io/hesape/view"

@go
type HomeData struct {
	view.Page
	Name string
}
@endgo

<h1>{{ .Name }}</h1>
<p>{!! .Name !!}</p>
`

// TestGeneratedViewsImportNativeViewDirectly is the import golden for both
// names a source view can need. Generated calls keep the reserved alias, while
// the view's own import keeps the plain name it wrote; both must resolve to the
// native package, and neither may pass through the compatibility bridge.
func TestGeneratedViewsImportNativeViewDirectly(t *testing.T) {
	file, err := kyse.Parse("resources/views/home.kyse.go", explicitlyImportedNativeView)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out, err := kyse.Generate(file, "home", "HomeData", "storage/framework/views/home.go")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	generated := string(out)

	const nativePath = `"github.com/arandu-io/hesape/view"`
	if n := strings.Count(generated, nativePath); n != 2 {
		t.Fatalf("the generated and source-owned view imports name the native package %d times, want 2:\n%s", n, generated)
	}
	if !strings.Contains(generated, "\tkyse__view "+nativePath+"\n") {
		t.Fatalf("the compiler's hygienic view import is not native:\n%s", generated)
	}
	if strings.Contains(generated, `"github.com/arandu-io/framework/view"`) {
		t.Fatalf("the generated Go still imports the Framework view bridge:\n%s", generated)
	}
	if strings.Contains(generated, "kyse__view.UnsafeText") {
		t.Fatalf("the generated Go still calls a symbol owned only by the Framework bridge:\n%s", generated)
	}
	if !strings.Contains(generated, "kyse__io.WriteString(kyse__w, kyse__view.Text(kyse__d.Name))") {
		t.Fatalf("raw interpolation does not use the native Text conversion:\n%s", generated)
	}
}

// TestAViewThatCallsThePackageItselfCompiles is the promise the rename broke.
//
// It is built rather than read, for the reason the test above is: what a
// withdrawn name costs is a type error, and only a type checker sees one.
// Looking for the import in the output would pass on a file that still does not
// compile.
func TestAViewThatCallsThePackageItselfCompiles(t *testing.T) {
	tool := goTool(t)
	root := t.TempDir()
	writeStubModule(t, root)

	for _, v := range []struct {
		dir      string
		view     string
		dataType string
		source   string
	}{
		{dir: "layouts", view: "layouts.app", dataType: "view.Layout", source: publishedPackageLayout},
		{dir: "views", view: "home", dataType: "HomeData", source: publishedPackagePage},
	} {
		path := "resources/views/" + v.dir + "/page.kyse.go"
		file, err := kyse.Parse(path, v.source)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		out, err := kyse.Generate(file, v.view, v.dataType, v.dir+"/page.go")
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		// The generator goes on calling the package under its own reserved
		// name. Publishing the plain one is an addition, never a swap: a
		// generated call that went through the view's name would be a call a
		// loop binding could still take.
		if !strings.Contains(string(out), "kyse__view.") {
			t.Errorf("the generated Go stopped calling the package under the reserved name:\n%s", out)
		}
		writeFile(t, filepath.Join(root, v.dir, "page.go"), string(out))
	}

	build := exec.Command(tool, "build", "./...")
	build.Dir = root
	build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off", "GOTOOLCHAIN=local")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("a view that calls the package by its own name does not build: %v\n%s", err, out)
	}
}

// TestAViewThatCallsThePackageAndBindsItsNameIsRefused is the cost of giving the
// name back, paid where it can be seen.
//
// A view that never calls the package may bind `view` in a loop and reach
// nothing but its own row -- that is the second half of this test, and it is
// what the rename was for. A view that does both has written a collision
// between two things it wrote, and it is told which line, rather than being
// handed a package renamed to a string for the length of a loop body.
func TestAViewThatCallsThePackageAndBindsItsNameIsRefused(t *testing.T) {
	const conflict = `//go:build kyse

package layouts

<link rel="stylesheet" href="{{ view.AssetURL("app.css") }}">
@foreach(.Nav() as view)
<a href="{{ view }}">{{ view }}</a>
@endforeach
@yield('content')
`

	file, err := kyse.Parse("resources/views/layouts/app.kyse.go", conflict)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := kyse.Generate(file, "layouts.app", "view.Layout", "layouts/app.go"); err == nil {
		t.Fatal("a view bound the name of a package it calls, and the build went on")
	} else {
		message := err.Error()
		for _, want := range []string{"resources/views/layouts/app.kyse.go:6", "as a package", `"view"`} {
			if !strings.Contains(message, want) {
				t.Errorf("the refusal does not say %q: %v", want, err)
			}
		}
	}

	// The same loop in a view that does not call the package. Nothing named
	// `view` is in that file's scope, so the binding is the ordinary one it
	// looks like.
	const alone = `//go:build kyse

package layouts

@foreach(.Nav() as view)
<a href="{{ view }}">{{ view }}</a>
@endforeach
@yield('content')
`

	file, err = kyse.Parse("resources/views/layouts/app.kyse.go", alone)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := kyse.Generate(file, "layouts.app", "view.Layout", "layouts/app.go"); err != nil {
		t.Fatalf("a view that binds a name it does not use as a package was refused: %v", err)
	}
}

// writeStubModule lays out the module the generated views are built in.
//
// The native view package beside them is a stub with the signatures the
// generated code calls and nothing behind them. A view that builds against it
// is a view whose names resolve, which is the whole of what this proves -- and
// it keeps the test from needing a checkout of anything, or a network.
func writeStubModule(t *testing.T, root string) {
	t.Helper()

	writeFile(t, filepath.Join(root, "go.mod"), `module example.test/views

go 1.21

require github.com/arandu-io/hesape v0.0.0

replace github.com/arandu-io/hesape => ./hesape
`)
	writeFile(t, filepath.Join(root, "hesape", "go.mod"), "module github.com/arandu-io/hesape\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "hesape", "view", "view.go"), `package view

import "io"

type Func func(w io.Writer, data any) error

type LayoutFunc func(w io.Writer, data any, sections map[string]func(io.Writer) error) error

type Layout interface {
	Title() string
	Nav() []string
}

func Register(name string, f Func) {}

func RegisterLayout(name string, f LayoutFunc) {}

func WrongData(view, want string, got any) error { return nil }

func RenderInto(w io.Writer, layout string, data any, sections map[string]func(io.Writer) error) error {
	return nil
}

func Yield(w io.Writer, sections map[string]func(io.Writer) error, name string) error { return nil }

func Include(w io.Writer, name string, data any) error { return nil }

func CSRF(w io.Writer, data any) error { return nil }

// Page is what a page embeds to be drawn inside the layout, and URL is how a
// view asks for an asset. Both are called by the view's own source rather than
// by the generated code around it, which is what makes them the reason the
// package has to be in scope under its own name.
type Page struct{ PageTitle string }

func AssetURL(name string) string { return "" }

func Text(v any) string { return "" }

func TextAttr(v any) string { return "" }

func TextURL(v any) (string, error) { return "", nil }

func TextJS(v any) string { return "" }

func TextCSS(v any) (string, error) { return "", nil }
`)
}

// goTool is the Go command the generated views are built with.
func goTool(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}
	return path
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The names Go declares for itself, which a loop binding cannot be given
// because they are not this compiler's to move.
//
// The reserved prefix answers for every name the generator chose; it cannot
// answer for the ones the language chose. `nil` is written by the guard in
// front of every write the generated code makes, so `@foreach(.Steps as nil)`
// broke a loop body that interpolated nothing at all -- `invalid operation:
// kyse__err == nil (mismatched types error and Step)`, once per line of markup,
// in a file whose header says DO NOT EDIT. `string` names the type a temporary
// is declared with and `len` counts the subject of a @forelse, and both failed
// the same way.
//
// The list is the universe block of the language specification rather than the
// three that were tripped over, because a list assembled from the failures
// somebody has already had is a list that is always one failure behind.
var universeBindings = []string{
	"any", "bool", "byte", "comparable", "complex64", "complex128", "error",
	"float32", "float64", "int", "int8", "int16", "int32", "int64", "rune",
	"string", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
	"true", "false", "iota",
	"nil",
	"append", "cap", "clear", "close", "complex", "copy", "delete", "imag",
	"len", "make", "max", "min", "new", "panic", "print", "println", "real",
	"recover",
}

// The keywords, which are refused one step further along and were already.
//
// A keyword is not an identifier at all, so nothing can be bound to it: the
// generated `for _, range := range …` does not parse. validGoIdent asks
// token.Lookup that question and loopBinding reports the answer as "is not a
// name" -- so there is no second check for keywords, and this list is here to
// hold that refusal in place rather than to add one.
var keywordBindings = []string{
	"break", "case", "chan", "const", "continue", "default", "defer", "else",
	"fallthrough", "for", "func", "go", "goto", "if", "import", "interface",
	"map", "package", "range", "return", "select", "struct", "switch", "type",
	"var",
}

// The two loops, each with its binding on line 9 -- so a refusal that carries
// no position, or carries the generated file's position instead of the view's,
// is visible as a wrong line rather than passing quietly.
const (
	foreachOnLine9 = `//go:build kyse

package views

@go
type D struct{ Items []string }
@endgo

@foreach(.Items as BINDING)
<li>row</li>
@endforeach
`

	forelseOnLine9 = `//go:build kyse

package views

@go
type D struct{ Items []string }
@endgo

@forelse(.Items as BINDING)
<li>row</li>
@empty
<li>none</li>
@endforelse
`
)

// TestABindingGoOwnsIsRefused is the third category of collision, and the one
// the prefix cannot reach.
//
// The first two are the compiler's own names and the packages it imports, and
// both were fixed by moving them: the generator renamed them into a namespace
// no view may spell. Go's own names cannot be moved -- `nil` is `nil` -- so the
// binding is refused instead, at the line the view wrote it on, with the name
// of the file it is in and a binding to write in its place.
//
// Refusing is the whole of the fix, and the alternative is worth naming: the
// generator could have guarded each name as somebody tripped over it, which is
// the chase this closes. There are forty-four names here and every one of them
// is answered by the same four lines of code.
func TestABindingGoOwnsIsRefused(t *testing.T) {
	const path = "resources/views/home.kyse.go"

	for _, source := range []string{foreachOnLine9, forelseOnLine9} {
		for _, binding := range append(append([]string{}, universeBindings...), keywordBindings...) {
			t.Run(binding, func(t *testing.T) {
				file, err := kyse.Parse(path, strings.ReplaceAll(source, "BINDING", binding))
				if err != nil {
					// A keyword may be refused by the parser instead, and that
					// is an answer too -- as long as it says where.
					assertRefusal(t, err, binding)
					return
				}
				_, err = kyse.Generate(file, "home", "D", "storage/framework/views/home.go")
				if err == nil {
					t.Fatalf("a view bound %q, which Go declares, and the build went on", binding)
				}
				assertRefusal(t, err, binding)
			})
		}
	}
}

// TestABindingEqualToTheGeneratedFunctionIsRefused is the same collision seen
// from the other side: not a name Go published to the file, but one this file
// publishes to the package it is in.
//
// A component becomes an exported function that other views call by name --
// `{!! Empty(.Empty) !!}` inside another component is the ordinary shape -- and
// a page becomes the render function the framework is handed. A loop that binds
// either name renames it over the whole loop body, so the call inside the loop
// reaches the row.
func TestABindingEqualToTheGeneratedFunctionIsRefused(t *testing.T) {
	for _, v := range []struct {
		view     string
		function string
	}{
		{view: "home", function: "renderHome"},
		{view: "components.button", function: "Button"},
	} {
		t.Run(v.function, func(t *testing.T) {
			path := "resources/views/" + v.view + ".kyse.go"
			file, err := kyse.Parse(path, strings.ReplaceAll(foreachOnLine9, "BINDING", v.function))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if _, err := kyse.Generate(file, v.view, "D", "storage/framework/views/home.go"); err == nil {
				t.Fatalf("a view bound %q, the function it compiles to, and the build went on", v.function)
			} else {
				assertRefusalAt(t, err, path, v.function)
			}
		})
	}
}

// TestTheOrdinaryNamesStayLegal is the boundary of the two tests above, and the
// reason they are lists of Go's names rather than of common words.
//
// Reserving a word a person would plausibly reach for is the failure this whole
// line of work exists to avoid: a compiler that takes `item` away is a compiler
// whose refusals nobody can predict. Every name here is one the generated code
// used to collide with, and every one of them is legal now.
func TestTheOrdinaryNamesStayLegal(t *testing.T) {
	for _, binding := range collidingVariables {
		t.Run(binding, func(t *testing.T) {
			file, err := kyse.Parse("resources/views/home.kyse.go", strings.ReplaceAll(foreachOnLine9, "BINDING", binding))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if _, err := kyse.Generate(file, "home", "D", "storage/framework/views/home.go"); err != nil {
				t.Fatalf("a view bound %q, which is a name a person writes, and it was refused: %v", binding, err)
			}
		})
	}
}

// assertRefusal checks the four things a person needs off one line of terminal:
// which file, which line, which binding, and what to write instead.
func assertRefusal(t *testing.T, err error, binding string) {
	t.Helper()
	assertRefusalAt(t, err, "resources/views/home.kyse.go", binding)
}

func assertRefusalAt(t *testing.T, err error, path, binding string) {
	t.Helper()
	message := err.Error()
	for _, want := range []string{path + ":9", strconv.Quote(binding), "as item"} {
		if !strings.Contains(message, want) {
			t.Errorf("the refusal of %q does not say %s:\n%v", binding, want, err)
		}
	}
}
