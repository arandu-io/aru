package lsp_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"

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
	if got := initialize.Result.Capabilities.CompletionProvider.TriggerCharacters; len(got) != 1 || got[0] != "@" {
		t.Errorf("completion triggers = %v, want [@]", got)
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
		`{"jsonrpc":"2.0","id":"completion-1","method":"textDocument/completion","params":{"textDocument":{"uri":"file:///workspace/resources/views/home.kyse.go"},"position":{"line":4,"character":1}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	)
	var output bytes.Buffer

	if err := lsp.Serve(bytes.NewReader(input), &output); err != nil {
		t.Fatalf("serve: %v", err)
	}

	var items []struct {
		Label      string `json:"label"`
		Kind       int    `json:"kind"`
		InsertText string `json:"insertText"`
	}
	for _, body := range readFrames(t, output.Bytes()) {
		var message struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(body, &message); err != nil {
			t.Fatalf("decode protocol message: %v", err)
		}
		if string(message.ID) != `"completion-1"` {
			continue
		}
		if err := json.Unmarshal(message.Result, &items); err != nil {
			t.Fatalf("decode completion items: %v", err)
		}
	}

	directives := kyse.Directives()
	if len(items) != len(directives) {
		t.Fatalf("completion items = %d, want every one of %d Kyse directives", len(items), len(directives))
	}
	for i, directive := range directives {
		wantLabel := "@" + directive
		if items[i].Label != wantLabel || items[i].InsertText != directive {
			t.Errorf("completion %d = label %q, insert %q; want label %q after an existing @", i, items[i].Label, items[i].InsertText, wantLabel)
		}
		if items[i].Kind != 14 {
			t.Errorf("completion %q kind = %d, want keyword (14)", items[i].Label, items[i].Kind)
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
