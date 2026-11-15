package kyse_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/kyse"
)

// Every attribute of the YPY table whose value is acted on is read by the
// compiler as what it is, under each spelling YPY resolves: the plain name, the
// data- alias and the inheritance suffixes.
//
// The names come from the table the YPY command-line tool generates its editor
// data from, grouped by what the value is: an address that is fetched or
// connected to, an expression that runs, and a list of triggers whose filters
// run. A name missing here is a name the compiler escapes as inert text.
func TestEveryYPYAttributeIsClassifiedByWhatItsValueIs(t *testing.T) {
	spellings := func(name string) []string {
		out := []string{name, "data-" + name}
		if !strings.Contains(name, ":inherited") {
			out = append(out, name+":inherited", name+":append", name+":inherited:append")
		}
		return out
	}

	addresses := []string{
		"ypy-get", "ypy-post", "ypy-put", "ypy-patch", "ypy-delete", "ypy-query", "ypy-action",
		"ypy-push-url", "ypy-replace-url", "ypy-lazy", "ypy-validate", "ypy-validate-fields",
		"ypy-stream", "ypy-stream.post", "ypy-stream.put", "ypy-stream.patch", "ypy-stream.delete",
		"ypy-sse:connect", "ypy-sse-connect", "ypy-ws:connect", "ypy-ws-connect", "ypy-multipart:connect",
	}
	for _, name := range addresses {
		for _, attr := range spellings(name) {
			t.Run("address "+attr, func(t *testing.T) {
				out, err := generateProbe(t, `<div `+attr+`="{{ .U }}"></div>`)
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				if got, want := escapesOf(out), []string{"url"}; !reflect.DeepEqual(got, want) {
					t.Errorf("the escapes chosen are %v, and an address asks for %v", got, want)
				}
			})
		}
	}

	expressions := []string{
		"ypy-on:click", "ypy-on::after-swap", "ypy-on:keydown", "ypy-live", "ypy-live:text", "ypy-live:?disabled",
		"ypy-signals", "ypy-signals.server", "ypy-signals.local", "ypy-signals.session", "ypy-computed:total",
		"ypy-text", "ypy-show", "ypy-class", "ypy-class:red", "ypy-attr", "ypy-attr:href",
	}
	for _, name := range expressions {
		for _, attr := range spellings(name) {
			t.Run("expression "+attr, func(t *testing.T) {
				_, err := generateProbe(t, `<div `+attr+`="{{ .Q }}"></div>`)
				if err == nil {
					t.Fatal("compiled, and the value lands in an expression")
				}
				if !strings.Contains(err.Error(), "is a script rather than text") {
					t.Errorf("the refusal does not say why: %v", err)
				}
			})
		}
	}

	triggers := []string{"ypy-trigger", "ypy-validate-trigger", "ypy-multipart:close"}
	for _, name := range triggers {
		for _, attr := range spellings(name) {
			t.Run("trigger "+attr, func(t *testing.T) {
				out, err := generateProbe(t, `<div `+attr+`="{{ .Q }}"></div>`)
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				if got, want := escapesOf(out), []string{"trigger"}; !reflect.DeepEqual(got, want) {
					t.Errorf("the escapes chosen are %v, and a trigger asks for %v", got, want)
				}
				_, err = generateProbe(t, `<div `+attr+`="keyup[key=='{{ .Q }}']"></div>`)
				if err == nil || !strings.Contains(err.Error(), "is a script rather than text") {
					t.Errorf("a value inside a filter is not refused as a script: %v", err)
				}
			})
		}
	}
}

// An address the view opens with js: is run by YPY as a script, which no
// browser does for an address, so it is a script to the compiler as well.
func TestAnAddressOpenedWithJSIsAScript(t *testing.T) {
	for _, attr := range []string{"ypy-get", "data-ypy-post", "ypy-action:inherited"} {
		t.Run(attr, func(t *testing.T) {
			_, err := generateProbe(t, `<div `+attr+`="js:fetch('{{ .Q }}')"></div>`)
			if err == nil || !strings.Contains(err.Error(), "is a script rather than text") {
				t.Errorf("the value inside a script address is not refused: %v", err)
			}
		})
	}
}

// YPY reads ypy-vals, ypy-headers and ypy-config as one JSON object only when
// the value opens with a brace and nothing before it, and as a list of pairs
// when it does not. A JSON escape keeps a value one string, and a pair ends at
// the first quote inside it, so every position where how the value begins is
// not settled by the view is refused.
func TestAnObjectAttributeIsReadAsPairsUnlessTheViewOpensABrace(t *testing.T) {
	for _, name := range []string{"ypy-vals", "ypy-headers", "ypy-config", "data-ypy-vals", "ypy-vals:inherited"} {
		t.Run(name+" with a brace", func(t *testing.T) {
			out, err := generateProbe(t, `<div `+name+`='{"q": "{{ .Q }}", "n": {{ .U }}}'></div>`)
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if got, want := escapesOf(out), []string{"jsonstring", "json"}; !reflect.DeepEqual(got, want) {
				t.Errorf("the escapes chosen are %v, and the positions ask for %v", got, want)
			}
		})
	}

	const ambiguous = "where how the value begins decides how it is read"
	for _, c := range []struct {
		what, markup string
	}{
		{"a whole value, which JSON escapes into a string and a pair reads as a key", `<div ypy-vals="{{ .Q }}"></div>`},
		{"a whole value in a configuration", `<div data-ypy-config:inherited="{{ .Q }}"></div>`},
		{"pairs", `<div ypy-vals="q:{{ .Q }}"></div>`},
		{"pairs with a quoted value", `<div ypy-headers="X-A:'{{ .Q }}'"></div>`},
		{"a space before the brace", `<div ypy-vals=' {"q": "{{ .Q }}"}'></div>`},
		{"a prefix that is not finished", `<div ypy-vals="j{{ .Q }}"></div>`},
		{"the append spelling", `<div ypy-vals:append='{"q": "{{ .Q }}"}'></div>`},
		{"the inherited append spelling", `<div data-ypy-headers:inherited:append='{"q": "{{ .Q }}"}'></div>`},
		{"a configuration of pairs", `<div ypy-config="timeout:{{ .Q }}"></div>`},
	} {
		t.Run(c.what, func(t *testing.T) {
			out, err := generateProbe(t, c.markup)
			if err == nil {
				t.Fatalf("compiled, and what the value does depends on how it begins:\n%s", out)
			}
			if !strings.Contains(err.Error(), ambiguous) {
				t.Errorf("the refusal does not say why: %v", err)
			}
			var positioned *kyse.Error
			if !errors.As(err, &positioned) || positioned.Line != 9 || positioned.Hint == "" {
				t.Errorf("the refusal does not point at the line with a hint: %#v", err)
			}
		})
	}

	// Nothing is refused where the view writes the whole value itself.
	for _, markup := range []string{
		`<div ypy-vals="q:1 r:'two'"></div>`,
		`<div ypy-vals:append="q:1"></div>`,
		`<div ypy-headers=' {"q": 1}'></div>`,
	} {
		if _, err := generateProbe(t, markup); err != nil {
			t.Errorf("%s refused: %v", markup, err)
		}
	}
}

// A confirmation is text, until it opens with js: -- and then YPY runs the rest.
func TestAConfirmationIsTextUnlessItOpensWithAScriptPrefix(t *testing.T) {
	out, err := generateProbe(t, `<button ypy-confirm="Delete {{ .Q }}?"></button>`)
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	if got, want := escapesOf(out), []string{"attr"}; !reflect.DeepEqual(got, want) {
		t.Errorf("the escapes chosen are %v, and text the view opened asks for %v", got, want)
	}

	out, err = generateProbe(t, `<button data-ypy-confirm:inherited="{{ .Q }}"></button>`)
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	if got, want := escapesOf(out), []string{"confirm"}; !reflect.DeepEqual(got, want) {
		t.Errorf("the escapes chosen are %v, and a value in front asks for the check of %v", got, want)
	}

	for _, c := range []struct {
		what, markup, says string
	}{
		{"js: in front", `<button ypy-confirm="js:confirm('{{ .Q }}')"></button>`, "is a script rather than text"},
		{"javascript: after a space", `<button ypy-confirm=" javascript:ask({{ .Q }})"></button>`, "is a script rather than text"},
		{"a prefix that is not finished", `<button ypy-confirm="java{{ .Q }}"></button>`, "where how the value begins decides how it is read"},
	} {
		t.Run(c.what, func(t *testing.T) {
			_, err := generateProbe(t, c.markup)
			if err == nil || !strings.Contains(err.Error(), c.says) {
				t.Errorf("want a refusal saying %q: %v", c.says, err)
			}
		})
	}
}

// A confirmation that is a whole value is checked when it is rendered: the
// build cannot know the text, and the text decides whether YPY runs it.
func TestAConfirmationInFrontIsCheckedWhenItIsRendered(t *testing.T) {
	root := t.TempDir()
	writeRuntimeModule(t, root)
	compileView(t, root, "views", "confirm", "", `//go:build kyse

package views

@go
type ConfirmData struct{ Q string }
@endgo

<button ypy-confirm="{{ .Q }}"></button>
`)
	writeFile(t, filepath.Join(root, "main.go"), `package main

import (
	"fmt"
	"strings"

	"github.com/arandu-io/hesape/view"

	"example.test/render/views"
)

func main() {
	for _, q := range []string{"Delete \"this\"?", "Delete this?", " JS:alert(1)", "javascript:alert(1)", "\ufeffjs:alert(1)", "js"} {
		var page strings.Builder
		err := view.Render(&page, "confirm", views.ConfirmData{Q: q})
		fmt.Printf("%q failed=%v\n", strings.TrimSpace(page.String()), err != nil)
	}
}
`)

	got := runModule(t, root)
	// A page that fails is discarded whole by the renderer, so what matters is
	// the error and that the value was never written.
	want := `"<button ypy-confirm=\"Delete &#34;this&#34;?\"></button>" failed=false` + "\n" +
		`"<button ypy-confirm=\"Delete this?\"></button>" failed=false` + "\n" +
		`"<button ypy-confirm=\"" failed=true` + "\n" +
		`"<button ypy-confirm=\"" failed=true` + "\n" +
		`"<button ypy-confirm=\"" failed=true` + "\n" +
		`"<button ypy-confirm=\"js\"></button>" failed=false` + "\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
