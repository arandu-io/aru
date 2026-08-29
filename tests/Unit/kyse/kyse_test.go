package kyse_test

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/kyse"
)

// The layout, written the way a view is written: the extension ends in .go and
// the file opens with the build tag.
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

func TestParseReadsTheTemplateDirectives(t *testing.T) {
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
	if len(f.Go) != 1 || !strings.Contains(f.Go[0].Body, "type HomeData struct") {
		t.Errorf("the @go block was not captured: %+v", f.Go)
	}
}

// TestTheGeneratedGoParses: the generator emits Go the compiler accepts. When it
// does not, the error has to say the bug is the generator's -- not the view's.
func TestTheGeneratedGoParses(t *testing.T) {
	f, err := kyse.Parse("resources/views/home.kyse.go", homeSource)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	out, err := kyse.Generate(f, "home", "HomeData", "out.go")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	src := string(out)
	for _, want := range []string{
		"package views",
		"type HomeData struct",                         // the @go block came through
		`kyse__view.Register("home", renderHome)`,      // registered by name
		"kyse__d, kyse__ok := kyse__data.(HomeData)",   // typed data
		"kyse__view.WrongData",                         // the error when the type does not match
		"kyse__template.HTMLEscapeString",              // {{ }} escapes
		`kyse__view.RenderInto(kyse__w, "layouts.app"`, // @extends
		"for _, item := range kyse__d.Itens",           // @foreach
		"if kyse__d.Ativo {",                           // @if
		"DO NOT EDIT",                                  // generated
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

	out, err := kyse.Generate(f, "layouts.app", "LayoutData", "out.go")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(string(out), `kyse__view.Yield(kyse__w, kyse__sections, "content")`) {
		t.Errorf("o @yield nao virou Yield:\n%s", out)
	}
}

// TestTheErrorNamesTheFileAndTheLine is kyse's exit criterion.
//
// A heuristic would recompile the template with markers inserted and look for
// the nearest one above the compiled line. We emit the Go, so the position is
// exact.
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

// TestMarkupForaDeSection: in a view that extends a layout, loose markup would
// be written before the layout and would land outside the <html>.
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
	out, err := kyse.Generate(f, "branch", "D", "out.go")
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
	yes, no := strings.Index(got, `WriteString(kyse__w, "YES`), strings.Index(got, `WriteString(kyse__w, "NO`)
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

	out, err := kyse.Generate(f, "home", "", "out.go")
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
	if _, err := kyse.Generate(f, "page", "", "out.go"); err == nil {
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
	if _, err := kyse.Generate(f, "layouts.bare", "", "out.go"); err != nil {
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
	if _, err := kyse.Generate(f, "list", "D", "out.go"); err != nil {
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
	out, err := kyse.Generate(f, "stray", "D", "out.go")
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
// A closed set is only a promise if something walks it.
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
		"csrf":     {"@csrf\n", "kyse__view.CSRF("},
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
			out, err := kyse.Generate(f, name, "D", "out.go")
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
// @forelse is the one directive of the set that earns its keep in every
// generated index page: a list and its empty state are one thought, and writing
// them as @foreach beside @if(len(…) == 0) states the condition twice, in two
// places that can drift.
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
	out, err := kyse.Generate(f, "list", "D", "out.go")
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
	if !strings.Contains(got, "len(kyse__d.Items) == 0") {
		t.Errorf("the empty state is not decided by the length of the list:\n%s", got)
	}
}

// mappedLine answers where the Go compiler will say a line of the generated
// file came from, by applying the rule the compiler applies: a `//line f:n`
// directive names the position of the line right below it, and each line after
// that counts up from there until the next directive.
//
// It is deliberately a reimplementation and not a call into the generator. A
// test that asked the generator where it put things would agree with itself.
func mappedLine(generated string, contains string) (file string, line int, ok bool) {
	for _, out := range strings.Split(generated, "\n") {
		switch {
		case strings.HasPrefix(out, "//line "):
			ref := strings.TrimPrefix(out, "//line ")
			cut := strings.LastIndex(ref, ":")
			if cut < 0 {
				continue
			}
			file = ref[:cut]
			n, err := strconv.Atoi(ref[cut+1:])
			if err != nil {
				continue
			}
			// The directive names the next line, and the increment at the
			// bottom of this iteration is what gets us there.
			line = n - 1
		case strings.Contains(out, contains):
			return file, line, true
		}
		line++
	}
	return "", 0, false
}

// TestTheCompilerIsToldWhichLineOfTheViewEachExpressionCameFrom: if the error
// points at the generated `.go`, the engine is not ready.
//
// A type error inside {{ }} is the most common mistake in a view and the one
// the whole compiler exists to catch. Reporting it against generated Go -- a
// file whose header says DO NOT EDIT -- hands the person a position they cannot
// act on.
//
// A heuristic would recompile the template with markers and give up after a
// bounded number of lines. We emit the Go, so we know.
func TestTheCompilerIsToldWhichLineOfTheViewEachExpressionCameFrom(t *testing.T) {
	// Written with the line numbers visible, because they are the assertion.
	src := strings.Join([]string{
		"//go:build kyse",        // 1
		"",                       // 2
		"package views",          // 3
		"",                       // 4
		"@go",                    // 5
		"// D is the data.",      // 6 -- a doc comment, which is where this broke
		"type D struct {",        // 7
		"\tName  string",         // 8
		"\tItems []string",       // 9
		"}",                      // 10
		"@endgo",                 // 11
		"",                       // 12
		"<h1>{{ .Name }}</h1>",   // 13
		"@if(.Name != \"\")",     // 14
		"<p>{!! .Name !!}</p>",   // 15
		"@endif",                 // 16
		"@foreach(.Items as it)", // 17
		"<li>x</li>",             // 18
		"@endforeach",            // 19
	}, "\n") + "\n"

	f, err := kyse.Parse("resources/views/home.kyse.go", src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out, err := kyse.Generate(f, "home", "D", "resources/views/home.go")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	generated := string(out)

	for _, want := range []struct {
		what string
		mark string
		line int
	}{
		// The declaration, which is what a wrong field type reports against.
		// It sits under a doc comment on purpose: go/doc moves a directive to
		// the END of the comment it lands in, so a directive written above the
		// comment came out against the declaration and claimed the comment's
		// first line for it.
		{"the struct the @go block declares", "type D struct", 7},
		{"the escaped interpolation", "kyse__template.HTMLEscapeString", 13},
		{"the condition of @if", "if kyse__d.Name !=", 14},
		// Matched without the escape around it: the escaped form on line 13
		// ends in `kyse__view.Text(…)))` and would be found first.
		{"the raw interpolation", "kyse__io.WriteString(kyse__w, kyse__view.Text(", 15},
		{"the subject of @foreach", "range kyse__d.Items", 17},
	} {
		file, line, ok := mappedLine(generated, want.mark)
		if !ok {
			t.Errorf("%s: %q is not in the generated file:\n%s", want.what, want.mark, generated)
			continue
		}
		if file != "resources/views/home.kyse.go" || line != want.line {
			t.Errorf("%s: the compiler would say %s:%d, want resources/views/home.kyse.go:%d",
				want.what, file, line, want.line)
		}
	}

	// And the other half: what this generator wrote itself has to be handed
	// back, or one interpolation claims every line below it -- including lines
	// the view does not have.
	if file, _, ok := mappedLine(generated, "return kyse__err"); !ok || file != "resources/views/home.go" {
		t.Errorf("the scaffolding still belongs to the view: %s", file)
	}
}

// The compiled view goes under storage, mirroring the tree it came from.
//
// A compiled view is build output, and build output next to its source is a file
// people read diffs of and eventually edit -- this one opens with DO NOT EDIT
// and is overwritten on the next build.
func TestTheCompiledViewGoesUnderStorage(t *testing.T) {
	for _, c := range []struct{ source, want string }{
		{"resources/views/home.kyse.go", "storage/framework/views/home.go"},
		{"resources/views/auth/login.kyse.go", "storage/framework/views/auth/login.go"},
		{"resources/views/layouts/app.kyse.go", "storage/framework/views/layouts/app.go"},
		{"resources/views/auth/passwords/reset.kyse.go", "storage/framework/views/auth/passwords/reset.go"},
	} {
		if got := kyse.OutputPath(c.source); got != c.want {
			t.Errorf("OutputPath(%q) = %q, want %q", c.source, got, c.want)
		}
	}

	// A library keeps its views at its own root and has no resources/views to
	// mirror. It compiles in place, because there is no storage in a library and
	// the output is what `go get` serves.
	if got, want := kyse.OutputPath("components/button.kyse.go"), "components/button.go"; got != want {
		t.Errorf("a library: OutputPath = %q, want %q", got, want)
	}
}

// A layout that declares no interface of its own renders with view.Layout, the
// contract the native view package publishes.
//
// Without the default a layout whose @go block is empty -- which is every layout
// whose chrome comes from the native view package -- would assert the data to the empty
// string and generate Go that does not parse.
func TestALayoutWithoutAnInterfaceUsesTheNativeViewContract(t *testing.T) {
	const source = `//go:build kyse

package views

<html>
	<body>
		@yield('content')
	</body>
</html>
`
	file, err := kyse.Parse("layouts/app.kyse.go", source)
	if err != nil {
		t.Fatal(err)
	}
	if got := kyse.RenderType(file); got != "view.Layout" {
		t.Errorf("RenderType = %q, want view.Layout", got)
	}
}

// A component compiles to an exported Go function, and that is what makes it
// checked by the compiler rather than by the person.
//
// The alternative -- looking the component up by name and asserting its props at
// run time -- reaches production for a typo, which is the failure this whole
// project exists to make impossible. A component is the last place that should
// be reintroduced, so this test pins the shape.
func TestAComponentCompilesToATypedFunction(t *testing.T) {
	const source = `//go:build kyse

package components

@go
type ButtonProps struct {
	Label   string
	Variant string
}
@endgo

<button class="btn" data-variant="{{ .Variant }}">{{ .Label }}</button>
`
	file, err := kyse.Parse("resources/views/components/button.kyse.go", source)
	if err != nil {
		t.Fatal(err)
	}
	out, err := kyse.Generate(file, "components.button", "ButtonProps", "resources/views/components/button.go")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	// Exported, taking its props, returning HTML that can be interpolated.
	if !strings.Contains(got, "func Button(kyse__props ButtonProps) kyse__template.HTML {") {
		t.Errorf("a component is not an exported typed function:\n%s", got)
	}
	// Not registered: nothing looks a component up by name, so there is no name
	// to get wrong.
	if strings.Contains(got, "view.Register") {
		t.Error("a component registered itself by name, which is the string lookup this shape replaces")
	}
	// The props are still read as the page data is, so every directive keeps
	// working.
	if !strings.Contains(got, "kyse__d.Label") {
		t.Errorf("the component does not read its props:\n%s", got)
	}
}

// The name of the function comes from the file, and a hyphen is a word break.
func TestAComponentIsNamedAfterItsFile(t *testing.T) {
	const source = `//go:build kyse

package components

<hr>
`
	file, err := kyse.Parse("resources/views/components/theme-toggle.kyse.go", source)
	if err != nil {
		t.Fatal(err)
	}
	out, err := kyse.Generate(file, "components.theme-toggle", "", "resources/views/components/theme-toggle.go")
	if err != nil {
		t.Fatal(err)
	}
	// No props declared, so the function takes none rather than an empty struct.
	if !strings.Contains(string(out), "func ThemeToggle() kyse__template.HTML {") {
		t.Errorf("wrong function name or signature:\n%s", out)
	}
}

// A comment spans lines, and none of it reaches the page.
//
// An interpolator that reads a line at a time cannot see the closing delimiter
// on the next, so a three-line note about why the markup is the way it is comes
// back reported as never closed. The note that runs to three lines is the one
// worth writing.
func TestACommentSpansLines(t *testing.T) {
	const source = `//go:build kyse

package views

<p>before</p>
{{-- why this is here:
     because of a thing,
     and another thing. --}}
<p>after</p>
<td>{{-- inline --}}kept</td>
`
	file, err := kyse.Parse("home.kyse.go", source)
	if err != nil {
		t.Fatal(err)
	}
	out, err := kyse.Generate(file, "home", "", "home.go")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	for _, gone := range []string{"why this is here", "another thing", "inline"} {
		if strings.Contains(got, gone) {
			t.Errorf("the comment reached the page: %q is in the output", gone)
		}
	}
	for _, kept := range []string{"before", "after", "kept"} {
		if !strings.Contains(got, kept) {
			t.Errorf("the markup around the comment was eaten: %q is missing\n%s", kept, got)
		}
	}
}

// A view can name a type the controller owns, with an alias.
//
// The struct belongs with the code that fills it: that is where the fields are
// set and where a missing one is a compile error. Repeating it in the view would
// be two declarations of one shape, kept in step by hand -- and the view still
// says which type it draws, in the one line that makes {{ .Email }} compile.
func TestAViewCanAliasATypeItDoesNotOwn(t *testing.T) {
	const source = `//go:build kyse

package auth

import authhttp "example.com/app/Http/Controllers/Auth"

@go
type LoginData = authhttp.LoginData
@endgo

<p>{{ .Email }}</p>
`
	file, err := kyse.Parse("resources/views/auth/login.kyse.go", source)
	if err != nil {
		t.Fatal(err)
	}
	if got := kyse.PageType(file); got != "LoginData" {
		t.Fatalf("PageType = %q, want LoginData", got)
	}

	out, err := kyse.Generate(file, "auth.login", kyse.RenderType(file), "resources/views/auth/login.go")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "kyse__d, kyse__ok := kyse__data.(LoginData)") {
		t.Errorf("the view does not assert the aliased type:\n%s", got)
	}
	if !strings.Contains(got, `authhttp "example.com/app/Http/Controllers/Auth"`) {
		t.Errorf("the import the alias needs did not reach the output:\n%s", got)
	}
}

// An interpolation spans lines, because a component call with six props does
// not fit on one.
//
// The lines are joined into a Go expression and gofmt lays it out again in the
// generated file. The view's line stays the one it opened on, which is where a
// type error inside it belongs.
func TestAnInterpolationSpansLines(t *testing.T) {
	const source = `//go:build kyse

package views

@go
type D struct{ Email string }
@endgo

<p>{{ .Email }} and {{
	.Email }}</p>
{!! components.Field(components.FieldProps{
	Name:  "email",
	Value: .Email,
}) !!}
<p>after</p>
`
	file, err := kyse.Parse("home.kyse.go", source)
	if err != nil {
		t.Fatal(err)
	}
	out, err := kyse.Generate(file, "home", "D", "home.go")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	if !strings.Contains(got, `components.Field(components.FieldProps{Name: "email", Value: kyse__d.Email})`) {
		t.Errorf("the component call was not folded into one expression:\n%s", got)
	}
	if !strings.Contains(got, "after") {
		t.Errorf("the markup after the interpolation was eaten:\n%s", got)
	}
}

// TestAViewWithNoLayoutKeepsItsBlankLines.
//
// The section-based case drops them, correctly: whitespace between @extends and
// @section is not content. Doing the same to a view with no layout is invisible
// in HTML -- a browser collapses whitespace, so every page still looked right --
// and destroys a plain-text e-mail, where the paragraph breaks are the only
// structure there is.
//
// The failure that found it: a URL alone on a line came out glued to the first
// word of the next sentence, so every mail client that autolinks produced a link
// with three extra characters on the end of the token.
func TestAViewWithNoLayoutKeepsItsBlankLines(t *testing.T) {
	f, err := kyse.Parse("resources/views/mail/note.kyse.go", `//go:build kyse

package mail

Confirm your address:
{{ .Link }}

The link works for 24 hours.
`)
	if err != nil {
		t.Fatalf("the view did not parse: %v", err)
	}
	generated, err := kyse.Generate(f, "mail.note", "NoteData", "out.go")
	if err != nil {
		t.Fatalf("the view did not compile: %v", err)
	}
	out := string(generated)

	// The blank line survives as a newline of its own, so the link and the
	// sentence after it are not on the same line.
	if !strings.Contains(out, `"\n"`) {
		t.Errorf("the blank line was dropped: a link on its own line ends up glued to the next sentence\n%s", out)
	}
}

// TestAViewWithALayoutStillRefusesMarkupOutsideASection: the blank-line fix must
// not turn the mistake it sits next to into silence.
func TestAViewWithALayoutStillRefusesMarkupOutsideASection(t *testing.T) {
	_, err := kyse.Parse("resources/views/home.kyse.go", `//go:build kyse

package views

@extends('layouts.app')

<p>this is outside every section</p>

@section('content')
	<p>this one is fine</p>
@endsection
`)
	if err == nil {
		t.Fatal("markup outside a section was accepted: it renders before <html>")
	}
	if !strings.Contains(err.Error(), "outside any @section") {
		t.Errorf("the error does not say what is wrong: %v", err)
	}
}

// The escape a value gets is chosen by where the value lands in the document.
//
// One escape everywhere is what this replaces, and it is wrong in both
// directions at once. The six characters that make a value inert in the body of
// an element do nothing to a scheme, which is dangerous for what it means; and
// inside an attribute whose value is a script they are decoded back into the
// characters they escaped before the script is read, so the escape hands the
// quote to the string it was meant to sit inside.
//
// The order is the assertion. A test that only asked whether each name appears
// somewhere would pass on a generator that chose them by counting.
func TestTheEscapeIsChosenByPosition(t *testing.T) {
	const source = `//go:build kyse

package views

@go
type D struct{ V string }
@endgo

<p>{{ .V }}</p>
<div class="{{ .V }}">x</div>
<a href="{{ .V }}">x</a>
<a href="/posts/{{ .V }}">x</a>
<button hx-get="{{ .V }}">x</button>
<div style="color: {{ .V }}">x</div>
<style>.a { color: {{ .V }} }</style>
<script>var n = {{ .V }};</script>
<input type="checkbox" {{ .V }}>
`
	file, err := kyse.Parse("resources/views/home.kyse.go", source)
	if err != nil {
		t.Fatal(err)
	}
	out, err := kyse.Generate(file, "home", "D", "storage/framework/views/home.go")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"body", // <p>{{ }}</p>
		"attr", // class="{{ }}"
		"url",  // href="{{ }}" -- the front of the value, where a scheme goes
		"attr", // href="/posts/{{ }}" -- the view already settled the scheme
		"url",  // hx-get="{{ }}" -- an address to the HTML parser is an attribute
		"css",  // style="color: {{ }}"
		"css",  // inside <style>
		"js",   // inside <script>
		"name", // where the name of an attribute goes
	}
	if got := escapersIn(string(out)); !reflect.DeepEqual(got, want) {
		t.Errorf("the escapes chosen are\n%v\nand the positions ask for\n%v\n%s", got, want, out)
	}
}

// escapersIn reads back which escape the generator chose, in the order it wrote
// them.
func escapersIn(generated string) []string {
	marks := []struct{ call, name string }{
		{"kyse__template.HTMLEscapeString(kyse__view.Text(", "body"},
		{"kyse__view.TextAttr(", "attr"},
		{"kyse__view.TextURL(", "url"},
		{"kyse__view.TextJS(", "js"},
		{"kyse__view.TextCSS(", "css"},
		// The one position with no escape: the value is examined instead.
		{"kyse__strings.ContainsAny(", "name"},
	}

	var out []string
	for at := 0; at < len(generated); {
		next, name := len(generated), ""
		for _, m := range marks {
			if i := strings.Index(generated[at:], m.call); i >= 0 && at+i < next {
				next, name = at+i+len(m.call), m.name
			}
		}
		if name == "" {
			return out
		}
		out = append(out, name)
		at = next
	}
	return out
}

// A position with no escape at all refuses the view, and says which position.
//
// Refusing at build time rather than at render time is the difference between
// these and the name of an attribute: there the value decides, and a page
// writing `checked` there works. In these four no value is safe, so waiting for
// one is waiting for nothing.
func TestAPositionWithNoEscapeRefusesTheView(t *testing.T) {
	for _, c := range []struct {
		what, markup, says string
		line               int
	}{
		{
			"an attribute value with no quotes",
			`<input value={{ .V }}>`,
			"has no quotes around it", 9,
		},
		{
			"an attribute whose value is a script",
			`<button onclick="f({{ .V }})">x</button>`,
			"is a script rather than text", 9,
		},
		{
			"an attribute of the client library, whose value is an expression",
			`<span x-text="{{ .V }}"></span>`,
			"is a script rather than text", 9,
		},
		{
			"the name of an element",
			`<{{ .V }} class="a">x`,
			"the name of an element goes", 9,
		},
		{
			"a tag left open by one branch of a block",
			"@if(.V != \"\")\n<span title=\"\n@endif\n{{ .V }}",
			"is not known here", 12,
		},
		{
			"markup this compiler cannot read, written inside a tag",
			`<div class="{!! .V !!}">{{ .V }}</div>`,
			"is not known here", 9,
		},
	} {
		t.Run(c.what, func(t *testing.T) {
			source := "//go:build kyse\n\npackage views\n\n@go\ntype D struct{ V string }\n@endgo\n\n" + c.markup + "\n"
			file, err := kyse.Parse("resources/views/home.kyse.go", source)
			if err != nil {
				t.Fatal(err)
			}
			out, err := kyse.Generate(file, "home", "D", "storage/framework/views/home.go")
			if err == nil {
				t.Fatalf("the view compiled, and the position it writes into has no escape:\n%s", out)
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("the refusal does not say which position: %v", err)
			}

			// A refusal nobody can act on is a refusal nobody acts on.
			var positioned *kyse.Error
			if !errors.As(err, &positioned) {
				t.Fatalf("the refusal carries no position: %v", err)
			}
			if positioned.Path != "resources/views/home.kyse.go" || positioned.Line != c.line {
				t.Errorf("the refusal points at %s:%d, and the view wrote it at resources/views/home.kyse.go:%d",
					positioned.Path, positioned.Line, c.line)
			}
			if positioned.Hint == "" {
				t.Error("the refusal says what is wrong and not what to do about it")
			}
		})
	}
}

// A refusal that happens at render time names the view that asked for the value.
//
// Two of the escapes answer with an error rather than a string, because what
// they refuse is data: a URL whose scheme a browser acts on, and a style value
// carrying a rule of its own. Neither can be decided at build time, and neither
// may stop the process -- a visitor would take the page down with what they
// typed. What is left is an error, and an error that does not name the view
// leaves somebody grepping a tree of generated files.
func TestARefusedValueNamesTheViewAndTheLine(t *testing.T) {
	const source = `//go:build kyse

package views

@go
type D struct{ V string }
@endgo

<a href="{{ .V }}">x</a>
`
	file, err := kyse.Parse("resources/views/home.kyse.go", source)
	if err != nil {
		t.Fatal(err)
	}
	out, err := kyse.Generate(file, "home", "D", "storage/framework/views/home.go")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	if !strings.Contains(got, `"resources/views/home.kyse.go:9"`) {
		t.Errorf("the refusal does not carry the view's position:\n%s", got)
	}
	// The value is written only when it was not refused. Writing it either way
	// is how an escape that refuses becomes an escape that reports.
	if !strings.Contains(got, "if kyse__err != nil {") ||
		!strings.Contains(got, "_, kyse__err = kyse__io.WriteString(kyse__w, kyse__v") {
		t.Errorf("the refused value is written anyway:\n%s", got)
	}
}

// Markup that balances inside a block leaves the position where it found it.
//
// Without this every component that writes a bare attribute inside an @if would
// give up the position for the rest of the file, and a file that has given up
// refuses every interpolation after it. The block has to be read, not feared.
func TestABalancedBlockKeepsThePosition(t *testing.T) {
	const source = `//go:build kyse

package views

@go
type D struct{ V string }
@endgo

<a class="btn @if(.V != "")on@endif" href="{{ .V }}">x</a>
@foreach(.V as row)
	<li>{{ row }}</li>
@endforeach
<p>{{ .V }}</p>
`
	file, err := kyse.Parse("resources/views/home.kyse.go", source)
	if err != nil {
		t.Fatal(err)
	}
	out, err := kyse.Generate(file, "home", "D", "storage/framework/views/home.go")
	if err != nil {
		t.Fatalf("a view whose markup balances was refused: %v", err)
	}

	want := []string{"url", "body", "body"}
	if got := escapersIn(string(out)); !reflect.DeepEqual(got, want) {
		t.Errorf("the escapes chosen are\n%v\nand the positions ask for\n%v\n%s", got, want, out)
	}
}

// What a directive writes is another file's markup, and it is refused inside a
// tag rather than followed.
//
// The two together are one contract, and it is what lets a section be compiled
// on its own: a @yield writes a section into the body of an element, and the
// section gives the position back where it found it. Broken from either side,
// the file that renders wrong is the other one -- which contains no mistake and
// shows nothing on being read, because the two are compiled separately and
// neither ever sees the other.
func TestASectionAndItsYieldMeetInTheBodyOfAnElement(t *testing.T) {
	t.Run("a @yield inside a tag", func(t *testing.T) {
		const source = `//go:build kyse

package views

<body
@yield('attributes')
>
</body>
`
		file, err := kyse.Parse("resources/views/layouts/app.kyse.go", source)
		if err != nil {
			t.Fatal(err)
		}
		_, err = kyse.Generate(file, "layouts.app", kyse.RenderType(file), "storage/framework/views/layouts/app.go")
		if err == nil {
			t.Fatal("a @yield inside a tag was accepted: it decides the position of a file it never sees")
		}
		if !strings.Contains(err.Error(), "@yield is written inside a tag") {
			t.Errorf("the refusal does not say what is wrong: %v", err)
		}
	})

	t.Run("a section that leaves a tag open", func(t *testing.T) {
		const source = `//go:build kyse

package views

@extends('layouts.app')

@section('content')
	<div class="
@endsection
`
		file, err := kyse.Parse("resources/views/home.kyse.go", source)
		if err != nil {
			t.Fatal(err)
		}
		_, err = kyse.Generate(file, "home", "view.Page", "storage/framework/views/home.go")
		if err == nil {
			t.Fatal("a section that leaves a tag open was accepted: it moves the layout's position by markup the layout never sees")
		}
		if !strings.Contains(err.Error(), "leaves a tag open") {
			t.Errorf("the refusal does not say what is wrong: %v", err)
		}
	})

	t.Run("a section that closes what it opens", func(t *testing.T) {
		const source = `//go:build kyse

package views

@extends('layouts.app')

@section('content')
	<div class="a"><p>x</p></div>
@endsection
`
		file, err := kyse.Parse("resources/views/home.kyse.go", source)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := kyse.Generate(file, "home", "view.Page", "storage/framework/views/home.go"); err != nil {
			t.Fatalf("a section whose markup balances was refused: %v", err)
		}
	})
}
