package kyse

import (
	"fmt"
	"go/format"
	goparser "go/parser"
	"go/scanner"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Generate turns a parsed view into the Go that renders it.
//
// The output is a function that writes strings to an io.Writer, plus an init()
// that registers it under the view's name. No runtime parse, no cache
// directory, no reflection: the data is the struct the view declared in its
// `@go` block, and a field that does not exist stops the build.
//
// A template engine usually compiles to source at run time and caches the
// result. Same idea, moved to build time -- which is what makes
// the typo a compile error instead of a warning nobody reads in production.
//
// # Why the output path is a parameter
//
// The generated Go carries `//line` directives, so the compiler reports a type
// error inside `{{ }}` at the line of the `.kyse.go` the person wrote. A
// directive stays in effect until the next one, which means the scaffolding
// between two interpolations has to be handed back to the generated file by
// name -- and this function is the only place that knows which lines are the
// view's and which are its own. Guessing the name from f.Path is not possible:
// The output is `auth/login.go` beside `auth/login.kyse.go`, and deriving one
// name from the other here would put the rule in two places -- OutputPath is
// where it lives, and a caller that disagreed with it would write the directive
// for a file that is not the one on disk.
//
// Both paths are written into the output verbatim -- the Go compiler prints a
// //line file name exactly as it finds it, without resolving it -- so passing
// them relative to the project root is what makes the reported position
// clickable from where `go build` runs.
func Generate(f *File, name, dataType, output string) ([]byte, error) {
	// A view that reads a field with no type to read it from cannot produce Go
	// that compiles, and it has to be said here rather than by the Go compiler
	// three steps later. See dataReference.
	g := &generator{file: f, name: name, dataType: dataType}
	// Which packages the view names for itself, decided before anything is
	// written: the import block needs it, and so does the check on a loop
	// binding, which runs first.
	g.published = publishedImports(f)

	if dataType == "" {
		if expr, line, found := dataReference(f, g); found {
			return nil, &Error{
				Path:    f.Path,
				Line:    line,
				Message: fmt.Sprintf("%s reads a field of the page data, and this view declares no type for it", expr),
				Hint: "declare the struct inside @go … @endgo, which is the only place Go is Go:\n" +
					"        @go\n" +
					"        type PageData struct{ … }\n" +
					"        @endgo\n" +
					"    A type declaration outside the block is markup, not a declaration.\n" +
					"    A view that extends a layout inherits the layout's type instead.",
			}
		}
	}

	if err := validateExpressions(f, g); err != nil {
		return nil, err
	}
	if err := yieldInsideASection(f); err != nil {
		return nil, err
	}
	if err := g.pageDirectiveInAComponent(); err != nil {
		return nil, err
	}

	g.emit()

	// A value written where no escape holds is refused here rather than
	// emitted, because no call would make it safe: the position takes text and
	// the value would arrive as syntax. Unlike the positions that refuse at
	// render time, these refuse whatever the value is, so the build is where it
	// belongs.
	if len(g.errs) > 0 {
		return nil, g.errs
	}

	src := g.out.String()
	formatted, err := format.Source([]byte(src))
	if err != nil {
		// The generated Go not parsing is a bug in this generator, never in the
		// view. Saying so saves the reader from looking in the wrong file.
		return nil, fmt.Errorf("kyse: the Go generated from %s does not parse -- this is a bug in the generator: %w\n%s",
			f.Path, err, numbered(src))
	}
	out := resolveLineMarkers(formatted, f.Path, output)

	// The last thing, on the bytes that are about to be written. What the
	// generator believes it emitted is not evidence about what it emitted, so
	// the output is read back and resolved by something that shares none of its
	// bookkeeping. See checkScopes.
	if err := checkScopes(out, f, output); err != nil {
		return nil, err
	}
	return out, nil
}

// The placeholders the generator writes where a line directive goes.
//
// They are already `//line` comments, and that is what keeps them in column 1:
// go/printer suspends indentation for a comment starting with `//line ` and
// indents every other one. A directive one tab in is an ordinary comment the
// compiler ignores, so a marker that survived formatting indented would be a
// silent loss of the whole feature.
//
// The second one carries a line number that does not exist until gofmt has
// settled the output, which is why both are resolved afterwards rather than
// written final.
const (
	markerSource = "//line kyse-source:"
	markerSelf   = "//line kyse-generated:1"
)

// The names the generated Go writes for itself.
//
// A render function holds two kinds of name: the ones a view chose -- the
// binding of @foreach and @forelse, the variables of an @for header -- and the
// ones this generator needs to do its work. They share one scope, so a name
// this generator writes and then reads is a name a view can take away from it:
// `@foreach(.Steps as s)` used to rename the temporary a URL escape writes
// into, and the view was reported for a type error inside code it never wrote.
// The reserved letter is the worst part of it, because nothing tells the person
// which letters are spoken for.
//
// So every name below carries a prefix that a binding cannot produce --
// loopBinding refuses one that starts with it -- and the temporaries that live
// for a single statement carry a counter as well, so no two of them are the
// same name either.
//
// The imports go the same way, for the same reason -- see the block below
// them: a package name is an identifier in the same scope, and a binding took
// one exactly as it took a variable.
//
// A view is left with the two names it writes on purpose: `d` for the page
// data, and the binding it chose. Both are declared from the names below and
// never read again by anything this generator emits, so a view may shadow
// either one and reach nothing but itself.
const hygienePrefix = "kyse__"

const (
	// varWriter is the io.Writer the page is written to.
	varWriter = hygienePrefix + "w"
	// varData is the untyped data the render function is handed.
	varData = hygienePrefix + "data"
	// varPage is the data after the type assertion, and what an interpolation
	// of a field reads.
	varPage = hygienePrefix + "d"
	// varOK is the second result of that assertion.
	varOK = hygienePrefix + "ok"
	// varErr is the error threaded through every write, which the first failure
	// stops the page on.
	varErr = hygienePrefix + "err"
	// varSections is the map of sections a layout renders.
	varSections = hygienePrefix + "sections"
	// varProps is a component's parameter.
	varProps = hygienePrefix + "props"
)

// The names the generated Go calls its imports by.
//
// A package name is an ordinary identifier in the file that imports it, and a
// loop binding is an ordinary identifier in the block it opens, so the two
// share one scope exactly the way the variables above do. `@foreach(.Steps as
// view)` renamed the package every escape is called through, and the view was
// reported for `view.TextURL undefined (type Step has no field or method
// TextURL)` -- the same failure as a taken variable, one indirection further
// out, and no more predictable: nothing tells the person that `io`, `fmt` or
// `template` are spoken for.
//
// So each import is renamed on the way in, into the same namespace a binding
// cannot reach. What is left in the generated file under a name a view could
// write is what the view itself put there: its own imports, its own types, and
// the binding it chose.
const (
	// pkgTemplate is html/template, whose escaper the body of an element takes
	// and whose HTML type a component returns.
	pkgTemplate = hygienePrefix + "template"
	// pkgIO is io: the writer a page is rendered into, and every write to it.
	pkgIO = hygienePrefix + "io"
	// pkgErrors is errors, which carries the refusal of a value examined at
	// render time.
	pkgErrors = hygienePrefix + "errors"
	// pkgFmt is fmt, which gives a refusal the view's own position before it
	// travels.
	pkgFmt = hygienePrefix + "fmt"
	// pkgStrings is strings: the builder a component writes into, and the test a
	// checked interpolation makes.
	pkgStrings = hygienePrefix + "strings"
	// pkgView is the native view package, which holds every escape, the
	// registries and the calls a directive compiles to.
	pkgView = hygienePrefix + "view"
)

// nativeView is the import path of the view package the generated file calls
// into, and the one path this file writes twice.
const nativeView = "github.com/arandu-io/hesape/view"

// republished is a package the generated file may name twice: once under the
// reserved prefix, for the calls this generator writes, and once under its own
// name, for the calls the view writes.
//
// Renaming every import into the reserved namespace closed the half of the
// shadowing hole that a package name opened, and withdrew a name in the same
// stroke. `view` was never only the generator's: a view writes
// `view.AssetURL("app.css")` in a link, embeds `view.Page` in the struct it draws,
// and registers an asset of its own from an `@go` block. Every one of those
// resolved through the import the generated file already carried, and a rename
// that takes a published name away without saying so fails the same way the
// shadowing did -- `undefined: view`, reported against a line the person did
// write.
//
// So the reserved name stays for the generator, and the plain one comes back
// only for the view that asks for it. A file whose own source calls the package
// gets it in scope; a file that does not gets a scope with no such name in it,
// where a loop may bind `view` and reach nothing but itself. Go accepts one path
// under two names, so the two live side by side.
//
// symbol is an exported name of the package, written into the silencing block so
// the plain import is read whatever becomes of the reference. Deciding from the
// source whether the call survives the generator's own translation means
// predicting it, and being wrong in that direction is an unused import -- a
// build that fails inside a file whose header says not to edit it.
type republished struct {
	path   string
	symbol string
}

// republishedPackages are the packages a view may name for itself: one entry
// per package this generator renames, and the completeness is the point.
//
// The table had `view` alone, on the reasoning that no view names the other
// five from its markup -- and markup was the wrong place to look. An `@go`
// block is Go the person writes and this generator copies through untouched,
// and a component declares `Icon template.HTML` there for the same reason any
// Go author would. That declaration lands in a file whose only `html/template`
// answers to the reserved name, so it fails as `undefined: template` against
// the line the person wrote: the exact failure the entry for `view` exists to
// prevent, in the four packages the table left out.
//
// So the rule is the rename, not the list. A package this generator renames is
// a package whose plain name it has taken away, and every one of them comes
// back under the same narrow condition -- the view's own source calls it. A
// view that never names one still gets a scope with no such name in it, which
// is what leaves `io`, `fmt` or `template` free for a loop to bind. Nothing a
// template can bind is reserved by this table; only what a view already spends.
//
// The symbols are values rather than types, because the silencing block reads
// each one into the blank identifier and a type is not a value there.
var republishedPackages = map[string]republished{
	"view":     {path: nativeView, symbol: "Text"},
	"template": {path: "html/template", symbol: "HTMLEscapeString"},
	"io":       {path: "io", symbol: "WriteString"},
	"fmt":      {path: "fmt", symbol: "Fprintf"},
	"strings":  {path: "strings", symbol: "NewReader"},
	"errors":   {path: "errors", symbol: "New"},
}

// publishedImports are the plain package names the generated file has to bind
// for the view's own code to compile, in a stable order.
func publishedImports(f *File) []string {
	refs := viewPackageRefs(f)
	if len(refs) == 0 {
		return nil
	}
	own, _ := viewImports(f)
	var out []string
	for name := range refs {
		// A view that binds the name in its own import block already has it,
		// and one name bound twice is the file Go refuses.
		if own[name] {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// viewPackageRefs are the republished packages the view's own source calls into.
//
// The question is deliberately narrow, because the answer decides two things at
// once: whether the generated file binds the name, and whether a loop may bind
// it. A name counts here only where it is written as the qualifier of an
// exported selector, outside every loop that binds it. Scanning for the bare
// word instead would find it in a comment and in prose, and a word in a sentence
// would then take a loop variable away from somebody who never called the
// package at all.
//
// Comments and string literals are left out by the Go scanner rather than by a
// rule invented here. Loop bindings are left out because a binding is what the
// name means inside the loop it opens: `@foreach(.Rows as view)` followed by
// `{{ view.URL }}` reads the row, in the generated Go exactly as in the source,
// and calling that a package reference would refuse a view for using a variable.
//
// What is left is the honest conflict -- a view that calls the package outside a
// loop and binds the same name in one -- and loopBinding reports it.
func viewPackageRefs(f *File) map[string]bool {
	out := map[string]bool{}
	collect := func(src string, bound map[string]bool) {
		for name := range qualifiers(src) {
			if _, ok := republishedPackages[name]; ok && !bound[name] {
				out[name] = true
			}
		}
	}
	// An `@go` block is Go the person wrote, and the struct a page draws is
	// declared in one. Nothing binds a name over it.
	//
	// It is asked the same question as the body and by the same scanner,
	// because a qualifier in a declaration the author wrote is a reference
	// exactly as much as one inside an interpolation -- and it is the only
	// place a declaration can be written at all.
	for _, block := range f.Go {
		collect(block.Body, nil)
	}

	var walk func(nodes []Node, bound map[string]bool)
	walk = func(nodes []Node, bound map[string]bool) {
		for _, n := range nodes {
			inner := bound
			if n.Kind != Text {
				// The header is read in the scope outside the loop it opens:
				// `.Rows` is not the binding's business.
				collect(n.Body, bound)
				inner = extend(bound, n)
			}
			walk(n.Children, inner)
		}
	}
	walk(f.Body, nil)
	for _, s := range f.Sections {
		walk(s.Nodes, nil)
	}
	return out
}

// extend adds what a directive binds over its children.
//
// Two directives bind: @foreach and @forelse name the element, and an @for
// header may declare its own variables the way any Go header does. Everything
// else is read in the scope it stands in.
func extend(bound map[string]bool, n Node) map[string]bool {
	if n.Kind != Directive {
		return bound
	}
	var names []string
	switch n.Name {
	case "foreach", "forelse":
		if _, binding := splitAs(n.Body); binding != "" {
			names = append(names, binding)
		}
	case "for":
		names = append(names, shortVarNames(n.Body)...)
	}
	if len(names) == 0 {
		return bound
	}
	out := make(map[string]bool, len(bound)+len(names))
	for name := range bound {
		out[name] = true
	}
	for _, name := range names {
		out[name] = true
	}
	return out
}

// shortVarNames are the variables the init clause of an @for header declares.
//
// It reads the first clause only, which is where a Go header declares, and it
// asks for names rather than parsing the statement: what a name is used for
// here is to stop a package reference from being counted, so a name too many
// costs a reference and a name too few costs nothing else.
func shortVarNames(header string) []string {
	init, _, found := strings.Cut(header, ";")
	if !found {
		init = header
	}
	lhs, _, found := strings.Cut(init, ":=")
	if !found {
		return nil
	}
	var out []string
	for _, part := range strings.Split(lhs, ",") {
		if name := strings.TrimSpace(part); validGoIdent(name) {
			out = append(out, name)
		}
	}
	return out
}

// qualifiers are the names src writes as the qualifier of an exported selector:
// the `view` of `view.URL`, and not the `d` of `d.Title`.
//
// It is a scan by the Go scanner and not a parse, because most of what it is
// handed is not a whole Go anything -- a directive's arguments, the expression
// inside an interpolation, the body of an `@go` block on its own. The scanner
// answers on a fragment, skips comments, and reads a string literal as one
// token, which is the whole of what this needs from it. What it cannot read it
// reports as an error and carries on past, and an unreadable fragment is refused
// by gofmt one step later.
//
// The selector has to be exported, because an unexported one cannot be reached
// across a package boundary and so was never a call into a package. The
// qualifier has to stand at the head of the chain, because the `Invoice` of
// `d.Invoice.Reference` is a field.
func qualifiers(src string) map[string]bool {
	out := map[string]bool{}
	fset := token.NewFileSet()
	var s scanner.Scanner
	s.Init(fset.AddFile("", fset.Base(), len(src)), []byte(src), nil, 0)

	type step struct {
		tok token.Token
		lit string
	}
	// The three tokens behind the one being read. A qualifier is the second of
	// them, in `<not a dot> IDENT . IDENT`.
	var window [3]step
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.IDENT && token.IsExported(lit) &&
			window[2].tok == token.PERIOD && window[1].tok == token.IDENT && window[0].tok != token.PERIOD {
			out[window[1].lit] = true
		}
		window[0], window[1], window[2] = window[1], window[2], step{tok, lit}
	}
	return out
}

// viewQualifier is how a caller names a type of the native view package, and it
// is the one qualifier in a data type this generator translates.
//
// The type a view draws is nearly always its own -- a struct its own @go block
// declares -- and such a name is written out untouched, because it is the view's
// name and resolves in the view's own file. The exception is a layout that
// declares no interface: the contract it renders with belongs to the native
// view package,
// and a caller names it the way a person writing Go would. The generated file
// calls that package by a name of its own, so this qualifier is replaced on the
// way in.
//
// Only the Go is translated. The name carried in the mismatch error is left as
// the caller wrote it, because it is read by somebody who never opens the
// generated file and would not recognise the other spelling.
const viewQualifier = "view."

// renderType is the data type as the generated file has to spell it.
func (g *generator) renderType() string {
	if rest, ok := strings.CutPrefix(g.dataType, viewQualifier); ok {
		return pkgView + "." + rest
	}
	return g.dataType
}

// viewData is the name a view writes for its own page data.
//
// `{{ .Title }}` is the form a view is written in, and it needs no name at all.
// This one exists because a view may also write the data out -- `@if(len(d.Rows)
// > 0)` -- and it is declared from varPage rather than being it, so that
// nothing this generator emits reads a name a view is free to shadow.
const viewData = "d"

// resolveLineMarkers turns the placeholders into the real directives.
//
// It replaces each marker line in place, never inserting or removing one, so
// the line numbers it hands out stay true for the markers below it.
func resolveLineMarkers(formatted []byte, source, generated string) []byte {
	lines := strings.Split(string(formatted), "\n")
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, markerSource):
			lines[i] = "//line " + source + ":" + line[len(markerSource):]
		case line == markerSelf:
			// A directive names the position of the line after it, and i counts
			// from zero, so the line after this one is i+2.
			lines[i] = fmt.Sprintf("//line %s:%d", generated, i+2)
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

type generator struct {
	file     *File
	name     string
	dataType string
	out      strings.Builder
	// fn is the current writer variable, so nested helpers write to the same place.
	depth int
	// scan is where in the document the next node writes. It is what decides
	// which escape a value needs, because the position decides it and the value
	// does not.
	scan htmlScanner
	// checks records that at least one value is examined at render time, which
	// the import block has to know before it is written.
	checks bool
	// wraps records that at least one escaper reports a refusal, whose message
	// is given the view's own position before it travels.
	wraps bool
	// errs are the interpolations written where no escape holds, collected
	// rather than returned at the first, so a view is fixed in one pass.
	errs Errors
	// temps counts the temporaries handed out so far, which is what keeps two
	// of them from sharing a name.
	temps int
	// published are the plain package names this file binds for the view's own
	// code, in the order they are written. See republishedPackages.
	published []string
}

// temp names a value that lives for one statement of the generated file.
//
// The counter is what separates one temporary from the next, and the prefix is
// what separates all of them from every name a view can write.
func (g *generator) temp() string {
	g.temps++
	return fmt.Sprintf("%sv%d", hygienePrefix, g.temps)
}

// at says that what comes next was written on this line of the view.
//
// It is called before every construct that carries Go the person typed, and
// paired with self. Text nodes and the directives that take only a string --
// @yield, @include, @csrf -- are left out on purpose: nothing in them can fail
// to compile, and a directive per line would double the size of the output for
// no diagnosis.
//
// A directive names the position of the NEXT line and only that one, so the
// line it precedes has to be the line the expression is on, already in the
// shape gofmt leaves it. Emitting `if err == nil { … }` on one line and letting
// the formatter break it into three put the expression two lines below the
// directive, and every interpolation was reported one line late.
func (g *generator) at(line int) { fmt.Fprintf(&g.out, "%s%d\n", markerSource, line) }

// guarded writes one statement that runs while nothing has failed yet, with the
// view's line pinned to the statement rather than to the `if` above it.
//
// The `if err == nil` around every write is what lets a view be a straight run
// of statements instead of a chain of early returns: the first failure stops
// the output and the rest become cheap no-ops.
func (g *generator) guarded(line int, format string, args ...any) {
	fmt.Fprintf(&g.out, "\tif %s == nil {\n", varErr)
	g.at(line)
	fmt.Fprintf(&g.out, "\t\t"+format+"\n", args...)
	g.self()
	g.out.WriteString("\t}\n")
}

// write emits one guarded write of a value its position has already escaped.
//
// The value is passed as an argument rather than folded into the format, so a
// percent sign inside a string literal the view wrote stays one.
func (g *generator) write(line int, format string, args ...any) {
	g.guarded(line, "_, "+varErr+" = "+pkgIO+".WriteString("+varWriter+", "+format+")", args...)
}

// checked writes one interpolation whose value is examined before it reaches
// the page, and refuses it when the position will not hold it.
//
// The build knows the position and never the value, so this is the half that
// has to happen at render time. It is where an attribute name goes, and it is
// the one position with no escape whose values a page still writes: what goes
// there today is a name a helper returns, so refusing every value would refuse
// the pages that work. Refusing the value rather than repairing it is the
// choice everywhere else too -- rewriting would change what the page says
// without saying so.
//
// The refusal stops the render and names the view's own file and line. Dropping
// the value instead would leave a page missing an attribute and nothing
// anywhere saying which one or why.
//
// denied is the set of characters the position refuses, tested with one call
// against a string literal. The test is a set and not a function literal because
// the interpolation the person typed has to stay on the line the //line
// directive above it names, and gofmt breaks a function literal whose body grows
// past a size of its own -- which put the expression two lines below its
// directive and reported every type error inside it against the wrong line.
func (g *generator) checked(line int, expr, denied, reason string) {
	g.checks = true
	value := g.temp()
	fmt.Fprintf(&g.out, "\tif %s == nil {\n", varErr)
	g.at(line)
	fmt.Fprintf(&g.out, "\t\tif %s := %s.Text(%s); %s.ContainsAny(%s, %s) {\n",
		value, pkgView, expr, pkgStrings, value, strconv.Quote(denied))
	g.self()
	fmt.Fprintf(&g.out, "\t\t\t%s = %s.New(%s)\n", varErr, pkgErrors,
		strconv.Quote(fmt.Sprintf("%s:%d: %s", g.file.Path, line, reason)))
	g.out.WriteString("\t\t} else {\n")
	fmt.Fprintf(&g.out, "\t\t\t_, %s = %s.WriteString(%s, %s)\n", varErr, pkgIO, varWriter, value)
	g.out.WriteString("\t\t}\n\t}\n")
}

// refusable writes one interpolation whose escaper may refuse the value, and
// gives the refusal the view's own position.
//
// Two of the escapers answer with an error instead of a string, and it is the
// difference between the two kinds of mistake. A method interpolated without
// its parentheses is a mistake in the view and stops the render wherever it is
// read; a URL with a scheme a browser acts on, or a style value carrying a rule
// of its own, is data, and data that stopped the process would let any visitor
// take the page down with what they typed.
//
// The refused value comes back as the empty string, so what reaches the page is
// nothing rather than the value -- and the error says which view and which line
// asked for it, which is the whole of what somebody needs to act on it.
func (g *generator) refusable(line int, escaper, expr string) {
	g.wraps = true
	value := g.temp()
	fmt.Fprintf(&g.out, "\tif %s == nil {\n", varErr)
	fmt.Fprintf(&g.out, "\t\tvar %s string\n", value)
	g.at(line)
	fmt.Fprintf(&g.out, "\t\t%s, %s = %s(%s)\n", value, varErr, escaper, expr)
	g.self()
	// The position is a separate argument rather than part of the format, so a
	// path holding a percent sign stays a path.
	fmt.Fprintf(&g.out, "\t\tif %s != nil {\n\t\t\t%s = %s.Errorf(\"%%s: %%w\", %s, %s)\n\t\t} else {\n",
		varErr, varErr, pkgFmt, strconv.Quote(fmt.Sprintf("%s:%d", g.file.Path, line)), varErr)
	fmt.Fprintf(&g.out, "\t\t\t_, %s = %s.WriteString(%s, %s)\n", varErr, pkgIO, varWriter, value)
	g.out.WriteString("\t\t}\n\t}\n")
}

// refuse records that an interpolation landed where nothing can be written
// safely, whatever the value turns out to be.
//
// It is collected rather than returned so that a view with three of them is
// answered once. Nothing is emitted for the node: the build does not produce a
// file, so what the body would have held does not matter.
func (g *generator) refuse(line int, message, hint string) {
	g.errs = append(g.errs, &Error{Path: g.file.Path, Line: line, Message: message, Hint: hint})
}

// self hands the position back to the generated file.
//
// Without it a single interpolation would claim every line below it, and the
// compiler would report a mistake in this generator at a line of the view --
// possibly a line the view does not have.
func (g *generator) self() { g.out.WriteString(markerSelf + "\n") }

// emit writes the file: the body first, then the header above it.
//
// The order is backwards on purpose. The import block depends on what the walk
// over the nodes found -- a value written where an attribute name goes is
// examined at render time, and the check needs two imports a view without one
// must not carry, because an unused import is a compile error. Only the walk
// knows, so the walk runs first and the header is written over it.
//
// The line markers are resolved after the whole file is formatted, so building
// it out of order costs nothing there.
func (g *generator) emit() {
	g.emitBody()
	body := g.out.String()
	g.out.Reset()
	g.emitHeader()
	g.out.WriteString(body)
}

// emitHeader writes everything above the render function: the generated-file
// notice, the package clause, the imports and the @go blocks verbatim.
func (g *generator) emitHeader() {
	fmt.Fprintf(&g.out, "// Code generated by `aru view:build` from %s. DO NOT EDIT.\n",
		path.Base(g.file.Path))
	fmt.Fprintf(&g.out, "//\n// Edit the .kyse.go and run `aru view:build`. Changes here are overwritten.\n\n")
	fmt.Fprintf(&g.out, "package %s\n\n", g.file.Package)

	// Every import this generator needs is renamed on the way in, so that no
	// call it writes goes through a name a view could bind. The rename is what
	// makes the block below able to repeat a path the view also imports: two
	// names for one package is a file that compiles, and it is the same name
	// twice that Go refuses.
	g.out.WriteString("import (\n")
	fmt.Fprintf(&g.out, "\t%s \"html/template\"\n\t%s \"io\"\n", pkgTemplate, pkgIO)
	// errors carries the refusal of a value the position cannot accept, and
	// nothing else in a generated view returns an error of its own.
	if g.checks {
		fmt.Fprintf(&g.out, "\t%s \"errors\"\n", pkgErrors)
	}
	// fmt gives the refusal an escaper returns the view's own file and line
	// before it travels, and is written only by a view that has one.
	if g.wraps {
		fmt.Fprintf(&g.out, "\t%s \"fmt\"\n", pkgFmt)
	}
	// strings is needed by two shapes: a component builds its markup into one
	// and returns it, and a checked interpolation examines the value before
	// writing it. gofmt drops nothing, so it is added here rather than always.
	if g.checks || g.isComponent() {
		fmt.Fprintf(&g.out, "\t%s \"strings\"\n", pkgStrings)
	}
	fmt.Fprintf(&g.out, "\n\t%s %q\n", pkgView, nativeView)
	// And the packages the view calls by their plain names a second time, under
	// those names -- `view.URL` in a link, `view.Page` in the struct it draws,
	// `template.HTML` on the field of a component's props. Each is already above
	// under the reserved name, and Go accepts one path twice. Only a view whose
	// own source names one gets it; see republishedPackages.
	for _, name := range g.published {
		fmt.Fprintf(&g.out, "\t%s %q\n", name, republishedPackages[name].path)
	}
	// What the view imported for itself, after the ones that are always here.
	//
	// It is written out whatever the block above holds. A component whose @go
	// block uses strings.Fields writes `import "strings"` in its source, which is
	// the honest thing to write and is now the only way that package reaches the
	// view's own code -- the one this generator imports answers to a name the
	// view cannot spell. Dropping it as a duplicate left the @go block calling a
	// package the file no longer named.
	//
	// A path the view names twice in its own source is still dropped, which is
	// where that check earns its keep and the only place it is asked now.
	//
	// gofmt groups and sorts what survives, so the order written is not the order
	// emitted.
	var own strings.Builder
	for _, imp := range g.file.Imports {
		if alreadyImported(own.String(), imp) {
			continue
		}
		own.WriteString(imp)
		own.WriteString("\n")
		fmt.Fprintf(&g.out, "\t%s\n", imp)
	}
	g.out.WriteString(")\n\n")

	// The @go blocks, verbatim. This is where the data struct is declared.
	for _, block := range g.file.Go {
		// The blank line after the directive is not cosmetic: it stands for the
		// `@go` line itself, and it is what keeps the directive out of the doc
		// comment below.
		//
		// A `//line` that lands inside a doc comment does not stay where it was
		// written -- go/doc treats it as a directive and moves it to the END of
		// the comment, against the declaration. The block then claimed its own
		// first line for a type declared twelve lines further down, and every
		// error inside the block was reported that far off.
		if block.Line > 1 {
			g.at(block.Line - 1)
			g.out.WriteString("\n")
		}
		g.out.WriteString(block.Body)
		g.out.WriteString("\n")
		g.self()
		g.out.WriteString("\n")
	}
}

// emitBody writes the render function, or the exported function a component is.
func (g *generator) emitBody() {
	if g.isComponent() {
		g.emitComponent()
		return
	}

	fn := g.renderFuncName()
	isLayout := g.yields()

	// A layout is handed the sections the child view declared; a page is not.
	// Two different contracts, and so two registries -- the @yield in the source
	// is what decides which one this file is.
	if isLayout {
		fmt.Fprintf(&g.out, "func init() { %s.RegisterLayout(%s, %s) }\n\n", pkgView, strconv.Quote(g.name), fn)
		fmt.Fprintf(&g.out, "// %s renders the %s layout.\nfunc %s(%s %s.Writer, %s any, %s map[string]func(%s.Writer) error) error {\n",
			fn, g.name, fn, varWriter, pkgIO, varData, varSections, pkgIO)
	} else {
		fmt.Fprintf(&g.out, "func init() { %s.Register(%s, %s) }\n\n", pkgView, strconv.Quote(g.name), fn)
		fmt.Fprintf(&g.out, "// %s renders %s.\nfunc %s(%s %s.Writer, %s any) error {\n", fn, g.name, fn, varWriter, pkgIO, varData)
	}

	if g.dataType != "" {
		fmt.Fprintf(&g.out, "\t%s, %s := %s.(%s)\n", varPage, varOK, varData, g.renderType())
		fmt.Fprintf(&g.out, "\tif !%s {\n\t\treturn %s.WrongData(%s, %s, %s)\n\t}\n",
			varOK, pkgView, strconv.Quote(g.name), strconv.Quote(g.dataType), varData)
		g.emitViewData()
	} else {
		fmt.Fprintf(&g.out, "\t_ = %s\n", varData)
	}
	switch {
	case g.file.Extends != "":
		// A view that extends a layout renders the layout, handing it the
		// sections as a map the @yield reads. The layout is a view like any
		// other, so this is one render calling another.
		g.emitSections()
		fmt.Fprintf(&g.out, "\treturn %s.RenderInto(%s, %s, %s, %s)\n",
			pkgView, varWriter, strconv.Quote(g.file.Extends), varData, varSections)
	default:
		fmt.Fprintf(&g.out, "\tvar %s error\n", varErr)
		for _, n := range g.file.Body {
			g.node(n)
		}
		fmt.Fprintf(&g.out, "\treturn %s\n", varErr)
	}

	g.out.WriteString("}\n")
	g.silenceUnused()
}

// emitViewData declares the two names that stand for the page data.
//
// varPage is what an interpolation of a field becomes; viewData is the name a
// view may write for the same value, and every view that reads its data out
// long-hand writes it. The second is a copy of the first, and nothing this
// generator emits reads it -- so a loop that binds the same name takes the
// name away from the view and from nothing else, which is what shadowing means
// everywhere else in Go.
//
// Both are silenced, because either may go unread: a layout that is only
// @yield interpolates nothing.
func (g *generator) emitViewData() {
	fmt.Fprintf(&g.out, "\t_ = %s\n", varPage)
	fmt.Fprintf(&g.out, "\t%s := %s\n\t_ = %s\n", viewData, varPage, viewData)
}

// alreadyImported reports whether the import block written so far names the same
// path as this line.
//
// It compares the quoted path rather than the whole line, so `strings` and
// `s "strings"` are recognised as the same import.
//
// It is asked only about the view's own import lines, against each other. What
// a repeated path costs is the name, not the path: one package may be imported
// twice under two names and the file still compiles, and it is the second line
// that would claim a name already taken which does not. So this is a guard
// against a view naming one path twice, and not a reason to drop a line because
// the block above it happens to carry the same path under a name of its own.
func alreadyImported(written, line string) bool {
	open := strings.IndexByte(line, '"')
	if open < 0 {
		return false
	}
	close := strings.IndexByte(line[open+1:], '"')
	if close < 0 {
		return false
	}
	return strings.Contains(written, line[open:open+close+2])
}

// isComponent reports whether this view is a component rather than a page.
//
// The directory decides. A component is not a page: nothing renders it by name,
// a controller never hands it data, and it has no layout.
func (g *generator) isComponent() bool { return strings.HasPrefix(g.name, "components.") }

// emitComponent writes the component as an ordinary exported Go function.
//
// This is the difference between a component and a page, and it is the whole
// reason components are worth having:
//
//	{!! components.Button(components.ButtonProps{Label: "Save"}) !!}
//
// The name is resolved by the Go compiler, the props are checked by the Go
// compiler, and a field that does not exist stops the build. A component looked
// up by string and asserted at run time would have neither -- a typo in the name
// would reach production as a blank space, and wrong props as a 500. That is the
// failure this framework exists to make impossible, so a component cannot be the
// one place it is reintroduced.
//
// It returns template.HTML rather than writing to an io.Writer so it can be
// interpolated. The value is safe by construction: every {{ }} inside the
// component was escaped by this generator on the way in, which is what makes
// {!! !!} the right form here and a mistake anywhere a person's text is used.
// renderFuncName is the package-level function this file declares for the view.
//
// A page and a layout become an unexported render function the framework is
// handed; a component becomes the exported function another view calls by name.
// One accessor for both, because the name is asked for twice -- once to write
// the declaration, and once to keep a loop binding from taking it away.
func (g *generator) renderFuncName() string {
	if g.isComponent() {
		return componentFuncName(g.name)
	}
	return funcName(g.name)
}

func (g *generator) emitComponent() {
	fn := g.renderFuncName()

	fmt.Fprintf(&g.out, "// %s renders the %s component.\n",
		fn, strings.TrimPrefix(g.name, "components."))
	if g.dataType == "" {
		fmt.Fprintf(&g.out, "func %s() %s.HTML {\n", fn, pkgTemplate)
	} else {
		fmt.Fprintf(&g.out, "func %s(%s %s) %s.HTML {\n", fn, varProps, g.renderType(), pkgTemplate)
		fmt.Fprintf(&g.out, "\t%s := %s\n", varPage, varProps)
		g.emitViewData()
	}

	// A strings.Builder never fails a write, so the err every node threads
	// through is dead here -- and dropping the guards would mean a second code
	// path through the node emitter, which is how the two drift apart.
	fmt.Fprintf(&g.out, "\t%s := &%s.Builder{}\n", varWriter, pkgStrings)
	fmt.Fprintf(&g.out, "\tvar %s error\n", varErr)
	for _, n := range g.file.Body {
		g.node(n)
	}
	fmt.Fprintf(&g.out, "\t_ = %s\n", varErr)
	fmt.Fprintf(&g.out, "\treturn %s.HTML(%s.String())\n", pkgTemplate, varWriter)
	g.out.WriteString("}\n")
	g.silenceUnused()
}

// componentFuncName turns the view name into the exported Go function.
//
//	components.button       -> Button
//	components.theme-toggle -> ThemeToggle
func componentFuncName(name string) string {
	var b strings.Builder
	for _, part := range strings.FieldsFunc(strings.TrimPrefix(name, "components."), func(r rune) bool {
		return r == '/' || r == '-' || r == '_' || r == '.'
	}) {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(part[1:])
	}
	return b.String()
}

// emitSections builds the map a layout's @yield reads.
func (g *generator) emitSections() {
	fmt.Fprintf(&g.out, "\t%s := map[string]func(%s.Writer) error{\n", varSections, pkgIO)
	for _, s := range g.file.Sections {
		fmt.Fprintf(&g.out, "\t\t%s: func(%s %s.Writer) error {\n\t\t\tvar %s error\n",
			strconv.Quote(s.Name), varWriter, pkgIO, varErr)
		// A section is written into the layout where its @yield is, which is the
		// body of an element -- a @yield anywhere else is refused, so this is
		// the position a section begins in and not a guess. Carrying the
		// position from the section above would be reading one file's markup as
		// the continuation of another's.
		g.scan = htmlScanner{}
		for _, n := range s.Nodes {
			g.node(n)
		}
		// And the other half of the same contract. The layout goes on reading
		// the document after the @yield, which it can only do if the section
		// ended where it began: a section that leaves a tag open moves the
		// layout's position by whatever a page put in it, and the layout is
		// compiled without ever seeing which page that is.
		if g.scan.state != stateText {
			g.refuse(s.Line, fmt.Sprintf("@section('%s') leaves a tag open where it ends", s.Name),
				"the layout goes on reading the document after the @yield that writes this section, so a tag left open here decides which escape the layout's own values get -- in a file that cannot see this one.\n"+
					"    Close inside the section whatever it opens.")
		}
		fmt.Fprintf(&g.out, "\t\t\treturn %s\n\t\t},\n", varErr)
	}
	g.out.WriteString("\t}\n")
}

func (g *generator) node(n Node) {
	switch n.Kind {
	case Text:
		if n.Body == "" {
			return
		}
		fmt.Fprintf(&g.out, "\tif %s == nil { _, %s = %s.WriteString(%s, %s) }\n",
			varErr, varErr, pkgIO, varWriter, strconv.Quote(n.Body))
		// The markup this node writes is what moves the position of the next
		// one, so the scanner reads exactly what the page gets.
		g.scan.feed(n.Body)

	case Echo:
		g.echo(n)

	case Raw:
		// What the raw form writes is markup, and this generator cannot read it:
		// it is the return of a Go function. In the body of an element that
		// costs nothing, because a component returns markup that closes what it
		// opens. Anywhere else the position of everything after it would be a
		// guess, so there is none.
		g.opaque()
		// Text only converts the value to characters. Escaped interpolation is
		// the separate path that wraps Text in the position-specific escape, so
		// using Text here preserves the raw form without relying on the legacy
		// Framework bridge's UnsafeText alias.
		g.write(n.Line, pkgView+".Text(%s)", g.expr(n.Body))

	case Directive:
		g.directive(n)
	}
}

// echo writes one escaped interpolation, with the escape its position takes.
//
// There is no default escape and no way for a view to ask for a different one.
// The same six characters that make a value inert in the body of an element
// leave it able to end an attribute name; a space, which no escape touches,
// ends an unquoted value on its own; and a scheme is dangerous for what it
// means rather than for how it is written, so no escape reaches it at all.
// Which one a value needs is read off the markup around it, which is why a view
// writes {{ }} and says nothing about escaping.
//
// Five of the cases refuse rather than escape. Three are positions with no
// escape at all, and a fourth is not a position but the absence of one; all
// four are refused here, because no value would be safe in them. The fifth --
// where the name of an attribute goes -- is refused at render time instead,
// because a page writes a name there today and only the value decides.
func (g *generator) echo(n Node) {
	expr := g.expr(n.Body)

	switch g.scan.position() {
	case posAttributeName:
		g.checked(n.Line, expr, deniedInAttributeName, reasonAttributeName)

	case posAttributeValue:
		g.write(n.Line, pkgView+".TextAttr(%s)", expr)

	case posURL:
		g.refusable(n.Line, pkgView+".TextURL", expr)

	case posScript:
		g.write(n.Line, pkgView+".TextJS(%s)", expr)

	case posStyle:
		g.refusable(n.Line, pkgView+".TextCSS", expr)

	case posUnquotedValue:
		g.refuse(n.Line, "this value is written into an attribute value that has no quotes around it",
			"an unquoted value ends at the first space, and no escape puts a space back inside it -- what follows the space is markup of its own.\n"+
				"    Put the value in quotes.")

	case posCode:
		g.refuse(n.Line, fmt.Sprintf("this value is written into %q, whose value is a script rather than text", g.scan.attr),
			"a character reference in an attribute is decoded before the script is read, so an escaped quote arrives as the quote it started as and closes the string it was meant to sit in.\n"+
				"    Write the value into an ordinary attribute and have the script read it from there.")

	case posElementName:
		g.refuse(n.Line, "this value is written where the name of an element goes",
			"an element name carries no character references, so a character that ends the name cannot be written as anything else.\n"+
				"    Write the elements out and choose between them with @if.")

	case posUnknown:
		g.refuse(n.Line, "where this value lands in the document is not known here",
			"the markup before it left the position open: a block that opens a tag in one branch and closes it in another, or a {!! !!}, an @include or a @yield written inside a tag.\n"+
				"    Open and close a tag in the same block, and keep those three out of a tag.")

	default:
		// The body of an element, and the escape every template engine applies
		// there. It is not optional: a view that interpolates without escaping
		// is the XSS this framework refuses to make easy.
		g.write(n.Line, pkgTemplate+".HTMLEscapeString("+pkgView+".Text(%s))", expr)
	}

	// Inside an attribute value the value written here is part of it from now
	// on, so a second interpolation beside it is no longer at the front -- and
	// the front is the only place a scheme can be introduced.
	switch g.scan.state {
	case stateAttrValueDouble, stateAttrValueSingle, stateAttrValueUnquoted:
		g.scan.valueBegun = true
	}
}

// opaque records markup this file writes and cannot read.
//
// A component returns markup, a partial is another file and a section comes
// from the view that extends this one. In the body of an element that costs
// nothing, because each of them closes what it opens. Inside a tag the position
// of everything after it would be a guess.
func (g *generator) opaque() {
	if g.scan.state != stateText {
		g.scan = htmlScanner{state: stateLost}
	}
}

// callsOut is opaque for a directive, which is refused rather than followed
// when it is written inside a tag.
//
// The difference from an interpolation is which file pays. What a @yield writes
// is a section of a view this one is compiled without ever seeing, so a @yield
// inside a tag moves that view's position by markup neither file can read --
// and the view whose escapes come out wrong is the other one, which contains no
// mistake and shows nothing on being read. Refusing here is what lets a section
// be compiled as what it looks like: markup that begins in the body of an
// element.
func (g *generator) callsOut(n Node) {
	if g.scan.state == stateText {
		return
	}
	g.refuse(n.Line, fmt.Sprintf("@%s is written inside a tag", n.Name),
		"what it writes is markup this compiler does not read, so where the document goes from here -- and which escape every value after it needs -- would be a guess.\n"+
			"    Write it between tags rather than inside one.")
	g.scan = htmlScanner{state: stateLost}
}

func (g *generator) directive(n Node) {
	switch n.Name {
	case "if":
		g.at(n.Line)
		fmt.Fprintf(&g.out, "\tif %s {\n", g.cond(n.Body))
		g.self()
		opened := g.scan
		// @else and @elseif arrive as children, because they are inline
		// directives inside the block the parser opened. Each one closes the
		// branch in progress and opens the next.
		//
		// Without this they had no case at all and were dropped in silence:
		// @if(x) A @else B @endif emitted `if x { A; B }`, so both halves
		// appeared when the condition held and neither when it did not.
		for _, c := range n.Children {
			if c.Kind == Directive && (c.Name == "else" || c.Name == "elseif") {
				// The branch that ends here ran or did not, so the branch after
				// it starts where this one started.
				g.rejoin(opened)
				if c.Name == "else" {
					g.out.WriteString("\t} else {\n")
				} else {
					g.at(c.Line)
					fmt.Fprintf(&g.out, "\t} else if %s {\n", g.cond(c.Body))
					g.self()
				}
				continue
			}
			g.node(c)
		}
		g.rejoin(opened)
		g.out.WriteString("\t}\n")

	case "else", "elseif":
		// Reached only outside an @if, where the branch above never sees it.
		// Emitting nothing is what hid the bug for as long as it lived, so this
		// writes Go that does not compile, naming the file and the fix.
		fmt.Fprintf(&g.out, "\t#error @%s outside @if in %s: every @%s needs an @if above it and an @endif below\n",
			n.Name, path.Base(g.file.Path), n.Name)

	case "foreach":
		// @foreach(.Items as item) -> for _, item := range the page's Items
		//
		// A view that binds nothing gets a name it cannot write, because a name
		// it could write is one a nested @foreach would take from it in
		// silence.
		subject, binding := splitAs(n.Body)
		if binding == "" {
			binding = g.temp()
		}
		g.at(n.Line)
		fmt.Fprintf(&g.out, "\tfor _, %s := range %s {\n\t\t_ = %s\n", binding, g.expr(subject), binding)
		g.self()
		opened := g.scan
		for _, c := range n.Children {
			g.node(c)
		}
		g.rejoin(opened)
		g.out.WriteString("\t}\n")

	case "for":
		// @for(i := 0; i < len(d.Items); i++) -> the same three clauses, in Go.
		//
		// The familiar form is @for(i = 0; i < 10; i++), and this is the same shape
		// with Go's syntax, exactly as @if already takes a Go condition rather
		// than inventing a second expression language.
		g.at(n.Line)
		fmt.Fprintf(&g.out, "\tfor %s {\n", g.clauses(n.Body))
		g.self()
		opened := g.scan
		for _, c := range n.Children {
			g.node(c)
		}
		g.rejoin(opened)
		g.out.WriteString("\t}\n")

	case "while":
		// Go has one loop keyword, so @while(cond) is `for cond`.
		g.at(n.Line)
		fmt.Fprintf(&g.out, "\tfor %s {\n", g.cond(n.Body))
		g.self()
		opened := g.scan
		for _, c := range n.Children {
			g.node(c)
		}
		g.rejoin(opened)
		g.out.WriteString("\t}\n")

	case "forelse":
		// @forelse(.Items as it) … @empty … @endforelse
		//
		// The one directive of the set that earns its keep in every
		// generated index page: a list and its empty state are one thought, and
		// writing them as @foreach next to @if(len(...) == 0) states the
		// condition twice, where the two can drift apart.
		//
		// The @empty half arrives as a child, the same shape @else does.
		subject, binding := splitAs(n.Body)
		if binding == "" {
			binding = g.temp()
		}
		before, after := splitAt(n.Children, "empty")
		g.at(n.Line)
		fmt.Fprintf(&g.out, "\tif len(%s) == 0 {\n", g.expr(subject))
		g.self()
		opened := g.scan
		for _, c := range after {
			g.node(c)
		}
		g.rejoin(opened)
		g.out.WriteString("\t} else {\n")
		g.at(n.Line)
		// The binding and the line that silences it are both the view's, so
		// the marker closes after them: a `_ =` on a name the view chose,
		// attributed to the generated file, would read as the generator
		// reaching into the view's scope.
		fmt.Fprintf(&g.out, "\t\tfor _, %s := range %s {\n\t\t\t_ = %s\n", binding, g.expr(subject), binding)
		g.self()
		for _, c := range before {
			g.node(c)
		}
		g.rejoin(opened)
		g.out.WriteString("\t\t}\n\t}\n")

	case "continue", "break":
		// Only meaningful inside a loop, and the Go compiler says so at the line
		// the view wrote, which is better than this generator guessing.
		g.at(n.Line)
		fmt.Fprintf(&g.out, "\t%s\n", n.Name)
		g.self()

	case "empty":
		// Reached only outside @forelse, where the branch above never sees it.
		fmt.Fprintf(&g.out, "\t#error @empty outside @forelse in %s: it separates the two halves of a @forelse\n",
			path.Base(g.file.Path))

	case "yield":
		// Only meaningful inside a layout: it renders the section the child view
		// declared, or nothing when it declared none.
		g.callsOut(n)
		fmt.Fprintf(&g.out, "\tif %s == nil { %s = %s.Yield(%s, %s, %s) }\n",
			varErr, varErr, pkgView, varWriter, varSections, strconv.Quote(unquote(n.Body)))

	case "include":
		// A partial shares the page's data, and that is all @include does.
		//
		// A second argument, letting a partial be handed data of its own, would
		// make two ways to draw a component -- this one, resolved by string at
		// run time, and the typed function a component compiles to -- and the
		// string one is the worse of the two by exactly the measure this project
		// is built on: a typo in the name reaches production. The
		// compiler-checked one is the one that stays.
		g.callsOut(n)
		fmt.Fprintf(&g.out, "\tif %s == nil { %s = %s.Include(%s, %s, %s) }\n",
			varErr, varErr, pkgView, varWriter, strconv.Quote(unquote(n.Body)), varData)

	case "csrf":
		g.callsOut(n)
		fmt.Fprintf(&g.out, "\tif %s == nil { %s = %s.CSRF(%s, %s) }\n", varErr, varErr, pkgView, varWriter, varData)

	case "attributes":
		g.attributes(n)
	}
}

// attributes writes a set of attributes a caller handed a component, inside the
// tag the component is drawing.
//
// # Why this is a directive and not an interpolation
//
// Every escape in this file is chosen by the position the value lands in, and
// the position is read off the markup around it. That works because the view
// wrote the markup: the attribute a value sits in is there, in the source, to be
// read. A set of attributes has no attribute around it -- the names arrive with
// the values -- so there is no position to read and nothing for the table of
// ten to answer with.
//
// The escape moves with the name instead, into view.Attributes, which refuses
// every name whose value a browser or a library would act on and escapes the
// rest. That is the whole reason this is one call and not a loop: a view can
// already write
//
//	@for(at := 0; at < .AttrCount(); at++)
//		{{ .AttrName(at) }}="{{ .AttrValue(at) }}"
//	@endfor
//
// and it compiles today -- the body is balanced, so the position survives it.
// What it loses is the name: the scanner records the attribute a value sits in,
// and there the name was written by the loop rather than by the view, so the
// field is empty and attributeHoldsURL and attributeHoldsCode never fire. Every
// value is escaped as if the attribute were inert, and a caller writing
// href with a value somebody typed gets javascript: onto the page. Repeated at
// every element of every component, with nothing at build time able to tell
// which of them went outside the pattern.
//
// # Why the position is kept rather than given up
//
// A component's markup is opaque to this compiler, and opaque markup inside a
// tag gives up the position for the rest of the file -- see opaque. This does
// not, because what it writes is known: view.Attributes answers a run of
// complete attributes and nothing else, which leaves the tokenizer exactly where
// it was, between names. settled is that state, and it is the one @if(.Disabled)
// disabled @endif already relies on.
func (g *generator) attributes(n Node) {
	if g.scan.position() != posAttributeName {
		g.refuse(n.Line, "@attributes is written where the name of an attribute does not go",
			"it writes attributes, so it belongs inside a tag, between the names -- not in the body of an element, not inside a value, and not after markup that left the position open.\n"+
				"    Write it in the opening tag, beside the attributes the component writes itself.")
		return
	}
	g.refusable(n.Line, pkgView+".Attributes", g.expr(n.Body))
	g.scan = g.scan.settled()
}

// rejoin puts the position back where a block began, and gives it up when the
// block did not leave it there.
//
// A branch runs or does not, and a loop body runs any number of times, so the
// position after a block is only known when the block ended where it started --
// which balanced markup always does, since a tag that opens inside a block
// closes inside it. Markup that does not balance makes the position of
// everything after the block depend on data, and there is no answer to give at
// build time.
//
// Giving up refuses every interpolation from there on, at build time. Choosing
// an escape for a position nobody knows is choosing one that is wrong for some
// of the data, which is the failure this whole table exists to remove -- and it
// is the one failure that is silent, because the page renders either way.
func (g *generator) rejoin(opened htmlScanner) {
	settled := opened.settled()
	if g.scan.settled() != settled {
		g.scan = htmlScanner{state: stateLost}
		return
	}
	g.scan = settled
}

// htmlPosition is the place in the document that an interpolation writes to.
//
// Which escape a value needs is decided by the position and not by the value:
// the six characters that make a value inert in the body of an element leave it
// able to end an attribute name, and a space -- which no escape touches --
// ends one on its own.
//
// The set is closed at ten: five positions with an escape of their own, four
// with none at all, and one that is not a position but the absence of one. An
// eleventh would mean a sixth escape, and a position answered with the escape
// of a different one is the hole this exists to remove.
//
// @attributes is not an eleventh, and the difference is worth stating because
// it looks like one. It writes into posAttributeName and is refused anywhere
// else, so it adds no position. What it adds is a value whose *name* is data,
// and a name that is data has no position to read -- which is why the check
// moved with it, into view.Attributes, rather than being another row here.
type htmlPosition int

const (
	// posBody is the body of an element, where the escape is the one every
	// template engine applies.
	posBody htmlPosition = iota
	// posAttributeValue is an attribute value between quotes. The quotes bound
	// the value, so the escape only has to keep it from ending them.
	//
	// An attribute whose value is JSON the view wrote by hand is this position
	// and is not answered by it: the HTML parser decodes the escaped quote back
	// into a quote before whatever reads the JSON ever sees it. Closing that
	// one means deciding how a view writes such an attribute, which is a
	// question about the view rather than about the escape.
	posAttributeValue
	// posURL is the front of an attribute a browser resolves as an address.
	// What decides safety there is the scheme, which is dangerous for what it
	// means rather than for how it is written, so no escape reaches it.
	posURL
	// posScript is the body of a script element, where the value has to arrive
	// as a literal rather than as source.
	posScript
	// posStyle is a style attribute, or the body of a style element.
	posStyle
	// posAttributeName is where the name of an attribute goes. An attribute
	// name carries no character references, so a character that ends the name
	// cannot be written as anything else.
	posAttributeName
	// posUnquotedValue is an attribute value with no quotes around it, which
	// ends at the first space -- and a space is not one of the six.
	posUnquotedValue
	// posCode is an attribute whose value is a script rather than text. The
	// HTML parser decodes a character reference there before the script engine
	// reads it, so escaping hands the character back.
	posCode
	// posElementName is where the name of an element goes, which carries no
	// character references either.
	posElementName
	// posUnknown is the position no longer being known, which is the absence of
	// a position rather than one of them. It is refused for the same reason the
	// four above are: an escape chosen for a position nobody knows is one that
	// is wrong for some of the data, and the page renders either way.
	posUnknown
)

// controlCharacters is U+0000 through U+001F.
//
// No position in a document accepts one: the HTML parser rewrites a NUL wherever
// it finds it, and JSON refuses every one of them inside a string. They are
// refused with the character the position names rather than apart from it,
// because a value carrying one is a value nobody meant to write.
var controlCharacters = func() string {
	var b strings.Builder
	for c := byte(0); c < ' '; c++ {
		b.WriteByte(c)
	}
	return b.String()
}()

// The characters each position refuses. Every set is closed and comes from the
// syntax of the position -- not from a list of characters seen in an attack,
// which is a list that is always one entry short.
var (
	// An attribute name ends at whitespace, at `/`, at `>` and at `=`, and the
	// syntax forbids a quote, `<` and a backtick inside one.
	deniedInAttributeName = controlCharacters + " \"'/<=>`"
)

// The reasons a refusal gives, which are what somebody reads when a page stops.
const (
	reasonAttributeName = "the value interpolated where an attribute name goes holds a character that would end the name and begin markup of its own, and an attribute name carries no entities, so there is no escape that would keep it one name"
)

// scanState is the state of the HTML tokenizer, cut to what deciding a position
// needs.
//
// It is the tokenizer of the HTML syntax and not a search for angle brackets,
// because the difference between the two is every place a bracket is text: a
// comment, the body of a script, and a value inside quotes.
type scanState int

const (
	stateText scanState = iota
	stateTagOpen
	stateTagName
	stateBeforeAttrName
	stateAttrName
	stateAfterAttrName
	stateBeforeAttrValue
	stateAttrValueDouble
	stateAttrValueSingle
	stateAttrValueUnquoted
	stateComment
	stateMarkupDecl
	stateRawText
	// stateLost is the position no longer being known. Every interpolation from
	// there on takes the escape of the body of a document, which is what the
	// generator did everywhere before it could tell positions apart.
	stateLost
)

// htmlScanner follows the markup a view writes and answers where the next value
// lands.
//
// Every field is comparable on purpose: a block's markup is balanced when the
// scanner comes out of it equal to what went in, and that is the whole test
// rejoin makes.
type htmlScanner struct {
	state scanState
	// tag is the name of the element whose tag is being read, lowercased. It is
	// what decides whether the text after the tag is markup or not.
	tag string
	// attr is the name of the attribute being read, lowercased.
	attr string
	// raw is the element whose text is not markup, while inside one.
	raw string
	// closing records that the tag being read is an end tag, which opens no
	// element.
	closing bool
	// valueBegun records that the attribute value being read already holds a
	// character. It separates the front of a value from the rest of it, which
	// is the whole of the difference between a URL whose scheme is still open
	// and one the view has already settled.
	//
	// Leading space does not begin a value: a browser strips it off an address
	// before resolving it, so a scheme can still arrive after it.
	valueBegun bool
}

// settled is the scanner as the next delimiter inside a tag would leave it.
//
// It exists for one shape, and the shape is the common one: a branch that writes
// a bare attribute -- `@if(.Disabled) disabled @endif` between two attributes --
// ends with a name half read, while the branch beside it ends between two names.
// The two are the same position and converge on the next byte either way, so
// asking rejoin to see them apart would give up the position for the rest of the
// file every time a component writes a boolean attribute.
//
// It only ever merges the positions of a name with each other. Nothing here can
// turn the inside of a value into a name, which is the direction that would cost
// a page that renders today.
func (s htmlScanner) settled() htmlScanner {
	switch s.state {
	case stateAttrName, stateAfterAttrName:
		s.state, s.attr = stateBeforeAttrName, ""
	}
	return s
}

// position answers where an interpolation written now would land.
func (s *htmlScanner) position() htmlPosition {
	switch s.state {
	case stateLost:
		return posUnknown

	case stateTagOpen, stateTagName:
		return posElementName

	case stateBeforeAttrName, stateAttrName, stateAfterAttrName:
		return posAttributeName

	// Before the value is where the quotes would be, so a value written there
	// has none: what follows an `=` and is not a quote is an unquoted value.
	case stateBeforeAttrValue, stateAttrValueUnquoted:
		return posUnquotedValue

	case stateAttrValueDouble, stateAttrValueSingle:
		switch {
		case attributeHoldsCode(s.attr):
			return posCode
		case s.attr == "style":
			return posStyle
		// Only at the front. Once the view has written a character of its own
		// into the value, the scheme is settled by what the view wrote, and a
		// colon further along belongs to a path or a query -- where refusing it
		// would refuse a search for `time: 10am`.
		case attributeHoldsURL(s.attr) && !s.valueBegun:
			return posURL
		}
		return posAttributeValue

	case stateRawText:
		if s.raw == "style" {
			return posStyle
		}
		return posScript
	}
	return posBody
}

// attributeHoldsURL reports whether a browser resolves this attribute's value
// as an address and acts on it.
//
// The list is of the attributes that are a single URL. srcset is not one: it
// holds several with a descriptor after each, and checking the scheme of the
// first would report a protection over the rest that is not there.
//
// The comparison is exact, so `data` is the address an object element loads and
// `data-id` is an ordinary attribute that happens to start with the same four
// letters.
func attributeHoldsURL(name string) bool {
	switch name {
	case "action", "background", "cite", "data", "formaction", "href",
		"manifest", "ping", "poster", "src", "xlink:href":
		return true
	// The verbs that make a request. They are the reason the escape cannot be
	// chosen by the runtime alone: to the HTML parser these are attributes like
	// any other, and only the compiler knows the value is fetched.
	case "hx-delete", "hx-get", "hx-patch", "hx-post", "hx-put":
		return true
	}
	return false
}

// attributeHoldsCode reports whether this attribute's value is a script rather
// than text.
//
// They are grouped by prefix rather than listed, because each family is open on
// purpose: an event handler exists for every event, and the two client
// libraries name theirs after whatever event the page listens for. A list would
// be one name short of every page that used a new one.
func attributeHoldsCode(name string) bool {
	switch {
	// Every HTML attribute beginning with `on` is an event handler.
	case strings.HasPrefix(name, "on"):
		return true
	// The same, in the two forms the client libraries spell it.
	case strings.HasPrefix(name, "hx-on"), strings.HasPrefix(name, "x-on:"), strings.HasPrefix(name, "@"):
		return true
	// A binding, whose value is the expression whose result becomes the
	// attribute. The bare colon is the shorthand for the long form.
	case strings.HasPrefix(name, "x-bind:"), strings.HasPrefix(name, ":"):
		return true
	}
	// The rest of the directives whose value is an expression. This family is
	// closed, which is why it is written out.
	switch name {
	case "x-data", "x-effect", "x-for", "x-html", "x-if", "x-init",
		"x-model", "x-modelable", "x-show", "x-text":
		return true
	}
	return false
}

// feed reads the markup a text node writes.
//
// The bytes are read one at a time and a byte that ends a state is read again in
// the next one, which is how the HTML syntax itself is defined and what keeps
// `<<div>` and `a < b` from being read as tags.
func (s *htmlScanner) feed(text string) {
	for i := 0; i < len(text); i++ {
		c := text[i]

		switch s.state {
		case stateLost:
			return

		case stateText:
			if c == '<' {
				s.state = stateTagOpen
			}

		case stateTagOpen:
			switch {
			case c == '!':
				// A comment ends at its own delimiter, and everything else that
				// opens with a bang -- a doctype -- ends at the first `>`.
				if strings.HasPrefix(text[i:], "!--") {
					s.state = stateComment
					i += 2
					break
				}
				s.state = stateMarkupDecl
			case c == '/':
				s.closing, s.tag = true, ""
				s.state = stateTagName
			case isASCIILetter(c):
				s.closing, s.tag = false, string(lowerASCII(c))
				s.state = stateTagName
			default:
				// Not a tag. The `<` was text, and so is this byte.
				s.state = stateText
				i--
			}

		case stateTagName:
			switch {
			case isHTMLSpace(c):
				s.state = stateBeforeAttrName
			case c == '/':
				s.state = stateBeforeAttrName
			case c == '>':
				s.closeTag()
			default:
				s.tag += string(lowerASCII(c))
			}

		case stateBeforeAttrName:
			switch {
			case isHTMLSpace(c), c == '/':
				// Still before the name.
			case c == '>':
				s.closeTag()
			default:
				s.attr = ""
				s.state = stateAttrName
				i--
			}

		case stateAttrName:
			switch {
			case isHTMLSpace(c):
				s.state = stateAfterAttrName
			case c == '=':
				s.state = stateBeforeAttrValue
			case c == '>':
				s.closeTag()
			case c == '/':
				s.state = stateBeforeAttrName
			default:
				s.attr += string(lowerASCII(c))
			}

		case stateAfterAttrName:
			switch {
			case isHTMLSpace(c):
				// Still after the name, and a `=` may yet arrive.
			case c == '=':
				s.state = stateBeforeAttrValue
			case c == '>':
				s.closeTag()
			case c == '/':
				s.state = stateBeforeAttrName
			default:
				// A name with no value, followed by the next name.
				s.attr = ""
				s.state = stateAttrName
				i--
			}

		case stateBeforeAttrValue:
			switch {
			case isHTMLSpace(c):
				// Still before the value.
			case c == '"':
				s.state, s.valueBegun = stateAttrValueDouble, false
			case c == '\'':
				s.state, s.valueBegun = stateAttrValueSingle, false
			case c == '>':
				s.closeTag()
			default:
				s.state, s.valueBegun = stateAttrValueUnquoted, false
				i--
			}

		case stateAttrValueDouble:
			switch {
			case c == '"':
				s.attr, s.valueBegun = "", false
				s.state = stateBeforeAttrName
			case !isHTMLSpace(c):
				s.valueBegun = true
			}

		case stateAttrValueSingle:
			switch {
			case c == '\'':
				s.attr, s.valueBegun = "", false
				s.state = stateBeforeAttrName
			case !isHTMLSpace(c):
				s.valueBegun = true
			}

		case stateAttrValueUnquoted:
			switch {
			case isHTMLSpace(c):
				s.attr, s.valueBegun = "", false
				s.state = stateBeforeAttrName
			case c == '>':
				s.closeTag()
			default:
				s.valueBegun = true
			}

		case stateComment:
			if strings.HasPrefix(text[i:], "-->") {
				i += 2
				s.state = stateText
			}

		case stateMarkupDecl:
			if c == '>' {
				s.state = stateText
			}

		case stateRawText:
			// Inside a script or a style nothing is markup until the end tag of
			// that same element, which is why a bracket in a comparison there
			// does not open a tag.
			if c == '<' && strings.HasPrefix(strings.ToLower(text[i:]), "</"+s.raw) {
				s.state = stateTagOpen
			}
		}
	}
}

// closeTag ends the tag being read and says what the text after it is.
//
// Everything the tag was carrying is cleared, and that is not tidiness: rejoin
// compares two scanners to ask whether a block's markup balanced, so a name left
// over from a tag that is already closed would answer the question with the
// element a branch happened to end on.
func (s *htmlScanner) closeTag() {
	s.state, s.raw = stateText, ""
	// The text inside a script or a style is not markup, so a bracket in it does
	// not open a tag. An end tag opens no element, so it never starts one.
	if !s.closing && (s.tag == "script" || s.tag == "style") {
		s.raw, s.state = s.tag, stateRawText
	}
	s.closing, s.tag, s.attr, s.valueBegun = false, "", "", false
}

func isASCIILetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// isHTMLSpace reports whether a byte separates the parts of a tag. It is the
// five the HTML syntax names, and a vertical tab is not one of them.
func isHTMLSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

func lowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

// dataReference finds the first expression that reads a field of the page data.
//
// `{{ .Name }}` compiles to a field read on the page data, which is declared
// only when the view has a data type -- from its own `@go` block, or inherited
// from the layout it extends. Without one the generator emitted the field read
// against a name it never declared, which parses; so `format.Source` accepted
// it, the command reported success, and the failure arrived later as an
// undefined name in a file whose header says DO NOT EDIT. Naming the `.kyse.go`
// and the line is the whole reason this compiler exists rather than a template
// library.
func dataReference(f *File, g *generator) (expr string, line int, found bool) {
	var walk func([]Node) bool
	walk = func(nodes []Node) bool {
		for _, n := range nodes {
			for _, e := range g.translated(n) {
				// The question is put to the one function that answers it. A
				// leading dot is the ordinary shape and a prefix test found it,
				// but a dot is a dot wherever it is written -- `f(A{.})` reads
				// the page data as surely as `.Title` does, and the prefix test
				// said no. The generated file then named a variable it never
				// declared, which parses; gofmt accepted it, `aru view:build`
				// reported the view compiled and exited 0.
				if namesIdent(e.gone, varPage) {
					expr, line, found = e.written, n.Line, true
					return true
				}
			}
			if walk(n.Children) {
				return true
			}
		}
		return false
	}

	if walk(f.Body) {
		return expr, line, true
	}
	// A view that extends a layout keeps everything in sections and leaves its
	// body empty, so stopping at the body would miss every page written the
	// ordinary way.
	for _, s := range f.Sections {
		if walk(s.Nodes) {
			return expr, line, true
		}
	}
	return "", 0, false
}

// translation is one fragment of a view, as the person wrote it and as the
// generated file will carry it.
type translation struct {
	written string
	gone    string
}

// translated is every fragment of Go a node contributes to the output.
//
// It is goExpressions plus the header of an @for, and the second half is the
// reason it exists. goExpressions drops an @for clause that is not an
// expression on its own -- which is right for the check it serves, since a
// statement is not asked to parse as an expression -- but the generator emits
// that clause all the same. `@for(.;;)` reached the output as the page data in
// a view that had declared none, and the file named a variable nothing
// declares.
func (g *generator) translated(n Node) []translation {
	var out []translation
	for _, e := range goExpressions(n) {
		e = strings.TrimSpace(e)
		out = append(out, translation{written: e, gone: g.expr(e)})
	}
	if n.Kind == Directive && n.Name == "for" {
		out = append(out, translation{written: n.Body, gone: g.clauses(n.Body)})
	}
	return out
}

// namesIdent reports whether the translated Go reads this identifier, as a
// whole word rather than as the start of a longer one.
//
// `kyse__d` is a prefix of `kyse__data`, and a substring test would read the
// second as the first.
func namesIdent(translated, name string) bool {
	for at := 0; ; {
		i := strings.Index(translated[at:], name)
		if i < 0 {
			return false
		}
		i += at
		before := i == 0 || !(isIdentByte(translated[i-1]) || isDigit(translated[i-1]))
		end := i + len(name)
		after := end >= len(translated) || !(isIdentByte(translated[end]) || isDigit(translated[end]))
		if before && after {
			return true
		}
		at = i + len(name)
	}
}

// goExpressions are the parts of a node that reach expr and become Go.
func goExpressions(n Node) []string {
	switch n.Kind {
	case Echo, Raw:
		return []string{n.Body}
	case Directive:
		switch n.Name {
		case "if", "elseif":
			return []string{n.Body}
		case "foreach", "forelse":
			// Only the subject. The binding is a variable name rather than an
			// expression, and checking it here would ask the wrong question: `0`
			// parses as an expression and is still not a name.
			subject, _ := splitAs(n.Body)
			return []string{subject}
		case "for":
			parts := strings.Split(strings.TrimSpace(n.Body), ";")
			if len(parts) == 3 {
				// The condition (middle) is an expression. The init and post
				// are simple statements that may or may not be expressions;
				// only validate the ones that pass ParseExpr.
				var out []string
				if s := strings.TrimSpace(parts[1]); s != "" {
					out = append(out, s)
				}
				if s := strings.TrimSpace(parts[0]); s != "" {
					if _, err := goparser.ParseExpr(s); err == nil {
						out = append(out, s)
					}
				}
				if s := strings.TrimSpace(parts[2]); s != "" {
					if _, err := goparser.ParseExpr(s); err == nil {
						out = append(out, s)
					}
				}
				return out
			}
			// A part header that does not have exactly three parts is passed
			// through unchanged, so the full body must be valid Go.
			if s := strings.TrimSpace(n.Body); s != "" {
				return []string{s}
			}
			return nil
		case "while":
			return []string{n.Body}
		}
	}
	return nil
}

// validGoIdent reports whether s is a valid Go identifier.
//
// A view's @foreach binding must be a Go identifier, because the generator
// writes it as a local variable name. Bindings like "000" would produce Go
// that does not parse.
func validGoIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_') {
				return false
			}
		} else {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') {
				return false
			}
		}
	}
	return !token.Lookup(s).IsKeyword()
}

// goUniverse are the names Go itself declares, and what each one is there.
//
// They are not this generator's to rename and cannot be given a prefix: `nil`
// is written by the guard in front of every write the generated code makes,
// `len` counts the subject of a @forelse, and `string` is the type a temporary
// is declared with. A loop binding shadows whichever one it spells for the
// whole of the loop body, so the guard that reads `nil` reads the row instead,
// and the file stops compiling for a reason the view's author cannot see.
//
// Reserving the compiler's own names is what the prefix is for, and it reaches
// only as far as the names the compiler chose. This is the other set: the ones
// the language chose. Between them they are closed, which is what leaves every
// remaining name -- a single letter, a common word, the name of a package --
// free for a view to bind.
//
// The list is the universe block of the language specification and not the
// subset some particular output happens to write today. A name added to the
// generated code tomorrow is covered already; a name guarded one at a time as
// somebody trips over it is a list that is always one failure behind.
//
// Keywords are absent on purpose: `range` and `func` are not identifiers at
// all, and validGoIdent already refuses them below, with the message for a
// binding that is not a name.
var goUniverse = map[string]string{
	// Types.
	"any":        "a type",
	"bool":       "a type",
	"byte":       "a type",
	"comparable": "a type",
	"complex64":  "a type",
	"complex128": "a type",
	"error":      "a type",
	"float32":    "a type",
	"float64":    "a type",
	"int":        "a type",
	"int8":       "a type",
	"int16":      "a type",
	"int32":      "a type",
	"int64":      "a type",
	"rune":       "a type",
	"string":     "a type",
	"uint":       "a type",
	"uint8":      "a type",
	"uint16":     "a type",
	"uint32":     "a type",
	"uint64":     "a type",
	"uintptr":    "a type",

	// Constants.
	"true":  "a constant",
	"false": "a constant",
	"iota":  "a constant",

	// The zero value.
	"nil": "the zero value",

	// Builtin functions.
	"append":  "a builtin function",
	"cap":     "a builtin function",
	"clear":   "a builtin function",
	"close":   "a builtin function",
	"complex": "a builtin function",
	"copy":    "a builtin function",
	"delete":  "a builtin function",
	"imag":    "a builtin function",
	"len":     "a builtin function",
	"make":    "a builtin function",
	"max":     "a builtin function",
	"min":     "a builtin function",
	"new":     "a builtin function",
	"panic":   "a builtin function",
	"print":   "a builtin function",
	"println": "a builtin function",
	"real":    "a builtin function",
	"recover": "a builtin function",
}

// loopBinding checks the variable a loop binds, which is a name and not an
// expression.
//
// The generator writes it out as a local variable -- `for _, it := range …` --
// so anything Go does not accept as an identifier produces a file that does not
// parse, reported against generated output whose header says not to edit it.
// Validating it as an expression is the wrong question and used to let `0`
// through, because a literal is an expression and is still not a name.
//
// An empty binding is not a mistake: @foreach(.Items) is written that way, and
// the generator names the variable itself.
//
// One namespace is closed to it. The generated file writes its writer, its
// data, its error, its temporaries and the names it calls its imports by under
// a prefix of their own, and a binding that started with the same prefix would
// be the one name a view can take away from them. Every other name is free -- a
// single letter, a package name, whichever it is.
//
// A second namespace is closed to it, and it is Go's rather than this
// compiler's: the names of the universe block, listed in goUniverse. Those
// cannot be moved out of the way the way the compiler's own were, because the
// generated code writes them as the language writes them, and a binding that
// spells one changes what that code means.
//
// Two names are closed to it in one file only, and both belong to the view.
// A view that calls a package by its plain name has that name bound in the Go
// compiled from it, so a loop that binds the same name in the same file renames
// the package over its whole body; and fn is the function the file declares for
// this view, which a call written in the view reaches by name. Those collisions
// are the view's own, between two things it wrote, and they are reported rather
// than resolved: silently preferring either side would make the other fail
// somewhere the person did not look. published is empty for every view that
// does not call such a package, which is what leaves the name free there.
func loopBinding(f *File, n Node, published map[string]bool, fn string) *Error {
	if n.Kind != Directive || (n.Name != "foreach" && n.Name != "forelse") {
		return nil
	}
	_, binding := splitAs(n.Body)
	if role, taken := goUniverse[binding]; taken {
		return &Error{
			Path:    f.Path,
			Line:    n.Line,
			Message: fmt.Sprintf("@%s binds %q, so inside this loop %s is the loop variable and no longer %s", n.Name, binding, binding, role),
			Hint: fmt.Sprintf("the generated Go is written in Go's own words: `if %s == nil` guards every write it makes, `len` counts the subject of a @forelse, and `string` is the type a temporary is declared with.\n"+
				"    A binding renames whichever one it spells for the whole of the loop body, and the generated code is then reported against this view -- `invalid operation: %s == nil (mismatched types error and Row)`, in a file whose header says not to edit it.\n"+
				"    Rename the loop variable: @%s(.Items as item).", varErr, varErr, n.Name),
		}
	}
	if fn != "" && binding == fn {
		return &Error{
			Path:    f.Path,
			Line:    n.Line,
			Message: fmt.Sprintf("@%s binds %q, which is the name of the function this view compiles to", n.Name, binding),
			Hint: "the generated file declares " + binding + " at package level, and the binding renames it over the whole of the loop body.\n" +
				"    A call to " + binding + " written inside the loop reaches the loop variable instead, and is reported against this view.\n" +
				"    Rename the loop variable: @" + n.Name + "(.Items as item).",
		}
	}
	if strings.HasPrefix(binding, hygienePrefix) {
		return &Error{
			Path:    f.Path,
			Line:    n.Line,
			Message: fmt.Sprintf("@%s binds %q, and a name beginning with %q is the compiler's own", n.Name, binding, hygienePrefix),
			Hint: "the generated Go writes its writer, its data, its temporaries and its imports under that prefix, and a binding that starts with it would rename one of them.\n" +
				"    Any other name works, including a single letter and the name of a package.\n" +
				"    Write it as @" + n.Name + "(.Items as item).",
		}
	}
	if published[binding] {
		return &Error{
			Path:    f.Path,
			Line:    n.Line,
			Message: fmt.Sprintf("@%s binds %q, and this view already uses %q as a package", n.Name, binding, binding),
			Hint: "the Go compiled from this view imports " + binding + " under that name, because the view calls it outside this loop.\n" +
				"    A loop that binds the same name renames the package for the whole of its body, and the calls through it would be reported against this view.\n" +
				"    Rename the loop variable: @" + n.Name + "(.Items as item).",
		}
	}
	if binding == "" || validGoIdent(binding) {
		return nil
	}
	return &Error{
		Path:    f.Path,
		Line:    n.Line,
		Message: fmt.Sprintf("@%s binds %q, which is not a name", n.Name, binding),
		Hint: "the loop variable becomes a Go identifier: a letter or _ first, then letters, digits or _, and not a keyword.\n" +
			"    Write it as @" + n.Name + "(.Items as item).",
	}
}

// yieldInsideASection refuses a @yield written inside a @section.
//
// The two directives are the two ends of one contract: a page declares sections
// and hands them to the layout it extends, and the layout reads them with
// @yield. A page's own section is the far end of that -- it is what a @yield
// writes, and there is nothing further down for it to read.
//
// It is refused here rather than emitted because what was emitted did not
// compile. The map of sections is built by one statement, and a @yield inside a
// section reads that map from within the expression that declares it, which Go
// does not allow. `aru view:build` wrote the file, said it had compiled the
// view and exited 0; the person met it as `undefined: kyse__sections` in a file
// whose header says DO NOT EDIT.
func yieldInsideASection(f *File) *Error {
	var walk func(section string, nodes []Node) *Error
	walk = func(section string, nodes []Node) *Error {
		for _, n := range nodes {
			if n.Kind == Directive && n.Name == "yield" {
				return &Error{
					Path:    f.Path,
					Line:    n.Line,
					Message: fmt.Sprintf("@yield inside @section('%s'), which has no sections of its own to read", section),
					Hint: "@yield belongs to a layout: it writes the section a view that extends the layout declared.\n" +
						"    A section is the other end of that exchange -- it is what a @yield writes.\n" +
						"    To draw shared markup here, use @include('partials.name').",
				}
			}
			if err := walk(section, n.Children); err != nil {
				return err
			}
		}
		return nil
	}
	for _, s := range f.Sections {
		if err := walk(s.Name, s.Nodes); err != nil {
			return err
		}
	}
	return nil
}

// componentDenies are the directives that read the page, and so are a page's.
//
// Each compiles to a call that is handed the data the framework gave the render
// function -- the token @csrf writes, the data @include passes down, the
// sections @yield reads. A component is not rendered by name and is handed no
// page: it is an ordinary Go function whose argument is its own props.
var componentDenies = map[string]string{
	"csrf":    "writes the request's token, which the page it is drawn on is handed and a component is not",
	"include": "hands the page's data to a partial, and a component has no page's data",
	"yield":   "writes a section a view declared, and nothing declares sections to a component",
}

// pageDirectiveInAComponent refuses a page's directive written in a component.
//
// It is refused rather than emitted because what was emitted did not compile.
// A component becomes a function with one parameter, its props, and none of the
// three names those calls reach for exists in it -- `aru view:build` wrote the
// file, said the view had compiled and exited 0, and the person met
// `undefined: kyse__data` in a file whose header says DO NOT EDIT.
func (g *generator) pageDirectiveInAComponent() *Error {
	if !g.isComponent() {
		return nil
	}
	var walk func([]Node) *Error
	walk = func(nodes []Node) *Error {
		for _, n := range nodes {
			if n.Kind == Directive {
				if why, denied := componentDenies[n.Name]; denied {
					return &Error{
						Path:    g.file.Path,
						Line:    n.Line,
						Message: fmt.Sprintf("@%s in a component: it %s", n.Name, why),
						Hint: "a component compiles to a Go function taking the props it declares, and that is all it is given.\n" +
							"    Declare what it needs in the props and pass it from the page that draws the component.",
					}
				}
			}
			if err := walk(n.Children); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(g.file.Body); err != nil {
		return err
	}
	for _, s := range g.file.Sections {
		if err := walk(s.Nodes); err != nil {
			return err
		}
	}
	return nil
}

// validateExpressions checks that every interpolation expression in the file is
// valid Go after translation. The generator calls g.expr to turn a DSL expression
// into a Go one, and what comes out must parse -- or the Go the generator writes
// would be a compile error in a file whose header says DO NOT EDIT.
//
// Expressions inside @for and @while headers are checked too, because the
// generator passes them through the same expr translation before they reach the
// Go compiler -- a header that is passed through unchanged (no semicolons) still
// arrives as-is in the generated Go.
func validateExpressions(f *File, g *generator) *Error {
	published := map[string]bool{}
	for _, name := range g.published {
		published[name] = true
	}
	fn := g.renderFuncName()
	var walk func([]Node) *Error
	walk = func(nodes []Node) *Error {
		for _, n := range nodes {
			if err := loopBinding(f, n, published, fn); err != nil {
				return err
			}
			for _, raw := range goExpressions(n) {
				translated := g.expr(raw)
				if _, err := goparser.ParseExpr(translated); err != nil {
					return &Error{
						Path:    f.Path,
						Line:    n.Line,
						Message: fmt.Sprintf("%q is not a Go expression", translated),
						Hint: fmt.Sprintf("the interpolation reads %q, and after translation it becomes %q, which is not valid Go: %s",
							raw, translated, err),
					}
				}
				// Again with something after it on the line, which is how the
				// generator writes it: inside a call, followed by the closing
				// parenthesis. An expression that parses alone and not there
				// swallowed what comes next -- `0//` is the shape.
				if _, err := goparser.ParseExpr("(" + translated + ")"); err != nil {
					return &Error{
						Path:    f.Path,
						Line:    n.Line,
						Message: fmt.Sprintf("%q does not end where it is written", translated),
						Hint: "the generator writes the expression inside a call, on one line, so a line comment in it comments out the rest of that line.\n" +
							"    Take the // out, or write /* */ instead.",
					}
				}
			}
			if err := walk(n.Children); err != nil {
				return err
			}
			// For @for directives, validate the full clause output as a Go for
			// header. Individual clause checks miss cases where the init or post
			// is a statement (not an expression) that Go rejects.
			if n.Kind == Directive && n.Name == "for" {
				full := g.clauses(n.Body)
				if full == "" {
					full = ";;"
				}
				src := "package p\nfunc f() {\nfor " + full + " {}\n}"
				if _, err := goparser.ParseFile(token.NewFileSet(), "", src, goparser.SkipObjectResolution); err != nil {
					return &Error{
						Path:    f.Path,
						Line:    n.Line,
						Message: fmt.Sprintf("@for(%s) does not produce a valid Go for loop", n.Body),
						Hint:    fmt.Sprintf("the header becomes %q, which is not valid: %s", full, err),
					}
				}
			}
		}
		return nil
	}
	if err := walk(f.Body); err != nil {
		return err
	}
	for _, s := range f.Sections {
		if err := walk(s.Nodes); err != nil {
			return err
		}
	}
	return nil
}

// clauses turns the three parts of an @for header into Go.
//
// Each is passed through expr on its own, so `.Items` means a field of the data
// in the condition the same way it does everywhere else:
//
//	@for(i := 0; i < len(.Items); i++)  ->  for i := 0; i < len(d.Items); i++
//
// A header without two semicolons is passed through untouched: it is either
// already Go or it is a mistake, and the Go compiler says which, at the line
// the view wrote.
func (g *generator) clauses(header string) string {
	parts := strings.Split(strings.TrimSpace(header), ";")
	if len(parts) != 3 {
		return strings.TrimSpace(header)
	}
	for i, p := range parts {
		if strings.TrimSpace(p) == "" {
			continue
		}
		parts[i] = g.expr(p)
	}
	return strings.Join(parts, "; ")
}

// expr turns a view expression into a Go one.
//
// `.Name` means "a field of the data", which is what a bare name means in
// context. Everything else is passed through, so `len(d.Items)` and a helper
// call work -- the view is Go, and pretending otherwise would be inventing a
// second expression language.
//
// The name a dot becomes is the one the generated file declares the data
// under, and not the `d` a view may write for the same value. The two are
// separate so that a loop binding named `d` shadows the view's own name and
// leaves every `.Field` in the file reading the page it was written against.
func (g *generator) expr(e string) string {
	e = strings.TrimSpace(e)

	var out strings.Builder

	// Whether a dot here selects from something, decided by what has been
	// written rather than by the byte before it in the source.
	//
	// The two agree everywhere except after a dot this loop has already turned
	// into the page data, and that is the case the source cannot answer:
	// `{{ .. }}` read its second dot against the first, saw a character that
	// ends no operand, and wrote the page data a second time -- `kyse__dkyse__d`,
	// which is not a mistake Go can see, because it is a name. It parses, so
	// nothing downstream refused it, and the build reported the view compiled.
	// Read from the output the second dot follows a `d`, is a selector, and the
	// expression is refused where every other malformed one is.
	selects := func() bool {
		written := out.String()
		return written != "" && endsOperand(written[len(written)-1])
	}

	for i := 0; i < len(e); i++ {
		c := e[i]

		// A string or rune literal is copied out whole. `.Title` inside quotes is
		// text somebody wrote, and rewriting it would change what the page says.
		if c == '"' || c == '\'' || c == '`' {
			end := closingQuote(e, i)
			out.WriteString(e[i : end+1])
			i = end
			continue
		}

		// A leading dot is a field of the page data. It is leading when what
		// precedes it cannot end an operand: `feature.Title` is a selector on a
		// loop variable, `1.5` is a number, and neither is ours.
		if c == '.' && i+1 < len(e) && isFieldStart(e[i+1]) && !selects() {
			out.WriteString(varPage + ".")
			continue
		}

		// A leading dot with no field after it is the page data itself.
		//
		// This is not a new rule, it is the one above without its exception: if
		// `.Title` is a field of the data, `.` is the data. It is what a
		// component that asks the page a question is handed --
		// `components.FieldProps{Name: "email", Page: .}` -- and without it the
		// only way to write that was the name the data is declared under, which
		// a view has no business knowing.
		//
		// A digit after the dot is still a number, so `.5` is untouched.
		if c == '.' && !selects() && (i+1 >= len(e) || !isDigit(e[i+1])) {
			out.WriteString(varPage)
			continue
		}
		out.WriteByte(c)
	}
	return out.String()
}

// closingQuote finds the end of the literal that opens at i, or the end of the
// expression when it is never closed -- which is a Go syntax error, reported by
// the Go compiler at the view's own line.
func closingQuote(e string, i int) int {
	quote := e[i]
	for j := i + 1; j < len(e); j++ {
		if e[j] == '\\' && quote != '`' {
			j++
			continue
		}
		if e[j] == quote {
			return j
		}
	}
	return len(e) - 1
}

// isFieldStart reports whether a byte can begin a field name.
//
// A letter or an underscore, which is what a Go identifier begins with, and
// never a digit -- that last part is what keeps `.5` a number.
//
// It read only capitals once, on the ground that a view cannot reach an
// unexported field. It can: a view compiles into the package its @go block
// declares the struct in, so `.total` is an ordinary field read of a type the
// view owns. What the narrow test did instead was leave `.total` to the rule
// below it, which took the dot for the page data and left the rest of the word
// glued to the name -- `{{ .total }}` became `kyse__dtotal`, a name the file
// never declares. That parses, so gofmt accepted it and the build reported the
// view compiled.
//
// Any byte above ASCII counts, because a Go identifier may hold any Unicode
// letter and `.Ѻ` glued its name to the page data's exactly as `.total` did.
// A dot followed by something that is not a letter at all -- an emoji, a
// symbol -- now produces a selector that does not parse, and is refused with a
// position instead of written out as a name.
//
// A field that genuinely cannot be reached -- an unexported one of a type the
// view only aliases -- is now the Go compiler's answer to give, at the view's
// own line, and it names the field.
func isFieldStart(c byte) bool { return isASCIILetter(c) || c == '_' || c >= 0x80 }

// isDigit reports whether a byte is a decimal digit. It is what keeps `.5` a
// number rather than the page data followed by a stray 5.
func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// endsOperand reports whether a byte can end something a dot would select from.
func endsOperand(c byte) bool {
	return c == '_' || c == ')' || c == ']' || c == '}' ||
		c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// cond is expr for a boolean position, where a bare field is the condition.
func (g *generator) cond(e string) string { return g.expr(e) }

// splitAs reads `.Items as item`.
func splitAs(s string) (subject, binding string) {
	parts := strings.SplitN(s, " as ", 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	return strings.TrimSpace(s), ""
}

// silenceUnused keeps the generated file compiling when a view uses none of the
// imports -- a layout with no interpolation, for instance.
func (g *generator) silenceUnused() {
	g.out.WriteString("\nvar (\n")
	fmt.Fprintf(&g.out, "\t_ = %s.HTMLEscapeString\n", pkgTemplate)
	fmt.Fprintf(&g.out, "\t_ = %s.WriteString\n", pkgIO)
	// view is unused by a view that interpolates nothing -- a component that is
	// only markup, a layout that is only @yield. It is imported unconditionally
	// because deciding per file would mean predicting which directives emit a
	// call, and being wrong in the other direction is a build that fails.
	fmt.Fprintf(&g.out, "\t_ = %s.Text\n", pkgView)
	// strings is only imported by a component, and only used by one that writes
	// something.
	if g.isComponent() {
		fmt.Fprintf(&g.out, "\t_ = %s.Builder{}\n", pkgStrings)
	}
	// A package the view named for itself may still go unread in the output: the
	// call may sit in a comment, or in a directive that compiles to nothing. The
	// import is kept read here rather than predicted there.
	for _, name := range g.published {
		fmt.Fprintf(&g.out, "\t_ = %s.%s\n", name, republishedPackages[name].symbol)
	}
	g.out.WriteString(")\n")
}

// funcName turns "auth/login" into "renderAuthLogin".
func funcName(name string) string {
	var b strings.Builder
	b.WriteString("render")
	for _, part := range strings.FieldsFunc(name, func(r rune) bool {
		return r == '/' || r == '-' || r == '_' || r == '.'
	}) {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(part[1:])
	}
	return b.String()
}

func numbered(src string) string {
	var b strings.Builder
	for i, line := range strings.Split(src, "\n") {
		fmt.Fprintf(&b, "%4d| %s\n", i+1, line)
	}
	return b.String()
}

func (g *generator) yields() bool { return g.file.IsLayout() }

// splitAt cuts a directive's children at the first inline directive with the
// given name, and drops the separator. It is how @forelse finds its @empty and
// how any two-part block would.
func splitAt(children []Node, name string) (before, after []Node) {
	for i, c := range children {
		if c.Kind == Directive && c.Name == name {
			return children[:i], children[i+1:]
		}
	}
	return children, nil
}
