package kyse_test

import (
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/kyse"
)

// The shapes that used to reach the disk.
//
// Every one of them produced a generated file that does not compile, and every
// one of them was written by a `view:build` that printed how many views it had
// compiled and exited 0. They were found by reading the output back rather than
// by anybody thinking of them, which is the whole argument for reading the
// output back: the list of constructs nobody thought of cannot be written down
// in advance.
//
// Each carries the name the generated file used to reach for and never declare,
// or -- for the two expression cases -- the name it used to invent by gluing
// two together.
var silentlyBrokenViews = []struct {
	name   string
	source string
	// component says the view compiles as a component, which is a function with
	// its props and no page.
	component bool
	// says is what the refusal has to mention for the person to act on it.
	says string
}{
	{
		name: "@yield inside a section",
		source: `//go:build kyse

package views

@extends('layouts.app')

@section('content')
@yield('sidebar')
@endsection
`,
		says: "@yield",
	},
	{
		name: "@csrf in a component",
		source: `//go:build kyse

package views

<form>
@csrf
</form>
`,
		component: true,
		says:      "@csrf",
	},
	{
		name: "@include in a component",
		source: `//go:build kyse

package views

@include('partials.footer')
`,
		component: true,
		says:      "@include",
	},
	{
		name: "a component that reads the page data it is never handed",
		source: `//go:build kyse

package views

<p>{{ . }}</p>
`,
		component: true,
		says:      "declares no type",
	},
	{
		name: "an @for header that reads data the view declares no type for",
		source: `//go:build kyse

package views

@for(. ; ; )
<p>x</p>
@endfor
`,
		says: "declares no type",
	},
	{
		name: "a doubled dot, which is not an expression and used to become a name",
		source: `//go:build kyse

package views

@go
type PageData struct{ Title string }
@endgo

<p>{{ .. }}</p>
`,
		says: "not a Go expression",
	},
}

// TestTheViewsThatUsedToCompileToBrokenGoAreRefused pins the outcome a person
// gets: a refusal that names the view and the line, at the moment they run the
// build, instead of a green build and a `go build` error later against a file
// whose header says DO NOT EDIT.
func TestTheViewsThatUsedToCompileToBrokenGoAreRefused(t *testing.T) {
	for _, c := range silentlyBrokenViews {
		t.Run(c.name, func(t *testing.T) {
			const path = "resources/views/page.kyse.go"
			file, err := kyse.Parse(path, c.source)
			if err != nil {
				t.Fatalf("the view does not parse, which is not what is under test: %v", err)
			}

			name := "page"
			if c.component {
				name = "components.page"
			}
			_, err = kyse.Generate(file, name, kyse.RenderType(file), "storage/framework/views/page.go")
			if err == nil {
				t.Fatal("the generator accepted a view it compiles to Go that does not build")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("the refusal does not say what is wrong (%q): %v", c.says, err)
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("the refusal does not name the view, so nobody can open it: %v", err)
			}
		})
	}
}

// TestALowercaseFieldIsAFieldAndNotAGluedName is the same failure in the one
// shape that is ordinary rather than pathological.
//
// A view compiles into the package its @go block declares the struct in, so
// `{{ .total }}` is a field read like any other. The translation only knew
// about capitals, so the dot became the page data and `total` was left glued to
// it: `kyse__dtotal`, a name the file never declares. That parses, so gofmt
// accepted it and the build reported the view compiled.
func TestALowercaseFieldIsAFieldAndNotAGluedName(t *testing.T) {
	const path = "resources/views/page.kyse.go"
	file, err := kyse.Parse(path, `//go:build kyse

package views

@go
type PageData struct{ total string }
@endgo

<p>{{ .total }}</p>
`)
	if err != nil {
		t.Fatalf("the view does not parse: %v", err)
	}
	out, err := kyse.Generate(file, "page", kyse.RenderType(file), "storage/framework/views/page.go")
	if err != nil {
		t.Fatalf("a view reading a field of its own struct was refused: %v", err)
	}
	if !strings.Contains(string(out), ".total") {
		t.Errorf("the generated Go does not read the field:\n%s", out)
	}
	if strings.Contains(string(out), "dtotal") {
		t.Errorf("the generated Go glued the field to the page data:\n%s", out)
	}
}
