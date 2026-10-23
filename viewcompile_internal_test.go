package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
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

// TestViewBuildRefusesWhenItsOwnNamespaceIsBroken is the command-level half of
// the check that reads the output back.
//
// The generated file writes every name of its own under a reserved prefix, and
// a view that writes one takes it away. `loopBinding` refuses it as a loop
// binding; nothing refused it inside an `@go` block, which is Go the person
// wrote and is copied out verbatim. What came out declared the writer twice,
// which parses and formats -- so the command wrote the file, printed
// "1 view(s) compiled" and exited 0, and the failure arrived at `go build`.
func TestViewBuildRefusesWhenItsOwnNamespaceIsBroken(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, viewsDir, "home"+viewSuffix)
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte(
		"//go:build kyse\n\npackage views\n\n@go\ntype HomeData struct{ Name string }\n\nvar kyse__w int\n@endgo\n\n<h1>{{ .Name }}</h1>\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	out := &strings.Builder{}
	err := compileViews(root, out)
	if err == nil {
		t.Fatal("view:build reported success over Go whose writer the view had renamed")
	}
	if strings.Contains(out.String(), "compiled") {
		t.Errorf("it also printed a success line:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "kyse__w") {
		t.Errorf("the error does not name what was written: %v", err)
	}
	if !strings.Contains(err.Error(), "home"+viewSuffix) {
		t.Errorf("the error does not name the view: %v", err)
	}
}

// TestAFailedBuildDoesNotLeaveTheLastOutputBehind: the view gained a field
// and broke in the same edit, and the build that refused it used to leave the
// output of the previous build in place. go build then compiled the old view,
// and a controller filling the new field was told the field did not exist.
// What is left now is a file that stops the build at the view's line.
func TestAFailedBuildDoesNotLeaveTheLastOutputBehind(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/stale\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, viewsDir, "notes", "index"+viewSuffix)
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(source, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("//go:build kyse\n\npackage notes\n\n@go\ntype IndexData struct{ Title string }\n@endgo\n\n<h1>{{ .Title }}</h1>\n")
	if err := compileViews(root, &strings.Builder{}); err != nil {
		t.Fatalf("the first build: %v", err)
	}
	target := filepath.Join(root, "storage", "framework", "views", "notes", "index.go")
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("the first build wrote nothing: %v", err)
	}

	// Line 9 closes a section that was never opened.
	write("//go:build kyse\n\npackage notes\n\n@go\ntype IndexData struct{ Title, Subtitle string }\n@endgo\n\n@endsection\n")
	if err := compileViews(root, &strings.Builder{}); err == nil {
		t.Fatal("a view with @endsection and no @section compiled")
	}
	stale, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("the compiled output is gone rather than replaced: %v", err)
	}
	if strings.Contains(string(stale), "IndexData") {
		t.Errorf("the output of the previous build is still in place:\n%s", stale)
	}

	// And the build that follows stops at the view, at the line that failed.
	tool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go toolchain on PATH")
	}
	cmd := exec.Command(tool, "build", "./...")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("go build accepted the stand-in for a view that did not compile")
	}
	if !strings.Contains(string(out), "resources/views/notes/index.kyse.go:9") || !strings.Contains(string(out), "viewDidNotCompile") {
		t.Errorf("go build does not stop at the view's line:\n%s", out)
	}
}

// TestAnAnalysisOfTheCompiledViewLandsInTheView: go vet, gopls and every
// analysis built on go/parser resolve a relative line directive against the
// directory of the file that carries it. The compiled view lives in
// storage/framework/views, so a directive naming resources/views/... from
// there pointed at storage/framework/views/notes/resources/views/..., a file
// that does not exist. Every position the parser maps has to be the view or
// the compiled file, and both have to be on disk.
func TestAnAnalysisOfTheCompiledViewLandsInTheView(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, viewsDir, "notes", "index"+viewSuffix)
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("//go:build kyse\n\npackage notes\n\n@go\ntype IndexData struct{ Title string }\n@endgo\n\n<h1>{{ .Title }}</h1>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := compileViews(root, &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "storage", "framework", "views", "notes", "index.go")

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, target, nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	landed := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		if n != nil {
			landed[fset.Position(n.Pos()).Filename] = true
		}
		return true
	})
	if !landed[source] {
		t.Errorf("no position of the compiled view resolves to %s; they resolve to %v", source, landed)
	}
	for name := range landed {
		if _, err := os.Stat(name); err != nil {
			t.Errorf("a position resolves to %s, which is not on disk", name)
		}
	}
}
