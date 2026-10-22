package kyse_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/kyse"
)

// A value written where the name of an attribute goes refuses the view.
//
// The escape of a value is chosen from the name of the attribute it sits in,
// and the name is read off the markup the view wrote. When the name is data,
// what the compiler read is not the name the browser reads: `<div {{ .N }}=
// "{{ .V }}">` is compiled as an attribute with no name, so the value gets the
// escape of inert text -- and with N set to onclick, hx-post or href the page
// carries a handler, a request or a javascript: address, with nothing at
// render time to catch it. The same holds for a value the view wrote itself
// behind a name from data, and for a name the view only began: data-{{ .N }}
// becomes data-hx-post, which HTMX reads as hx-post.
//
// Each case is one of the shapes, written with the probe that showed it. The
// view is refused at build time and the refusal names the line and the one
// way left to write an attribute whose name is data, which checks the name.
func TestAValueWrittenWhereAnAttributeNameGoesRefusesTheView(t *testing.T) {
	for _, c := range []struct {
		what, markup string
		line         int
	}{
		{
			"a name from data and its value from data, as onclick=alert(3)",
			`<div {{ .N }}="{{ .V }}"></div>`, 9,
		},
		{
			"a name from data and its value from data, as hx-post=/account/delete",
			`<button type="button" {{ .N }}="{{ .V }}">Go</button>`, 9,
		},
		{
			"a name from data and its value from data, as href=javascript:alert(4)",
			`<a class="link" {{ .N }}="{{ .V }}">x</a>`, 9,
		},
		{
			"a name from data before a value the view wrote, as onclick",
			`<div {{ .N }}="alert(1)"></div>`, 9,
		},
		{
			"a name from data with no value, as autofocus or hx-get",
			`<input type="checkbox" {{ .N }}>`, 9,
		},
		{
			"a name the view began and data finished, as data-hx-post",
			`<div data-{{ .N }}="{{ .V }}"></div>`, 9,
		},
		{
			"a name data began and the view finished, as onclick",
			`<div {{ .N }}click="{{ .V }}"></div>`, 9,
		},
		{
			"a name from data on a line of its own inside a tag",
			"<div\n\tclass=\"a\"\n\t{{ .N }}=\"{{ .V }}\"\n></div>", 11,
		},
		{
			"a name from data inside a block inside a tag",
			"<div\n@if(.N != \"\")\n\t{{ .N }}=\"{{ .V }}\"\n@endif\n></div>", 11,
		},
	} {
		t.Run(c.what, func(t *testing.T) {
			source := "//go:build kyse\n\npackage views\n\n@go\ntype D struct{ N, V string }\n@endgo\n\n" + c.markup + "\n"
			file, err := kyse.Parse("resources/views/home.kyse.go", source)
			if err != nil {
				t.Fatal(err)
			}
			out, err := kyse.Generate(file, "home", "D", "storage/framework/views/home.go")
			if err == nil {
				t.Fatalf("the view compiled with a name from data, so the escape of its value was chosen for a name the browser does not read:\n%s", out)
			}
			if !strings.Contains(err.Error(), "where the name of an attribute goes") {
				t.Errorf("the refusal does not say which position: %v", err)
			}

			var positioned *kyse.Error
			if !errors.As(err, &positioned) {
				t.Fatalf("the refusal carries no position: %v", err)
			}
			if positioned.Path != "resources/views/home.kyse.go" || positioned.Line != c.line {
				t.Errorf("the refusal points at %s:%d, and the view wrote it at resources/views/home.kyse.go:%d",
					positioned.Path, positioned.Line, c.line)
			}
			if !strings.Contains(positioned.Hint, "@attributes") {
				t.Errorf("the refusal does not name the way that checks the name: %q", positioned.Hint)
			}
		})
	}
}

// What stays legal around the refused position still compiles.
//
// The refusal is of a value where a name goes, not of the names around it: a
// fixed attribute chosen with @if, a set of attributes written with
// @attributes, and a value in quotes after a name the view wrote all keep their
// escape. A refusal that took any of these with it would break views that are
// correct.
func TestANameTheViewWroteStillTakesItsValue(t *testing.T) {
	const source = `//go:build kyse

package views

@go
type D struct{ V string; On bool; A map[string]string }
@endgo

<input
	type="checkbox"
	@if(.On)
		checked
	@endif
	@attributes(.A)
	title="{{ .V }}"
	data-id="{{ .V }}"
>
<a href="{{ .V }}">x</a>
`
	file, err := kyse.Parse("resources/views/home.kyse.go", source)
	if err != nil {
		t.Fatal(err)
	}
	out, err := kyse.Generate(file, "home", "D", "storage/framework/views/home.go")
	if err != nil {
		t.Fatalf("a view writing every name itself was refused: %v", err)
	}
	if got, want := strings.Join(escapersIn(string(out)), " "), "attr attr url"; got != want {
		t.Errorf("the escapes chosen are %q, and the positions ask for %q:\n%s", got, want, out)
	}
}
