package kyse

import (
	"fmt"
	"sort"
	"strings"
)

// Parse reads a view and returns its tree.
//
// It reports every problem it can find rather than stopping at the first, for
// the same reason the spec validator does: somebody fixing a view should not
// discover the mistakes one build at a time.
func Parse(path, source string) (*File, error) {
	p := &parser{path: path, src: source}
	p.split()

	file := &File{Path: path}
	if err := p.header(file); err != nil {
		return nil, err
	}

	nodes := p.nodes(&file.Go, 0)
	p.assemble(file, nodes)

	if len(p.errs) > 0 {
		return nil, p.errs
	}
	return file, nil
}

type parser struct {
	path  string
	src   string
	lines []string
	errs  Errors

	// i is the current line, 0-indexed. Positions reported are i+1.
	i int
}

func (p *parser) split() { p.lines = strings.Split(p.src, "\n") }

func (p *parser) fail(line int, message, hint string) {
	p.errs = append(p.errs, &Error{Path: p.path, Line: line, Message: message, Hint: hint})
}

// header reads the build tag and the package clause, and stops there.
//
// Both are mandatory, and the message says why: without the tag the Go compiler
// tries to parse the markup, and without the package clause it fails as soon as
// a generated file lands in the same directory.
func (p *parser) header(file *File) error {
	sawTag := false
	for ; p.i < len(p.lines); p.i++ {
		line := strings.TrimSpace(p.lines[p.i])

		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "//go:build"):
			sawTag = strings.Contains(line, "kyse")
			continue
		case strings.HasPrefix(line, "//"):
			continue
		case strings.HasPrefix(line, "package "):
			lineNo := p.i + 1
			file.Package = strings.TrimSpace(strings.TrimPrefix(line, "package "))
			p.i++
			if !sawTag {
				return &Error{Path: p.path, Line: 1,
					Message: "this view has no `//go:build kyse` on the first line",
					Hint: "without it the Go compiler reads the markup below as Go and fails.\n" +
						"    Add it, followed by a blank line, before the package clause."}
			}
			// What follows `package` is copied into the generated file as it is
			// written, so anything that is not a name lands in a clause the Go
			// compiler refuses -- in a file whose header says not to edit it.
			if !validGoIdent(file.Package) {
				return &Error{Path: p.path, Line: lineNo,
					Message: fmt.Sprintf("%q is not a package name", truncate(file.Package)),
					Hint: "the generated file opens with this clause, so it is a Go identifier and nothing else:\n" +
						"    a letter or _ first, then letters, digits or _, and not a keyword."}
			}
			p.imports(file)
			return nil
		default:
			return &Error{Path: p.path, Line: p.i + 1,
				Message: fmt.Sprintf("expected the package clause and found %q", truncate(line)),
				Hint: "a view starts with `//go:build kyse`, a blank line, then `package views`.\n" +
					"    The markup goes after that."}
		}
	}
	return &Error{Path: p.path, Line: 1,
		Message: "this view has no package clause",
		Hint:    "add `package views` after the build tag, or the Go compiler refuses the directory."}
}

// imports reads the import block, when the view has one, and stops at the first
// line that is neither an import nor blank.
//
// It is Go's own import statement rather than a directive of ours: the source is
// a Go file, the person is already writing Go in @go, and the directive set
// stays closed. What it buys is a view that draws a component from another
// package --
//
//	import "example.com/app/resources/views/components"
//
//	@include('components.button', components.ButtonProps{Label: "Save"})
//
// -- which is the whole point of components living in a directory of their own.
//
// Nothing here validates the paths. An import that does not resolve, or one
// nothing uses, is reported by the Go compiler against the generated file, and
// the //line directives put that report on the view's own line.
func (p *parser) imports(file *File) {
	for ; p.i < len(p.lines); p.i++ {
		line := strings.TrimSpace(p.lines[p.i])

		switch {
		case line == "":
			continue

		case strings.HasPrefix(line, "import ("):
			for p.i++; p.i < len(p.lines); p.i++ {
				inner := strings.TrimSpace(p.lines[p.i])
				if inner == ")" {
					break
				}
				if inner != "" {
					file.Imports = append(file.Imports, inner)
				}
			}
			if p.i == len(p.lines) {
				p.fail(len(p.lines), "the import block was never closed", "add ) where it ends.")
			}

		case strings.HasPrefix(line, "import "):
			file.Imports = append(file.Imports, strings.TrimSpace(strings.TrimPrefix(line, "import")))

		default:
			return
		}
	}
}

// nodes reads the body, from the current line to the end or to a closing
// directive it was not told to consume.
// nodes reads the body. depth 0 is the top level, where a closing directive has
// nothing to close and is an error rather than the end of the run — parsing
// continues so the problems after it are reported too.
func (p *parser) nodes(goBlocks *[]Block, depth int) []Node {
	var out []Node
	var text strings.Builder
	textLine := p.i + 1

	flush := func() {
		if text.Len() > 0 {
			out = append(out, Node{Kind: Text, Body: text.String(), Line: textLine})
			text.Reset()
		}
	}

	for p.i < len(p.lines) {
		line := p.lines[p.i]
		trimmed := strings.TrimSpace(line)
		lineNo := p.i + 1

		// A closing directive ends this run of nodes; the caller consumes it. At
		// the top level there is nothing to close, so it is a mistake — and
		// returning here would hide every problem after it.
		if isClosing(trimmed) {
			if depth == 0 {
				opener := strings.TrimPrefix(strings.Fields(trimmed)[0], "@end")
				p.fail(lineNo, trimmed+" closes a block that was never opened",
					"remove it, or open the block with @"+opener+" above.")
				p.i++
				continue
			}
			flush()
			return out
		}

		// A comment that opens on this line and closes on a later one is consumed
		// whole, before the line is read as markup.
		//
		// interpolate works one line at a time and cannot see the closing
		// delimiter on the next, so without this a comment spanning lines was
		// reported as never closed -- and the note somebody wrote about why a
		// line is the way it is is exactly the thing that runs to three lines.
		if at := strings.Index(line, "{{--"); at >= 0 && !strings.Contains(line[at:], "--}}") {
			flush()
			if before := line[:at]; strings.TrimSpace(before) != "" {
				out = append(out, p.interpolate(before, lineNo)...)
			}
			p.skipComment(lineNo)
			textLine = p.i + 1
			continue
		}

		// An interpolation that opens here and closes further down is joined into
		// one logical line first.
		//
		// A component call is the reason. Six props do not fit on a line, and
		// splitting them the way Go would is what anybody writes:
		//
		//	{!! components.Field(components.FieldProps{
		//		Name:  "email",
		//		Label: "Email",
		//	}) !!}
		//
		// The interpolator reads one line at a time, so without this each of
		// those lines is an unterminated {!! and the view is refused.
		line = p.joinInterpolation(line, lineNo)

		name, args, closed := directiveOn(trimmed)
		if name == "" {
			// Ordinary markup, with the interpolations inside it.
			flush()
			out = append(out, p.interpolate(line, lineNo)...)
			text.Reset()
			textLine = lineNo + 1
			p.i++
			continue
		}

		flush()
		p.i++

		// A directive whose arguments are not the end of the line is refused
		// here rather than emitted without them. The name is checked first, so
		// that markup beginning with an @ that is nothing of ours -- an event
		// handler on a line of its own -- is still answered with what it is.
		if !closed && isDirective(name) {
			p.fail(lineNo, fmt.Sprintf("@%s takes its arguments in parentheses that end the line", name),
				"close them with ) as the last thing on the line. A directive takes the whole line, and markup goes on the next one.")
			textLine = p.i + 1
			continue
		}

		switch {
		case name == "go":
			body := p.until("endgo", lineNo, "@go")
			// lineNo is the `@go` itself; the body starts on the line below it.
			*goBlocks = append(*goBlocks, Block{Body: body, Line: lineNo + 1})

		case blockDirectives[name] != "":
			children := p.nodes(goBlocks, depth+1)
			closing := ""
			if p.i < len(p.lines) {
				closing = strings.TrimSpace(p.lines[p.i])
			}
			want := "@" + blockDirectives[name]
			if !strings.HasPrefix(closing, want) {
				p.fail(lineNo, fmt.Sprintf("@%s was never closed", name),
					fmt.Sprintf("add %s where the block ends.", want))
			} else {
				p.i++
			}
			out = append(out, Node{Kind: Directive, Name: name, Body: args, Children: children, Line: lineNo})

		case inlineDirectives[name]:
			out = append(out, Node{Kind: Directive, Name: name, Body: args, Line: lineNo})

		default:
			p.fail(lineNo, fmt.Sprintf("@%s is not a directive kyse knows", name), suggest(name))
		}
		textLine = p.i + 1
	}

	flush()
	return out
}

// joinInterpolation folds the lines of an interpolation that spans several into
// one, and advances past them.
//
// The lines are joined with a space rather than a newline: what comes out is a
// Go expression, and gofmt lays it out again in the generated file. The view's
// line stays the one the interpolation opened on, which is where a type error
// inside it belongs.
//
// It gives up at the end of the file rather than looping, and the unterminated
// delimiter is then reported by interpolate with the position it opened at.
func (p *parser) joinInterpolation(line string, lineNo int) string {
	for openUnclosed(line) {
		if p.i+1 >= len(p.lines) {
			return line
		}
		p.i++
		line += " " + strings.TrimSpace(p.lines[p.i])
	}
	return line
}

// openUnclosed reports whether the line opens an interpolation it does not
// close.
//
// It looks at the last opener rather than the first, because a line can hold
// several: `{{ .A }} and {{ .B` is unterminated, and asking about the first one
// would say it is fine.
func openUnclosed(line string) bool {
	raw, esc := strings.LastIndex(line, "{!!"), strings.LastIndex(line, "{{")

	// A comment opens with the same two braces as an escaped interpolation and
	// is consumed before this runs. Treating it as one here would swallow the
	// markup after it.
	if esc >= 0 && strings.HasPrefix(line[esc:], "{{--") {
		esc = -1
	}

	switch {
	case raw > esc:
		return !strings.Contains(line[raw:], "!!}")
	case esc >= 0:
		return !strings.Contains(line[esc:], "}}")
	}
	return false
}

// skipComment consumes a comment that spans lines, and whatever follows its
// closing delimiter on that last line.
//
// Nothing it reads is emitted, which is the difference between this and an HTML
// comment: a note about why the markup is the way it is does not belong in the
// page source of every request.
func (p *parser) skipComment(openLine int) {
	for ; p.i < len(p.lines); p.i++ {
		end := strings.Index(p.lines[p.i], "--}}")
		if end < 0 {
			continue
		}
		// What is left on the closing line is markup again. Rewriting the line in
		// place and re-reading it is what keeps `--}}<td>` working without a
		// second path through the interpolator.
		p.lines[p.i] = p.lines[p.i][end+4:]
		if strings.TrimSpace(p.lines[p.i]) == "" {
			p.i++
		}
		return
	}
	p.fail(openLine, "{{-- was never closed", "close it with --}}.")
}

// until reads raw lines to a closing directive, without interpreting them. It is
// how @go keeps Go code intact.
func (p *parser) until(end string, openLine int, what string) string {
	var body strings.Builder
	for p.i < len(p.lines) {
		trimmed := strings.TrimSpace(p.lines[p.i])
		if trimmed == "@"+end {
			p.i++
			return body.String()
		}
		body.WriteString(p.lines[p.i])
		body.WriteByte('\n')
		p.i++
	}
	p.fail(openLine, what+" was never closed", "add @"+end+" where the block ends.")
	return body.String()
}

// interpolate splits one line of markup into text and interpolations.
func (p *parser) interpolate(line string, lineNo int) []Node {
	var out []Node
	rest := line

	for {
		// {{-- --}} is checked first, before both interpolations, because it opens
		// with the same two braces as the escaped form. A comment that were read as
		// an interpolation would try to compile its own text.
		//
		// Every template language has it and kyse did not: a view language with no
		// way to write a note is a view language whose notes end up in the markup,
		// visible in the page source of every request.
		if at := strings.Index(rest, "{{--"); at >= 0 && at <= indexOr(strings.Index(rest, "{!!"), len(rest)) {
			end := strings.Index(rest[at:], "--}}")
			if end < 0 {
				p.fail(lineNo, "{{-- was never closed", "close it with --}} on the same line.")
				out = append(out, Node{Kind: Text, Body: rest + "\n", Line: lineNo})
				return out
			}
			if at > 0 {
				out = append(out, Node{Kind: Text, Body: rest[:at], Line: lineNo})
			}
			// The comment itself emits nothing: it does not reach the page, which
			// is the difference from an HTML comment.
			rest = rest[at+end+4:]
			continue
		}

		// {!! !!} is checked before {{ }} so the raw form is not read as an
		// escaped one containing a bang.
		rawAt := strings.Index(rest, "{!!")
		escAt := strings.Index(rest, "{{")

		switch {
		case rawAt >= 0 && (escAt < 0 || rawAt < escAt):
			end := strings.Index(rest[rawAt+3:], "!!}")
			if end < 0 {
				p.fail(lineNo, "{!! was never closed", "close it with !!} on the same line.")
				out = append(out, Node{Kind: Text, Body: rest + "\n", Line: lineNo})
				return out
			}
			if rawAt > 0 {
				out = append(out, Node{Kind: Text, Body: rest[:rawAt], Line: lineNo})
			}
			expr := strings.TrimSpace(rest[rawAt+3 : rawAt+3+end])
			// Empty is refused in both forms, and for the same reason: the
			// generator emits the call around the expression either way, so
			// nothing between the delimiters is a call with nothing in its
			// parentheses -- Go that parses and does not compile, reported
			// against a generated file whose header says not to edit it.
			if expr == "" {
				p.fail(lineNo, "{!! !!} with nothing inside", "put the expression between the delimiters, as in {!! .Body !!}.")
			}
			out = append(out, Node{Kind: Raw, Body: expr, Line: lineNo})
			rest = rest[rawAt+3+end+3:]

		case escAt >= 0:
			end := strings.Index(rest[escAt:], "}}")
			if end < 0 {
				p.fail(lineNo, "{{ was never closed", "close it with }} on the same line.")
				out = append(out, Node{Kind: Text, Body: rest + "\n", Line: lineNo})
				return out
			}
			if escAt > 0 {
				out = append(out, Node{Kind: Text, Body: rest[:escAt], Line: lineNo})
			}
			expr := strings.TrimSpace(rest[escAt+2 : escAt+end])
			if expr == "" {
				p.fail(lineNo, "{{ }} with nothing inside", "put the expression between the braces, as in {{ .Name }}.")
			}
			out = append(out, Node{Kind: Echo, Body: expr, Line: lineNo})
			rest = rest[escAt+end+2:]

		default:
			out = append(out, Node{Kind: Text, Body: rest + "\n", Line: lineNo})
			return out
		}
	}
}

// assemble turns the top-level nodes into the file shape: @extends and the
// sections come out, everything else is the body.
func (p *parser) assemble(file *File, nodes []Node) {
	for _, n := range nodes {
		switch {
		case n.Kind == Directive && n.Name == "extends":
			if file.Extends != "" {
				p.fail(n.Line, "a view extends only one layout",
					fmt.Sprintf("it already extends %q on an earlier line.", file.Extends))
			}
			file.Extends = unquote(n.Body)

		case n.Kind == Directive && n.Name == "section":
			file.Sections = append(file.Sections, Section{
				Name: unquote(n.Body), Nodes: n.Children, Line: n.Line,
			})

		default:
			file.Body = append(file.Body, n)
		}
	}

	// A view that extends a layout puts its content in sections. Markup outside
	// one would be written before the layout and land outside <html>.
	//
	// Blank lines out there are not markup -- they are the spacing between
	// @extends and @section, which nobody wrote as content -- so they are
	// dropped rather than reported.
	if file.Extends != "" {
		kept := file.Body[:0]
		for _, n := range file.Body {
			if n.Kind == Text && strings.TrimSpace(n.Body) == "" {
				continue
			}
			kept = append(kept, n)
		}
		file.Body = kept

		if len(file.Body) > 0 {
			p.fail(file.Body[0].Line, "this markup is outside any @section, in a view that extends a layout",
				"wrap it in @section('content') … @endsection, or it renders outside the page.")
		}
		return
	}

	// A view with NO layout keeps its blank lines, and that is not a nicety.
	//
	// Dropping them here was correct for the section-based case and wrong for
	// this one, and the difference does not show in HTML -- a browser collapses
	// whitespace, so every page looked right. It shows in a plain-text e-mail,
	// where the paragraph breaks vanish and, worse, a URL on its own line ends
	// up glued to the first word of the next sentence:
	//
	//	https://example.com/auth/verify/confirm?token=abcThe link works for 24 hours.
	//
	// Every mail client that autolinks then produces a link with "The" on the
	// end of the token.
}

func isClosing(trimmed string) bool {
	if !strings.HasPrefix(trimmed, "@end") {
		return false
	}
	name := strings.TrimPrefix(trimmed, "@")
	for _, end := range blockDirectives {
		if name == end {
			return true
		}
	}
	return false
}

// directiveOn reads `@name(args)` or `@name` at the start of a trimmed line.
//
// An empty name means the line is not a directive at all. closed reports whether
// the arguments are the end of the line, which is the only shape there is: the
// parentheses open and close on one line, and nothing follows them.
//
// A line that did not have that shape used to come back as a directive with no
// arguments, and the arguments were lost with it. `@if(.Ok` -- the closing
// parenthesis left off, or a comment written after it -- became `if {`, and the
// generator reported its own output as a bug in itself.
func directiveOn(trimmed string) (name, args string, closed bool) {
	if !strings.HasPrefix(trimmed, "@") || len(trimmed) < 2 {
		return "", "", false
	}
	rest := trimmed[1:]

	end := len(rest)
	for i, r := range rest {
		if !isNameRune(r) {
			end = i
			break
		}
	}
	name = rest[:end]
	if name == "" {
		return "", "", false
	}

	rest = strings.TrimSpace(rest[end:])
	switch {
	case rest == "":
		return name, "", true
	case strings.HasPrefix(rest, "(") && strings.HasSuffix(rest, ")"):
		return name, rest[1 : len(rest)-1], true
	}
	return name, "", false
}

// isDirective reports whether the name is one kyse knows, block or inline.
func isDirective(name string) bool {
	return blockDirectives[name] != "" || inlineDirectives[name]
}

func isNameRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `'"`)
	return s
}

func truncate(s string) string {
	if len(s) > 40 {
		return s[:40] + "…"
	}
	return s
}

// suggest names the near miss, because a typo in a directive is the most common
// mistake and the alphabet is small.
//
// The candidates are sorted, and that is not presentation. They are collected by
// walking two maps, whose order Go randomises per run, so `@f` answered "did you
// mean @for or @foreach or @forelse?" and then, for the same file, "@foreach or
// @forelse or @for". One build, two sentences: nothing downstream can be
// compared, matched or diffed.
func suggest(name string) string {
	var known []string
	for d := range blockDirectives {
		known = append(known, "@"+d)
	}
	for d := range inlineDirectives {
		known = append(known, "@"+d)
	}

	var near []string
	for _, k := range known {
		if strings.HasPrefix(k[1:], name[:min(2, len(name))]) {
			near = append(near, k)
		}
	}
	if len(near) > 0 {
		sort.Strings(near)
		return "did you mean " + strings.Join(near, " or ") + "?"
	}
	return "what does not fit a directive is written in Go, inside @go … @endgo."
}

// indexOr returns fallback when i is negative, so a missing delimiter sorts last
// instead of first.
func indexOr(i, fallback int) int {
	if i < 0 {
		return fallback
	}
	return i
}
