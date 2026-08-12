package kyse

import (
	"fmt"
	"go/format"
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
	g.emit()

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

// self hands the position back to the generated file.
//
// Without it a single interpolation would claim every line below it, and the
// compiler would report a mistake in this generator at a line of the view --
// possibly a line the view does not have.
func (g *generator) self() { g.out.WriteString(markerSelf + "\n") }

func (g *generator) emit() {
	fmt.Fprintf(&g.out, "// Code generated by `aru view:build` from %s. DO NOT EDIT.\n",
		path.Base(g.file.Path))
	fmt.Fprintf(&g.out, "//\n// Edit the .kyse.go and run `aru view:build`. Changes here are overwritten.\n\n")
	fmt.Fprintf(&g.out, "package %s\n\n", g.file.Package)

	g.out.WriteString("import (\n")
	g.out.WriteString("\t\"html/template\"\n\t\"io\"\n")
	// A component builds its markup into a string and returns it, so it is the
	// one shape that needs strings. gofmt drops nothing, so it is added here
	// rather than always.
	if g.isComponent() {
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
// The directory decides, and it is the same directory Blade uses. A component is
// not a page: nothing renders it by name, a controller never hands it data, and
// it has no layout.
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
		for _, n := range s.Nodes {
			g.node(n)
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

	case Echo:
		// template.HTMLEscapeString is the escape every template engine applies, and it
		// is not optional: a view that interpolates without escaping is the XSS
		// this framework refuses to make easy.
		g.guarded(n.Line, "_, err = io.WriteString(w, template.HTMLEscapeString(view.Text(%s)))", g.expr(n.Body))

	case Raw:
		g.guarded(n.Line, "_, err = io.WriteString(w, view.Text(%s))", g.expr(n.Body))

	case Directive:
		g.directive(n)
	}
}

func (g *generator) directive(n Node) {
	switch n.Name {
	case "if":
		g.at(n.Line)
		fmt.Fprintf(&g.out, "\tif %s {\n", g.cond(n.Body))
		g.self()
		// @else and @elseif arrive as children, because they are inline
		// directives inside the block the parser opened. Each one closes the
		// branch in progress and opens the next.
		//
		// Without this they had no case at all and were dropped in silence:
		// @if(x) A @else B @endif emitted `if x { A; B }`, so both halves
		// appeared when the condition held and neither when it did not.
		for _, c := range n.Children {
			if c.Kind == Directive && (c.Name == "else" || c.Name == "elseif") {
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
		for _, c := range n.Children {
			g.node(c)
		}
		g.out.WriteString("\t}\n")

	case "for":
		// @for(i := 0; i < len(d.Items); i++) -> the same three clauses, in Go.
		//
		// The familiar form is @for(i = 0; i < 10; i++), and this is the same shape
		// with Go's syntax, exactly as @if already takes a Go condition rather
		// than inventing a second expression language (RULE 15).
		g.at(n.Line)
		fmt.Fprintf(&g.out, "\tfor %s {\n", g.clauses(n.Body))
		g.self()
		for _, c := range n.Children {
			g.node(c)
		}
		g.out.WriteString("\t}\n")

	case "while":
		// Go has one loop keyword, so @while(cond) is `for cond`.
		g.at(n.Line)
		fmt.Fprintf(&g.out, "\tfor %s {\n", g.cond(n.Body))
		g.self()
		for _, c := range n.Children {
			g.node(c)
		}
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
		for _, c := range after {
			g.node(c)
		}
		g.out.WriteString("\t} else {\n")
		g.at(n.Line)
		fmt.Fprintf(&g.out, "\t\tfor _, %s := range %s {\n", binding, g.expr(subject))
		g.self()
		fmt.Fprintf(&g.out, "\t\t\t_ = %s\n", binding)
		for _, c := range before {
			g.node(c)
		}
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
		fmt.Fprintf(&g.out, "\tif err == nil { err = view.Yield(w, sections, %s) }\n",
			strconv.Quote(unquote(n.Body)))

	case "include":
		// A partial shares the page's data, and that is all @include does.
		//
		// It briefly took a second argument, so a partial could be handed data of
		// its own. That made two ways to draw a component -- this one, resolved
		// by string at run time, and the typed function a component compiles to
		// -- and the string one is the worse of the two by exactly the measure
		// this project is built on: a typo in the name reaches production. RULE 9
		// says the second way does not get to exist, and the compiler-checked one
		// is the one that stays.
		fmt.Fprintf(&g.out, "\tif err == nil { err = view.Include(w, %s, data) }\n",
			strconv.Quote(unquote(n.Body)))

	case "csrf":
		g.out.WriteString("\tif err == nil { err = view.CSRF(w, data) }\n")
	}
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
		case "foreach":
			subject, _ := splitAs(n.Body)
			return []string{subject}
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
// second expression language (RULE 15).
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
