package kyse_test

import (
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/kyse"
)

// pageSource is a page extending the layout, with the @go block given.
func pageSource(imports, block string) string {
	return "//go:build kyse\n\npackage notes\n\n" + imports + "@go\n" + block + "@endgo\n\n@extends('layouts.app')\n\n@section('content')\n<p>{{ .Title }}</p>\n@endsection\n"
}

// TestThePageDrawsTheStructThatEmbedsViewPage: a helper declared above the
// page's own struct is a helper, not the type the compiled view asserts to.
// Taking the first struct turned the row of a table into the page's data, and
// the build failed on a field the page declares.
func TestThePageDrawsTheStructThatEmbedsViewPage(t *testing.T) {
	for _, c := range []struct {
		name, imports, block, want string
	}{
		{"view.Page below a helper", "", "type NoteRow struct{ Title string }\n\ntype NotesIndexData struct {\n\tview.Page\n\tRows []NoteRow\n}\n", "NotesIndexData"},
		{"view.Page under the name the view imports", "import hview \"github.com/arandu-io/hesape/view\"\n\n",
			"type Feature struct{ Title string }\n\ntype HomeData struct {\n\t*hview.Page\n\tCount int\n}\n", "HomeData"},
		{"view.Page beside a struct it does not hold", "", "type Flash struct{ Text string }\n\ntype ShowData struct {\n\tview.Page\n\tTitle string\n}\n", "ShowData"},
		{"asserted to the layout", "", "type Row struct{ Title string }\n\ntype ListData struct{ Count int }\n\nvar _ view.Layout = ListData{}\n", "ListData"},
		{"one struct, unmarked", "", "type ShowData struct{ Title string }\n", "ShowData"},
		{"an unmarked helper above the struct that holds it", "", "type Row struct{ Title string }\n\ntype PageData struct{ Rows []Row }\n", "PageData"},
	} {
		t.Run(c.name, func(t *testing.T) {
			file, err := kyse.Parse("resources/views/notes/index.kyse.go", pageSource(c.imports, c.block))
			if err != nil {
				t.Fatal(err)
			}
			if got := kyse.PageType(file); got != c.want {
				t.Errorf("PageType = %q, want %q", got, c.want)
			}
			if got := kyse.RenderType(file); got != c.want {
				t.Errorf("RenderType = %q, want %q", got, c.want)
			}
		})
	}
}

// TestAPageThatCouldDrawTwoTypesIsRefused: when nothing says which type the
// page draws, the compiler does not guess. The refusal names both
// declarations and says how to mark the one that is the page.
func TestAPageThatCouldDrawTwoTypesIsRefused(t *testing.T) {
	for _, c := range []struct{ name, block string }{
		{"two structs, neither marked nor held by the other", "type NoteRow struct{ Title string }\n\ntype NotesIndexData struct{ Count int }\n"},
		{"two structs that both embed view.Page", "type A struct{ view.Page }\n\ntype B struct{ view.Page }\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := kyse.Parse("resources/views/notes/index.kyse.go", pageSource("", c.block))
			if err == nil {
				t.Fatal("a page whose type is ambiguous was compiled")
			}
			message := err.Error()
			for _, want := range []string{"resources/views/notes/index.kyse.go:", "more than one type it could draw", "line 6", "embeds view.Page", "var _ view.Layout ="} {
				if !strings.Contains(message, want) {
					t.Errorf("the refusal does not say %q:\n%s", want, message)
				}
			}
		})
	}
}

// TestAComponentStillDrawsItsFirstStruct: a component extends no layout and
// embeds no view.Page. Its props are the first struct it declares, with the
// types of its items after them, as every component of the library is written.
func TestAComponentStillDrawsItsFirstStruct(t *testing.T) {
	source := "//go:build kyse\n\npackage components\n\n@go\ntype AccordionProps struct {\n\tItems []AccordionItem\n}\n\ntype AccordionItem struct{ Title string }\n@endgo\n\n<div>{{ len(.Items) }}</div>\n"
	file, err := kyse.Parse("components/accordion.kyse.go", source)
	if err != nil {
		t.Fatal(err)
	}
	if got := kyse.PageType(file); got != "AccordionProps" {
		t.Errorf("PageType = %q, want AccordionProps", got)
	}
}
