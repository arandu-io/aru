package kyse_test

import (
	"errors"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/kyse"
)

// The type a view draws is read from its `@go` block as the second word of a
// `type` line, and the generator writes that word unread into the assertion
// every render opens with. A line can be valid Go and still have a second word
// that is not a name -- a type parameter list or a * glued to it -- and the
// generated file then did not parse, which the person was told was a bug in
// the generator. These tests hold the parser to refusing the line instead.

func TestATypeNameGoCannotAssertToIsRefusedAtItsLine(t *testing.T) {
	// The @go body starts on line 5 of every view below.
	for _, c := range []struct {
		what, source string
		line         int
		name         string
	}{
		{
			"a * glued to the name of a struct",
			"//go:build kyse\n\npackage views\n\n@go\n// Page is drawn.\ntype Page* struct{ A int }\n@endgo\n\n<p>{{ .A }}</p>\n",
			7, "Page*",
		},
		{
			"a generic struct",
			"//go:build kyse\n\npackage views\n\n@go\ntype Page[T any] struct{ A T }\n@endgo\n\n<p>{{ .A }}</p>\n",
			6, "Page[T",
		},
		{
			"a generic alias",
			"//go:build kyse\n\npackage views\n\nimport \"example.com/app\"\n\n@go\ntype Page[T any] = app.Page[T]\n@endgo\n\n<p>{{ .A }}</p>\n",
			8, "Page[T",
		},
		{
			"the blank identifier",
			"//go:build kyse\n\npackage views\n\n@go\ntype _ struct{ A int }\n@endgo\n\n<p>{{ .A }}</p>\n",
			6, "_",
		},
		{
			"a layout's generic interface",
			"//go:build kyse\n\npackage layouts\n\n@go\ntype Chrome[T any] interface{ Title() T }\n@endgo\n\n@yield('content')\n",
			6, "Chrome[T",
		},
		{
			"a layout's generic struct, which its pages inherit",
			"//go:build kyse\n\npackage layouts\n\n@go\ntype Chrome interface{ Title() string }\ntype Shell[T any] struct{ A T }\n@endgo\n\n@yield('content')\n",
			7, "Shell[T",
		},
	} {
		t.Run(c.what, func(t *testing.T) {
			_, err := kyse.Parse("resources/views/page.kyse.go", c.source)
			if err == nil {
				t.Fatal("parsed: the generator would write an assertion that does not parse")
			}
			var problem *kyse.Error
			if !errors.As(err, &problem) {
				t.Fatalf("refused without a position: %v", err)
			}
			if problem.Line != c.line {
				t.Errorf("refused at line %d, want %d: %v", problem.Line, c.line, err)
			}
			if !strings.Contains(problem.Message, `"`+c.name+`" is the type this view draws`) {
				t.Errorf("the message does not name %q: %v", c.name, err)
			}
			if !strings.Contains(problem.Hint, "type PageData struct") {
				t.Errorf("the hint does not show the shape that works: %v", err)
			}
		})
	}
}

// What the refusal must not take away: an alias is a name followed by the type
// it stands for, a generic declaration the view does not draw is the view's own
// Go, and each compiles to a file that parses as it always did.
func TestATypeNameTheViewDoesNotDrawStaysTheViewsBusiness(t *testing.T) {
	for _, c := range []struct {
		what, source, name string
	}{
		{
			"an alias of a type the controller owns",
			"//go:build kyse\n\npackage views\n\nimport \"example.com/app\"\n\n@go\ntype Page = app.Page\n@endgo\n\n<p>{{ .A }}</p>\n",
			"page",
		},
		{
			"a generic struct below the one the page draws",
			"//go:build kyse\n\npackage views\n\n@go\ntype Page struct{ A int }\ntype Row[T any] struct{ V T }\n@endgo\n\n<p>{{ .A }}</p>\n",
			"page",
		},
		{
			"a generic interface on a page, which draws its struct",
			"//go:build kyse\n\npackage views\n\n@go\ntype Page struct{ A int }\ntype Lister[T any] interface{ List() []T }\n@endgo\n\n<p>{{ .A }}</p>\n",
			"page",
		},
	} {
		t.Run(c.what, func(t *testing.T) {
			file, err := kyse.Parse("resources/views/page.kyse.go", c.source)
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			out, err := kyse.Generate(file, c.name, kyse.RenderType(file), "storage/framework/views/page.go")
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if _, err := parser.ParseFile(token.NewFileSet(), "page.go", out, parser.SkipObjectResolution); err != nil {
				t.Fatalf("the generated Go does not parse: %v\n%s", err, out)
			}
		})
	}
}
