package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestViewBuildRefusesToReportSuccessOverBrokenGo is the command-level half of
// the same guarantee.
//
// A view with no `@extends` whose struct sits outside the `@go` block produced
// `_ = data` followed by `d.Name`. That parses, so the compiler wrote the file,
// printed "1 view(s) compiled" and exited zero -- and the person found out at
// `go build`, as `undefined: d`, in a file whose header says DO NOT EDIT.
func TestViewBuildRefusesToReportSuccessOverBrokenGo(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, viewsDir, "home"+viewSuffix)
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	// The `type` line is markup here, not Go: only an @go block declares.
	if err := os.WriteFile(source, []byte(
		"//go:build kyse\n\npackage views\n\ntype HomeData struct{ Name string }\n\n<h1>Hello {{ .Name }}</h1>\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	out := &strings.Builder{}
	err := compileViews(root, out)
	if err == nil {
		t.Fatal("view:build reported success over Go that does not compile")
	}
	if strings.Contains(out.String(), "compiled") {
		t.Errorf("it also printed a success line:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "home"+viewSuffix) {
		t.Errorf("the error does not name the view: %v", err)
	}
	if !strings.Contains(err.Error(), "@go") {
		t.Errorf("the error does not say what to do: %v", err)
	}
}
