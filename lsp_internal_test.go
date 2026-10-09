package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestLSPCommandUsesStandardInputAndWritesOnlyProtocolToStandardOutput(t *testing.T) {
	input := lspTestFrames(
		`{"jsonrpc":"2.0","id":"cli-1","method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	)
	cmd := exec.Command(os.Args[0], "-test.run=^TestLSPCommandHelper$")
	cmd.Env = append(os.Environ(), "ARU_LSP_TEST_HELPER=1")
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("run aru lsp helper: %v\nstderr:\n%s", err, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("aru lsp stderr = %q, want no errors", stderr.String())
	}

	messages := readLSPTestFrames(t, stdout.Bytes())
	if len(messages) != 2 {
		t.Fatalf("protocol messages = %d, want initialize and shutdown responses", len(messages))
	}
	var response struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(messages[0], &response); err != nil {
		t.Fatalf("decode initialize response: %v", err)
	}
	if string(response.ID) != `"cli-1"` {
		t.Fatalf("initialize response id = %s, want cli-1 unchanged", response.ID)
	}
}

func TestLSPCommandHelper(t *testing.T) {
	if os.Getenv("ARU_LSP_TEST_HELPER") != "1" {
		return
	}
	os.Exit(run([]string{"lsp"}, os.Stdout, os.Stderr))
}

func lspTestFrames(messages ...string) []byte {
	var out bytes.Buffer
	for _, message := range messages {
		fmt.Fprintf(&out, "Content-Length: %d\r\n\r\n%s", len(message), message)
	}
	return out.Bytes()
}

func readLSPTestFrames(t *testing.T, data []byte) [][]byte {
	t.Helper()
	reader := bufio.NewReader(bytes.NewReader(data))
	var messages [][]byte
	for {
		header, err := reader.ReadString('\n')
		if err == io.EOF && header == "" {
			return messages
		}
		if err != nil {
			t.Fatalf("read frame header: %v", err)
		}
		header = strings.TrimSuffix(strings.TrimSuffix(header, "\n"), "\r")
		name, value, ok := strings.Cut(header, ":")
		if !ok || !strings.EqualFold(name, "Content-Length") {
			t.Fatalf("non-protocol stdout = %q", header)
		}
		length, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			t.Fatalf("parse content length: %v", err)
		}
		if separator, err := reader.ReadString('\n'); err != nil || separator != "\r\n" {
			t.Fatalf("frame separator = %q, %v", separator, err)
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(reader, body); err != nil {
			t.Fatalf("read frame body: %v", err)
		}
		messages = append(messages, body)
	}
}
