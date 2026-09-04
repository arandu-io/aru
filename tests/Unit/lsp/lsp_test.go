package lsp_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/doctor"
	"github.com/arandu-io/aru/internal/kyse"
	"github.com/arandu-io/aru/internal/lsp"
)

func TestInitializeAdvertisesKyseDocumentSyncAndCompletion(t *testing.T) {
	input := frames(
		`{"jsonrpc":"2.0","id":"editor-1","method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	)
	var output bytes.Buffer

	if err := lsp.Serve(bytes.NewReader(input), &output); err != nil {
		t.Fatalf("serve: %v", err)
	}

	messages := readFrames(t, output.Bytes())
	if len(messages) != 2 {
		t.Fatalf("response count = %d, want 2", len(messages))
	}

	var initialize struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  struct {
			Capabilities struct {
				PositionEncoding string `json:"positionEncoding"`
				TextDocumentSync struct {
					OpenClose bool `json:"openClose"`
					Change    int  `json:"change"`
				} `json:"textDocumentSync"`
				CompletionProvider struct {
					TriggerCharacters []string `json:"triggerCharacters"`
				} `json:"completionProvider"`
			} `json:"capabilities"`
		} `json:"result"`
	}
	if err := json.Unmarshal(messages[0], &initialize); err != nil {
		t.Fatalf("decode initialize response: %v", err)
	}
	if initialize.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want 2.0", initialize.JSONRPC)
	}
	if string(initialize.ID) != `"editor-1"` {
		t.Errorf("id = %s, want string id unchanged", initialize.ID)
	}
	if initialize.Result.Capabilities.PositionEncoding != "utf-16" {
		t.Errorf("position encoding = %q, want utf-16", initialize.Result.Capabilities.PositionEncoding)
	}
	if !initialize.Result.Capabilities.TextDocumentSync.OpenClose {
		t.Error("initialize did not advertise open and close notifications")
	}
	if initialize.Result.Capabilities.TextDocumentSync.Change != 1 {
		t.Errorf("text sync kind = %d, want full document sync (1)", initialize.Result.Capabilities.TextDocumentSync.Change)
	}
	if got := initialize.Result.Capabilities.CompletionProvider.TriggerCharacters; len(got) != 2 || got[0] != "@" || got[1] != "-" {
		t.Errorf("completion triggers = %v, want [@ -]", got)
	}

	var shutdown struct {
		ID     json.RawMessage `json:"id"`
		Result any             `json:"result"`
	}
	if err := json.Unmarshal(messages[1], &shutdown); err != nil {
		t.Fatalf("decode shutdown response: %v", err)
	}
	if string(shutdown.ID) != "2" || shutdown.Result != nil {
		t.Errorf("shutdown response = id %s, result %#v; want id 2 and null result", shutdown.ID, shutdown.Result)
	}
}

func TestDocumentChangesPublishKyseDiagnosticsAndCloseClearsThem(t *testing.T) {
	const uri = "file:///workspace/resources/views/home.kyse.go"
	input := frames(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///workspace/resources/views/home.kyse.go","languageId":"kyse","version":1,"text":"//go:build kyse\n\npackage views\n\n<p>ok</p>"}}}`,
		`{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"textDocument":{"uri":"file:///workspace/resources/views/home.kyse.go","version":2},"contentChanges":[{"text":"//go:build kyse\n\npackage views\n\n@endif"}]}}`,
		`{"jsonrpc":"2.0","method":"textDocument/didClose","params":{"textDocument":{"uri":"file:///workspace/resources/views/home.kyse.go"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	)
	var output bytes.Buffer

	if err := lsp.Serve(bytes.NewReader(input), &output); err != nil {
		t.Fatalf("serve: %v", err)
	}

	type position struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	}
	type diagnostic struct {
		Range struct {
			Start position `json:"start"`
			End   position `json:"end"`
		} `json:"range"`
		Severity int    `json:"severity"`
		Source   string `json:"source"`
		Message  string `json:"message"`
	}
	type publication struct {
		URI         string       `json:"uri"`
		Diagnostics []diagnostic `json:"diagnostics"`
	}
	var publications []publication
	for _, body := range readFrames(t, output.Bytes()) {
		var message struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(body, &message); err != nil {
			t.Fatalf("decode protocol message: %v", err)
		}
		if message.Method != "textDocument/publishDiagnostics" {
			continue
		}
		var params publication
		if err := json.Unmarshal(message.Params, &params); err != nil {
			t.Fatalf("decode diagnostic publication: %v", err)
		}
		publications = append(publications, params)
	}

	if len(publications) != 3 {
		t.Fatalf("diagnostic publications = %d, want open, change and close", len(publications))
	}
	for i, publication := range publications {
		if publication.URI != uri {
			t.Errorf("publication %d URI = %q, want %q", i, publication.URI, uri)
		}
	}
	if len(publications[0].Diagnostics) != 0 {
		t.Errorf("opening valid source published %d diagnostics, want none", len(publications[0].Diagnostics))
	}
	if len(publications[1].Diagnostics) != 1 {
		t.Fatalf("changing to invalid source published %d diagnostics, want 1", len(publications[1].Diagnostics))
	}
	got := publications[1].Diagnostics[0]
	if got.Range.Start.Line != 4 || got.Range.Start.Character != 0 {
		t.Errorf("diagnostic starts at %d:%d, want 4:0", got.Range.Start.Line, got.Range.Start.Character)
	}
	if got.Range.End.Line != 4 || got.Range.End.Character != 6 {
		t.Errorf("diagnostic ends at %d:%d, want 4:6", got.Range.End.Line, got.Range.End.Character)
	}
	if got.Severity != 1 || got.Source != "kyse" {
		t.Errorf("diagnostic severity/source = %d/%q, want 1/kyse", got.Severity, got.Source)
	}
	if !strings.Contains(got.Message, "@endif closes a block that was never opened") {
		t.Errorf("diagnostic message = %q", got.Message)
	}
	if len(publications[2].Diagnostics) != 0 {
		t.Errorf("closing the document published %d diagnostics, want none", len(publications[2].Diagnostics))
	}
}

func TestDiagnosticRangesUseUTF16AndExcludeTheCarriageReturn(t *testing.T) {
	input := frames(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///workspace/resources/views/home.kyse.go","languageId":"kyse","version":1,"text":"//go:build kyse\r\n\r\npackage views\r\n\r\n@if(💥\r\n<p>never</p>"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	)
	var output bytes.Buffer

	if err := lsp.Serve(bytes.NewReader(input), &output); err != nil {
		t.Fatalf("serve: %v", err)
	}

	var end struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	}
	found := false
	for _, body := range readFrames(t, output.Bytes()) {
		var message struct {
			Method string `json:"method"`
			Params struct {
				Diagnostics []struct {
					Range struct {
						End struct {
							Line      int `json:"line"`
							Character int `json:"character"`
						} `json:"end"`
					} `json:"range"`
				} `json:"diagnostics"`
			} `json:"params"`
		}
		if err := json.Unmarshal(body, &message); err != nil {
			t.Fatalf("decode protocol message: %v", err)
		}
		if message.Method != "textDocument/publishDiagnostics" || len(message.Params.Diagnostics) != 1 {
			continue
		}
		end.Line = message.Params.Diagnostics[0].Range.End.Line
		end.Character = message.Params.Diagnostics[0].Range.End.Character
		found = true
	}
	if !found {
		t.Fatal("the invalid directive did not publish one diagnostic")
	}
	// @if( is four UTF-16 code units and the astral rune is two. The carriage
	// return belongs to CRLF, not to the source line exposed by the protocol.
	if end.Line != 4 || end.Character != 6 {
		t.Errorf("diagnostic ends at %d:%d, want 4:6 in UTF-16", end.Line, end.Character)
	}
}

func TestCompletionOffersEveryKyseDirective(t *testing.T) {
	input := frames(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///workspace/resources/views/home.kyse.go","languageId":"kyse","version":1,"text":"💥@"}}}`,
		`{"jsonrpc":"2.0","id":"triggered","method":"textDocument/completion","params":{"textDocument":{"uri":"file:///workspace/resources/views/home.kyse.go"},"position":{"line":0,"character":3}}}`,
		`{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"textDocument":{"uri":"file:///workspace/resources/views/home.kyse.go","version":2},"contentChanges":[{"text":""}]}}`,
		`{"jsonrpc":"2.0","id":"manual","method":"textDocument/completion","params":{"textDocument":{"uri":"file:///workspace/resources/views/home.kyse.go"},"position":{"line":0,"character":0}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	)
	var output bytes.Buffer

	if err := lsp.Serve(bytes.NewReader(input), &output); err != nil {
		t.Fatalf("serve: %v", err)
	}

	type completion struct {
		Label      string `json:"label"`
		Kind       int    `json:"kind"`
		InsertText string `json:"insertText"`
	}
	itemsByID := map[string][]completion{}
	for _, body := range readFrames(t, output.Bytes()) {
		var message struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(body, &message); err != nil {
			t.Fatalf("decode protocol message: %v", err)
		}
		id := string(message.ID)
		if id != `"triggered"` && id != `"manual"` {
			continue
		}
		var items []completion
		if err := json.Unmarshal(message.Result, &items); err != nil {
			t.Fatalf("decode completion items: %v", err)
		}
		itemsByID[id] = items
	}

	directives := kyse.Directives()
	for id, wantPrefix := range map[string]string{`"triggered"`: "", `"manual"`: "@"} {
		items := itemsByID[id]
		if len(items) != len(directives) {
			t.Fatalf("completion %s items = %d, want every one of %d Kyse directives", id, len(items), len(directives))
		}
		for i, directive := range directives {
			wantLabel := "@" + directive
			wantInsert := wantPrefix + directive
			if items[i].Label != wantLabel || items[i].InsertText != wantInsert {
				t.Errorf("completion %s item %d = label %q, insert %q; want %q/%q", id, i, items[i].Label, items[i].InsertText, wantLabel, wantInsert)
			}
			if items[i].Kind != 14 {
				t.Errorf("completion %q kind = %d, want keyword (14)", items[i].Label, items[i].Kind)
			}
		}
	}
}

func TestPartiallyTypedDirectiveCompletionKeepsTheExistingAtSignAtUTF16Position(t *testing.T) {
	const uri = "file:///workspace/resources/views/home.kyse.go"
	input := frames(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///workspace/resources/views/home.kyse.go","languageId":"kyse","version":1,"text":"💥@fo"}}}`,
		`{"jsonrpc":"2.0","id":"partial","method":"textDocument/completion","params":{"textDocument":{"uri":"file:///workspace/resources/views/home.kyse.go"},"position":{"line":0,"character":5}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	)
	var output bytes.Buffer
	if err := lsp.Serve(bytes.NewReader(input), &output); err != nil {
		t.Fatalf("serve: %v", err)
	}

	for _, body := range readFrames(t, output.Bytes()) {
		var message struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(body, &message); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if string(message.ID) != `"partial"` {
			continue
		}
		var items []struct {
			Label      string `json:"label"`
			InsertText string `json:"insertText"`
		}
		if err := json.Unmarshal(message.Result, &items); err != nil {
			t.Fatalf("decode completion items: %v", err)
		}
		for _, item := range items {
			if item.Label == "@foreach" {
				if item.InsertText != "foreach" {
					t.Errorf("@foreach insertText = %q, want foreach so %s stays with one at-sign", item.InsertText, uri)
				}
				return
			}
		}
		t.Fatal("completion did not offer @foreach for @fo")
	}
	t.Fatal("partial completion request received no response")
}

func TestCompletionDescribesHTMXAttributesInsideAnOpeningTag(t *testing.T) {
	const uri = "file:///workspace/resources/views/home.kyse.go"
	input := frames(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///workspace/resources/views/home.kyse.go","languageId":"kyse","version":1,"text":"<button hx-"}}}`,
		`{"jsonrpc":"2.0","id":"attributes","method":"textDocument/completion","params":{"textDocument":{"uri":"file:///workspace/resources/views/home.kyse.go"},"position":{"line":0,"character":11}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	)
	var output bytes.Buffer
	if err := lsp.Serve(bytes.NewReader(input), &output); err != nil {
		t.Fatalf("serve: %v", err)
	}

	type completion struct {
		Label         string `json:"label"`
		Kind          int    `json:"kind"`
		Detail        string `json:"detail"`
		Documentation string `json:"documentation"`
		InsertText    string `json:"insertText"`
		SortText      string `json:"sortText"`
		Tags          []int  `json:"tags"`
	}
	for _, body := range readFrames(t, output.Bytes()) {
		var message struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(body, &message); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if string(message.ID) != `"attributes"` {
			continue
		}
		var items []completion
		if err := json.Unmarshal(message.Result, &items); err != nil {
			t.Fatalf("decode completion items: %v", err)
		}
		if len(items) < 10 {
			t.Fatalf("HTMX completion count = %d, want the request and swap attributes", len(items))
		}
		for _, item := range items {
			if item.Label != "hx-get" {
				continue
			}
			if item.Kind != 10 {
				t.Errorf("hx-get kind = %d, want property (10)", item.Kind)
			}
			if item.Detail == "" || item.Documentation == "" {
				t.Errorf("hx-get editor copy = detail %q, documentation %q", item.Detail, item.Documentation)
			}
			if !strings.Contains(item.Documentation, "https://htmx.org/attributes/hx-get/") {
				t.Errorf("hx-get documentation does not name the page it came from: %q", item.Documentation)
			}
			if item.InsertText != "hx-get" || item.SortText == "" {
				t.Errorf("hx-get insertion = %q sort=%q", item.InsertText, item.SortText)
			}
			if len(item.Tags) != 0 {
				t.Errorf("hx-get tags = %v, want none: it is not deprecated", item.Tags)
			}
			return
		}
		t.Fatal("completion did not offer hx-get")
	}
	t.Fatal("HTMX completion request received no response for " + uri)
}

// htmxAttributeNames is every attribute HTMX 2 declares, written out here so
// the suite states the set independently of the table it checks.
//
// The table is generated from the metadata HTMX publishes, and a generator that
// silently emitted half of them would be a table the server offers without
// anything noticing -- which is what the hand-written thirteen were. Comparing
// against a second copy of the list is the only way this check says anything.
var htmxAttributeNames = []string{
	"hx-boost", "hx-confirm", "hx-delete", "hx-disable", "hx-disabled-elt",
	"hx-disinherit", "hx-encoding", "hx-ext", "hx-get", "hx-headers",
	"hx-history", "hx-history-elt", "hx-include", "hx-indicator", "hx-inherit",
	"hx-on", "hx-params", "hx-patch", "hx-post", "hx-preserve",
	"hx-prompt", "hx-push-url", "hx-put", "hx-replace-url", "hx-request",
	"hx-select", "hx-select-oob", "hx-swap", "hx-swap-oob", "hx-sync",
	"hx-target", "hx-trigger", "hx-validate", "hx-vals", "hx-vars",
}

func TestCompletionOffersEveryHTMXAttributeAndMarksTheDeprecatedOne(t *testing.T) {
	items := htmxCompletionAt(t, "<button hx-", 0, 11)

	offered := make(map[string]completionShape, len(items))
	for _, item := range items {
		offered[item.Label] = item
	}
	if len(offered) != len(htmxAttributeNames) {
		t.Errorf("HTMX completion offered %d attributes, want %d", len(offered), len(htmxAttributeNames))
	}
	for _, name := range htmxAttributeNames {
		item, found := offered[name]
		if !found {
			t.Errorf("completion did not offer %s", name)
			continue
		}
		if item.Detail == "" {
			t.Errorf("%s has no detail, so the popup shows a bare name", name)
		}
		if item.InsertText != name {
			t.Errorf("%s inserts %q", name, item.InsertText)
		}
	}
	for label := range offered {
		if !slices.Contains(htmxAttributeNames, label) {
			t.Errorf("completion offered %q, which HTMX does not declare", label)
		}
	}

	// hx-vars is the one HTMX deprecated in favour of hx-vals. It stays on the
	// list because a view that already uses it must not read as unknown, and it
	// carries the tag so the editor draws it struck through.
	if tags := offered["hx-vars"].Tags; len(tags) != 1 || tags[0] != 1 {
		t.Errorf("hx-vars tags = %v, want the deprecated tag [1]", tags)
	}
	for _, name := range htmxAttributeNames {
		if name == "hx-vars" {
			continue
		}
		if tags := offered[name].Tags; len(tags) != 0 {
			t.Errorf("%s tags = %v, want none", name, tags)
		}
	}
}

// TestHTMXCompletionOffersNoAlpineOrAngularAttribute is the negative the list
// itself cannot state.
//
// The table is built by reading a file, and a generator that read the wrong
// section of it would produce a plausible list of attributes from a framework
// this stack refuses to serve. Naming the prefixes is what makes the check fail
// on that rather than pass on any list at all.
func TestHTMXCompletionOffersNoAlpineOrAngularAttribute(t *testing.T) {
	for _, item := range htmxCompletionAt(t, "<button hx-", 0, 11) {
		if !strings.HasPrefix(item.Label, "hx-") {
			t.Errorf("HTMX completion offered %q, which is not an HTMX attribute", item.Label)
		}
		for _, foreign := range []string{"x-", "v-", "ng-", "data-x-", "@click", ":class"} {
			if strings.HasPrefix(item.Label, foreign) {
				t.Errorf("completion offered %q, an attribute of a framework this stack does not serve", item.Label)
			}
		}
	}
}

type completionShape struct {
	Label         string `json:"label"`
	Kind          int    `json:"kind"`
	Detail        string `json:"detail"`
	Documentation string `json:"documentation"`
	InsertText    string `json:"insertText"`
	SortText      string `json:"sortText"`
	Tags          []int  `json:"tags"`
}

// htmxCompletionAt drives one completion request over the protocol and returns
// what came back.
//
// It goes through Serve rather than calling into the package, because what an
// editor receives is the JSON and not the Go value: a field that does not
// marshal is invisible to a test that skips the encoding.
func htmxCompletionAt(t *testing.T, text string, line, character int) []completionShape {
	t.Helper()
	return runCompletion(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, text, line, character)
}

// runCompletion drives one completion request, with whatever initialize the
// caller needs: the project-backed families answer only when a root was named.
func runCompletion(t *testing.T, initialize, text string, line, character int) []completionShape {
	t.Helper()
	input := frames(
		initialize,
		fmt.Sprintf(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///workspace/resources/views/home.kyse.go","languageId":"kyse","version":1,"text":%q}}}`, text),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":"completion","method":"textDocument/completion","params":{"textDocument":{"uri":"file:///workspace/resources/views/home.kyse.go"},"position":{"line":%d,"character":%d}}}`, line, character),
		`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	)
	var output bytes.Buffer
	if err := lsp.Serve(bytes.NewReader(input), &output); err != nil {
		t.Fatalf("serve: %v", err)
	}
	for _, body := range readFrames(t, output.Bytes()) {
		var message struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(body, &message); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if string(message.ID) != `"completion"` {
			continue
		}
		var items []completionShape
		if err := json.Unmarshal(message.Result, &items); err != nil {
			t.Fatalf("decode completion items: %v", err)
		}
		return items
	}
	t.Fatal("completion request received no response")
	return nil
}

func TestHTMXCompletionStaysOutOfTextAndAttributeValues(t *testing.T) {
	for _, test := range []struct {
		name      string
		text      string
		character int
	}{
		{name: "text", text: "<p>hx-", character: 6},
		{name: "attribute value", text: `<button title="hx-`, character: 18},
		{name: "closing tag", text: "</hx-", character: 5},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := frames(
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
				fmt.Sprintf(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///workspace/resources/views/home.kyse.go","languageId":"kyse","version":1,"text":%q}}}`, test.text),
				fmt.Sprintf(`{"jsonrpc":"2.0","id":"completion","method":"textDocument/completion","params":{"textDocument":{"uri":"file:///workspace/resources/views/home.kyse.go"},"position":{"line":0,"character":%d}}}`, test.character),
				`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`,
				`{"jsonrpc":"2.0","method":"exit"}`,
			)
			var output bytes.Buffer
			if err := lsp.Serve(bytes.NewReader(input), &output); err != nil {
				t.Fatalf("serve: %v", err)
			}
			for _, body := range readFrames(t, output.Bytes()) {
				var message struct {
					ID     json.RawMessage `json:"id"`
					Result json.RawMessage `json:"result"`
				}
				if err := json.Unmarshal(body, &message); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if string(message.ID) != `"completion"` {
					continue
				}
				var items []struct {
					Label string `json:"label"`
				}
				if err := json.Unmarshal(message.Result, &items); err != nil {
					t.Fatalf("decode completion items: %v", err)
				}
				for _, item := range items {
					if strings.HasPrefix(item.Label, "hx-") {
						t.Fatalf("completion offered %q outside an HTML attribute name", item.Label)
					}
				}
				return
			}
			t.Fatal("completion request received no response")
		})
	}
}

func TestProjectGraphUsesTheInitializedRootAndReturnsClickableZeroBasedLocations(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project with spaces")
	writeProjectGraphFixture(t, root)
	rootURI := (&url.URL{Scheme: "file", Path: root}).String()
	initialize, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]string{"rootUri": rootURI},
	})
	if err != nil {
		t.Fatalf("encode initialize request: %v", err)
	}
	input := frames(
		string(initialize),
		`{"jsonrpc":"2.0","id":"graph-1","method":"arandu/projectGraph"}`,
		`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	)
	var output bytes.Buffer
	if err := lsp.Serve(bytes.NewReader(input), &output); err != nil {
		t.Fatalf("serve: %v", err)
	}

	var graph doctor.ProjectGraph
	found := false
	for _, body := range readFrames(t, output.Bytes()) {
		var message struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(body, &message); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if string(message.ID) != `"graph-1"` {
			continue
		}
		if err := json.Unmarshal(message.Result, &graph); err != nil {
			t.Fatalf("decode project graph: %v", err)
		}
		found = true
	}
	if !found {
		t.Fatal("arandu/projectGraph did not answer its request")
	}
	if graph.SchemaVersion != 1 || len(graph.Groups) != 9 {
		t.Fatalf("graph schema/groups = %d/%d, want 1/9", graph.SchemaVersion, len(graph.Groups))
	}

	modelFound := false
	for _, node := range graph.Nodes {
		if node.File == "" {
			continue
		}
		location, err := url.Parse(node.File)
		if err != nil || location.Scheme != "file" {
			t.Errorf("node %q file = %q, want a clickable file URI", node.ID, node.File)
			continue
		}
		if !strings.HasPrefix(filepath.Clean(location.Path), filepath.Clean(root)+string(filepath.Separator)) {
			t.Errorf("node %q points outside initialized root: %q", node.ID, location.Path)
		}
		if node.Line < 0 || node.Column < 0 {
			t.Errorf("node %q has negative protocol position %d:%d", node.ID, node.Line, node.Column)
		}
		if node.Kind == "model" {
			modelFound = true
			if node.Line != 2 || node.Column != 0 {
				t.Errorf("model location = %d:%d, want zero-based 2:0", node.Line, node.Column)
			}
			if !strings.HasSuffix(location.Path, "/app/Models/Invoice.go") {
				t.Errorf("model URI path = %q", location.Path)
			}
		}
	}
	if !modelFound {
		t.Fatal("project graph contains no model node")
	}
}

func TestSecondInitializeIsRejectedAndCannotReplaceTheOriginalRoot(t *testing.T) {
	firstRoot := filepath.Join(t.TempDir(), "first")
	secondRoot := filepath.Join(t.TempDir(), "second")
	writeProjectGraphFixture(t, firstRoot)
	writeProjectGraphFixture(t, secondRoot)
	firstURI := (&url.URL{Scheme: "file", Path: firstRoot}).String()
	secondURI := (&url.URL{Scheme: "file", Path: secondRoot}).String()
	input := frames(
		fmt.Sprintf(`{"jsonrpc":"2.0","id":"first","method":"initialize","params":{"rootUri":%q}}`, firstURI),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":"second","method":"initialize","params":{"rootUri":%q}}`, secondURI),
		`{"jsonrpc":"2.0","id":"graph","method":"arandu/projectGraph"}`,
		`{"jsonrpc":"2.0","id":"shutdown","method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	)
	var output bytes.Buffer
	if err := lsp.Serve(bytes.NewReader(input), &output); err != nil {
		t.Fatalf("serve: %v", err)
	}

	secondRejected := false
	graphUsesFirstRoot := false
	for _, body := range readFrames(t, output.Bytes()) {
		var message struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &message); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		switch string(message.ID) {
		case `"second"`:
			secondRejected = message.Error != nil && message.Error.Code == -32600
		case `"graph"`:
			var graph doctor.ProjectGraph
			if err := json.Unmarshal(message.Result, &graph); err != nil {
				t.Fatalf("decode project graph: %v", err)
			}
			for _, node := range graph.Nodes {
				if node.Kind != "model" {
					continue
				}
				location, err := url.Parse(node.File)
				if err != nil {
					t.Fatalf("parse model URI: %v", err)
				}
				graphUsesFirstRoot = strings.HasPrefix(filepath.Clean(location.Path), filepath.Clean(firstRoot)+string(filepath.Separator))
			}
		}
	}
	if !secondRejected {
		t.Error("a second initialize did not receive JSON-RPC Invalid Request (-32600)")
	}
	if !graphUsesFirstRoot {
		t.Error("the second initialize replaced the original project root")
	}
}

func TestRequestsRespectInitializationShutdownAndMethodErrors(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		id       string
		wantCode int
	}{
		{
			name: "request before initialize",
			input: frames(
				`{"jsonrpc":"2.0","id":"before","method":"textDocument/completion","params":{}}`,
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
				`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`,
				`{"jsonrpc":"2.0","method":"exit"}`,
			),
			id: `"before"`, wantCode: -32002,
		},
		{
			name: "request after shutdown",
			input: frames(
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
				`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`,
				`{"jsonrpc":"2.0","id":"after","method":"textDocument/completion","params":{}}`,
				`{"jsonrpc":"2.0","method":"exit"}`,
			),
			id: `"after"`, wantCode: -32600,
		},
		{
			name: "unknown request",
			input: frames(
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
				`{"jsonrpc":"2.0","method":"arandu/unknownNotification"}`,
				`{"jsonrpc":"2.0","id":"unknown","method":"arandu/unknownRequest"}`,
				`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`,
				`{"jsonrpc":"2.0","method":"exit"}`,
			),
			id: `"unknown"`, wantCode: -32601,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := lsp.Serve(bytes.NewReader(test.input), &output); err != nil {
				t.Fatalf("serve: %v", err)
			}
			found := false
			for _, body := range readFrames(t, output.Bytes()) {
				var response struct {
					ID    json.RawMessage `json:"id"`
					Error *struct {
						Code int `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal(body, &response); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if string(response.ID) != test.id {
					continue
				}
				found = true
				if response.Error == nil || response.Error.Code != test.wantCode {
					t.Errorf("response error = %#v, want code %d", response.Error, test.wantCode)
				}
			}
			if !found {
				t.Fatalf("request id %s received no response", test.id)
			}
		})
	}
}

func TestMalformedJSONAndInvalidParamsDoNotTerminateTheServer(t *testing.T) {
	input := frames(
		`{"jsonrpc":"2.0",`,
		`{"jsonrpc":"2.0","id":"initialize","method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":42}}`,
		`{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"textDocument":{"uri":"file:///workspace/home.kyse.go"},"contentChanges":[]}}`,
		`{"jsonrpc":"2.0","method":"textDocument/didClose","params":{"textDocument":false}}`,
		`{"jsonrpc":"2.0","id":"bad-params","method":"textDocument/completion","params":[]}`,
		`{"jsonrpc":"2.0","id":"still-alive","method":"textDocument/completion","params":{"textDocument":{"uri":"file:///workspace/home.kyse.go"},"position":{"line":0,"character":0}}}`,
		`{"jsonrpc":"2.0","id":"shutdown","method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	)
	var output bytes.Buffer
	if err := lsp.Serve(bytes.NewReader(input), &output); err != nil {
		t.Fatalf("serve terminated on recoverable client input: %v", err)
	}

	messages := readFrames(t, output.Bytes())
	if len(messages) != 5 {
		t.Fatalf("protocol messages = %d, want parse error, initialize, invalid params, completion and shutdown", len(messages))
	}
	codes := map[string]int{}
	completionAnswered := false
	for _, body := range messages {
		var message struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &message); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if message.Error != nil {
			codes[string(message.ID)] = message.Error.Code
		}
		if string(message.ID) == `"still-alive"` && len(message.Result) > 0 {
			completionAnswered = true
		}
	}
	if got := codes["null"]; got != -32700 {
		t.Errorf("malformed JSON error code = %d, want -32700", got)
	}
	if got := codes[`"bad-params"`]; got != -32602 {
		t.Errorf("invalid request params error code = %d, want -32602", got)
	}
	if !completionAnswered {
		t.Error("valid request after malformed notifications received no response")
	}
}

func TestValidJSONWithAnInvalidJSONRPCEnvelopeGetsInvalidRequest(t *testing.T) {
	input := frames(
		`[]`,
		`{}`,
		`null`,
		`{"jsonrpc":"1.0","id":"wrong-version","method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":"initialize","method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":"shutdown","method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	)
	var output bytes.Buffer
	if err := lsp.Serve(bytes.NewReader(input), &output); err != nil {
		t.Fatalf("serve after invalid JSON-RPC envelopes: %v", err)
	}

	messages := readFrames(t, output.Bytes())
	if len(messages) != 6 {
		t.Fatalf("responses = %d, want four invalid requests, initialize and shutdown", len(messages))
	}
	for i := 0; i < 4; i++ {
		var message struct {
			ID    json.RawMessage `json:"id"`
			Error *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(messages[i], &message); err != nil {
			t.Fatalf("decode invalid-request response %d: %v", i, err)
		}
		if message.Error == nil || message.Error.Code != -32600 {
			t.Errorf("response %d error = %#v, want Invalid Request (-32600)", i, message.Error)
		}
		if i < 3 && string(message.ID) != "null" {
			t.Errorf("response %d ID = %s, want null", i, message.ID)
		}
		if i == 3 && string(message.ID) != `"wrong-version"` {
			t.Errorf("wrong-version response ID = %s", message.ID)
		}
	}
}

func TestStructurallyInvalidLSPParamsAreIgnoredOrRejectedWithoutSideEffects(t *testing.T) {
	input := frames(
		`{"jsonrpc":"2.0","id":"invalid-initialize","method":"initialize","params":null}`,
		`{"jsonrpc":"2.0","id":"initialize","method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{}}`,
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///workspace/resources/views/home.kyse.go","languageId":"kyse","version":1}}}`,
		`{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"textDocument":{"uri":""},"contentChanges":[{"text":"@if"}]}}`,
		`{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"textDocument":{"uri":"file:///workspace/resources/views/home.kyse.go","version":2},"contentChanges":[{}]}}`,
		`{"jsonrpc":"2.0","method":"textDocument/didClose","params":null}`,
		`{"jsonrpc":"2.0","id":"missing-position","method":"textDocument/completion","params":{"textDocument":{"uri":"file:///workspace/resources/views/home.kyse.go"}}}`,
		`{"jsonrpc":"2.0","id":"empty-position","method":"textDocument/completion","params":{"textDocument":{"uri":"file:///workspace/resources/views/home.kyse.go"},"position":{}}}`,
		`{"jsonrpc":"2.0","id":"negative-position","method":"textDocument/completion","params":{"textDocument":{"uri":"file:///workspace/resources/views/home.kyse.go"},"position":{"line":-1,"character":0}}}`,
		`{"jsonrpc":"2.0","id":"empty-document","method":"textDocument/completion","params":{}}`,
		`{"jsonrpc":"2.0","id":"still-alive","method":"textDocument/completion","params":{"textDocument":{"uri":"file:///workspace/resources/views/home.kyse.go"},"position":{"line":0,"character":0}}}`,
		`{"jsonrpc":"2.0","id":"shutdown","method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	)
	var output bytes.Buffer
	if err := lsp.Serve(bytes.NewReader(input), &output); err != nil {
		t.Fatalf("serve after structurally invalid params: %v", err)
	}

	messages := readFrames(t, output.Bytes())
	if len(messages) != 8 {
		t.Fatalf("protocol messages = %d, want five param errors, initialize, live completion and shutdown", len(messages))
	}
	wantIDs := []string{`"invalid-initialize"`, `"initialize"`, `"missing-position"`, `"empty-position"`, `"negative-position"`, `"empty-document"`, `"still-alive"`, `"shutdown"`}
	for i, body := range messages {
		var message struct {
			ID    json.RawMessage `json:"id"`
			Error *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &message); err != nil {
			t.Fatalf("decode response %d: %v", i, err)
		}
		if got := string(message.ID); got != wantIDs[i] {
			t.Errorf("response %d ID = %s, want %s; invalid notifications must produce no output", i, got, wantIDs[i])
		}
		if (i == 0 || i >= 2 && i <= 5) && (message.Error == nil || message.Error.Code != -32602) {
			t.Errorf("response %d error = %#v, want Invalid params (-32602)", i, message.Error)
		}
	}
}

func TestRequestOnlyMethodsAreIgnoredWhenSentAsNotifications(t *testing.T) {
	input := frames(
		`{"jsonrpc":"2.0","method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":"initialize","method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"textDocument/completion","params":{"textDocument":{"uri":"file:///workspace/home.kyse.go"},"position":{"line":0,"character":0}}}`,
		`{"jsonrpc":"2.0","method":"arandu/projectGraph"}`,
		`{"jsonrpc":"2.0","method":"shutdown"}`,
		`{"jsonrpc":"2.0","id":"still-alive","method":"textDocument/completion","params":{"textDocument":{"uri":"file:///workspace/home.kyse.go"},"position":{"line":0,"character":0}}}`,
		`{"jsonrpc":"2.0","id":"shutdown","method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	)
	var output bytes.Buffer
	if err := lsp.Serve(bytes.NewReader(input), &output); err != nil {
		t.Fatalf("serve: %v", err)
	}

	messages := readFrames(t, output.Bytes())
	if len(messages) != 3 {
		t.Fatalf("responses = %d, want only initialize, live completion and shutdown", len(messages))
	}
	wantIDs := []string{`"initialize"`, `"still-alive"`, `"shutdown"`}
	for i, body := range messages {
		var message struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal(body, &message); err != nil {
			t.Fatalf("decode response %d: %v", i, err)
		}
		if got := string(message.ID); got != wantIDs[i] {
			t.Errorf("response %d ID = %s, want %s", i, got, wantIDs[i])
		}
	}
}

func writeProjectGraphFixture(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		"go.mod": `module example.test/editor

go 1.26
`,
		"arandu.mod.toml": `name = "example/editor"
framework = ">= 0.3"
profiles = ["conventional"]

[permissions]
network = false
filesystem = false
exec = false
migrations = true
`,
		"app/Models/Invoice.go": `package models

type Invoice struct{}
`,
	}
	for name, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create fixture directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
}

func TestOversizedFrameIsRefusedBeforeReadingItsBody(t *testing.T) {
	input := strings.NewReader("Content-Length: 16777217\r\n\r\n")
	var output bytes.Buffer

	err := lsp.Serve(input, &output)
	if err == nil {
		t.Fatal("an oversized frame was accepted")
	}
	if !strings.Contains(err.Error(), "16777217") || !strings.Contains(err.Error(), "16777216") {
		t.Fatalf("oversized frame error = %q, want measured and maximum lengths", err)
	}
	if output.Len() != 0 {
		t.Fatalf("oversized frame wrote %q to the protocol stream", output.String())
	}
}

func TestExitBeforeShutdownFailsWithoutWritingProtocolText(t *testing.T) {
	input := frames(`{"jsonrpc":"2.0","method":"exit"}`)
	var output bytes.Buffer

	err := lsp.Serve(bytes.NewReader(input), &output)
	if err == nil || !strings.Contains(err.Error(), "exit received before shutdown") {
		t.Fatalf("early exit error = %v, want shutdown lifecycle error", err)
	}
	if output.Len() != 0 {
		t.Fatalf("early exit wrote %q to the protocol stream", output.String())
	}
}

func frames(messages ...string) []byte {
	var out bytes.Buffer
	for _, message := range messages {
		fmt.Fprintf(&out, "Content-Length: %d\r\n\r\n%s", len(message), message)
	}
	return out.Bytes()
}

func readFrames(t *testing.T, data []byte) [][]byte {
	t.Helper()
	reader := bufio.NewReader(bytes.NewReader(data))
	var messages [][]byte
	for {
		line, err := reader.ReadString('\n')
		if err == io.EOF && line == "" {
			return messages
		}
		if err != nil {
			t.Fatalf("read frame header: %v", err)
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		name, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(name, "Content-Length") {
			t.Fatalf("unexpected output before a protocol frame: %q", line)
		}
		length, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			t.Fatalf("parse content length: %v", err)
		}
		if blank, err := reader.ReadString('\n'); err != nil || blank != "\r\n" {
			t.Fatalf("frame separator = %q, %v", blank, err)
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(reader, body); err != nil {
			t.Fatalf("read frame body: %v", err)
		}
		messages = append(messages, body)
	}
}
