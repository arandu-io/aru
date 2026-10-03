package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWhatMakeModuleWritesIsWhatModelBuildWould: the query file a module comes
// with is rendered by the renderer model:build runs, so --check on a project
// fresh from make:module has nothing to say. Two renderers would disagree the
// first time one of them changed, and every build would rewrite the file the
// generator had just written.
func TestWhatMakeModuleWritesIsWhatModelBuildWould(t *testing.T) {
	root := projectWithModule(t, "purchase_order")
	t.Chdir(root)

	if code, stdout, stderr := exercise(t, "model:build", "--check"); code != 0 {
		t.Fatalf("--check on a fresh module exited %d:\n%s%s", code, stdout, stderr)
	}
}

// TestCheckFailsUntilTheBuildRuns: --check names the file and exits 1, writes
// nothing, and passes once model:build has run.
func TestCheckFailsUntilTheBuildRuns(t *testing.T) {
	root := projectWithModule(t, "purchase_order")
	t.Chdir(root)
	query := filepath.Join(root, "app", "Models", "PurchaseOrderQuery.go")
	if err := os.Remove(query); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := exercise(t, "model:build", "--check")
	if code == 0 {
		t.Fatal("--check passed with the query file missing")
	}
	if !strings.Contains(stdout, "app/Models/PurchaseOrderQuery.go is missing") {
		t.Errorf("--check does not name the missing file: %q", stdout)
	}
	if !strings.Contains(stderr, "run `aru model:build`") {
		t.Errorf("--check does not say what fixes it: %q", stderr)
	}
	if _, err := os.Stat(query); err == nil {
		t.Fatal("--check wrote the file it was only asked to check")
	}

	if code, stdout, stderr := exercise(t, "model:build"); code != 0 || !strings.Contains(stdout, "wrote app/Models/PurchaseOrderQuery.go") {
		t.Fatalf("model:build exited %d and said %q %q", code, stdout, stderr)
	}
	if code, stdout, _ := exercise(t, "model:build", "--check"); code != 0 || stdout != "" {
		t.Errorf("--check after the build exited %d and said %q", code, stdout)
	}
}

// TestAModuleWithNoEntityIsLeftAlone: a module root with no app/, no entity
// and no arandu.toml is a place the command runs, and it writes nothing and
// says nothing there.
func TestAModuleWithNoEntityIsLeftAlone(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.test/library\n")
	writeFile(t, filepath.Join(root, "library.go"), "package library\n\nfunc Answer() int { return 42 }\n")
	t.Chdir(root)

	for _, args := range [][]string{{"model:build"}, {"model:build", "--check"}} {
		if code, stdout, stderr := exercise(t, args...); code != 0 || stdout != "" || stderr != "" {
			t.Errorf("%v exited %d and said %q %q", args, code, stdout, stderr)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("model:build wrote into a module with no entity: %d entries", len(entries))
	}
}
