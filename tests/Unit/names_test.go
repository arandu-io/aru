package unit_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/arandu-io/aru/tests"
)

// A name from somewhere else must not be written in this module's Go.
//
// Part of this command began as a copy of an existing tool, and the record of
// where it came from belongs in the two files that exist to hold it: the
// licence, and the note beside it. Anywhere else the name is residue. It
// reaches further than a comment reads: an environment variable is an interface
// a pipeline sets, an error message is what a user pastes into a search, and a
// package path becomes a symbol in every binary built from here.
//
// This was found by grepping the tree after the port: one environment variable
// and one comment still carried the other project's name, and the variable was
// the one that mattered -- it was the documented way to hand the packager a
// signing password, spelled with a name nobody could look up.

// foreign are the names that must not appear in a Go file of this module.
//
// Written as whole words: the shortest of them is three letters, and without a
// boundary it matches inside ordinary ones.
var foreign = []string{"gogio", "gioui", "gio"}

// TestNoSourceFileCarriesAForeignName reads every Go file the module ships.
//
// Files under testdata/ are left out, as they are everywhere else here: the go
// tool does not build them, nothing there is a package, and some of it does not
// parse on purpose.
func TestNoSourceFileCarriesAForeignName(t *testing.T) {
	pattern := regexp.MustCompile(`(?i)\b(` + strings.Join(foreign, "|") + `)\b`)
	root := tests.Root(t)

	// This file writes the names out, which is the one place in the module that
	// has to. Reading itself, it would report itself forever.
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("this file cannot say where it is, so it cannot leave itself out of the walk")
	}

	read := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if skipped(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || path == self {
			return nil
		}

		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		read++

		for _, found := range pattern.FindAll(source, -1) {
			relative, _ := filepath.Rel(root, path)
			t.Errorf("%s writes %q, which names another project", relative, found)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// A walk that matched nothing any more would pass in silence, and so would
	// one pointed at the wrong directory. The floor is far below what this
	// module has, so it fails on a broken walk rather than on a deleted file.
	if read < 120 {
		t.Errorf("only %d Go files were read, and this module has many more; the walk found nothing to check", read)
	}
}

// skipped answers whether a directory is outside what this module ships.
func skipped(name string) bool {
	switch {
	case name == "testdata", name == "vendor", name == "node_modules":
		return true
	case strings.HasPrefix(name, "."):
		// Version control, editor state, and the scratch worktrees an agent
		// works in. None of them is compiled.
		return name != "."
	}
	return false
}
