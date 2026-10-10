package kyse_test

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/kyse"
)

// A directive is read only at the start of a line. One written after markup on
// the same line used to reach the page as text: the anchor below rendered as
// `<a href="/" class="logo" @if(.LogoTarget != "")target="">`, with the
// condition never evaluated and the attribute written on every request. The
// build reported success. These tests hold the compiler to refusing that line
// with the shape that reads, and to leaving every @ that is not a directive
// exactly where it was.

// topbarView is the shape the swagger module's topbar shipped: the directive
// glued to the attribute it guards, inside the tag, on a line that begins with
// markup.
const topbarView = `//go:build kyse

package views

@go
type Topbar struct{ HomeURL, LogoTarget string }
@endgo

<header>
	<a href="{{ .HomeURL }}" class="logo" @if(.LogoTarget != "")target="{{ .LogoTarget }}"@endif>Home</a>
</header>
`

// topbarSpaced is the same view written the way the refusal says to write it.
const topbarSpaced = `//go:build kyse

package views

@go
type Topbar struct{ HomeURL, LogoTarget string }
@endgo

<header>
	<a href="{{ .HomeURL }}" class="logo"
	@if(.LogoTarget != "")
	target="{{ .LogoTarget }}"
	@endif
	>Home</a>
</header>
`

func problems(err error) []*kyse.Error {
	var out []*kyse.Error
	var group interface{ Unwrap() []error }
	if errors.As(err, &group) {
		for _, e := range group.Unwrap() {
			var p *kyse.Error
			if errors.As(e, &p) {
				out = append(out, p)
			}
		}
		return out
	}
	var p *kyse.Error
	if errors.As(err, &p) {
		out = append(out, p)
	}
	return out
}

func TestTheSwaggerTopbarShapeIsRefusedWithTheLineSplit(t *testing.T) {
	file, err := kyse.Parse("resources/views/topbar.kyse.go", topbarView)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, err = kyse.Generate(file, "topbar", "Topbar", "storage/framework/views/topbar.go")
	if err == nil {
		t.Fatal("a directive glued inside a tag compiled, and the page would print it as text")
	}

	got := problems(err)
	if len(got) != 1 {
		t.Fatalf("want one refusal for the one line, got %d:\n%v", len(got), err)
	}
	p := got[0]
	if p.Line != 10 {
		t.Errorf("the refusal names line %d; the directive is on line 10", p.Line)
	}
	if want := "@if follows markup on its line"; !strings.Contains(p.Message, want) {
		t.Errorf("message %q does not say %q", p.Message, want)
	}
	spaced := "        <a href=\"{{ .HomeURL }}\" class=\"logo\"\n" +
		"        @if(.LogoTarget != \"\")\n" +
		"        target=\"{{ .LogoTarget }}\"\n" +
		"        @endif\n" +
		"        >Home</a>"
	if !strings.Contains(p.Hint, spaced) {
		t.Errorf("the hint does not show the line split the way it reads:\n%s\nwant it to contain:\n%s", p.Hint, spaced)
	}
}

// The shape the refusal shows is one that compiles and does what the glued one
// meant: the attribute is written when the condition holds, and no @ reaches
// the page either way. It is run rather than read, because what is under test
// is the markup a browser would be handed.
func TestTheSpacedTopbarRendersTheAttributeOnlyWhenAsked(t *testing.T) {
	root := t.TempDir()
	writeRuntimeModule(t, root)
	compileView(t, root, "views", "topbar", "", topbarSpaced)
	writeFile(t, filepath.Join(root, "main.go"), `package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/arandu-io/hesape/view"

	"example.test/render/views"
)

func main() {
	for _, target := range []string{"", "_blank"} {
		var page strings.Builder
		if err := view.Render(&page, "topbar", views.Topbar{HomeURL: "/", LogoTarget: target}); err != nil {
			fmt.Println("error:", err)
			os.Exit(1)
		}
		fmt.Printf("%q\n", page.String())
	}
}
`)

	lines := strings.Split(strings.TrimSpace(runModule(t, root)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want two renders, got:\n%s", strings.Join(lines, "\n"))
	}
	without, with := lines[0], lines[1]
	if strings.Contains(without, "target") || strings.Contains(without, "@") {
		t.Errorf("with no target the anchor carries one, or a directive reached the page: %s", without)
	}
	if !strings.Contains(with, `target=\"_blank\"`) || strings.Contains(with, "@") {
		t.Errorf("with a target the anchor does not carry it, or a directive reached the page: %s", with)
	}
}

func TestADirectiveAfterMarkupIsRefusedWhereverThePageWouldPrintIt(t *testing.T) {
	for _, c := range []struct {
		what, markup, name string
	}{
		{"spaced, inside a tag", `<a href="/" @if(.V != "") target="{{ .V }}" @endif>x</a>`, "if"},
		{"in the body of an element", `<p>state: @if(.V != "") on @else off @endif</p>`, "if"},
		{"a conditional class inside a value", `<a class="btn @if(.V != "")active@endif" href="/">x</a>`, "if"},
		{"inside a script", `<script>@if(.V != "") console.log(1) @endif</script>`, "if"},
		{"an include between two tags", `<div>@include('partials.row')</div>`, "include"},
		{"CSRF glued between two tags", `<form method="post">@csrf<button>Go</button></form>`, "csrf"},
		{"a closing directive glued inside a tag", `<a href="/" target="_blank"@endif>x</a>`, "endif"},
		{"a set of attributes inside a tag", `<button class="btn" @attributes>Go</button>`, "attributes"},
	} {
		t.Run(c.what, func(t *testing.T) {
			_, err := generateProbe(t, c.markup)
			if err == nil {
				t.Fatalf("compiled, and the page would print @%s as text", c.name)
			}
			got := problems(err)
			if len(got) != 1 {
				t.Fatalf("want one refusal for the one line, got %d:\n%v", len(got), err)
			}
			p := got[0]
			if p.Line != 9 {
				t.Errorf("the refusal names line %d; the markup is on line 9", p.Line)
			}
			if want := "@" + c.name + " follows markup on its line"; !strings.Contains(p.Message, want) {
				t.Errorf("message %q does not say %q", p.Message, want)
			}
			if !strings.Contains(p.Hint, "\n        @"+c.name) {
				t.Errorf("the hint does not put @%s on a line of its own:\n%s", c.name, p.Hint)
			}
			if !strings.Contains(p.Hint, "&#64;") {
				t.Errorf("the hint does not say how to write the characters themselves:\n%s", p.Hint)
			}
		})
	}
}

// An @ that does not begin a directive is text, and so is a directive a person
// writes about rather than uses. Each line here compiles to exactly the bytes
// it was written as.
func TestAnAtThatIsNotADirectiveStaysText(t *testing.T) {
	for _, c := range []struct{ what, markup string }{
		{"an e-mail address", `<a href="mailto:team@example.com">team@example.com</a>`},
		{"an address whose parts spell a directive", `<p>team@if.example and ops@for(sales).example</p>`},
		{"a CSS at-rule", `<style>@media (max-width: 600px) { .a { color: red } }</style>`},
		{"a CSS import", `<style>@import url("/a.css");</style>`},
		{"a decorator in a code sample", `<pre><code>@dataclass</code></pre>`},
		{"an annotation in a code sample", `<code>@Override public String toString()</code>`},
		{"an attribute that is not a directive", `<button @click="open = true">Open</button>`},
		{"a directive named in an HTML comment", `<!-- One @yield, and it is 'content'. -->`},
		{"a directive with arguments in an HTML comment", `<!-- @if(.Ready) was here -->`},
		{"a directive named in a sentence", `<p>Write @csrf in every form, and close @if with @endif.</p>`},
		{"a directive named in a value", `<abbr title="the @csrf directive">CSRF</abbr>`},
	} {
		t.Run(c.what, func(t *testing.T) {
			out, err := generateProbe(t, c.markup)
			if err != nil {
				t.Fatalf("refused text that holds no directive: %v", err)
			}
			if want := strconv.Quote(c.markup + "\n"); !strings.Contains(out, want) {
				t.Errorf("the line is not written as it was:\nwant %s in\n%s", want, out)
			}
		})
	}
}

func TestADirectiveAtTheStartOfALineWithMarkupGluedAfterItIsRefusedOnce(t *testing.T) {
	const head = "//go:build kyse\n\npackage views\n\n"
	for _, c := range []struct {
		what, body string
		line       int
		message    string
		hint       string
	}{
		{
			"an opening directive with the attribute glued after it",
			"<a href=\"/\"\n\t@if(.T != \"\")target=\"{{ .T }}\"\n\t@endif\n>x</a>\n",
			6, "@if is followed by markup on the same line",
			"        @if(.T != \"\")\n        target=\"{{ .T }}\"",
		},
		{
			"a closing directive with the end of the tag glued after it",
			"<a href=\"/\"\n\t@if(.T != \"\")\n\ttarget=\"{{ .T }}\"\n\t@endif>x</a>\n",
			8, "@endif is followed by markup on the same line",
			"        @endif\n        >x</a>",
		},
		{
			"a closing directive after markup, which never closes its block",
			"<a href=\"/\"\n\t@if(.T != \"\")\n\ttarget=\"{{ .T }}\"@endif>x</a>\n",
			6, "@if was never closed",
			"the @endif on line 7 follows markup on its line",
		},
		{
			"CSRF with the button glued after it",
			"<form method=\"post\">\n@csrf<button>Go</button>\n</form>\n",
			6, "@csrf is followed by markup on the same line",
			"        @csrf\n        <button>Go</button>",
		},
		{
			"a parenthesis left open keeps its own message",
			"@if(.T\n<p>x</p>\n@endif\n",
			5, "@if takes its arguments in parentheses that end the line",
			"close them with )",
		},
		{
			"arguments without parentheses keep their own message",
			"@if .T\n<p>x</p>\n@endif\n",
			5, "@if takes its arguments in parentheses that end the line",
			"close them with )",
		},
	} {
		t.Run(c.what, func(t *testing.T) {
			_, err := kyse.Parse("resources/views/home.kyse.go", head+c.body)
			if err == nil {
				t.Fatal("parsed")
			}
			got := problems(err)
			if c.message != "@if takes its arguments in parentheses that end the line" && len(got) != 1 {
				t.Fatalf("want one problem for the one mistake, got %d:\n%v", len(got), err)
			}
			p := got[0]
			if p.Line != c.line || !strings.Contains(p.Message, c.message) {
				t.Errorf("got line %d %q, want line %d %q", p.Line, p.Message, c.line, c.message)
			}
			if !strings.Contains(p.Hint, c.hint) {
				t.Errorf("the hint does not contain\n%s\nit is:\n%s", c.hint, p.Hint)
			}
		})
	}
}
