// Package kyse compiles a view into Go.
//
// The name is guarani for knife. It is the Blade of Arandu: the directives have
// the same names, the same shape and the same meaning, so a developer arriving
// from Laravel writes a view on the first day without learning a syntax.
//
// # The file
//
// The source is `home.kyse.go` and the output is `home.go`, side by side in the
// same package. The extension ends in `.go` for the same reason `.blade.php`
// ends in `.php`: the host language is part of the name.
//
//	//go:build kyse
//
//	package views
//
//	@go
//	type HomeData struct{ Name string }
//	@endgo
//
//	@extends('layouts.app')
//
//	@section('content')
//	    <h1>Olá {{ .Name }}</h1>
//	@endsection
//
// The build tag is what makes it legal. Go reads the constraint, sees the file
// is excluded, and stops at the package clause -- it never parses the markup
// below. Verified on Go 1.26.5: build, vet, test and run all pass with the
// generated file in the same directory. `gofmt` is the only tool that ignores
// build constraints, and the CI calls it filtering `*.kyse.go`.
//
// # What it is not
//
// It is not a template engine with a runtime. There is no parse at boot, no
// cache directory, no reflection over the data. The output is a Go function
// that writes strings, and the data is the struct the view declared in its `@go`
// block -- so a field that does not exist is a compile error rather than a blank
// page. That is the whole reason to have written this instead of using
// html/template.
package kyse

import (
	"fmt"
	"strings"
)

// Kind is what a piece of a view is.
type Kind int

const (
	// Text is literal markup, written out as-is.
	Text Kind = iota
	// Echo is `{{ expr }}` -- escaped.
	Echo
	// Raw is `{!! expr !!}` -- not escaped.
	Raw
	// Directive is `@name(args)`.
	Directive
	// GoBlock is the body between `@go` and `@endgo`, copied verbatim into the
	// generated file.
	GoBlock
)

// Node is one piece of a parsed view, with the position it came from.
//
// The position is the reason this exists as a tree rather than a string
// rewrite: an error has to name the line of the `.kyse.go`, not of the
// generated file. Laravel solves the same problem with a heuristic -- its
// BladeMapper recompiles the template inserting markers and gives up after
// twenty lines -- and we do not have to, because we emit the Go ourselves.
type Node struct {
	Kind Kind
	// Name is the directive name for Directive, empty otherwise.
	Name string
	// Body is the text, the expression, or the directive arguments.
	Body string
	// Children are the nodes inside a block directive.
	Children []Node
	// Line is the 1-indexed line in the source file.
	Line int
}

// File is a parsed view.
type File struct {
	// Package is the clause the Go compiler stops at.
	Package string
	// Go is the content of the @go blocks, in order, copied verbatim.
	Go []string
	// Extends is the layout this view extends, or empty.
	Extends string
	// Sections are the named blocks, in declaration order.
	Sections []Section
	// Body is the top-level content of a view that extends nothing -- a layout
	// is written this way.
	Body []Node
	// Path is the source file, for error messages.
	Path string
}

// IsLayout reports whether this file is a layout.
//
// The distinction is not a naming convention: a layout receives the sections a
// child declared, and a page does not. The source says which by containing the
// directive that reads them.
//
// It matters for the data type too. A layout renders with the interface it
// declares, so any page that satisfies it can be drawn inside; a page renders
// with its own struct, because it interpolates fields. Reading one type for both
// is what made `aru make:auth` install a layout typed by the sign-in struct, and
// from then on every page `aru make:module` generated failed to render -- with a
// green `go build`, because the disagreement is a type assertion at run time.
func (f *File) IsLayout() bool {
	var walk func([]Node) bool
	walk = func(nodes []Node) bool {
		for _, n := range nodes {
			if n.Kind == Directive && n.Name == "yield" {
				return true
			}
			if walk(n.Children) {
				return true
			}
		}
		return false
	}
	return walk(f.Body)
}

// Section is one `@section(name) … @endsection`.
type Section struct {
	Name  string
	Nodes []Node
	Line  int
}

// Error is a compile error that names the source position.
//
// The whole point of writing a compiler instead of using a template library is
// that this can be exact. `resources/views/home.kyse.go:12: @endsection with no
// @section` is a sentence somebody acts on; a stack trace into a generated file
// is not.
type Error struct {
	Path    string
	Line    int
	Message string
	// Hint is what to do about it, when there is something to say.
	Hint string
}

func (e *Error) Error() string {
	out := fmt.Sprintf("%s:%d: %s", e.Path, e.Line, e.Message)
	if e.Hint != "" {
		out += "\n    " + e.Hint
	}
	return out
}

// Errors is more than one problem, reported together.
//
// At once, not at the first: a person fixing a view should not discover the
// problems one build at a time. Same reasoning as the spec validator.
type Errors []*Error

func (e Errors) Error() string {
	parts := make([]string, len(e))
	for i, err := range e {
		parts[i] = err.Error()
	}
	return strings.Join(parts, "\n")
}

// blockDirectives are the ones that open a region and need a matching end.
//
// These two maps are the whole set kyse knows, and the parser reads them
// directly -- there is no second answer to "is this a directive". The set is
// closed, and that is RULE 15 applied to the view: a directive that grows on
// demand becomes a language, and a language has to be maintained forever. What
// does not fit is written in Go, inside `@go`.
var blockDirectives = map[string]string{
	"section": "endsection",
	"if":      "endif",
	"foreach": "endforeach",
	"for":     "endfor",
	"while":   "endwhile",
	"go":      "endgo",
}

// inlineDirectives take arguments and emit in place.
var inlineDirectives = map[string]bool{
	"extends": true,
	"yield":   true,
	"include": true,
	"csrf":    true,
	"elseif":  true,
	"else":    true,
}

// OutputPath says where the Go generated from a view goes.
//
// The source keeps Laravel's tree — `resources/views/auth/login.kyse.go` — and
// the generated Go lands flat in `resources/views/`, named after the path:
// `auth_login.go`.
//
// It is not beside the source when the source is nested, and that is a
// constraint of the language rather than a preference. **Go has one package per
// directory.** With the generated file beside a nested source,
// `resources/views/layouts/` would be a different package from
// `resources/views/`, and the data struct a layout declares would not be visible
// to the page that extends it. One package is what lets a page embed the
// layout's data.
//
// The alternative — flattening the source too — would cost the thing the whole
// structure exists for: a Laravel developer opening `resources/views/auth/` and
// finding `login`, `register` and `passwords/` where they expect them.
func OutputPath(viewsDir, source string) string {
	rel := strings.TrimPrefix(strings.TrimPrefix(source, viewsDir), "/")
	rel = strings.TrimSuffix(rel, ".kyse.go")
	return viewsDir + "/" + strings.ReplaceAll(rel, "/", "_") + ".go"
}

// Name is the name a view is rendered by: the path under resources/views, with
// dots. `auth/login.kyse.go` is rendered as "auth.login", which is exactly how
// Laravel names it.
func Name(viewsDir, source string) string {
	rel := strings.TrimPrefix(strings.TrimPrefix(source, viewsDir), "/")
	rel = strings.TrimSuffix(rel, ".kyse.go")
	return strings.ReplaceAll(rel, "/", ".")
}

// RenderType is the type a view asserts the data to before drawing.
//
// For a layout that is the interface, when it declares one. That is the whole
// fix for a defect that cost a full cycle: `aru make:auth` installs a layout
// whose @go block declares the sign-in struct, and reading the first type for
// both questions made the layout render with THAT struct. From then on every
// page `aru make:module` generated answered
//
//	view "layouts.app" takes AuthPage and got views.InvoicesIndexData
//
// with `go build` green, because a type assertion fails at run time and the
// routes hide it: the wiring is manual, so the page 404s before anyone reaches
// the 500.
//
// For a page it is the struct, because a page interpolates fields.
func RenderType(f *File) string {
	if f.IsLayout() {
		if name := firstType(f, " interface"); name != "" {
			return name
		}
	}
	return PageType(f)
}

// PageType is the struct a view declared, and what a layout publishes to the
// views that extend it.
func PageType(f *File) string { return firstType(f, " struct") }

// firstType finds the first `type X <kind>` in the @go blocks.
//
// Order of declaration does not decide anything: the caller asks for the kind it
// needs. A layout that declares the interface below the struct behaves the same
// as one that declares it above.
func firstType(f *File, kind string) string {
	for _, block := range f.Go {
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "type ") || !strings.Contains(line, kind) {
				continue
			}
			if fields := strings.Fields(line); len(fields) >= 2 {
				return fields[1]
			}
		}
	}
	return ""
}
