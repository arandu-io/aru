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
	"go/ast"
	goparser "go/parser"
	"go/token"
	"sort"
	"strconv"
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

	// lines are the source as it was written, one entry per line, so a
	// refusal can show the line it names rewritten into the shape that reads.
	lines []string
}

// sourceLine is line n of the view, 1-indexed, or fallback when the file was
// not built by Parse and carries no source.
func (f *File) sourceLine(n int, fallback string) string {
	if n < 1 || n > len(f.lines) {
		return fallback
	}
	return f.lines[n-1]
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
	"extends":    true,
	"yield":      true,
	"include":    true,
	"csrf":       true,
	"attributes": true,
	"elseif":     true,
	"else":       true,
	"continue":   true,
	"break":      true,
	"empty":      true,
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

// BlockEnds maps each block directive kyse knows to the directive that closes
// it: "if" to "endif". A directive Directives returns that is in neither the
// keys nor the values of this map is an inline one.
//
// It answers a copy, so a caller listing the set cannot change what the
// parser accepts.
func BlockEnds() map[string]string {
	out := make(map[string]string, len(blockDirectives))
	for open, end := range blockDirectives {
		out[open] = end
	}
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
// like any other, and the chrome a layout declares lives in the native view
// package, which every generated file already imports.
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
// the native view package publishes and that view.Page satisfies. That is the
// ordinary case now: the chrome every application draws identically -- the
// title, the brand, the token, the four navigation links -- belongs to that
// package, so a layout only declares an interface when it wants a different one.
func RenderType(f *File) string {
	name, _ := renderType(f)
	return name
}

// renderType is RenderType with the line the name was declared on, or 0 when
// the name is the native view.Layout and no line of the view declares it.
func renderType(f *File) (string, int) {
	if f.IsLayout() {
		if name, line := firstType(f, " interface"); name != "" {
			return name, line
		}
		return "view.Layout", 0
	}
	return pageType(f)
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
	name, _ := pageType(f)
	return name
}

// pageType is PageType with the source line the name was declared on.
func pageType(f *File) (string, int) {
	name, line, _ := pageChoice(f)
	return name, line
}

// typeDecl is one type an `@go` block declares, as far as choosing the page's
// data type needs to know about it.
type typeDecl struct {
	name string
	line int
	// isStruct is false for an alias.
	isStruct bool
	// marked is true for a struct that embeds view.Page, and for any type the
	// block asserts to an interface with `var _ I = Name{}`.
	marked bool
	// referenced is true for a type another struct of the blocks names in a
	// field: the row of a table, the item of a list.
	referenced bool
}

// pageChoice decides the type a page draws, and answers the declarations that
// make the choice ambiguous when there is no one answer.
//
// The page's data is the struct that embeds view.Page, which is what lets the
// layout draw it, or the type the view asserts to view.Layout or to the
// interface of its own layout. A struct declared beside it -- the row of a
// table, the item of a list -- is a helper, wherever it sits in the block.
//
// A page that marks no type draws the one no other struct of its blocks names
// in a field: the row a table holds is part of the page that holds the table.
// When that leaves more than one, or none, the page is refused rather than
// guessed at, because the first one in the block is as likely to be the helper.
//
// A view that extends no layout is not a page: a component's props and a
// partial's data are the first struct it declares, or the first alias when it
// declares no struct.
func pageChoice(f *File) (string, int, []typeDecl) {
	decls, ok := declaredTypes(f)
	if !ok {
		// A block that does not parse, or that declares a type no assertion
		// can name, is read line by line as it was written: the Go compiler or
		// refuseUnusableTypeNames refuses it, about the line and not about a
		// choice between types.
		if name, line := firstType(f, " struct"); name != "" {
			return name, line, nil
		}
		name, line := firstType(f, " = ")
		return name, line, nil
	}

	var marked []typeDecl
	for _, d := range decls {
		if d.marked {
			marked = append(marked, d)
		}
	}
	switch {
	case len(marked) == 1:
		return marked[0].name, marked[0].line, nil
	case len(marked) > 1:
		return marked[0].name, marked[0].line, marked
	case len(decls) > 1 && f.Extends != "":
		var unused []typeDecl
		for _, d := range decls {
			if !d.referenced {
				unused = append(unused, d)
			}
		}
		if len(unused) == 1 {
			return unused[0].name, unused[0].line, nil
		}
		if len(unused) == 0 {
			unused = decls
		}
		return unused[0].name, unused[0].line, unused
	}
	for _, d := range decls {
		if d.isStruct {
			return d.name, d.line, nil
		}
	}
	if len(decls) > 0 {
		return decls[0].name, decls[0].line, nil
	}
	return "", 0, nil
}

// declaredTypes reads the struct and alias declarations of the `@go` blocks, in
// source order, and reports false when a block does not parse as Go.
//
// Each block is parsed on its own, behind a package clause of its own, so the
// line of a declaration is the block's first line plus its line in the block.
func declaredTypes(f *File) ([]typeDecl, bool) {
	views := viewImportNames(f.Imports)
	var decls []typeDecl
	asserted := map[string]bool{}
	// named holds every identifier a struct field's type names, so a type
	// another struct refers to is known to be a part of it.
	named := map[string]bool{}
	for _, block := range f.Go {
		fset := token.NewFileSet()
		file, err := goparser.ParseFile(fset, "", "package kyse\n"+block.Body, goparser.SkipObjectResolution)
		if err != nil {
			return nil, false
		}
		for _, decl := range file.Decls {
			gen, isGen := decl.(*ast.GenDecl)
			if !isGen {
				continue
			}
			for _, spec := range gen.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					// A type parameter list, or a * Go reads as a pointer to the
					// struct, is a declaration the compiled view cannot assert
					// to. The line is read as written instead, and Parse
					// refuses the name it spells.
					if star, isStar := s.Type.(*ast.StarExpr); s.TypeParams != nil || (isStar && isStruct(star.X)) {
						return nil, false
					}
					line := block.Line + fset.Position(s.Name.Pos()).Line - 2
					switch t := s.Type.(type) {
					case *ast.StructType:
						for _, field := range t.Fields.List {
							ast.Inspect(field.Type, func(n ast.Node) bool {
								if id, ok := n.(*ast.Ident); ok && id.Name != s.Name.Name {
									named[id.Name] = true
								}
								return true
							})
						}
						decls = append(decls, typeDecl{name: s.Name.Name, line: line, isStruct: true, marked: embedsPage(t, views)})
					default:
						if s.Assign.IsValid() {
							decls = append(decls, typeDecl{name: s.Name.Name, line: line})
						}
					}
				case *ast.ValueSpec:
					if s.Type == nil {
						continue
					}
					for i, name := range s.Names {
						if name.Name == "_" && i < len(s.Values) {
							if asserts := assertedType(s.Values[i]); asserts != "" {
								asserted[asserts] = true
							}
						}
					}
				}
			}
		}
	}
	for i := range decls {
		if asserted[decls[i].name] {
			decls[i].marked = true
		}
		if named[decls[i].name] {
			decls[i].referenced = true
		}
	}
	return decls, true
}

// isStruct reports whether the expression is a struct type.
func isStruct(e ast.Expr) bool {
	_, ok := e.(*ast.StructType)
	return ok
}

// embedsPage reports whether the struct embeds view.Page, by value or by
// pointer, under any name the view imports the view package as.
func embedsPage(t *ast.StructType, views map[string]bool) bool {
	for _, field := range t.Fields.List {
		if len(field.Names) != 0 {
			continue
		}
		typ := field.Type
		if star, ok := typ.(*ast.StarExpr); ok {
			typ = star.X
		}
		sel, ok := typ.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Page" {
			continue
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && views[pkg.Name] {
			return true
		}
	}
	return false
}

// assertedType is the type an assertion `var _ I = <value>` is about: Name{},
// &Name{} or (*Name)(nil), and empty for any other value.
func assertedType(value ast.Expr) string {
	if unary, ok := value.(*ast.UnaryExpr); ok && unary.Op == token.AND {
		value = unary.X
	}
	switch v := value.(type) {
	case *ast.CompositeLit:
		if id, ok := v.Type.(*ast.Ident); ok {
			return id.Name
		}
	case *ast.CallExpr:
		if paren, ok := v.Fun.(*ast.ParenExpr); ok {
			if star, ok := paren.X.(*ast.StarExpr); ok {
				if id, ok := star.X.(*ast.Ident); ok {
					return id.Name
				}
			}
		}
	}
	return ""
}

// viewImportNames are the names a view calls the view package by: view, which
// the generator always provides, and the name of every import whose path ends
// in /view.
func viewImportNames(imports []string) map[string]bool {
	names := map[string]bool{"view": true}
	for _, spec := range imports {
		fields := strings.Fields(spec)
		if len(fields) == 0 {
			continue
		}
		path, err := strconv.Unquote(fields[len(fields)-1])
		if err != nil || !strings.HasSuffix(path, "/view") {
			continue
		}
		if len(fields) == 2 {
			names[fields[0]] = true
		}
	}
	return names
}

// firstType finds the first `type X <kind>` in the @go blocks, and the source
// line it is on.
//
// The first one in source order wins, so this answers about one kind only: a
// layout's interface, which a layout declares one of, and the fallback for a
// block that does not parse. Which struct a page draws is pageChoice's
// question, and it is not decided by order.
//
// The name is the second word of the line, whatever it is. Parse refuses the
// view when that word is not one Go can use as a type name, so a name RenderType
// or PageType answers for a parsed view is always an identifier.
func firstType(f *File, kind string) (string, int) {
	for _, block := range f.Go {
		for offset, line := range strings.Split(block.Body, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "type ") || !strings.Contains(line, kind) {
				continue
			}
			if fields := strings.Fields(line); len(fields) >= 2 {
				return fields[1], block.Line + offset
			}
		}
	}
	return "", 0
}
