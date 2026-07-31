package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMakeAuthProducesCodeThatCompiles is the expensive half of the check: it
// generates the starter kit into a throwaway project and builds it against real
// checkouts of the framework and of porang.
//
// It skips when the sibling checkouts are not there, which is the case in CI --
// the cheap structural checks in internal/gen are what run everywhere. This one
// exists because "the template renders" and "the template compiles" are
// different claims, and only the second one matters to the person running the
// command.
func TestMakeAuthProducesCodeThatCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a project: skipped under -short")
	}

	framework, porang := siblingCheckouts(t)
	templ := findTempl(t)

	project := t.TempDir()
	writeFile(t, filepath.Join(project, "go.mod"), `module example.test/project

go 1.25

require (
	github.com/arandu-io/framework v0.2.1
	github.com/arandu-io/porang v0.2.1
)

replace github.com/arandu-io/framework => `+framework+`

replace github.com/arandu-io/porang => `+porang+`
`)
	writeFile(t, filepath.Join(project, "cmd", "app", "main.go"), "package main\n\nfunc main() {}\n")

	chdir(t, project)
	var out, errOut strings.Builder
	if err := makeAuth(nil, &out, &errOut); err != nil {
		t.Fatalf("make:auth: %v\n%s", err, errOut.String())
	}
	for _, name := range []string{"module.go", "handlers.go", "views.templ"} {
		if !strings.Contains(out.String(), name) {
			t.Errorf("%s was not reported as created:\n%s", name, out.String())
		}
	}

	runInDir(t, project, templ, "generate")
	runInDir(t, project, goTool(t), "mod", "tidy")
	runInDir(t, project, goTool(t), "build", "./...")
}

func siblingCheckouts(t *testing.T) (framework, porang string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(wd)
	framework = filepath.Join(parent, "framework")
	porang = filepath.Join(parent, "porang")
	for _, dir := range []string{framework, porang} {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
			t.Skipf("no checkout at %s: this test needs the sibling repositories", dir)
		}
	}
	return framework, porang
}

func findTempl(t *testing.T) string {
	t.Helper()
	if path, err := exec.LookPath("templ"); err == nil {
		return path
	}
	home, err := os.UserHomeDir()
	if err == nil {
		candidate := filepath.Join(home, "go", "bin", "templ")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	t.Skip("templ is not installed: run `aru view:build` once, or go install github.com/a-h/templ/cmd/templ@latest")
	return ""
}

func goTool(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}
	return path
}

func runInDir(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	// The generated project resolves its dependencies for real, so the module
	// cache and the network settings of the machine apply.
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", filepath.Base(name), strings.Join(args, " "), err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
}
