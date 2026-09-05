package lsp_test

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The framework's view package, as a project resolves it: an embed directive
// naming what is compiled in, beside a source file that is not.
//
// `app.src.css` is the whole point of the fixture. It sits in the same
// directory and is not embedded, so a list built by reading the directory would
// offer it -- and view.URL panics on a name it was not given, which takes the
// page down rather than showing a broken link.
const (
	frameworkAssets = `package view

import "embed"

//go:embed assets/app.css assets/htmx.min.js assets/ui.js

var files embed.FS

func Asset(name string) string { return name }
`

	projectAssets = `package js

import "github.com/arandu-io/hesape/view"

func init() {
	view.RegisterAsset("custom.js", "application/javascript; charset=utf-8", nil)
}
`
)

// assetView calls view.URL with an unfinished argument, which is where an
// editor asks.
const assetView = `//go:build kyse

package layouts

<link rel="stylesheet" href="{{ view.Asset(" }}">
<p>view.Asset("not a call, this is prose") </p>
`

func writeCompletionFixture(t *testing.T) (root string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "app")
	cache := filepath.Join(base, "modcache")
	view := filepath.Join(cache, "github.com", "arandu-io", "hesape@v1.0.0", "view")

	files := map[string]string{
		filepath.Join(root, "go.mod"): `module example.test/app

go 1.26

require github.com/arandu-io/hesape v1.0.0
require example.test/Kyse v1.2.3
`,
		filepath.Join(root, "resources", "js", "js.go"):                                      projectAssets,
		filepath.Join(view, "assets.go"):                                                     frameworkAssets,
		filepath.Join(view, "assets", "app.css"):                                             "body{}",
		filepath.Join(view, "assets", "app.src.css"):                                         "@source '.';",
		filepath.Join(view, "assets", "htmx.min.js"):                                         "//",
		filepath.Join(view, "assets", "ui.js"):                                               "//",
		filepath.Join(cache, "example.test", "!kyse@v1.2.3", "components", "button.go"):      buttonGenerated,
		filepath.Join(cache, "example.test", "!kyse@v1.2.3", "components", "button.kyse.go"): buttonSource,
	}
	for path, contents := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create fixture directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", path, err)
		}
	}
	t.Setenv("GOMODCACHE", cache)
	return root
}

// completionIn drives one completion request against a project root.
func completionIn(t *testing.T, root, text string, line, character int) []completionShape {
	t.Helper()
	rootURI := (&url.URL{Scheme: "file", Path: root}).String()
	return runCompletion(t,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":%q}}`, rootURI),
		text, line, character)
}

func labelsOf(items []completionShape) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.Label
	}
	return out
}

func TestCompletionOffersTheAssetNamesViewURLWillAccept(t *testing.T) {
	root := writeCompletionFixture(t)

	line, character := offsetOf(t, assetView, `view.Asset("`)
	items := completionIn(t, root, assetView, line, character+len(`view.Asset("`))

	got := labelsOf(items)
	want := []string{"app.css", "custom.js", "htmx.min.js", "ui.js"}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("asset completion = %v, want %v", got, want)
	}

	// app.src.css is in the same directory and is not embedded. view.URL
	// panics on a name it was never given, so offering it would hand somebody
	// the one argument that takes the page down.
	if slices.Contains(got, "app.src.css") {
		t.Error("completion offered app.src.css, which is beside the assets and is not one")
	}

	for _, item := range items {
		if item.Detail == "" {
			t.Errorf("%s is offered with no indication of where it came from", item.Label)
		}
	}
	for _, item := range items {
		if item.Label == "custom.js" && !strings.Contains(item.Detail, "resources/js/js.go") {
			t.Errorf("custom.js detail = %q, want the file that registered it", item.Detail)
		}
	}
}

// TestAssetCompletionStaysInsideTheCallItIsAbout is the negative for the
// position, and it is the one that decides whether the list is a help or a
// nuisance.
func TestAssetCompletionStaysInsideTheCallItIsAbout(t *testing.T) {
	root := writeCompletionFixture(t)

	for _, test := range []struct {
		name   string
		text   string
		needle string
		offset int
	}{
		{
			name:   "after the argument is already written",
			text:   "//go:build kyse\n\npackage layouts\n\n<link href=\"{{ view.Asset(\"app.css\") }}\">\n",
			needle: `view.Asset("app.css")`,
			offset: len(`view.Asset("app.css")`),
		},
		{
			name:   "in a different call that also ends in URL",
			text:   "//go:build kyse\n\npackage layouts\n\n<a href=\"{{ route.URL(\" }}\">\n",
			needle: `route.URL("`,
			offset: len(`route.URL("`),
		},
		{
			name:   "in ordinary markup",
			text:   assetView,
			needle: "this is prose",
			offset: 4,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			line, character := offsetOf(t, test.text, test.needle)
			for _, item := range completionIn(t, root, test.text, line, character+test.offset) {
				if item.Label == "app.css" || item.Label == "custom.js" {
					t.Errorf("completion offered the asset %q outside view.URL's argument", item.Label)
				}
			}
		})
	}
}

// componentView imports the component package and calls into it, which is what
// makes the qualifier mean anything at all.
const componentView = `//go:build kyse

package views

import "example.test/Kyse/components"

@go
type PageData struct{ Page struct{ Title string } }
@endgo

<p>{{ .Page.Title }}</p>
{!! components.Button(components.ButtonProps{}) !!}
<p>widgets.Anything and components.Button as prose</p>
`

func TestCompletionOffersWhatTheImportedComponentPackageDeclares(t *testing.T) {
	root := writeCompletionFixture(t)

	line, character := offsetOf(t, componentView, "{!! components.")
	items := completionIn(t, root, componentView, line, character+len("{!! components."))

	if got := labelsOf(items); !slices.Equal(got, []string{"Button"}) {
		t.Fatalf("component completion = %v, want the one function the package declares", got)
	}
	// The props type is the component's whole contract, and seeing it is what
	// says which struct literal comes next.
	if items[0].Detail != "ButtonProps" {
		t.Errorf("Button detail = %q, want its props type", items[0].Detail)
	}
	if !strings.Contains(items[0].Documentation, "Button draws a button") {
		t.Errorf("Button documentation = %q, want the declaration's own doc", items[0].Documentation)
	}
}

// TestComponentCompletionRefusesAQualifierThatMeansNothingHere covers the three
// ways a dot appears in a view without naming a package.
//
// Answering any of them with a component list would offer names that do not
// compile in that position, which is the failure that makes people turn
// completion off.
func TestComponentCompletionRefusesAQualifierThatMeansNothingHere(t *testing.T) {
	root := writeCompletionFixture(t)

	for _, test := range []struct {
		name   string
		text   string
		needle string
		offset int
		why    string
	}{
		{
			name:   "a package the view never imported",
			text:   strings.Replace(componentView, "{!! components.Button", "{!! widgets.Button", 1),
			needle: "{!! widgets.",
			offset: len("{!! widgets."),
			why:    "the import block is what says which package a qualifier means",
		},
		{
			name:   "a field reached through a value",
			text:   componentView,
			needle: "{{ .Page.",
			offset: len("{{ .Page."),
			why:    "a field is not a package member, and the two are spelled the same",
		},
		{
			name:   "the qualifier written as prose in the markup",
			text:   componentView,
			needle: "<p>widgets.Anything and components.",
			offset: len("<p>widgets.Anything and components."),
			why:    "text is text, and a paragraph naming a package does not import one",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			line, character := offsetOf(t, test.text, test.needle)
			for _, item := range completionIn(t, root, test.text, line, character+test.offset) {
				if item.Label == "Button" {
					t.Errorf("completion offered the component %q; %s", item.Label, test.why)
				}
			}
		})
	}
}

// TestCompletionStillAnswersWithoutAProject fixes what a client that opened no
// folder gets.
//
// The directives and the attributes are true of every view, whether or not
// there is a tree to read, so they are what the fall-through offers -- and a
// server that answered nothing at all would look broken rather than unopened.
func TestCompletionStillAnswersWithoutAProject(t *testing.T) {
	items := runCompletion(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		"//go:build kyse\n\npackage views\n\n@", 4, 1)
	if len(items) == 0 {
		t.Fatal("completion without a project offered nothing at all")
	}
	if !slices.Contains(labelsOf(items), "@extends") {
		t.Errorf("completion without a project = %v, want the directives", labelsOf(items))
	}
}
