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
	// workspace is what the tree on disk declares, and stays nil until an
	// initialize names a root that resolves. Everything that answers about the
	// project checks it, so a client that opened no folder gets an empty answer
	// rather than one read from whatever directory the process was started in.
	var workspace *project
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
			if rootURI != "" {
				root, err := pathFromFileURI(rootURI, nativeFilePathStyle())
				if err == nil {
					workspace = newProject(root)
				}
			}
			initialized = true
			result := initializeResult{
				Capabilities: serverCapabilities{
					PositionEncoding: "utf-16",
					TextDocumentSync: textDocumentSyncOptions{
						OpenClose: true,
						Change:    1,
					},
					DefinitionProvider: true,
					CompletionProvider: completionOptions{
						TriggerCharacters: []string{"@", "-"},
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
			// Go source is completed by the Go language server, which knows the
			// types. Offering the view vocabulary there would put directives and
			// asset names in a list beside identifiers that actually compile.
			items := []completionItem{}
			if !goSourceDocument(params.TextDocument.URI) {
				items = workspace.completionItems(documents[params.TextDocument.URI], at)
			}
			if err := writeResult(out, message.ID, items); err != nil {
				return err
			}
		case "textDocument/definition":
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
			source := documents[params.TextDocument.URI]
			// The two languages are asked different questions. A view names
			// components and layouts, and both resolve here; Go source is the Go
			// language server's, and the one thing it cannot resolve there is
			// the view a string names.
			var locations []protocolLocation
			if goSourceDocument(params.TextDocument.URI) {
				locations = workspace.viewDefinitionsInGoSource(source, at)
			} else {
				locations = workspace.definitionsFor(source, at)
			}
			if err := writeResult(out, message.ID, locations); err != nil {
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
	case "initialize", "shutdown", "textDocument/completion", "textDocument/definition", "arandu/projectGraph":
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
	PositionEncoding string                  `json:"positionEncoding"`
	TextDocumentSync textDocumentSyncOptions `json:"textDocumentSync"`
	// DefinitionProvider is what makes go-to-definition appear in an editor.
	// A client registers the feature from this alone, so the whole of what
	// the adapter has to do about it is nothing.
	DefinitionProvider bool              `json:"definitionProvider"`
	CompletionProvider completionOptions `json:"completionProvider"`
}

// protocolLocation is one place an editor can open, in the protocol's
// zero-based coordinates.
type protocolLocation struct {
	URI   string        `json:"uri"`
	Range protocolRange `json:"range"`
}

type textDocumentSyncOptions struct {
	OpenClose bool `json:"openClose"`
	Change    int  `json:"change"`
}

type completionOptions struct {
	TriggerCharacters []string `json:"triggerCharacters"`
}

type completionItem struct {
	Label         string `json:"label"`
	Kind          int    `json:"kind"`
	Detail        string `json:"detail,omitempty"`
	Documentation string `json:"documentation,omitempty"`
	InsertText    string `json:"insertText"`
	SortText      string `json:"sortText,omitempty"`
	Tags          []int  `json:"tags,omitempty"`
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

// goSourceDocument reports whether a document is Go the compiler reads.
//
// A `.kyse.go` ends in `.go` and is not Go -- it carries markup below the
// package clause -- so the longer suffix is what decides, and it is checked
// first.
func goSourceDocument(uri string) bool {
	name := sourcePath(uri)
	return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, ".kyse.go")
}

func diagnosticsFor(uri, source string) []diagnostic {
	diagnostics := make([]diagnostic, 0)
	// Go source is not markup, and the view parser reads it as markup: a
	// controller run through it comes back as a file of errors about a language
	// it is not written in.
	if goSourceDocument(uri) {
		return diagnostics
	}
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

// completionItems chooses which vocabulary the cursor is in.
//
// The order is most specific first. An asset name and a package member are
// decided by what encloses the cursor -- a particular call, a particular
// qualifier -- and answering either of them with the directive list would be
// answering a question nobody asked.
//
// Both project-backed families return nothing when the tree cannot answer, and
// the fall-through is the vocabulary that is true of every view: the directives
// the compiler knows and the attributes the runtime serves.
func (p *project) completionItems(source string, at position) []completionItem {
	if assetCompletionAt(source, at) {
		if items := p.assetCompletionItems(); len(items) > 0 {
			return items
		}
		return []completionItem{}
	}
	if qualifier, ok := memberCompletionAt(source, at); ok {
		if items := p.memberCompletionItems(source, qualifier); len(items) > 0 {
			return items
		}
		return []completionItem{}
	}
	if htmlAttributeNamePosition(source, at) {
		return htmxCompletionItems()
	}

	insertPrefix := "@"
	if directiveAtSignBeforeUTF16Position(sourceLine(source, at.Line), at.Character) {
		insertPrefix = ""
	}
	directives := kyse.Directives()
	items := make([]completionItem, len(directives))
	for i, directive := range directives {
		items[i] = completionItem{
			Label:      "@" + directive,
			Kind:       completionItemKindKeyword,
			InsertText: insertPrefix + directive,
		}
	}
	return items
}

const (
	completionItemKindProperty = 10
	completionItemKindKeyword  = 14
)

// htmxAttribute is one row of the table htmxgen writes.
//
// The table is generated rather than typed because the hand-written one covered
// thirteen of the thirty-five attributes and had no way of saying so.
type htmxAttribute struct {
	name          string
	detail        string
	documentation string
	// deprecated marks an attribute HTMX still answers to and no longer
	// recommends. It is offered, and it is offered struck through: leaving it
	// out would make an attribute already written in a view look unknown.
	deprecated bool
}

// completionItemTagDeprecated is the protocol's tag for an item an editor
// should draw struck through.
const completionItemTagDeprecated = 1

func htmxCompletionItems() []completionItem {
	items := make([]completionItem, len(htmxAttributes))
	for index, attribute := range htmxAttributes {
		items[index] = completionItem{
			Label:         attribute.name,
			Kind:          completionItemKindProperty,
			Detail:        attribute.detail,
			Documentation: attribute.documentation,
			InsertText:    attribute.name,
			SortText:      fmt.Sprintf("%02d-%s", index, attribute.name),
		}
		if attribute.deprecated {
			items[index].Tags = []int{completionItemTagDeprecated}
		}
	}
	return items
}

func htmlAttributeNamePosition(source string, at position) bool {
	prefix, ok := sourcePrefixAtUTF16Position(source, at)
	if !ok {
		return false
	}

	insideTag := false
	quote := byte(0)
	tagStart := -1
	for index := 0; index < len(prefix); index++ {
		if !insideTag {
			if prefix[index] != '<' {
				continue
			}
			if strings.HasPrefix(prefix[index:], "<!--") {
				end := strings.Index(prefix[index+4:], "-->")
				if end < 0 {
					return false
				}
				index += end + 6
				continue
			}
			insideTag = true
			tagStart = index + 1
			continue
		}

		current := prefix[index]
		if quote != 0 {
			if current == quote {
				quote = 0
			}
			continue
		}
		switch current {
		case '\'', '"':
			quote = current
		case '>':
			insideTag = false
			tagStart = -1
		case '<':
			tagStart = index + 1
		}
	}
	if !insideTag || quote != 0 || tagStart < 0 {
		return false
	}

	tag := strings.TrimSpace(prefix[tagStart:])
	if tag == "" || strings.HasPrefix(tag, "/") || strings.HasPrefix(tag, "!") || strings.HasPrefix(tag, "?") {
		return false
	}
	return strings.ContainsAny(tag, " \t\r\n")
}

func sourcePrefixAtUTF16Position(source string, at position) (string, bool) {
	lines := strings.Split(source, "\n")
	if at.Line < 0 || at.Line >= len(lines) {
		return "", false
	}
	line := strings.TrimSuffix(lines[at.Line], "\r")
	prefix, ok := linePrefixAtUTF16Position(line, at.Character)
	if !ok {
		return "", false
	}
	if at.Line == 0 {
		return prefix, true
	}
	return strings.Join(lines[:at.Line], "\n") + "\n" + prefix, true
}

func linePrefixAtUTF16Position(line string, character int) (string, bool) {
	if character < 0 {
		return "", false
	}
	units := 0
	var prefix strings.Builder
	for _, current := range line {
		width := utf16Length(string(current))
		if units+width > character {
			return "", false
		}
		units += width
		prefix.WriteRune(current)
		if units == character {
			return prefix.String(), true
		}
	}
	return prefix.String(), units == character
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
