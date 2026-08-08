package kyse

import (
	"fmt"
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
			file.Package = strings.TrimSpace(strings.TrimPrefix(line, "package "))
			p.i++
			if !sawTag {
				return &Error{Path: p.path, Line: 1,
					Message: "this view has no `//go:build kyse` on the first line",
					Hint: "without it the Go compiler reads the markup below as Go and fails.\n" +
						"    Add it, followed by a blank line, before the package clause."}
			}
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

		name, args, ok := directiveOn(trimmed)
		if !ok {
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
			end := strings.Index(rest[rawAt:], "!!}")
			if end < 0 {
				p.fail(lineNo, "{!! was never closed", "close it with !!} on the same line.")
				out = append(out, Node{Kind: Text, Body: rest + "\n", Line: lineNo})
				return out
			}
			if rawAt > 0 {
				out = append(out, Node{Kind: Text, Body: rest[:rawAt], Line: lineNo})
			}
			out = append(out, Node{Kind: Raw, Body: strings.TrimSpace(rest[rawAt+3 : rawAt+end]), Line: lineNo})
			rest = rest[rawAt+end+3:]

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

		case n.Kind == Text && strings.TrimSpace(n.Body) == "":
			// Whitespace between directives at the top level is not content.

		default:
			file.Body = append(file.Body, n)
		}
	}

	// A view that extends a layout puts its content in sections. Markup outside
	// one would be written before the layout and land outside <html>.
	if file.Extends != "" {
		for _, n := range file.Body {
			if n.Kind == Text && strings.TrimSpace(n.Body) == "" {
				continue
			}
			p.fail(n.Line, "this markup is outside any @section, in a view that extends a layout",
				"wrap it in @section('content') … @endsection, or it renders outside the page.")
			break
		}
	}
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
func directiveOn(trimmed string) (name, args string, ok bool) {
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
	if strings.HasPrefix(rest, "(") && strings.HasSuffix(rest, ")") {
		args = rest[1 : len(rest)-1]
	}
	return name, args, true
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
