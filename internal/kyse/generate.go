package kyse

import (
	"fmt"
	"go/format"
	goparser "go/parser"
	"go/token"
	"path"
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
	if dataType == "" {
		if expr, line, found := dataReference(f); found {
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

	g := &generator{file: f, name: name, dataType: dataType}

	if err := validateExpressions(f, g); err != nil {
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
	return resolveLineMarkers(formatted, f.Path, output), nil
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
	g.out.WriteString("\tif err == nil {\n")
	g.at(line)
	fmt.Fprintf(&g.out, "\t\t"+format+"\n", args...)
	g.self()
	g.out.WriteString("\t}\n")
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
func (g *generator) checked(line int, expr, denied, write, reason string) {
	g.checks = true
	g.out.WriteString("\tif err == nil {\n")
	g.at(line)
	fmt.Fprintf(&g.out, "\t\tif v := view.Text(%s); strings.ContainsAny(v, %s) {\n",
		expr, strconv.Quote(denied))
	g.self()
	fmt.Fprintf(&g.out, "\t\t\terr = errors.New(%s)\n",
		strconv.Quote(fmt.Sprintf("%s:%d: %s", g.file.Path, line, reason)))
	g.out.WriteString("\t\t} else {\n")
	fmt.Fprintf(&g.out, "\t\t\t_, err = io.WriteString(w, %s)\n", write)
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
	g.out.WriteString("\tif err == nil {\n")
	g.out.WriteString("\t\tvar s string\n")
	g.at(line)
	fmt.Fprintf(&g.out, "\t\ts, err = %s(%s)\n", escaper, expr)
	g.self()
	// The position is a separate argument rather than part of the format, so a
	// path holding a percent sign stays a path.
	fmt.Fprintf(&g.out, "\t\tif err != nil {\n\t\t\terr = fmt.Errorf(\"%%s: %%w\", %s, err)\n\t\t} else {\n",
		strconv.Quote(fmt.Sprintf("%s:%d", g.file.Path, line)))
	g.out.WriteString("\t\t\t_, err = io.WriteString(w, s)\n")
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

	g.out.WriteString("import (\n")
	g.out.WriteString("\t\"html/template\"\n\t\"io\"\n")
	// errors carries the refusal of a value the position cannot accept, and
	// nothing else in a generated view returns an error of its own.
	if g.checks {
		g.out.WriteString("\t\"errors\"\n")
	}
	// fmt gives the refusal an escaper returns the view's own file and line
	// before it travels, and is written only by a view that has one.
	if g.wraps {
		g.out.WriteString("\t\"fmt\"\n")
	}
	// strings is needed by two shapes: a component builds its markup into one
	// and returns it, and a checked interpolation examines the value before
	// writing it. gofmt drops nothing, so it is added here rather than always.
	if g.checks || g.isComponent() {
		g.out.WriteString("\t\"strings\"\n")
	}
	g.out.WriteString("\n\t\"github.com/arandu-io/framework/view\"\n")
	// What the view imported for itself, after the ones that are always here.
	//
	// Anything already emitted above is dropped rather than repeated: a component
	// whose @go block uses strings.Fields writes `import "strings"` in its source,
	// which is the honest thing to write, and Go refuses the file if the same
	// path appears twice.
	//
	// gofmt groups and sorts what survives, so the order written is not the order
	// emitted.
	for _, imp := range g.file.Imports {
		if alreadyImported(g.out.String(), imp) {
			continue
		}
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

	fn := funcName(g.name)
	isLayout := g.yields()

	// A layout is handed the sections the child view declared; a page is not.
	// Two different contracts, and so two registries -- the @yield in the source
	// is what decides which one this file is.
	if isLayout {
		fmt.Fprintf(&g.out, "func init() { view.RegisterLayout(%s, %s) }\n\n", strconv.Quote(g.name), fn)
		fmt.Fprintf(&g.out, "// %s renders the %s layout.\nfunc %s(w io.Writer, data any, sections map[string]func(io.Writer) error) error {\n", fn, g.name, fn)
	} else {
		fmt.Fprintf(&g.out, "func init() { view.Register(%s, %s) }\n\n", strconv.Quote(g.name), fn)
		fmt.Fprintf(&g.out, "// %s renders %s.\nfunc %s(w io.Writer, data any) error {\n", fn, g.name, fn)
	}

	if g.dataType != "" {
		fmt.Fprintf(&g.out, "\td, ok := data.(%s)\n", g.dataType)
		fmt.Fprintf(&g.out, "\tif !ok {\n\t\treturn view.WrongData(%s, %s, data)\n\t}\n",
			strconv.Quote(g.name), strconv.Quote(g.dataType))
		// `d` may go unused: a layout that is only @yield interpolates nothing.
		g.out.WriteString("\t_ = d\n")
	} else {
		g.out.WriteString("\t_ = data\n")
	}
	switch {
	case g.file.Extends != "":
		// A view that extends a layout renders the layout, handing it the
		// sections as a map the @yield reads. The layout is a view like any
		// other, so this is one render calling another.
		g.emitSections()
		fmt.Fprintf(&g.out, "\treturn view.RenderInto(w, %s, data, sections)\n", strconv.Quote(g.file.Extends))
	default:
		g.out.WriteString("\tvar err error\n")
		for _, n := range g.file.Body {
			g.node(n)
		}
		g.out.WriteString("\treturn err\n")
	}

	g.out.WriteString("}\n")
	g.silenceUnused()
}

// alreadyImported reports whether the import block written so far names the same
// path as this line.
//
// It compares the quoted path rather than the whole line, so `strings` and
// `s "strings"` are recognised as the same import -- which they are to the
// compiler, and a duplicate path is a compile error whatever it is named.
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
func (g *generator) emitComponent() {
	fn := componentFuncName(g.name)

	fmt.Fprintf(&g.out, "// %s renders the %s component.\n",
		fn, strings.TrimPrefix(g.name, "components."))
	if g.dataType == "" {
		fmt.Fprintf(&g.out, "func %s() template.HTML {\n", fn)
	} else {
		fmt.Fprintf(&g.out, "func %s(props %s) template.HTML {\n", fn, g.dataType)
		g.out.WriteString("\td := props\n\t_ = d\n")
	}

	// A strings.Builder never fails a write, so the err every node threads
	// through is dead here -- and dropping the guards would mean a second code
	// path through the node emitter, which is how the two drift apart.
	g.out.WriteString("\tw := &strings.Builder{}\n")
	g.out.WriteString("\tvar err error\n")
	for _, n := range g.file.Body {
		g.node(n)
	}
	g.out.WriteString("\t_ = err\n")
	g.out.WriteString("\treturn template.HTML(w.String())\n")
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
	g.out.WriteString("\tsections := map[string]func(io.Writer) error{\n")
	for _, s := range g.file.Sections {
		fmt.Fprintf(&g.out, "\t\t%s: func(w io.Writer) error {\n\t\t\tvar err error\n", strconv.Quote(s.Name))
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
		g.out.WriteString("\t\t\treturn err\n\t\t},\n")
	}
	g.out.WriteString("\t}\n")
}

func (g *generator) node(n Node) {
	switch n.Kind {
	case Text:
		if n.Body == "" {
			return
		}
		fmt.Fprintf(&g.out, "\tif err == nil { _, err = io.WriteString(w, %s) }\n", strconv.Quote(n.Body))
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
		g.guarded(n.Line, "_, err = io.WriteString(w, view.UnsafeText(%s))", g.expr(n.Body))

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
		g.checked(n.Line, expr, deniedInAttributeName, "v", reasonAttributeName)

	case posAttributeValue:
		g.guarded(n.Line, "_, err = io.WriteString(w, view.TextAttr(%s))", expr)

	case posURL:
		g.refusable(n.Line, "view.TextURL", expr)

	case posScript:
		g.guarded(n.Line, "_, err = io.WriteString(w, view.TextJS(%s))", expr)

	case posStyle:
		g.refusable(n.Line, "view.TextCSS", expr)

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
		g.guarded(n.Line, "_, err = io.WriteString(w, template.HTMLEscapeString(view.Text(%s)))", expr)
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
		// @foreach(.Items as item) -> for _, item := range d.Items
		subject, binding := splitAs(n.Body)
		if binding == "" {
			binding = "item"
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
			binding = "item"
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
		fmt.Fprintf(&g.out, "\t\tfor _, %s := range %s {\n", binding, g.expr(subject))
		g.self()
		fmt.Fprintf(&g.out, "\t\t\t_ = %s\n", binding)
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
		fmt.Fprintf(&g.out, "\tif err == nil { err = view.Yield(w, sections, %s) }\n",
			strconv.Quote(unquote(n.Body)))

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
		fmt.Fprintf(&g.out, "\tif err == nil { err = view.Include(w, %s, data) }\n",
			strconv.Quote(unquote(n.Body)))

	case "csrf":
		g.callsOut(n)
		g.out.WriteString("\tif err == nil { err = view.CSRF(w, data) }\n")
	}
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
// `{{ .Name }}` compiles to `d.Name`, and `d` is declared only when the view has
// a data type -- from its own `@go` block, or inherited from the layout it
// extends. Without one the generator emitted `_ = data` and then `d.Name`, which
// parses; so `format.Source` accepted it, the command reported success, and the
// failure arrived later as `undefined: d` in a file whose header says DO NOT
// EDIT. Naming the `.kyse.go` and the line is the whole reason this compiler
// exists rather than a template library.
func dataReference(f *File) (expr string, line int, found bool) {
	var walk func([]Node) bool
	walk = func(nodes []Node) bool {
		for _, n := range nodes {
			for _, e := range goExpressions(n) {
				if e = strings.TrimSpace(e); strings.HasPrefix(e, ".") {
					expr, line, found = e, n.Line, true
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
func loopBinding(f *File, n Node) *Error {
	if n.Kind != Directive || (n.Name != "foreach" && n.Name != "forelse") {
		return nil
	}
	_, binding := splitAs(n.Body)
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
	var walk func([]Node) *Error
	walk = func(nodes []Node) *Error {
		for _, n := range nodes {
			if err := loopBinding(f, n); err != nil {
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
func (g *generator) expr(e string) string {
	e = strings.TrimSpace(e)

	var out strings.Builder
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
		if c == '.' && i+1 < len(e) && isFieldStart(e[i+1]) && (i == 0 || !endsOperand(e[i-1])) {
			out.WriteString("d.")
			continue
		}

		// A leading dot with no field after it is the page data itself.
		//
		// This is not a new rule, it is the one above without its exception: if
		// `.Title` is a field of the data, `.` is the data. It is what a
		// component that asks the page a question is handed --
		// `components.FieldProps{Name: "email", Page: .}` -- and without it the
		// only way to write that was `d`, the generator's own variable name,
		// which a view has no business knowing.
		//
		// A digit after the dot is still a number, so `.5` is untouched.
		if c == '.' && (i == 0 || !endsOperand(e[i-1])) &&
			(i+1 >= len(e) || !isDigit(e[i+1])) {
			out.WriteByte('d')
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

// isFieldStart reports whether a byte can begin an exported field name. Only
// exported ones: a view cannot read an unexported field anyway, and requiring
// the capital is what keeps `.5` a number.
func isFieldStart(c byte) bool { return c >= 'A' && c <= 'Z' }

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
	g.out.WriteString("\t_ = template.HTMLEscapeString\n")
	g.out.WriteString("\t_ = io.WriteString\n")
	// view is unused by a view that interpolates nothing -- a component that is
	// only markup, a layout that is only @yield. It is imported unconditionally
	// because deciding per file would mean predicting which directives emit a
	// call, and being wrong in the other direction is a build that fails.
	g.out.WriteString("\t_ = view.Text\n")
	g.out.WriteString("\t_ = view.UnsafeText\n")
	// strings is only imported by a component, and only used by one that writes
	// something.
	if g.isComponent() {
		g.out.WriteString("\t_ = strings.Builder{}\n")
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
