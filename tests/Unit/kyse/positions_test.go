package kyse_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/kyse"
)

// escapeCall matches each escape the generator writes around an interpolation,
// longest form first so a JSON escape is not read as the two calls inside it.
var escapeCall = regexp.MustCompile(
	`kyse__view\.TextAttr\(kyse__view\.TextJS\(` + // json
		`|kyse__view\.TextJS\(kyse__view\.Text\(` + // json string
		`|kyse__template\.HTMLEscapeString\(kyse__view\.Text\(` + // body
		`|kyse__view\.TextAttr\(kyse__d\.` + // attr
		`|kyse__view\.TextURL\(` + // url
		`|kyse__view\.TextJS\(kyse__d\.` + // js
		`|kyse__view\.TextCSS\(` + // css
		`|kyse__strings\.ContainsAny\(kyse__v\d+, "\[\]"\)` + // trigger
		`|kyse__strings\.ContainsAny\(`) // name

// escapesOf names the escapes in generated Go, in the order they are written.
func escapesOf(generated string) []string {
	names := map[string]string{
		"kyse__view.TextAttr(kyse__view.TextJS(":           "json",
		"kyse__view.TextJS(kyse__view.Text(":               "jsonstring",
		"kyse__template.HTMLEscapeString(kyse__view.Text(": "body",
		"kyse__view.TextAttr(kyse__d.":                     "attr",
		"kyse__view.TextURL(":                              "url",
		"kyse__view.TextJS(kyse__d.":                       "js",
		"kyse__view.TextCSS(":                              "css",
		"kyse__strings.ContainsAny(":                       "name",
	}
	var out []string
	for _, m := range escapeCall.FindAllString(generated, -1) {
		if strings.HasSuffix(m, `"[]")`) {
			out = append(out, "trigger")
			continue
		}
		out = append(out, names[m])
	}
	return out
}

// Every position the HTML syntax, HTMX and SVG give an attribute value, and the
// escape or refusal each one gets.
//
// The table is what a probe of the compiler found, turned into assertions. The
// rows marked as refusals are the ones that used to compile to an escape that
// does not hold there: HTMX reads every attribute through a data- alias, so
// data-hx-post is fetched and data-hx-on runs; a trigger filter, srcdoc, the
// content of a meta http-equiv and an SVG animation are all acted on; and an
// address whose front a value can still finish is one the URL check never
// reads whole.
func TestEveryAttributePositionTakesItsEscapeOrIsRefused(t *testing.T) {
	const (
		script  = "is a script rather than text"
		acts    = "which the browser acts on rather than displays"
		open    = "before the scheme and the host of the address are fixed"
		lateMet = "http-equiv is written after a value"
	)
	for _, c := range []struct {
		what, markup string
		want         []string
		refused      string
	}{
		{"href", `<a href="{{ .U }}">x</a>`, []string{"url"}, ""},
		{"hx-get", `<div hx-get="{{ .U }}"></div>`, []string{"url"}, ""},
		{"data-hx-get", `<div data-hx-get="{{ .U }}"></div>`, []string{"url"}, ""},
		{"data-hx-post", `<button data-hx-post="{{ .U }}"></button>`, []string{"url"}, ""},
		{"data-hx-delete", `<button data-hx-delete="{{ .U }}"></button>`, []string{"url"}, ""},
		{"hx-push-url", `<div hx-push-url="{{ .U }}"></div>`, []string{"url"}, ""},
		{"data-hx-replace-url", `<div data-hx-replace-url="{{ .U }}"></div>`, []string{"url"}, ""},
		{"formaction", `<button formaction="{{ .U }}"></button>`, []string{"url"}, ""},
		{"a ping", `<a ping="{{ .U }}" href="/">x</a>`, []string{"url"}, ""},
		{"object data", `<object data="{{ .U }}"></object>`, []string{"url"}, ""},
		{"embed src", `<embed src="{{ .U }}">`, []string{"url"}, ""},
		{"form action", `<form action="{{ .U }}"></form>`, []string{"url"}, ""},
		{"base href", `<base href="{{ .U }}">`, []string{"url"}, ""},
		{"a space written as a reference before the address", `<a href="&#32;{{ .U }}">x</a>`, []string{"url"}, ""},
		{"a tab before the address", "<a href=\"\t{{ .U }}\">x</a>", []string{"url"}, ""},

		{"a path the view wrote", `<a href="/posts/{{ .U }}">x</a>`, []string{"attr"}, ""},
		{"a path from a checked address", `<a href="{{ .U }}/items/{{ .V }}">x</a>`, []string{"url", "attr"}, ""},
		{"a query after a checked address", `<div hx-get="{{ .U }}?page={{ .V }}"></div>`, []string{"url", "attr"}, ""},
		{"a fragment", `<a href="#{{ .U }}">x</a>`, []string{"attr"}, ""},
		{"a query", `<a href="?cursor={{ .U }}">x</a>`, []string{"attr"}, ""},
		{"a scheme the view wrote", `<a href="mailto:{{ .U }}">x</a>`, []string{"attr"}, ""},
		{"a host the view wrote", `<img src="//cdn.example/{{ .U }}">`, []string{"attr"}, ""},
		{"srcset holds several addresses and is not one of them", `<link rel="preload" imagesrcset="{{ .U }}">`, []string{"attr"}, ""},

		{"two values at the front", `<a href="{{ .U }}{{ .V }}">x</a>`, nil, open},
		{"a value behind a scheme that is not finished", `<a href="j{{ .U }}">x</a>`, nil, open},
		{"a value behind a lone slash", `<script src="/{{ .U }}"></script>`, nil, open},
		{"a value behind a slash after a space", `<a href=" /{{ .U }}">x</a>`, nil, open},
		{"a scheme two values write together", `<a href="{{ .U }}:{{ .V }}">x</a>`, nil, open},
		{"a value inside a javascript address", `<a href="javascript:show('{{ .Q }}')">x</a>`, nil, script},
		{"a javascript scheme spelled with references", `<a href="&#x6a;ava&#9;script:{{ .Q }}">x</a>`, nil, script},

		{"hx-on", `<div hx-on:click="{{ .U }}"></div>`, nil, script},
		{"data-hx-on", `<div data-hx-on:click="{{ .U }}"></div>`, nil, script},
		{"data-hx-on with dashes", `<div data-hx-on--after-request="{{ .U }}"></div>`, nil, script},
		{"hx-trigger", `<div hx-trigger="{{ .Q }}"></div>`, []string{"trigger"}, ""},
		{"data-hx-trigger after a closed filter", `<div data-hx-trigger="click[ctrlKey] from:{{ .Q }}"></div>`, []string{"trigger"}, ""},
		{"hx-trigger inside a filter", `<div hx-trigger="keyup[key=='{{ .Q }}']"></div>`, nil, script},
		{"data-hx-trigger", `<div data-hx-trigger="click[{{ .Q }}]"></div>`, nil, script},
		{"hx-vars", `<div hx-vars="q:{{ .Q }}"></div>`, nil, script},
		{"data-hx-vars", `<div data-hx-vars="q:{{ .Q }}"></div>`, nil, script},

		{"hx-vals", `<div hx-vals='{"q": "{{ .Q }}"}'></div>`, []string{"jsonstring"}, ""},
		{"data-hx-vals", `<div data-hx-vals='{"q": "{{ .Q }}"}'></div>`, []string{"jsonstring"}, ""},
		{"data-hx-headers", `<div data-hx-headers='{"X-A": {{ .Q }}}'></div>`, []string{"json"}, ""},
		{"data-hx-vals evaluated", `<div data-hx-vals='js:{q: {{ .Q }}}'></div>`, nil, script},

		{"srcdoc", `<iframe srcdoc="{{ .Q }}"></iframe>`, nil, acts},
		{"the content of a meta refresh", `<meta http-equiv="refresh" content="0;url={{ .U }}">`, nil, acts},
		{"http-equiv after the content", `<meta content="0;url={{ .U }}" http-equiv="refresh">`, nil, lateMet},
		{"the content of an ordinary meta", `<meta name="description" content="{{ .Q }}">`, []string{"attr"}, ""},
		{"an SVG animation of an address", `<svg><a><animate attributeName="href" to="{{ .U }}"/></a></svg>`, nil, acts},
		{"an SVG set", `<svg><set attributeName="href" to="{{ .U }}"/></svg>`, nil, acts},
		{"the attribute an SVG animation writes", `<svg><animate attributeName="{{ .Q }}" to="1"/></svg>`, nil, acts},

		{"script", `<script>var a = {{ .Q }};</script>`, []string{"js"}, ""},
		{"style attribute", `<div style="color: {{ .Q }}"></div>`, []string{"css"}, ""},
		{"style element in SVG", `<svg><style>{{ .Q }}</style></svg>`, []string{"css"}, ""},
		{"textarea", `<textarea>{{ .Q }}</textarea>`, []string{"body"}, ""},
		{"title", `<title>{{ .Q }}</title>`, []string{"body"}, ""},
		{"attribute name", `<div {{ .Q }}></div>`, []string{"name"}, ""},
		{"an ordinary data attribute", `<div data-src="{{ .Q }}" data-id="{{ .Q }}"></div>`, []string{"attr", "attr"}, ""},
	} {
		t.Run(c.what, func(t *testing.T) {
			out, err := generateProbe(t, c.markup)
			if c.refused != "" {
				if err == nil {
					t.Fatalf("compiled with %v, and the position has no escape that holds:\n%s", escapesOf(out), out)
				}
				if !strings.Contains(err.Error(), c.refused) {
					t.Errorf("the refusal does not say why: %v", err)
				}
				var positioned *kyse.Error
				if !errors.As(err, &positioned) || positioned.Line != 9 || positioned.Hint == "" {
					t.Errorf("the refusal does not point at the line with a hint: %#v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if got := escapesOf(out); !reflect.DeepEqual(got, c.want) {
				t.Errorf("the escapes chosen are %v, and the position asks for %v\n%s", got, c.want, out)
			}
		})
	}
}

// A branch inside an address that fixes the front in one arm and not the other
// leaves the position unknown, and the value after it is refused.
func TestABranchThatDecidesTheAddressInOneArmIsRefused(t *testing.T) {
	_, err := generateProbe(t, "<a href=\"\n@if(.V != \"\")\n/x/\n@endif\n{{ .U }}\">x</a>")
	if err == nil {
		t.Fatal("compiled, and the address is fixed in one arm of the branch only")
	}
	if !strings.Contains(err.Error(), "is not known here") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

// An HTMX trigger takes the events a component listens for, and refuses at
// render time a value that would open a filter -- the part of a trigger HTMX
// evaluates as a script.
func TestATriggerRefusesAFilterAtRenderTime(t *testing.T) {
	root := t.TempDir()
	writeRuntimeModule(t, root)
	compileView(t, root, "views", "trigger", "", `//go:build kyse

package views

@go
type D struct{ Q string }
@endgo

<div hx-trigger="{{ .Q }}"></div>
`)
	writeFile(t, filepath.Join(root, "main.go"), `package main

import (
	"fmt"
	"strings"

	"github.com/arandu-io/hesape/view"

	"example.test/render/views"
)

func main() {
	for _, q := range []string{"input changed delay:300ms, search", "click[alert(document.domain)]", "keyup] from:body"} {
		var page strings.Builder
		err := view.Render(&page, "trigger", views.D{Q: q})
		fmt.Printf("%q %v\n", strings.TrimSpace(page.String()), err != nil)
	}
}
`)

	got := runModule(t, root)
	// A page that fails is discarded whole by the renderer, so what matters is
	// the error and that the value was never written.
	want := `"<div hx-trigger=\"input changed delay:300ms, search\"></div>" false` + "\n" +
		`"<div hx-trigger=\"" true` + "\n" +
		`"<div hx-trigger=\"" true` + "\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(got, "alert") || strings.Contains(got, "keyup]") {
		t.Errorf("a trigger holding a filter reached the page:\n%s", got)
	}
}

// An interpolation inside a branch of an ordinary attribute keeps the position,
// because only an address and a JSON value keep a front that could tell the two
// arms apart.
func TestABranchInsideAnOrdinaryAttributeKeepsThePosition(t *testing.T) {
	out, err := generateProbe(t, "<div class=\"btn\n@if(.V != \"\")\n{{ .V }}\n@endif\n\">{{ .Q }}</div>")
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	if got, want := escapesOf(out), []string{"attr", "body"}; !reflect.DeepEqual(got, want) {
		t.Errorf("the escapes chosen are %v, and the positions ask for %v", got, want)
	}
}
