package kyse

import (
	"strings"
	"testing"
)

// The view every case below is checked against.
//
// It binds `row`, imports `strings` under its own name and declares one type,
// so the check has a real answer to give about which names belong to the view
// and which were invented by the generator.
const scopedView = `//go:build kyse

package views

import "strings"

@go
type PageData struct {
	Title string
	Rows  []string
}
@endgo

<h1>{{ .Title }}</h1>
@foreach(.Rows as row)
<p>{{ row }}</p>
@endforeach
`

func parseScoped(t *testing.T) *File {
	t.Helper()
	f, err := Parse("resources/views/page.kyse.go", scopedView)
	if err != nil {
		t.Fatalf("the view under test does not parse: %v", err)
	}
	return f
}

// TestTheOutputIsReadBackBeforeItIsWritten is the whole point of the check: the
// generator's belief about what it emitted is not evidence about what it
// emitted, so the bytes are read back and resolved.
//
// Each case is a shape of generated Go the generator could produce tomorrow.
// None of them fails to parse and none of them fails to format, which is why
// `aru view:build` used to write them, print how many views it had compiled and
// exit 0 -- and why the person met the failure as a `go build` error against a
// file whose header says DO NOT EDIT.
func TestTheOutputIsReadBackBeforeItIsWritten(t *testing.T) {
	file := parseScoped(t)

	cases := []struct {
		name      string
		generated string
		// names is what the refusal has to say, so that a person reading it
		// knows which name went wrong.
		names []string
	}{
		{
			name: "an import the generator did not rename",
			generated: `package views

import (
	view "github.com/arandu-io/hesape/view"
	"strings"
)

func renderPage() { _ = view.Text; _ = strings.TrimSpace }
`,
			names: []string{`"view"`},
		},
		{
			name: "an import under a name the view never wrote, whatever the package",
			generated: `package views

import (
	"bytes"
	"strings"
)

func renderPage() { _ = bytes.MinRead; _ = strings.TrimSpace }
`,
			names: []string{`"bytes"`},
		},
		{
			name: "a temporary the generator invented outside its namespace",
			generated: `package views

import "strings"

func renderPage() {
	for _, row := range []string{} {
		tmp := strings.TrimSpace(row)
		_ = tmp
	}
}
`,
			names: []string{`"tmp"`},
		},
		{
			name: "a reserved name read and never declared",
			generated: `package views

func renderPage() error { return kyse__err }
`,
			names: []string{`"kyse__err"`},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkScopes([]byte(c.generated), file, "storage/framework/views/page.go")
			if err == nil {
				t.Fatal("the check accepted generated Go whose names are not the generator's to rely on")
			}
			for _, name := range c.names {
				if !strings.Contains(err.Error(), name) {
					t.Errorf("the refusal does not name %s: %v", name, err)
				}
			}
			if !strings.Contains(err.Error(), "bug in the view compiler") {
				t.Errorf("the refusal blames the view instead of the compiler: %v", err)
			}
		})
	}
}

// TestTheCheckAcceptsWhatTheGeneratorActuallyWrites is the other half, and the
// half that decides whether the check is usable.
//
// A build step that refuses correct work is worse than one that is silent, so
// every shape the generator emits today has to pass: a page, a layout, a
// component, and a view that binds each of the names the generated file calls
// its own imports by.
func TestTheCheckAcceptsWhatTheGeneratorActuallyWrites(t *testing.T) {
	bindings := []string{"row", "s", "err", "w", "d", "data", "v", "ok", "props", "sections", "item",
		"view", "io", "fmt", "template", "strings", "errors"}

	for _, binding := range bindings {
		t.Run(binding, func(t *testing.T) {
			source := strings.ReplaceAll(`//go:build kyse

package views

@go
type PageData struct {
	Title string
	Rows  []Row
}

type Row struct {
	URL   string
	Label string
}
@endgo

<h1>{{ .Title }}</h1>
@foreach(.Rows as BINDING)
<a href="{{ BINDING.URL }}">{{ BINDING.Label }}</a>
@endforeach
@forelse(.Rows as BINDING)
<i>{{ BINDING.Label }}</i>
@empty
<p>none</p>
@endforelse
`, "BINDING", binding)

			file, err := Parse("resources/views/page.kyse.go", source)
			if err != nil {
				t.Fatalf("the view does not parse: %v", err)
			}
			if _, err := Generate(file, "page", RenderType(file), "storage/framework/views/page.go"); err != nil {
				t.Fatalf("a view binding %q was refused: %v", binding, err)
			}
		})
	}
}

// TestAViewCannotWriteInTheCompilersNamespace covers the surface loopBinding
// does not: an @go block is Go the person wrote, copied in verbatim, and a
// declaration there under the reserved prefix shadows the generator's for the
// whole file.
func TestAViewCannotWriteInTheCompilersNamespace(t *testing.T) {
	source := `//go:build kyse

package views

@go
type PageData struct{ Title string }

var kyse__w int
@endgo

<h1>{{ .Title }}</h1>
`
	file, err := Parse("resources/views/page.kyse.go", source)
	if err != nil {
		t.Fatalf("the view does not parse: %v", err)
	}
	_, err = Generate(file, "page", RenderType(file), "storage/framework/views/page.go")
	if err == nil {
		t.Fatal("a view declaring kyse__w was accepted, and it renames the writer the whole file writes to")
	}
	if !strings.Contains(err.Error(), "kyse__w") {
		t.Errorf("the refusal does not name what was written: %v", err)
	}
}
