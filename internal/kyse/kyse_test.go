package kyse_test

import (
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/kyse"
)

// O layout, escrito como um dev Laravel escreveria — só que a extensão termina
// em .go e o arquivo abre com a tag de build.
const layoutSource = `//go:build kyse

package views

@go
type LayoutData struct {
	Title string
}
@endgo

<!doctype html>
<html lang="pt-BR">
<head><title>{{ .Title }}</title></head>
<body>
    @yield('content')
</body>
</html>
`

const homeSource = `//go:build kyse

package views

@go
type HomeData struct {
	Nome   string
	Ativo  bool
	Itens  []string
}
@endgo

@extends('layouts.app')

@section('content')
    <h1>Olá {{ .Nome }}</h1>
    @if(.Ativo)
        <span class="badge">ativo</span>
    @endif
    @foreach(.Itens as item)
        <li>{{ item }}</li>
    @endforeach
@endsection
`

func TestParseReadsTheBladeShape(t *testing.T) {
	f, err := kyse.Parse("resources/views/home.kyse.go", homeSource)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if f.Package != "views" {
		t.Errorf("package = %q", f.Package)
	}
	if f.Extends != "layouts.app" {
		t.Errorf("extends = %q, want layouts.app", f.Extends)
	}
	if len(f.Sections) != 1 || f.Sections[0].Name != "content" {
		t.Fatalf("sections = %+v", f.Sections)
	}
	if len(f.Go) != 1 || !strings.Contains(f.Go[0], "type HomeData struct") {
		t.Errorf("o bloco @go nao foi capturado: %+v", f.Go)
	}
}

// TestTheGeneratedGoParses: the generator emits Go the compiler accepts. When it
// does not, the error has to say the bug is the generator's -- not the view's.
func TestTheGeneratedGoParses(t *testing.T) {
	f, err := kyse.Parse("resources/views/home.kyse.go", homeSource)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	out, err := kyse.Generate(f, "home", "HomeData")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	src := string(out)
	for _, want := range []string{
		"package views",
		"type HomeData struct",              // o bloco @go veio junto
		`view.Register("home", renderHome)`, // registrado pelo nome
		"d, ok := data.(HomeData)",          // dado tipado
		"view.WrongData",                    // the error when the type does not match
		"template.HTMLEscapeString",         // {{ }} escapa
		`view.RenderInto(w, "layouts.app"`,  // @extends
		"for _, item := range d.Itens",      // @foreach
		"if d.Ativo {",                      // @if
		"DO NOT EDIT",                       // gerado
	} {
		if !strings.Contains(src, want) {
			t.Errorf("o Go gerado nao tem %q", want)
		}
	}
}

// TestTheLayoutYields: a layout extends nothing and has a @yield.
func TestTheLayoutYields(t *testing.T) {
	f, err := kyse.Parse("resources/views/layouts/app.kyse.go", layoutSource)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Extends != "" {
		t.Errorf("um layout nao estende nada, e este estende %q", f.Extends)
	}

	out, err := kyse.Generate(f, "layouts.app", "LayoutData")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(string(out), `view.Yield(w, sections, "content")`) {
		t.Errorf("o @yield nao virou Yield:\n%s", out)
	}
}

// TestTheErrorNamesTheFileAndTheLine is kyse's exit criterion.
//
// Laravel solves this with a heuristic: BladeMapper recompiles the template with
// markers inserted, looks for the nearest one above the compiled line, and gives
// up after twenty. We emit the Go, so the position is exact.
func TestTheErrorNamesTheFileAndTheLine(t *testing.T) {
	cases := []struct {
		nome   string
		source string
		linha  int
		diz    string
	}{
		{
			nome: "@section sem @endsection",
			source: `//go:build kyse

package views

@section('content')
    <h1>oi</h1>
`,
			linha: 5,
			diz:   "@section",
		},
		{
			nome: "diretiva que nao existe",
			source: `//go:build kyse

package views

@secton('content')
@endsection
`,
			linha: 5,
			diz:   "@secton",
		},
		{
			nome: "{{ sem fechar",
			source: `//go:build kyse

package views

<h1>{{ .Nome</h1>
`,
			linha: 5,
			diz:   "{{",
		},
		{
			nome: "sem a tag de build",
			source: `package views

<h1>oi</h1>
`,
			linha: 1,
			diz:   "//go:build kyse",
		},
	}

	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			_, err := kyse.Parse("resources/views/home.kyse.go", c.source)
			if err == nil {
				t.Fatal("compilou uma view quebrada")
			}

			msg := err.Error()
			if !strings.Contains(msg, "resources/views/home.kyse.go") {
				t.Errorf("o erro nao nomeia o arquivo:\n%s", msg)
			}
			if !strings.Contains(msg, ":"+itoa(c.linha)+":") {
				t.Errorf("o erro nao aponta a linha %d:\n%s", c.linha, msg)
			}
			if !strings.Contains(msg, c.diz) {
				t.Errorf("o erro nao menciona %q:\n%s", c.diz, msg)
			}
		})
	}
}

// TestEveryProblemAtOnce: whoever fixes a view should not discover its problems
// one build at a time. Same reason as the spec validator.
func TestEveryProblemAtOnce(t *testing.T) {
	source := `//go:build kyse

package views

@secton('a')
@endsection

<h2>{{ .Sem fechar</h2>

@blah
`
	_, err := kyse.Parse("resources/views/x.kyse.go", source)
	if err == nil {
		t.Fatal("compilou")
	}
	if n := strings.Count(err.Error(), "x.kyse.go:"); n < 2 {
		t.Errorf("reportou %d problemas, esperava pelo menos 2:\n%s", n, err)
	}
}

// TestMarkupForaDeSection: numa view que estende layout, markup solto seria
// escrito antes do layout e sairia fora do <html>.
func TestMarkupForaDeSection(t *testing.T) {
	source := `//go:build kyse

package views

@extends('layouts.app')

<h1>isto fica fora da pagina</h1>

@section('content')
@endsection
`
	_, err := kyse.Parse("resources/views/x.kyse.go", source)
	if err == nil {
		t.Fatal("aceitou markup fora de @section")
	}
	if !strings.Contains(err.Error(), "@section") {
		t.Errorf("o erro nao diz o que fazer:\n%v", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestElseTakesOnlyOneBranch: @else was in the parser's directive set and had no
// case in the generator, so the node was dropped -- `@if(x) A @else B @endif`
// became `if x { A; B }`, which prints both halves when the condition holds and
// neither when it does not. It is why the nine starter-kit views were written
// with `@if(.X)` followed by `@if(!d.X)` instead of an else.
func TestElseTakesOnlyOneBranch(t *testing.T) {
	src := "//go:build kyse\n\npackage views\n\n@go\ntype D struct{ Ok bool }\n@endgo\n\n@if(.Ok)\nYES\n@else\nNO\n@endif\n"

	f, err := kyse.Parse("branch.kyse.go", src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out, err := kyse.Generate(f, "branch", "D")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	got := string(out)

	elseAt := strings.Index(got, "} else {")
	if elseAt < 0 {
		t.Fatalf("the generated Go has no else branch: @else was dropped\n%s", got)
	}
	// The literals are matched inside the WriteString call: a bare "NO"
	// also appears in the "DO NOT EDIT" header.
	yes, no := strings.Index(got, `WriteString(w, "YES`), strings.Index(got, `WriteString(w, "NO`)
	if yes < 0 || no < 0 {
		t.Fatalf("both branches must be emitted:\n%s", got)
	}
	if !(yes < elseAt && elseAt < no) {
		t.Errorf("the branches are not separated by the else:\n%s", got)
	}
}

// TestAViewThatReadsDataWithoutDeclaringItIsRefused is the command refusing to
// report success over Go that does not compile.
//
// `{{ .Name }}` becomes `d.Name`, and `d` exists only when the view has a data
// type. Without one the generator emitted `_ = data` and then `d.Name` -- which
// parses, so `format.Source` accepted it, `aru view:build` printed "1 view(s)
// compiled", and the failure surfaced later as `undefined: d` in a file marked
// DO NOT EDIT.
//
// The struct outside `@go` is how it happens in practice: a `type` line in the
// markup is text, so nothing declares anything.
func TestAViewThatReadsDataWithoutDeclaringItIsRefused(t *testing.T) {
	src := "//go:build kyse\n\npackage views\n\ntype HomeData struct{ Name string }\n\n<h1>Hello {{ .Name }}</h1>\n"

	f, err := kyse.Parse("resources/views/home.kyse.go", src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	out, err := kyse.Generate(f, "home", "")
	if err == nil {
		t.Fatalf("the generator reported success over Go that does not compile:\n%s", out)
	}

	msg := err.Error()
	for _, want := range []string{
		"resources/views/home.kyse.go", // the source, never the generated file
		":7:",                          // and the line the expression sits on
		".Name",                        // and which expression it was
		"@go",                          // and what to do about it
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the error does not mention %q:\n%s", want, msg)
		}
	}
}

// TestTheSectionsAreCheckedToo: a view that extends a layout puts everything in
// sections and leaves its body empty, so a check that only walked the body would
// miss every page written the normal way.
func TestTheSectionsAreCheckedToo(t *testing.T) {
	src := "//go:build kyse\n\npackage views\n\n@extends('layouts.app')\n\n@section('content')\n<h1>{{ .Title }}</h1>\n@endsection\n"

	f, err := kyse.Parse("resources/views/page.kyse.go", src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// The layout it extends declared no type either, so there is nothing to
	// inherit -- which is exactly the state compileViews passes down.
	if _, err := kyse.Generate(f, "page", ""); err == nil {
		t.Error("a section reading .Title with no data type was accepted")
	}
}

// TestAViewThatReadsNothingNeedsNoType guards the other side: a layout that only
// frames its sections has no data to type, and demanding one would be inventing
// a rule the language does not have.
func TestAViewThatReadsNothingNeedsNoType(t *testing.T) {
	src := "//go:build kyse\n\npackage views\n\n<html><body>\n@yield('content')\n</body></html>\n"

	f, err := kyse.Parse("resources/views/layouts/bare.kyse.go", src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := kyse.Generate(f, "layouts.bare", ""); err != nil {
		t.Errorf("a layout that reads no data was refused: %v", err)
	}
}

// TestABoundVariableIsNotThePageData: inside @foreach the binding is its own
// variable, and it is legal without a data type on the view.
func TestABoundVariableIsNotThePageData(t *testing.T) {
	src := "//go:build kyse\n\npackage views\n\n@go\ntype D struct{ Items []string }\n@endgo\n\n@foreach(.Items as item)\n<li>{{ item }}</li>\n@endforeach\n"

	f, err := kyse.Parse("resources/views/list.kyse.go", src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := kyse.Generate(f, "list", "D"); err != nil {
		t.Errorf("Generate: %v", err)
	}
}

// TestElseOutsideIfDoesNotCompile: a misplaced directive has to fail loudly.
// Emitting nothing is what hid the bug above for as long as it lived.
func TestElseOutsideIfDoesNotCompile(t *testing.T) {
	src := "//go:build kyse\n\npackage views\n\n@go\ntype D struct{}\n@endgo\n\n@else\n"

	f, err := kyse.Parse("stray.kyse.go", src)
	if err != nil {
		return // rejecting it at parse time is just as good
	}
	out, err := kyse.Generate(f, "stray", "D")
	if err != nil {
		return
	}
	if !strings.Contains(string(out), "#error") {
		t.Errorf("a stray @else generated valid Go, so nothing reports it:\n%s", out)
	}
}

// TestEveryDirectiveEmitsSomething is the structural guard for a defect that
// happened three times.
//
// A directive declared in the parser's set with no case in the generator is
// dropped in silence: @else made both halves of an if appear at once, and @for
// and @while emitted the loop with an empty body. Each time the build stayed
// green, `aru view:build` reported success, and the page was missing exactly
// what the author wrote -- which is the failure that takes longest to find,
// because nothing anywhere says a word.
//
// A closed set (RULE 15) is only a promise if something walks it.
func TestEveryDirectiveEmitsSomething(t *testing.T) {
	// The ones that only make sense in a position of their own, and are covered
	// by their own tests: the pairing halves, the layout pair, and @go, whose
	// content is copied verbatim above the render function.
	skip := map[string]bool{
		"endsection": true, "endif": true, "endforeach": true, "endfor": true,
		"endwhile": true, "endgo": true, "go": true, "section": true,
		"extends": true, "else": true, "elseif": true, "endforelse": true,
	}

	// One minimal view per directive, and what the generated Go must contain if
	// the case ran. The ones that wrap content are proved by the content coming
	// out the other side; the ones that emit a call are proved by the call.
	type probe struct{ source, wants string }
	body := map[string]probe{
		"if":       {"@if(d.Ok)\nMARCADOR\n@endif\n", "MARCADOR"},
		"foreach":  {"@foreach(.Items as it)\nMARCADOR\n@endforeach\n", "MARCADOR"},
		"for":      {"@for(i := 0; i < d.N; i++)\nMARCADOR\n@endfor\n", "MARCADOR"},
		"while":    {"@while(d.Ok)\nMARCADOR\n@endwhile\n", "MARCADOR"},
		"yield":    {"@yield('MARCADOR')\n", "MARCADOR"},
		"include":  {"@include('MARCADOR')\n", "MARCADOR"},
		"csrf":     {"@csrf\n", "view.CSRF("},
		"forelse":  {"@forelse(.Items as it)\nMARCADOR\n@empty\nVAZIO\n@endforelse\n", "MARCADOR"},
		"continue": {"@foreach(.Items as it)\n@continue\n@endforeach\n", "continue"},
		"break":    {"@foreach(.Items as it)\n@break\n@endforeach\n", "break"},
		"empty":    {"@forelse(.Items as it)\nX\n@empty\nMARCADOR\n@endforelse\n", "MARCADOR"},
	}

	for _, name := range kyse.Directives() {
		if skip[name] {
			continue
		}
		p, known := body[name]
		if !known {
			t.Errorf("@%s is in the directive set and this test does not exercise it: add a case above, or take it out of the set", name)
			continue
		}

		t.Run(name, func(t *testing.T) {
			full := "//go:build kyse\n\npackage views\n\n@go\ntype D struct {\n\tOk    bool\n\tN     int\n\tItems []string\n}\n@endgo\n\n" + p.source
			f, err := kyse.Parse(name+".kyse.go", full)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			out, err := kyse.Generate(f, name, "D")
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if !strings.Contains(string(out), p.wants) {
				t.Errorf("@%s generated Go with no trace of %q -- the node was dropped:\n%s", name, p.wants, out)
			}
		})
	}
}

// TestForelseTakesOneBranchAndCommentsDoNotReachThePage reads the generated Go
// instead of trusting that a case ran.
//
// Both are Blade's, and both were missing. @forelse is the one directive of the
// set that earns its keep in every generated index page: a list and its empty
// state are one thought, and writing them as @foreach beside @if(len(…) == 0)
// states the condition twice, in two places that can drift.
//
// {{-- --}} is the other half of the same gap: a view language with no way to
// write a note is one whose notes go in an HTML comment, visible in the page
// source of every request.
func TestForelseTakesOneBranchAndCommentsDoNotReachThePage(t *testing.T) {
	src := "//go:build kyse\n\npackage views\n\n@go\ntype D struct{ Items []string }\n@endgo\n\n" +
		"{{-- isto nao pode chegar ao navegador --}}\n" +
		"@forelse(.Items as it)\nCOM ITENS\n@empty\nLISTA VAZIA\n@endforelse\n"

	f, err := kyse.Parse("list.kyse.go", src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out, err := kyse.Generate(f, "list", "D")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	got := string(out)

	if strings.Contains(got, "isto nao pode chegar") {
		t.Error("the comment was emitted: it would show in the page source of every request")
	}

	// The two halves have to be separated by an else, or both run.
	empty, loop := strings.Index(got, "LISTA VAZIA"), strings.Index(got, "COM ITENS")
	elseAt := strings.Index(got, "} else {")
	if empty < 0 || loop < 0 {
		t.Fatalf("one of the branches was dropped:\n%s", got)
	}
	if elseAt < 0 || !(empty < elseAt && elseAt < loop) {
		t.Errorf("the empty state and the loop are not exclusive:\n%s", got)
	}
	if !strings.Contains(got, "len(d.Items) == 0") {
		t.Errorf("the empty state is not decided by the length of the list:\n%s", got)
	}
}
