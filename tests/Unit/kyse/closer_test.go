package kyse_test

import (
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/kyse"
)

// A block closes with its own end directive. The closer used to be compared by
// prefix, so `@for` closed by `@endforeach` -- which begins with `@endfor` --
// was accepted and compiled as if it had been written right. These tests hold
// the parser to comparing the whole name.

const closerHead = "//go:build kyse\n\npackage views\n\n@go\ntype Page struct{ Ok bool; Rows []string }\n@endgo\n\n"

func TestEveryBlockClosesWithItsOwnEndDirective(t *testing.T) {
	for _, body := range []string{
		"@if(.Ok)\n<p>x</p>\n@endif\n",
		"@foreach(.Rows as row)\n<p>{{ row }}</p>\n@endforeach\n",
		"@forelse(.Rows as row)\n<p>{{ row }}</p>\n@empty\n<p>none</p>\n@endforelse\n",
		"@for(i := 0; i < 3; i++)\n<p>{{ i }}</p>\n@endfor\n",
		"@while(.Ok)\n<p>x</p>\n@break\n@endwhile\n",
		"@foreach(.Rows as row)\n@for(i := 0; i < 2; i++)\n<p>{{ row }}{{ i }}</p>\n@endfor\n@endforeach\n",
	} {
		file, err := kyse.Parse("resources/views/page.kyse.go", closerHead+body)
		if err != nil {
			t.Errorf("refused a block closed with its own directive:\n%s\n%v", body, err)
			continue
		}
		if _, err := kyse.Generate(file, "page", "Page", "storage/framework/views/page.go"); err != nil {
			t.Errorf("Generate:\n%s\n%v", body, err)
		}
	}
}

func TestACloserOfAnotherBlockIsRefusedNamingBothLinesAndTheExpectedOne(t *testing.T) {
	// The body starts on line 9 of the view: closerHead is eight lines.
	for _, c := range []struct {
		what, body string
		line       int
		message    string
		hint       string
	}{
		{
			"@for closed by @endforeach, which shares its first letters",
			"@for(i := 0; i < 3; i++)\n<p>{{ i }}</p>\n@endforeach\n",
			11, "@endforeach closes the @for opened on line 9", "write @endfor here",
		},
		{
			"@for closed by @endforelse",
			"@for(i := 0; i < 3; i++)\n<p>{{ i }}</p>\n@endforelse\n",
			11, "@endforelse closes the @for opened on line 9", "write @endfor here",
		},
		{
			"@foreach closed by @endfor",
			"@foreach(.Rows as row)\n<p>{{ row }}</p>\n@endfor\n",
			11, "@endfor closes the @foreach opened on line 9", "write @endforeach here",
		},
		{
			"@if closed by @endwhile",
			"@if(.Ok)\n<p>x</p>\n@endwhile\n",
			11, "@endwhile closes the @if opened on line 9", "write @endif here",
		},
		{
			"a nested block closed wrong inside one closed right",
			"@if(.Ok)\n@for(i := 0; i < 3; i++)\n<p>{{ i }}</p>\n@endforeach\n@endif\n",
			12, "@endforeach closes the @for opened on line 10", "write @endfor here",
		},
		{
			"an enclosing block's closer leaves the inner block never closed",
			"@foreach(.Rows as row)\n@if(.Ok)\n<p>{{ row }}</p>\n@endforeach\n",
			10, "@if was never closed",
			"the @endforeach on line 12 closes the @foreach opened on line 9; add @endif above it.",
		},
	} {
		t.Run(c.what, func(t *testing.T) {
			_, err := kyse.Parse("resources/views/page.kyse.go", closerHead+c.body)
			if err == nil {
				t.Fatal("parsed")
			}
			got := problems(err)
			if len(got) != 1 {
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
