package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAProjectPinnedBelowTheModelCoreIsRefused: the entity make:model and
// make:module write embeds the non-generic model.Model, so a project on an
// earlier hesape would receive files that do not compile. The refusal names the
// pin, the release that has the core and the two commands that move the
// project, and writes nothing.
func TestAProjectPinnedBelowTheModelCoreIsRefused(t *testing.T) {
	for _, command := range [][]string{
		{"make:model", "Invoice", "--fields", "reference:string"},
		{"make:module", "invoice", "--fields", "reference:string"},
		{"make:factory", "Invoice"},
	} {
		t.Run(command[0], func(t *testing.T) {
			root := bareProject(t)
			writeFile(t, filepath.Join(root, "go.mod"),
				"module example.test/project\n\ngo 1.26\n\nrequire (\n\tgithub.com/arandu-io/framework v0.50.2\n\tgithub.com/arandu-io/hesape v0.46.0\n)\n")
			t.Chdir(root)

			code, _, stderr := exercise(t, command...)
			if code == 0 {
				t.Fatal("the command ran in a project pinned below the model core")
			}
			for _, want := range []string{
				"pins github.com/arandu-io/hesape v0.46.0",
				"needs " + modelCoreRelease + " or later",
				"go run github.com/arandu-io/aru/cmd/model-upgrade@latest ./...",
				"go get github.com/arandu-io/hesape@" + modelCoreRelease,
			} {
				if !strings.Contains(stderr, want) {
					t.Errorf("the refusal does not say %q:\n%s", want, stderr)
				}
			}
			if _, err := os.Stat(filepath.Join(root, "app")); err == nil {
				t.Error("the command refused and wrote files anyway")
			}
		})
	}
}

// TestTheFloorReadsWhatGoModMeans: the pin after a replace that names a
// version, nothing for a replace with a directory or a project that does not
// require hesape, and a pre-release of the core release below the release.
func TestTheFloorReadsWhatGoModMeans(t *testing.T) {
	for _, c := range []struct {
		name   string
		gomod  string
		refuse bool
	}{
		{"below", "require github.com/arandu-io/hesape v0.46.0\n", true},
		{"at", "require github.com/arandu-io/hesape v0.47.0\n", false},
		{"above", "require (\n\tgithub.com/arandu-io/hesape v0.48.2 // indirect\n)\n", false},
		{"pre-release of the core", "require github.com/arandu-io/hesape v0.47.0-rc.1\n", true},
		{"pseudo-version after the last tag", "require github.com/arandu-io/hesape v0.46.1-0.20261003120000-abcdef123456\n", true},
		{"replaced by a newer version", "require github.com/arandu-io/hesape v0.46.0\nreplace github.com/arandu-io/hesape => github.com/arandu-io/hesape v0.47.1\n", false},
		{"replaced by a directory", "require github.com/arandu-io/hesape v0.46.0\nreplace github.com/arandu-io/hesape => ../hesape\n", false},
		{"not required", "require github.com/arandu-io/framework v0.50.2\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "go.mod"), "module example.test/project\n\ngo 1.26\n\n"+c.gomod)
			err := requireModelCore("make:model", root)
			if (err != nil) != c.refuse {
				t.Errorf("refused = %v, want %v (%v)", err != nil, c.refuse, err)
			}
		})
	}
}
