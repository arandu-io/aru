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

// TestTheGeneratedGoParses: o gerador emite Go que o compilador aceita. Se ele
// nao emitir, o erro tem que dizer que o bug e do gerador — nao da view.
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
		"view.WrongData",                    // e o erro quando nao bate
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

// TestTheLayoutYields: um layout nao estende ninguem e tem @yield.
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

// TestOErroDizArquivoELinha é o critério de saída do kyse.
//
// O Laravel resolve isso com heurística: o BladeMapper recompila o template
// inserindo marcadores, procura o mais próximo acima da linha compilada e
// desiste após vinte linhas. Nós emitimos o Go, então a posição é exata.
func TestOErroDizArquivoELinha(t *testing.T) {
	casos := []struct {
		nome  string
		fonte string
		linha int
		diz   string
	}{
		{
			nome: "@section sem @endsection",
			fonte: `//go:build kyse

package views

@section('content')
    <h1>oi</h1>
`,
			linha: 5,
			diz:   "@section",
		},
		{
			nome: "diretiva que nao existe",
			fonte: `//go:build kyse

package views

@secton('content')
@endsection
`,
			linha: 5,
			diz:   "@secton",
		},
		{
			nome: "{{ sem fechar",
			fonte: `//go:build kyse

package views

<h1>{{ .Nome</h1>
`,
			linha: 5,
			diz:   "{{",
		},
		{
			nome: "sem a tag de build",
			fonte: `package views

<h1>oi</h1>
`,
			linha: 1,
			diz:   "//go:build kyse",
		},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			_, err := kyse.Parse("resources/views/home.kyse.go", c.fonte)
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

// TestTodosOsProblemasDeUmaVez: quem conserta uma view nao deve descobrir os
// erros um build por vez. Mesma razao do validador de spec.
func TestTodosOsProblemasDeUmaVez(t *testing.T) {
	fonte := `//go:build kyse

package views

@secton('a')
@endsection

<h2>{{ .Sem fechar</h2>

@blah
`
	_, err := kyse.Parse("resources/views/x.kyse.go", fonte)
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
	fonte := `//go:build kyse

package views

@extends('layouts.app')

<h1>isto fica fora da pagina</h1>

@section('content')
@endsection
`
	_, err := kyse.Parse("resources/views/x.kyse.go", fonte)
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
