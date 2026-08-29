// Package lsp serves Kyse language intelligence over the Language Server Protocol.
package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/arandu-io/aru/internal/doctor"
	"github.com/arandu-io/aru/internal/kyse"
)

const maxFrameSize = 16 << 20

// Serve reads Language Server Protocol frames from in and writes protocol
// responses and notifications to out. Messages are processed in input order.
func Serve(in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	initialized := false
	shutdown := false
	rootURI := ""
	documents := map[string]string{}
	for {
		body, err := readFrame(reader)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		var message request
		if err := json.Unmarshal(body, &message); err != nil {
			code, description := -32700, "Parse error"
			var id json.RawMessage
			if json.Valid(body) {
				code, description = -32600, "Invalid Request"
				id = responseIDForInvalidRequest(body)
			}
			if err := writeError(out, id, code, description); err != nil {
				return err
			}
			continue
		}
		if !validRequestEnvelope(message) {
			if err := writeError(out, responseIDForInvalidRequest(body), -32600, "Invalid Request"); err != nil {
				return err
			}
			continue
		}
		isRequest := len(message.ID) > 0
		if !isRequest && requestOnlyMethod(message.Method) {
			continue
		}

		if message.Method == "exit" {
			if !shutdown {
				return fmt.Errorf("exit received before shutdown")
			}
			return nil
		}
		if shutdown {
			if isRequest {
				if err := writeError(out, message.ID, -32600, "Invalid Request"); err != nil {
					return err
				}
			}
			continue
		}
		if !initialized && message.Method != "initialize" {
			if isRequest {
				if err := writeError(out, message.ID, -32002, "Server not initialized"); err != nil {
					return err
				}
			}
			continue
		}
		if initialized && message.Method == "initialize" {
			if isRequest {
				if err := writeError(out, message.ID, -32600, "Invalid Request"); err != nil {
					return err
				}
			}
			continue
		}

		switch message.Method {
		case "initialize":
			var params initializeParams
			if !decodeObjectParams(message.Params, &params) {
				if isRequest {
					if err := writeError(out, message.ID, -32602, "Invalid params"); err != nil {
						return err
					}
				}
				continue
			}
			if params.RootURI != "" {
				if _, err := pathFromFileURI(params.RootURI, nativeFilePathStyle()); err != nil {
					if isRequest {
						if err := writeError(out, message.ID, -32602, "Invalid rootUri"); err != nil {
							return err
						}
					}
					continue
				}
			}
			rootURI = params.RootURI
			initialized = true
			result := initializeResult{
				Capabilities: serverCapabilities{
					PositionEncoding: "utf-16",
					TextDocumentSync: textDocumentSyncOptions{
						OpenClose: true,
						Change:    1,
					},
					CompletionProvider: completionOptions{
						TriggerCharacters: []string{"@"},
					},
				},
			}
			if err := writeResult(out, message.ID, result); err != nil {
				return err
			}
		case "shutdown":
			if err := writeResult(out, message.ID, nil); err != nil {
				return err
			}
			shutdown = true
		case "textDocument/didOpen":
			var params didOpenParams
			if !decodeObjectParams(message.Params, &params) || !validTextDocumentItem(params.TextDocument) {
				if isRequest {
					if err := writeError(out, message.ID, -32602, "Invalid params"); err != nil {
						return err
					}
				}
				continue
			}
			documents[params.TextDocument.URI] = *params.TextDocument.Text
			if err := publishDiagnostics(out, params.TextDocument.URI, *params.TextDocument.Text); err != nil {
				return err
			}
		case "textDocument/didChange":
			var params didChangeParams
			if !decodeObjectParams(message.Params, &params) || params.TextDocument == nil || params.TextDocument.URI == "" || params.TextDocument.Version == nil {
				if isRequest {
					if err := writeError(out, message.ID, -32602, "Invalid params"); err != nil {
						return err
					}
				}
				continue
			}
			if !validContentChanges(params.ContentChanges) {
				if isRequest {
					if err := writeError(out, message.ID, -32602, "Invalid params"); err != nil {
						return err
					}
				}
				continue
			}
			text := *params.ContentChanges[len(params.ContentChanges)-1].Text
			documents[params.TextDocument.URI] = text
			if err := publishDiagnostics(out, params.TextDocument.URI, text); err != nil {
				return err
			}
		case "textDocument/didClose":
			var params didCloseParams
			if !decodeObjectParams(message.Params, &params) || params.TextDocument == nil || params.TextDocument.URI == "" {
				if isRequest {
					if err := writeError(out, message.ID, -32602, "Invalid params"); err != nil {
						return err
					}
				}
				continue
			}
			delete(documents, params.TextDocument.URI)
			if err := writeFrame(out, notification{
				JSONRPC: "2.0",
				Method:  "textDocument/publishDiagnostics",
				Params: publishDiagnosticsParams{
					URI:         params.TextDocument.URI,
					Diagnostics: []diagnostic{},
				},
			}); err != nil {
				return err
			}
		case "textDocument/completion":
			var params completionParams
			if !decodeObjectParams(message.Params, &params) || !validCompletionParams(params) {
				if isRequest {
					if err := writeError(out, message.ID, -32602, "Invalid params"); err != nil {
						return err
					}
				}
				continue
			}
			at := position{Line: *params.Position.Line, Character: *params.Position.Character}
			items := completionItems(documents[params.TextDocument.URI], at)
			if err := writeResult(out, message.ID, items); err != nil {
				return err
			}
		case "arandu/projectGraph":
			if rootURI == "" {
				if err := writeError(out, message.ID, -32602, "initialize rootUri is required"); err != nil {
					return err
				}
				continue
			}
			root, err := pathFromFileURI(rootURI, nativeFilePathStyle())
			if err != nil {
				if err := writeError(out, message.ID, -32602, "initialize rootUri is invalid"); err != nil {
					return err
				}
				continue
			}
			analysis, err := doctor.Analyze(root, doctor.Conventional)
			if err != nil {
				if err := writeError(out, message.ID, -32603, "Project analysis failed: "+err.Error()); err != nil {
					return err
				}
				continue
			}
			graph, err := projectGraphForProtocol(root, analysis.Graph)
			if err != nil {
				if err := writeError(out, message.ID, -32603, "Project graph location failed: "+err.Error()); err != nil {
					return err
				}
				continue
			}
			if err := writeResult(out, message.ID, graph); err != nil {
				return err
			}
		default:
			if isRequest {
				if err := writeError(out, message.ID, -32601, "Method not found"); err != nil {
					return err
				}
			}
		}
	}
}

func requestOnlyMethod(method string) bool {
	switch method {
	case "initialize", "shutdown", "textDocument/completion", "arandu/projectGraph":
		return true
	default:
		return false
	}
}

func validRequestEnvelope(message request) bool {
	return message.JSONRPC == "2.0" && message.Method != "" && validRequestID(message.ID)
}

func validRequestID(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var id any
	if err := decoder.Decode(&id); err != nil {
		return false
	}
	switch id.(type) {
	case nil, string, json.Number:
		return true
	default:
		return false
	}
}

func responseIDForInvalidRequest(body []byte) json.RawMessage {
	var envelope struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || len(envelope.ID) == 0 || !validRequestID(envelope.ID) {
		return nil
	}
	return envelope.ID
}

func decodeObjectParams(raw json.RawMessage, target any) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{' && json.Unmarshal(trimmed, target) == nil
}

func validTextDocumentItem(document *textDocumentItem) bool {
	return document != nil && document.URI != "" && document.LanguageID != nil && document.Version != nil && document.Text != nil
}

func validContentChanges(changes []contentChange) bool {
	if len(changes) == 0 {
		return false
	}
	for _, change := range changes {
		if change.Text == nil {
			return false
		}
	}
	return true
}

func validCompletionParams(params completionParams) bool {
	return params.TextDocument != nil && params.TextDocument.URI != "" &&
		params.Position != nil && params.Position.Line != nil && params.Position.Character != nil &&
		*params.Position.Line >= 0 && *params.Position.Character >= 0
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *responseError  `json:"error,omitempty"`
}

type responseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type initializeResult struct {
	Capabilities serverCapabilities `json:"capabilities"`
}

type initializeParams struct {
	RootURI string `json:"rootUri"`
}

type serverCapabilities struct {
	PositionEncoding   string                  `json:"positionEncoding"`
	TextDocumentSync   textDocumentSyncOptions `json:"textDocumentSync"`
	CompletionProvider completionOptions       `json:"completionProvider"`
}

type textDocumentSyncOptions struct {
	OpenClose bool `json:"openClose"`
	Change    int  `json:"change"`
}

type completionOptions struct {
	TriggerCharacters []string `json:"triggerCharacters"`
}

type completionItem struct {
	Label      string `json:"label"`
	Kind       int    `json:"kind"`
	InsertText string `json:"insertText"`
}

type completionParams struct {
	TextDocument *textDocumentIdentifier `json:"textDocument"`
	Position     *completionPosition     `json:"position"`
}

type completionPosition struct {
	Line      *int `json:"line"`
	Character *int `json:"character"`
}

type textDocumentIdentifier struct {
	URI string `json:"uri"`
}

type textDocumentItem struct {
	URI        string  `json:"uri"`
	LanguageID *string `json:"languageId"`
	Version    *int    `json:"version"`
	Text       *string `json:"text"`
}

type didOpenParams struct {
	TextDocument *textDocumentItem `json:"textDocument"`
}

type didChangeParams struct {
	TextDocument   *versionedTextDocumentIdentifier `json:"textDocument"`
	ContentChanges []contentChange                  `json:"contentChanges"`
}

type contentChange struct {
	Text *string `json:"text"`
}

type versionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version *int   `json:"version"`
}

type didCloseParams struct {
	TextDocument *textDocumentIdentifier `json:"textDocument"`
}

type publishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Diagnostics []diagnostic `json:"diagnostics"`
}

type diagnostic struct {
	Range    protocolRange `json:"range"`
	Severity int           `json:"severity"`
	Source   string        `json:"source"`
	Message  string        `json:"message"`
}

type protocolRange struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

func publishDiagnostics(out io.Writer, uri, source string) error {
	diagnostics := diagnosticsFor(uri, source)
	return writeFrame(out, notification{
		JSONRPC: "2.0",
		Method:  "textDocument/publishDiagnostics",
		Params: publishDiagnosticsParams{
			URI:         uri,
			Diagnostics: diagnostics,
		},
	})
}

func diagnosticsFor(uri, source string) []diagnostic {
	diagnostics := make([]diagnostic, 0)
	_, err := kyse.Parse(sourcePath(uri), source)
	if err == nil {
		return diagnostics
	}

	switch problems := err.(type) {
	case kyse.Errors:
		for _, problem := range problems {
			diagnostics = append(diagnostics, diagnosticFor(problem, source))
		}
	case *kyse.Error:
		diagnostics = append(diagnostics, diagnosticFor(problems, source))
	default:
		diagnostics = append(diagnostics, diagnostic{
			Range:    protocolRange{},
			Severity: 1,
			Source:   "kyse",
			Message:  err.Error(),
		})
	}
	return diagnostics
}

func diagnosticFor(problem *kyse.Error, source string) diagnostic {
	line := max(problem.Line-1, 0)
	lineText := sourceLine(source, line)
	message := problem.Message
	if problem.Hint != "" {
		message += "\n" + problem.Hint
	}
	return diagnostic{
		Range: protocolRange{
			Start: position{Line: line},
			End:   position{Line: line, Character: utf16Length(lineText)},
		},
		Severity: 1,
		Source:   "kyse",
		Message:  message,
	}
}

func sourceLine(source string, line int) string {
	lines := strings.Split(source, "\n")
	if line < 0 || line >= len(lines) {
		return ""
	}
	return strings.TrimSuffix(lines[line], "\r")
}

func utf16Length(value string) int {
	return len(utf16.Encode([]rune(value)))
}

func completionItems(source string, at position) []completionItem {
	insertPrefix := "@"
	if directiveAtSignBeforeUTF16Position(sourceLine(source, at.Line), at.Character) {
		insertPrefix = ""
	}
	directives := kyse.Directives()
	items := make([]completionItem, len(directives))
	for i, directive := range directives {
		items[i] = completionItem{
			Label:      "@" + directive,
			Kind:       14,
			InsertText: insertPrefix + directive,
		}
	}
	return items
}

func directiveAtSignBeforeUTF16Position(line string, character int) bool {
	if character <= 0 {
		return false
	}
	units := 0
	prefix := make([]rune, 0, len(line))
	for _, current := range line {
		width := utf16Length(string(current))
		if units+width > character {
			return false
		}
		units += width
		prefix = append(prefix, current)
		if units == character {
			break
		}
	}
	if units != character {
		return false
	}
	index := len(prefix) - 1
	for index >= 0 && directiveNameRune(prefix[index]) {
		index--
	}
	return index >= 0 && prefix[index] == '@'
}

func directiveNameRune(value rune) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func sourcePath(uri string) string {
	path, err := pathFromFileURI(uri, nativeFilePathStyle())
	if err != nil {
		return uri
	}
	return path
}

func projectGraphForProtocol(root string, graph doctor.ProjectGraph) (doctor.ProjectGraph, error) {
	for i := range graph.Nodes {
		node := &graph.Nodes[i]
		if node.File != "" {
			path := filepath.FromSlash(node.File)
			if !filepath.IsAbs(path) {
				path = filepath.Join(root, path)
			}
			uri, err := fileURIFromPath(filepath.Clean(path), nativeFilePathStyle())
			if err != nil {
				return doctor.ProjectGraph{}, err
			}
			node.File = uri
		}
		if node.Line > 0 {
			node.Line--
		}
		if node.Column > 0 {
			node.Column--
		}
	}
	return graph, nil
}

func readFrame(reader *bufio.Reader) ([]byte, error) {
	contentLength := -1
	readHeader := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF && !readHeader && line == "" {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("read LSP header: %w", err)
		}
		readHeader = true
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			break
		}

		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("invalid LSP header %q", line)
		}
		if !strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			continue
		}
		length, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || length < 0 {
			return nil, fmt.Errorf("invalid Content-Length %q", strings.TrimSpace(value))
		}
		contentLength = length
	}

	if contentLength < 0 {
		return nil, fmt.Errorf("LSP frame has no Content-Length header")
	}
	if contentLength > maxFrameSize {
		return nil, fmt.Errorf("LSP frame length %d exceeds maximum %d", contentLength, maxFrameSize)
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, fmt.Errorf("read LSP body: %w", err)
	}
	return body, nil
}

func writeResult(out io.Writer, id json.RawMessage, result any) error {
	encodedResult, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode JSON-RPC result: %w", err)
	}
	return writeFrame(out, response{
		JSONRPC: "2.0",
		ID:      bytes.Clone(id),
		Result:  encodedResult,
	})
}

func writeError(out io.Writer, id json.RawMessage, code int, message string) error {
	return writeFrame(out, response{
		JSONRPC: "2.0",
		ID:      bytes.Clone(id),
		Error:   &responseError{Code: code, Message: message},
	})
}

func writeFrame(out io.Writer, message any) error {
	body, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode JSON-RPC message: %w", err)
	}
	if _, err := fmt.Fprintf(out, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return fmt.Errorf("write LSP header: %w", err)
	}
	if _, err := out.Write(body); err != nil {
		return fmt.Errorf("write LSP body: %w", err)
	}
	return nil
}
