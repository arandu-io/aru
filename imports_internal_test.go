package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFixPreviewsThenWritesThenHasNothingLeft: --fix shows a diff and writes
// nothing, --fix --apply writes, and a third run finds every import already
// canonical. The generated Go of a view is never touched; its source is.
func TestFixPreviewsThenWritesThenHasNothingLeft(t *testing.T) {
	root := mcpProject(t)
	service := filepath.Join(root, "app", "Services", "Ledger.go")
	writeFile(t, service, `package services

import "github.com/arandu-io/framework/security"

func Allowed(g security.Grant) bool { return g != security.Grant{} }
`)
	view := filepath.Join(root, "resources", "views", "portal", "quote.kyse.go")
	writeFile(t, view, "//go:build kyse\n\npackage portal\n\nimport \"github.com/arandu-io/framework/security\"\n\n@go\ntype QuoteData struct{ G security.Grant }\n@endgo\n")
	generated := filepath.Join(root, "storage", "framework", "views", "portal", "quote.go")
	generatedBody := "package portal\n\nimport \"github.com/arandu-io/framework/security\"\n\ntype QuoteData struct{ G security.Grant }\n"
	writeFile(t, generated, generatedBody)
	t.Chdir(root)

	before, _ := os.ReadFile(service)
	code, stdout, stderr := exercise(t, "imports:catalog", "--fix")
	if code != 0 {
		t.Fatalf("--fix exited %d: %s", code, stderr)
	}
	for _, want := range []string{"+++ b/app/Services/Ledger.go", "+import \"github.com/arandu-io/hesape/auth\"", "resources/views/portal/quote.kyse.go", "2 file(s) would change"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the preview does not carry %q:\n%s", want, stdout)
		}
	}
	if after, _ := os.ReadFile(service); string(after) != string(before) {
		t.Fatal("--fix without --apply wrote the file")
	}

	if code, _, stderr = exercise(t, "imports:catalog", "--fix", "--apply"); code != 0 {
		t.Fatalf("--fix --apply exited %d: %s", code, stderr)
	}
	after, _ := os.ReadFile(service)
	if !strings.Contains(string(after), "auth.Grant") || strings.Contains(string(after), "framework/security") {
		t.Errorf("--apply did not rewrite the service:\n%s", after)
	}
	if body, _ := os.ReadFile(generated); string(body) != generatedBody {
		t.Errorf("the generated Go of a view was rewritten:\n%s", body)
	}

	_, stdout, _ = exercise(t, "imports:catalog", "--fix")
	if !strings.Contains(stdout, "every import already names its symbols by their canonical path") {
		t.Errorf("a second --fix found something to do:\n%s", stdout)
	}

	if code, _, stderr = exercise(t, "imports:catalog", "--apply"); code == 0 || !strings.Contains(stderr, "--fix") {
		t.Errorf("--apply alone was accepted: %d %s", code, stderr)
	}
}
