package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMakeAuthProducesCodeThatCompiles is the expensive half of the check: it
// generates the starter kit into a throwaway project and builds it against a
// real checkout of the framework.
//
// It skips when the sibling checkout is not there, which is the case in CI --
// the cheap structural checks in internal/gen are what run everywhere. This one
// exists because "the template renders" and "the template compiles" are
// different claims, and only the second one matters to the person running the
// command.
func TestMakeAuthProducesCodeThatCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a project: skipped under -short")
	}

	framework := frameworkCheckout(t)

	project := t.TempDir()
	writeFile(t, filepath.Join(project, "go.mod"), `module example.test/project

go 1.25

require github.com/arandu-io/framework v0.11.2

replace github.com/arandu-io/framework => `+framework+`
`)
	writeFile(t, filepath.Join(project, "main.go"), "package main\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(project, "arandu.toml"), "name = \"test\"\n")

	// The base controller every controller in the directory embeds. A real
	// project gets it from `aru new`; the kit publishes HomeController into the
	// same package and expects it to be there.
	writeFile(t, filepath.Join(project, "app", "Http", "Controllers", "Controller.go"),
		"package controllers\n\n// Controller is what every controller embeds.\ntype Controller struct{}\n")

	chdir(t, project)
	var out, errOut strings.Builder
	if err := makeAuth(nil, &out, &errOut); err != nil {
		t.Fatalf("make:auth: %v\n%s", err, errOut.String())
	}
	for _, name := range []string{"LoginController.go", "LoginController_handlers.go", "auth/login.kyse.go"} {
		if !strings.Contains(out.String(), name) {
			t.Errorf("%s was not reported as created:\n%s", name, out.String())
		}
	}

	runInDir(t, project, goTool(t), "mod", "tidy")
	runInDir(t, project, goTool(t), "build", "./...")
}

// frameworkCheckout is the sibling framework repository, which the generated
// project builds against. Without it the module resolves to the published
// version and the test would be checking a release rather than this working tree.
func frameworkCheckout(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(filepath.Dir(wd), "framework")
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Skipf("no checkout at %s: this test needs the sibling repository", dir)
	}
	return dir
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
