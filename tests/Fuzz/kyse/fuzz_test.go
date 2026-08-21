package kyse_test

import (
	"errors"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/arandu-io/aru/internal/kyse"
	"github.com/arandu-io/aru/tests"
)

// FuzzParse feeds arbitrary bytes through the view compiler and checks what the
// compiler promises about whatever comes out.
//
// A test written by hand covers the mistakes somebody thought of, and the three
// delimiter traps this parser already carries were each found by being bitten:
// the opener it looks at is the last one and not the first, the join gives up at
// the end of the file instead of turning, and the comment rewrites its own
// closing line. Those are the shape of defect nobody writes a case for, which is
// what this is here to reach.
//
// The properties, for input nobody imagined:
//
//   - one outcome and not both -- a tree, or an error
//   - the same source parses to the same tree twice, so nothing survives one
//     call into the next
//   - a rejection is a positioned diagnosis, naming the file it was given and a
//     line the file has
//   - a diagnosis about valid UTF-8 is itself valid UTF-8, or the person reads
//     mojibake
//   - every directive node in an accepted tree carries a name the generator has
//     a case for, and no position past the end of the source: a `//line` cannot
//     point at a line the view does not have
//   - an expression the generator will emit is never empty, in either
//     interpolation form
//   - what the generator writes parses as Go, whenever the Go the view wrote
//     does
//   - a value the generator refuses is refused at a line the view has, the same
//     as a problem the parser reports
//
// The last one is the generator's own claim about itself: output that does not
// parse is its bug, never the view's. It is checked only when the verbatim
// regions -- the package clause, the imports and the `@go` blocks -- parse on
// their own, because those are copied out unread and a syntax error inside one
// of them belongs to whoever typed it.
//
// The corpus is seeded twice over, from two sources that do not overlap: the
// shapes below, which are the pathological ones a valid view never contains,
// and every view source in the repository, which are the ordinary ones. A
// fuzzer with neither spends its budget discovering that a view opens with a
// build tag.
func FuzzParse(f *testing.F) {
	for _, seed := range pathologicalViews {
		f.Add(seed, false)
	}
	for _, seed := range viewsInRepository(f) {
		f.Add(seed, false)
	}

	f.Fuzz(func(t *testing.T, source string, component bool) {
		const path = "resources/views/fuzz.kyse.go"

		file, err := kyse.Parse(path, source)
		if (file == nil) == (err == nil) {
			t.Fatalf("Parse returned tree=%t and error=%t: exactly one of the two is the answer", file != nil, err != nil)
		}

		// The parser rewrites its own line buffer to consume a comment that
		// spans lines. A rewrite that outlived the call would make the second
		// answer differ from the first, and a build would depend on how many
		// times a file had been read.
		twice, errTwice := kyse.Parse(path, source)
		if (errTwice == nil) != (err == nil) {
			t.Fatalf("two parses of one source disagree on whether it is valid:\nfirst:  %v\nsecond: %v", err, errTwice)
		}
		if err != nil && errTwice.Error() != err.Error() {
			t.Fatalf("two parses of one source reported different problems:\nfirst:  %v\nsecond: %v", err, errTwice)
		}
		if !reflect.DeepEqual(file, twice) {
			t.Fatal("two parses of one source returned different trees")
		}

		// A source of N newlines is N+1 lines, the last of them empty. That is
		// what strings.Split gives the parser, so it is the range every position
		// it reports has to fall in.
		lines := strings.Count(source, "\n") + 1

		if err != nil {
			checkDiagnosis(t, err, path, source, lines)
			return
		}

		checkTree(t, file, lines)
		checkGeneratedGoParses(t, file, component, lines)
	})
}

// checkDiagnosis holds the parser to the shape of a refusal: positioned, about
// the file it was handed, and readable.
func checkDiagnosis(t *testing.T, err error, path, source string, lines int) {
	t.Helper()

	problems := diagnoses(err)
	if len(problems) == 0 {
		t.Fatalf("Parse refused the view with %T, which carries no position: a refusal has to name the line to be acted on -- %v", err, err)
	}

	for _, p := range problems {
		if p.Path != path {
			t.Errorf("a problem names %q and the view is %q: the position is not clickable from where the build runs", p.Path, path)
		}
		if p.Line < 1 || p.Line > lines {
			t.Errorf("a problem is reported at line %d of a source that has %d: %s", p.Line, lines, p.Message)
		}
		if p.Message == "" {
			t.Errorf("a problem at line %d has no message", p.Line)
		}
	}

	// Mojibake in the sentence a person reads. The parser quotes the offending
	// line back, and cutting a long one to length by bytes splits whatever rune
	// straddles the cut.
	if utf8.ValidString(source) && !utf8.ValidString(err.Error()) {
		t.Errorf("the source is valid UTF-8 and the diagnosis is not: %q", err.Error())
	}
}

// checkTree holds the accepted tree to what the generator can consume: known
// names, real positions, and no empty expression.
func checkTree(t *testing.T, file *kyse.File, lines int) {
	t.Helper()

	known := make(map[string]bool)
	for _, name := range kyse.Directives() {
		known[name] = true
	}

	var walk func(nodes []kyse.Node)
	walk = func(nodes []kyse.Node) {
		for _, n := range nodes {
			if n.Line < 1 || n.Line > lines {
				t.Errorf("a node is at line %d of a source that has %d: a //line directive would point past the end of the view", n.Line, lines)
			}
			switch n.Kind {
			case kyse.Directive:
				switch {
				case !known[n.Name]:
					t.Errorf("@%s reached the tree and is not a directive kyse knows: the generator has no case for it and drops it in silence", n.Name)
				case strings.HasPrefix(n.Name, "end"):
					t.Errorf("@%s reached the tree as a node: a closing directive is consumed by the block it closes, never emitted", n.Name)
				}
			case kyse.Echo, kyse.Raw:
				if strings.TrimSpace(n.Body) == "" {
					t.Errorf("an interpolation at line %d carries no expression: the generator emits the call around it either way, with nothing inside the parentheses", n.Line)
				}
				if len(n.Children) > 0 {
					t.Errorf("an interpolation at line %d has children, which nothing renders", n.Line)
				}
			case kyse.Text:
				if len(n.Children) > 0 {
					t.Errorf("text at line %d has children, which nothing renders", n.Line)
				}
			case kyse.GoBlock:
				// The @go body travels in File.Go, not as a node of the body.
				t.Errorf("a GoBlock node reached the tree at line %d: the blocks are collected in File.Go", n.Line)
			}
			walk(n.Children)
		}
	}

	walk(file.Body)
	for _, s := range file.Sections {
		if s.Line < 1 || s.Line > lines {
			t.Errorf("section %q is at line %d of a source that has %d", s.Name, s.Line, lines)
		}
		walk(s.Nodes)
	}
	for _, b := range file.Go {
		if b.Line < 1 || b.Line > lines {
			t.Errorf("an @go block starts at line %d of a source that has %d: every type error inside it would be reported off the end of the view", b.Line, lines)
		}
	}
}

// checkGeneratedGoParses holds the generator to its claim: Go it wrote that does
// not parse is its own bug.
//
// Three refusals are the view's and are let through. A view that reads a field
// with no type to read it from is refused by name, with a position. A value
// written where the document has no escape that holds it -- an unquoted
// attribute value, an attribute whose value is a script, the name of an element,
// or a position the markup before it left open -- is refused the same way. A
// misplaced @else, @elseif or @empty is written out as Go that does not compile
// on purpose: emitting nothing is what hid the mistake for as long as it lived.
func checkGeneratedGoParses(t *testing.T, file *kyse.File, component bool, lines int) {
	t.Helper()

	if !verbatimGoParses(file) {
		return
	}

	name := "fuzz"
	if component {
		name = "components.fuzz"
	}

	out, err := kyse.Generate(file, name, kyse.RenderType(file), "storage/framework/views/fuzz.go")
	if err != nil {
		var positioned *kyse.Error
		if errors.As(err, &positioned) {
			// A refusal is acted on by opening the view at the line it names,
			// so a line the view does not have is a refusal nobody can act on.
			for _, p := range diagnoses(err) {
				if p.Line < 1 || p.Line > lines {
					t.Errorf("the generator refused an interpolation at line %d of a view that has %d: %s", p.Line, lines, p.Message)
				}
			}
			return
		}
		if misplacedInline(file) {
			return
		}
		t.Fatalf("Generate refused a view whose own Go parses: %v", err)
	}

	if _, err := parser.ParseFile(token.NewFileSet(), "fuzz.go", out, parser.SkipObjectResolution); err != nil {
		t.Fatalf("the generated Go does not parse: %v\n%s", err, out)
	}
}

// verbatimGoParses reports whether the regions the generator copies out unread
// are Go on their own: the package clause, the imports and the @go blocks.
//
// It is the filter in front of the generator's claim about itself, and it is
// deliberately the whole file rather than each region -- a declaration that
// parses alone can still be refused next to another one.
func verbatimGoParses(f *kyse.File) bool {
	var b strings.Builder
	b.WriteString("package " + f.Package + "\n")
	if len(f.Imports) > 0 {
		b.WriteString("import (\n")
		for _, imp := range f.Imports {
			b.WriteString(imp + "\n")
		}
		b.WriteString(")\n")
	}
	for _, block := range f.Go {
		b.WriteString(block.Body + "\n")
	}
	_, err := format.Source([]byte(b.String()))
	return err == nil
}

// misplacedInline reports whether the tree holds an inline directive outside the
// block that reads it, which is what the generator answers with Go that does not
// compile.
func misplacedInline(f *kyse.File) bool {
	var walk func(nodes []kyse.Node, parent string) bool
	walk = func(nodes []kyse.Node, parent string) bool {
		for _, n := range nodes {
			if n.Kind != kyse.Directive {
				continue
			}
			switch n.Name {
			case "else", "elseif":
				if parent != "if" {
					return true
				}
			case "empty":
				if parent != "forelse" {
					return true
				}
			}
			if walk(n.Children, n.Name) {
				return true
			}
		}
		return false
	}

	if walk(f.Body, "") {
		return true
	}
	for _, s := range f.Sections {
		if walk(s.Nodes, "section") {
			return true
		}
	}
	return false
}

// diagnoses unwraps what Parse returns into the problems it found, one or many.
func diagnoses(err error) []*kyse.Error {
	switch e := err.(type) {
	case kyse.Errors:
		return e
	case *kyse.Error:
		return []*kyse.Error{e}
	}
	return nil
}

// viewsInRepository reads every view source in the tree, to be seeded as it is
// written.
//
// It fails rather than seeding nothing: a corpus that quietly emptied itself
// leaves the fuzzer generating bytes that never reach the markup at all, and the
// run reports success either way.
//
// The walk starts at the module root and not at a fixed number of parent
// directories, which is the same failure one level down: "../.." was this
// package's own way of naming the root, and it went on resolving to a real
// directory after the file moved -- one that holds no view at all.
func viewsInRepository(f *testing.F) []string {
	var out []string

	err := filepath.WalkDir(tests.Root(f), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".kyse.go") && !strings.HasSuffix(path, ".kyse.go.golden") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out = append(out, string(body))
		return nil
	})
	if err != nil {
		f.Fatalf("reading the views of this repository: %v", err)
	}
	if len(out) == 0 {
		f.Fatal("no view source found in this repository: the corpus would carry only the shapes written above")
	}
	return out
}

// The pathological shapes: what a valid view never contains, and what the three
// delimiter traps in the parser are made of. Every one of them is a mutation
// away from a dozen others, which is the point of seeding them rather than
// asserting on them.
var pathologicalViews = []string{
	// The header, in the three ways it can be wrong: no build tag, no package
	// clause, and markup where the clause belongs.
	"package views\n\n<p>no build tag</p>\n",
	"//go:build kyse\n\n<p>no package clause</p>\n",
	"//go:build kyse\n\n<h1>markup first</h1>\n\npackage views\n",

	// An accented line long enough to be cut to length, where the parser quotes
	// it back into the message.
	"//go:build kyse\n\nrelatório de faturamento com acentuação suficiente para passar do corte\n\npackage views\n",

	// Both interpolation forms with nothing inside, and both unterminated.
	"//go:build kyse\n\npackage views\n\n{{ }}\n{!! !!}\n{{\n{!!\n",

	// The opener the line closes and the one it does not, on one line.
	"//go:build kyse\n\npackage views\n\n<p>{{ .A }} and {{ .B</p>\n",

	// A comment that spans lines, with markup after the closing delimiter --
	// the rewrite-in-place path.
	"//go:build kyse\n\npackage views\n\n<tr>{{-- why this row\n     is here --}}<td>{{ .Cell }}</td></tr>\n",

	// A comment that never closes, and one that opens inside another.
	"//go:build kyse\n\npackage views\n\n{{-- never closed\n<p>{{ .A }}</p>\n",
	"//go:build kyse\n\npackage views\n\n{{-- outer {{-- inner --}}\n",

	// An interpolation across lines, which is how a component call is written.
	"//go:build kyse\n\npackage views\n\nimport \"example.com/app/resources/views/components\"\n\n" +
		"{!! components.Field(components.FieldProps{\n\tName:  \"email\",\n\tLabel: \"Email\",\n}) !!}\n",

	// An import block that never closes.
	"//go:build kyse\n\npackage views\n\nimport (\n\t\"strings\"\n",

	// A block that never closes, one closed by the wrong end, and an end with
	// nothing above it.
	"//go:build kyse\n\npackage views\n\n@if(.Ok)\n<p>x</p>\n",
	"//go:build kyse\n\npackage views\n\n@if(.Ok)\n<p>x</p>\n@endforeach\n",
	"//go:build kyse\n\npackage views\n\n@endsection\n",

	// The inline directives away from the block that reads them.
	"//go:build kyse\n\npackage views\n\n@else\n@elseif(.Ok)\n@empty\n",

	// A directive that does not exist, one with an unbalanced argument list, and
	// an @ on its own.
	"//go:build kyse\n\npackage views\n\n@sektion('content')\n@endsection\n",
	"//go:build kyse\n\npackage views\n\n@if(.Ok\n@endif\n",
	"//go:build kyse\n\npackage views\n\n@\n@@\n",

	// Two layouts, and markup outside a section in a view that extends one.
	"//go:build kyse\n\npackage views\n\n@extends('a')\n@extends('b')\n<p>outside</p>\n",

	// A section nested in a block, which the assembler only reads at the top.
	"//go:build kyse\n\npackage views\n\n@if(.Ok)\n@section('content')\n<p>x</p>\n@endsection\n@endif\n",

	// @go with Go that does not parse, and @go that never closes.
	"//go:build kyse\n\npackage views\n\n@go\ntype D struct{\n@endgo\n",
	"//go:build kyse\n\npackage views\n\n@go\ntype D struct{}\n",

	// A layout and a page in the same file: the two contracts at once.
	"//go:build kyse\n\npackage views\n\n@extends('layouts.app')\n\n@section('content')\n@yield('sidebar')\n@endsection\n",

	// Nesting deep enough to matter, and a loop with its own delimiters inside.
	"//go:build kyse\n\npackage views\n\n@foreach(.Rows as row)\n@if(row.Ok)\n@while(.More)\n{{ row.Cell }}\n@endwhile\n@endif\n@endforeach\n",
	"//go:build kyse\n\npackage views\n\n@for(i := 0; i < len(.Items); i++)\n{{ .Items[i] }}\n@endfor\n",
	"//go:build kyse\n\npackage views\n\n@forelse(.Items as it)\n{{ it }}\n@empty\n<p>none</p>\n@endforelse\n",

	// Windows line endings, a lone carriage return, and a NUL byte.
	"//go:build kyse\r\n\r\npackage views\r\n\r\n<p>{{ .A }}</p>\r\n",
	"//go:build kyse\n\npackage views\n\n<p>{{ .A }}</p>\r",
	"//go:build kyse\n\npackage views\n\n<p>\x00{{ .A }}</p>\n",

	// Bytes that are not UTF-8 at all.
	"//go:build kyse\n\npackage views\n\n<p>\xff\xfe{{ .A }}</p>\n",

	// A package clause that is not an identifier, and one with nothing after it.
	"//go:build kyse\n\npackage 1 2 3\n\n<p>x</p>\n",
	"//go:build kyse\n\npackage\n\n<p>x</p>\n",
}
