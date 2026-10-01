package kyse_test

import (
	"encoding/json"
	"errors"
	"html"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/kyse"
)

// generateProbe compiles one line of markup in a page whose data has a single
// string field, Q, and returns the generated Go or the refusal.
func generateProbe(t *testing.T, markup string) (string, error) {
	t.Helper()
	source := "//go:build kyse\n\npackage views\n\n@go\ntype D struct{ Q, U, V string }\n@endgo\n\n" + markup + "\n"
	file, err := kyse.Parse("resources/views/home.kyse.go", source)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out, err := kyse.Generate(file, "home", "D", "storage/framework/views/home.go")
	return string(out), err
}

// The escape an attribute whose value is JSON takes is chosen by where in the
// JSON the value lands.
//
// Inside a string it is escaped for the string and then for the attribute;
// outside one it becomes a whole JSON literal and is then escaped for the
// attribute. Escaping only for the attribute -- what these attributes used to
// get -- hands the HTML parser a quote it decodes back into a quote, which ends
// the string the view opened.
func TestAJSONAttributeEscapesForWhereTheValueLandsInTheJSON(t *testing.T) {
	const inString = "kyse__view.TextJS(kyse__view.Text(kyse__d.Q))"
	const literal = "kyse__view.TextAttr(kyse__view.TextJS(kyse__d.Q))"

	for _, c := range []struct {
		what, markup, want string
	}{
		{"the CSRF header a layout sends", `<body hx-headers='{"X-CSRF-Token": "{{ .Q }}"}'></body>`, inString},
		{"a value inside a string", `<div hx-vals='{"q": "{{ .Q }}"}'></div>`, inString},
		{"quotes written as character references", `<div hx-vals="{&quot;q&quot;: &quot;{{ .Q }}&quot;}"></div>`, inString},
		{"the props of a client behaviour", `<div data-kyse-props='{"target": "{{ .Q }}"}'></div>`, inString},
		{"a value outside a string", `<div hx-vals='{"page": {{ .Q }}}'></div>`, literal},
		{"the whole value", `<div hx-vals="{{ .Q }}"></div>`, literal},
		{"a request setting", `<div hx-request='{"timeout": {{ .Q }}}'></div>`, literal},
		{"after a string that holds an escaped quote", `<div hx-vals='{"a\"b": {{ .Q }}}'></div>`, literal},
	} {
		t.Run(c.what, func(t *testing.T) {
			out, err := generateProbe(t, c.markup)
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if !strings.Contains(out, c.want) {
				t.Errorf("want %s in the output:\n%s", c.want, out)
			}
			if strings.Contains(out, "kyse__view.TextAttr(kyse__d.Q)") {
				t.Errorf("the value is escaped only for the attribute:\n%s", out)
			}
		})
	}
}

// A JSON attribute that is evaluated rather than parsed refuses the value, as
// every attribute whose value is a script does.
func TestAJSONAttributeThatIsAScriptRefusesTheValue(t *testing.T) {
	for _, c := range []struct {
		what, markup, says string
	}{
		{"js: in front", `<div hx-vals='js:{q: "{{ .Q }}"}'></div>`, "is a script rather than text"},
		{"javascript: after a space", `<div hx-headers=' javascript:{q: {{ .Q }}}'></div>`, "is a script rather than text"},
		{"js: spelled with a character reference", `<div hx-vals='&#106;s:{q: "{{ .Q }}"}'></div>`, "is a script rather than text"},
		{"a no-break space before js:", `<div hx-request='&nbsp;js:{timeout: {{ .Q }}}'></div>`, "is a script rather than text"},
		{"the attribute HTMX always evaluates", `<div hx-vars="q:{{ .Q }}"></div>`, "is a script rather than text"},
		{"right after a backslash in a string", `<div hx-vals='{"q": "\{{ .Q }}"}'></div>`, "right after a backslash"},
	} {
		t.Run(c.what, func(t *testing.T) {
			out, err := generateProbe(t, c.markup)
			if err == nil {
				t.Fatalf("compiled, and the value lands in a script:\n%s", out)
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("the refusal does not say why: %v", err)
			}
			var positioned *kyse.Error
			if !errors.As(err, &positioned) || positioned.Line != 9 || positioned.Hint == "" {
				t.Errorf("the refusal does not point at the line with a hint: %#v", err)
			}
		})
	}
}

// What reaches the JSON parser is the value as a string and nothing else,
// whatever the value holds.
//
// The views are rendered and the attribute is read back the way a browser
// does it: character references decoded, then the text parsed as JSON. The
// first payload is the attack -- closing the string the view opened and adding
// a key -- and the rest are the characters each escape exists for.
func TestAJSONAttributeCarriesTheValueAsData(t *testing.T) {
	root := t.TempDir()
	writeRuntimeModule(t, root)
	compileView(t, root, "views", "json", "", `//go:build kyse

package views

@go
type D struct{ Q string }
@endgo

<div hx-vals='{"q": "{{ .Q }}"}'></div>
<body hx-headers='{"X-CSRF-Token": "{{ .Q }}"}'></body>
<div hx-vals="{&quot;q&quot;: &quot;{{ .Q }}&quot;}"></div>
<div data-kyse-props='{"q": {{ .Q }}}'></div>
`)
	payloads := []string{
		`x", "role": "admin`,
		`x\", "role": "admin`,
		`it's </script><script>alert(1)</script>`,
		"line\u2028break\nand\ttab\x01",
		`&quot;&#34;&#x22;`,
	}
	var calls strings.Builder
	for _, p := range payloads {
		calls.WriteString("\trender(" + goQuote(p) + ")\n")
	}
	writeFile(t, filepath.Join(root, "main.go"), `package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/arandu-io/hesape/view"

	"example.test/render/views"
)

func render(q string) {
	var page strings.Builder
	if err := view.Render(&page, "json", views.D{Q: q}); err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
	fmt.Print(page.String())
}

func main() {
`+calls.String()+`}
`)

	got := runModule(t, root)
	values := attributeValuesIn(got)
	if len(values) != 4*len(payloads) {
		t.Fatalf("read %d JSON attributes, want %d:\n%s", len(values), 4*len(payloads), got)
	}
	for i, raw := range values {
		payload := payloads[i/4]
		var decoded map[string]string
		if err := json.Unmarshal([]byte(html.UnescapeString(raw)), &decoded); err != nil {
			t.Errorf("the attribute for %q is not JSON: %v\n%s", payload, err, raw)
			continue
		}
		key := "q"
		if i%4 == 1 {
			key = "X-CSRF-Token"
		}
		if want := map[string]string{key: payload}; !reflect.DeepEqual(decoded, want) {
			t.Errorf("the JSON parser reads %v, and the view sent %v", decoded, want)
		}
	}
}

// attributeValuesIn returns the values of the JSON attributes in rendered
// markup, as the raw text between their quotes.
func attributeValuesIn(markup string) []string {
	var out []string
	for i := 0; i < len(markup); i++ {
		for _, name := range []string{"hx-vals=", "hx-headers=", "data-kyse-props="} {
			if !strings.HasPrefix(markup[i:], name) {
				continue
			}
			at := i + len(name)
			quote := markup[at]
			end := strings.IndexByte(markup[at+1:], quote)
			if end < 0 {
				return out
			}
			out = append(out, markup[at+1:at+1+end])
			i = at + end
		}
	}
	return out
}

// goQuote writes a string as a Go literal for the generated main package.
func goQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
