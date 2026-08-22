// Package kyse compiles a view into Go.
//
// The name is guarani for knife. The directives are the ones a template
// language is expected to have, with the names people already use for them, so
// a view is written on the first day without learning a syntax.
//
// # The file
//
// The source is `home.kyse.go` and the output is `home.go`, side by side in the
// same package. The extension ends in `.go` because the host language is part
// of the name -- which is what lets the compiler see the file and skip it.
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
	"sort"
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

// Block is one `@go … @endgo` body, with the line its first line came from.
//
// The line is what lets a type error inside the block name the view instead of
// the generated file. The body is copied verbatim, so the Nth line of Body is
// the (Line+N-1)th line of the source, and one directive at the top is enough
// to carry the whole block.
type Block struct {
	// Body is the Go between the two directives, unchanged.
	Body string
	// Line is the 1-indexed source line the body starts on -- the line after
	// `@go`, not the `@go` itself.
	Line int
}

// Node is one piece of a parsed view, with the position it came from.
//
// The position is the reason this exists as a tree rather than a string
// rewrite: an error has to name the line of the `.kyse.go`, not of the
// generated file. An engine that compiles at run time can only guess at this --
// recompile with markers, search, give up after twenty lines -- and we do not
// have to guess, because we emit the Go ourselves.
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
	// Imports are the lines of the import block, when the view has one, copied
	// verbatim into the generated file. It is how a view draws a component that
	// lives in another package.
	Imports []string
	// Go is the content of the @go blocks, in order, copied verbatim.
	Go []Block
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
// is what made the starter kit install a layout typed by the sign-in struct, and
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

// Unwrap exposes the problems one at a time, so errors.As reaches a position
// through the group.
//
// Without it a caller asking whether a failure names a line got no for an
// answer whenever there was more than one problem, which is the case where the
// answer matters most.
func (e Errors) Unwrap() []error {
	out := make([]error, len(e))
	for i, err := range e {
		out[i] = err
	}
	return out
}

// blockDirectives are the ones that open a region and need a matching end.
//
// These two maps are the whole set kyse knows, and the parser reads them
// directly -- there is no second answer to "is this a directive". The set is
// closed: a directive set that grows on demand becomes a language, and a
// language has to be maintained forever. What does not fit is written in Go,
// inside `@go`.
var blockDirectives = map[string]string{
	"section": "endsection",
	"if":      "endif",
	"foreach": "endforeach",
	"forelse": "endforelse",
	"for":     "endfor",
	"while":   "endwhile",
	"go":      "endgo",
}

// inlineDirectives take arguments and emit in place.
var inlineDirectives = map[string]bool{
	"extends":  true,
	"yield":    true,
	"include":  true,
	"csrf":     true,
	"elseif":   true,
	"else":     true,
	"continue": true,
	"break":    true,
	"empty":    true,
}

// Directives returns every directive kyse knows, block and inline, sorted.
//
// It exists so a test can walk the whole set. Three times now a directive was
// declared here and had no case in the generator, and each time the node was
// dropped in silence: @else made both halves of an if appear at once, and @for
// and @while emitted the loop with an empty body. The build stays green, the
// command reports success, and the page is simply missing what the author wrote.
//
// A closed set is only a promise if something checks that every member of it
// does something.
func Directives() []string {
	out := make([]string, 0, len(blockDirectives)*2+len(inlineDirectives))
	for open, end := range blockDirectives {
		out = append(out, open, end)
	}
	for name := range inlineDirectives {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// OutputPath says where the Go generated from a view goes: under storage,
// mirroring the tree of the source.
//
//	resources/views/auth/login.kyse.go -> storage/framework/views/auth/login.go
//
// It is build output, so it is gitignored: written beside the source, every
// `aru view:build` would put 28 files nobody wrote into `git status`, and a
// reviewer would have to skip them in every review.
//
// Each directory of views is its own Go package, named by the person in the
// source's package clause and copied verbatim into the output. That is the
// ordinary arrangement of a Go tree, and it is what keeps `resources/views/`
// readable: opening `auth/` shows the four screens of `auth/` and nothing else.
//
// The output does not land flat. Go has one package per directory, which is what
// makes flattening look necessary -- but a nested package imports what it needs
// like any other, and the chrome a layout declares lives in `framework/view`,
// which every generated file already imports.
//
// The cost is one blank import per directory of views in bootstrap, next to the
// one that was already there. That is the registration the whole design is built
// on -- the same shape as a database/sql driver -- and paying it per directory
// rather than once is what fifteen files leaving the top level costs.
func OutputPath(source string) string {
	at := strings.Index(source, viewsPath)
	if at < 0 {
		// A library keeps its views at its own root and has no resources/views
		// to mirror, nor a storage to mirror it into. It compiles in place,
		// because the output is what `go get` serves rather than build output
		// somebody regenerates.
		return strings.TrimSuffix(source, ".kyse.go") + ".go"
	}
	return source[:at] + compiledPath + strings.TrimSuffix(source[at+len(viewsPath):], ".kyse.go") + ".go"
}

// Where a view is written and where its compiled form goes.
//
// The tree under compiledPath mirrors the tree under viewsPath, so
// `resources/views/posts/index.kyse.go` compiles to
// `storage/framework/views/posts/index.go`.
const (
	viewsPath    = "resources/views/"
	compiledPath = "storage/framework/views/"
)

// CompiledDir is compiledPath as a caller can use it, without the trailing
// separator.
//
// Exported so that whatever sweeps the compiled tree and whatever writes into
// it read the same constant. Two string literals for one directory is how a
// sweeper comes to look somewhere the writer never wrote.
const CompiledDir = "storage/framework/views"

// Name is the name a view is rendered by: the path under resources/views, with
// dots. `auth/login.kyse.go` is rendered as "auth.login".
func Name(viewsDir, source string) string {
	rel := strings.TrimPrefix(strings.TrimPrefix(source, viewsDir), "/")
	rel = strings.TrimSuffix(rel, ".kyse.go")
	return strings.ReplaceAll(rel, "/", ".")
}

// RenderType is the type a view asserts the data to before drawing.
//
// For a layout that is the interface, when it declares one. That is the whole
// fix for a defect that cost a full cycle: the starter kit installs a layout
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
//
// A layout that declares no interface renders with view.Layout, the contract
// framework/view publishes and that view.Page satisfies. That is the ordinary
// case now: the chrome every application drew identically -- the title, the
// brand, the token, the four navigation links -- moved into the framework, so a
// layout only declares an interface when it wants a different one.
func RenderType(f *File) string {
	if f.IsLayout() {
		if name := firstType(f, " interface"); name != "" {
			return name
		}
		return "view.Layout"
	}
	return PageType(f)
}

// PageType is the type a view declared, and what a layout publishes to the views
// that extend it.
//
// A declaration or an alias, and the alias is the interesting one:
//
//	@go
//	type LoginData = authhttp.LoginData
//	@endgo
//
// It is how a screen renders a struct the controller owns. The struct belongs
// with the code that fills it -- that is where the fields are set and where a
// missing one is a compile error -- and repeating it in the view would be two
// declarations of one shape, kept in step by hand.
//
// The view still says which type it draws, in one line, and that line is what
// makes `{{ .Email }}` compile or not.
func PageType(f *File) string {
	if name := firstType(f, " struct"); name != "" {
		return name
	}
	return firstType(f, " = ")
}

// firstType finds the first `type X <kind>` in the @go blocks.
//
// Order of declaration does not decide anything: the caller asks for the kind it
// needs. A layout that declares the interface below the struct behaves the same
// as one that declares it above.
func firstType(f *File, kind string) string {
	for _, block := range f.Go {
		for _, line := range strings.Split(block.Body, "\n") {
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
