package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The helpers below are shared across the test files of this package, so they
// live on their own instead of inside whichever one happened to be written
// first.

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
