// Package lsp serves Kyse language intelligence over the Language Server Protocol.
package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/arandu-io/aru/internal/kyse"
)

const maxFrameSize = 16 << 20

// Serve reads Language Server Protocol frames from in and writes protocol
// responses and notifications to out. Messages are processed in input order.
func Serve(in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	shutdown := false
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
			return fmt.Errorf("decode JSON-RPC message: %w", err)
		}

		switch message.Method {
		case "initialize":
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
		case "exit":
			if !shutdown {
				return fmt.Errorf("exit received before shutdown")
			}
			return nil
		case "textDocument/didOpen":
			var params didOpenParams
			if err := json.Unmarshal(message.Params, &params); err != nil {
				return fmt.Errorf("decode didOpen params: %w", err)
			}
			if err := publishDiagnostics(out, params.TextDocument.URI, params.TextDocument.Text); err != nil {
				return err
			}
		case "textDocument/didChange":
			var params didChangeParams
			if err := json.Unmarshal(message.Params, &params); err != nil {
				return fmt.Errorf("decode didChange params: %w", err)
			}
			if len(params.ContentChanges) == 0 {
				return fmt.Errorf("didChange has no content changes")
			}
			text := params.ContentChanges[len(params.ContentChanges)-1].Text
			if err := publishDiagnostics(out, params.TextDocument.URI, text); err != nil {
				return err
			}
		case "textDocument/didClose":
			var params didCloseParams
			if err := json.Unmarshal(message.Params, &params); err != nil {
				return fmt.Errorf("decode didClose params: %w", err)
			}
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
			directives := kyse.Directives()
			items := make([]completionItem, len(directives))
			for i, directive := range directives {
				items[i] = completionItem{
					Label:      "@" + directive,
					Kind:       14,
					InsertText: directive,
				}
			}
			if err := writeResult(out, message.ID, items); err != nil {
				return err
			}
		}
	}
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
	Result  json.RawMessage `json:"result"`
}

type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type initializeResult struct {
	Capabilities serverCapabilities `json:"capabilities"`
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

type textDocumentIdentifier struct {
	URI string `json:"uri"`
}

type textDocumentItem struct {
	URI  string `json:"uri"`
	Text string `json:"text"`
}

type didOpenParams struct {
	TextDocument textDocumentItem `json:"textDocument"`
}

type didChangeParams struct {
	TextDocument   textDocumentIdentifier `json:"textDocument"`
	ContentChanges []contentChange        `json:"contentChanges"`
}

type contentChange struct {
	Text string `json:"text"`
}

type didCloseParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
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

func sourcePath(uri string) string {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "file" {
		return uri
	}
	return parsed.Path
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
