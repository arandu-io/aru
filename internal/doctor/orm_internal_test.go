package doctor

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The model-first project.
//
// testdata/orm is a tree with no app/Repositories in it and no type named
// InvoiceRepository anywhere. That absence is the fixture: every authorization
// rule used to be gated on one or the other, so a project shaped like this
// reported nothing at all -- and a rule that finds nothing does not fail, it
// passes. The tree existing is what keeps that from happening again, and it is
// a checklist item rather than an assertion because a fixture nobody wrote
// cannot fail.

// TestTheModelFirstProjectIsAudited plants two violations and requires both.
func TestTheModelFirstProjectIsAudited(t *testing.T) {
	findings, err := Run("testdata/orm", Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := map[string]string{
		"grant-not-checked":         "Unauthorized",
		"handler-reaches-the-model": "Index",
	}

	got := map[string]string{}
	for _, f := range findings {
		got[f.Rule] = f.Message
	}

	for rule, method := range want {
		message, fired := got[rule]
		if !fired {
			t.Errorf("%s did not fire on a project with no repositories in it", rule)
			continue
		}
		if !strings.Contains(message, method) {
			t.Errorf("%s fired on %q, want it to name %s", rule, message, method)
		}
	}

	// And nothing else. A rule that reports the clean half of this tree is a
	// rule firing on correct code, which is how a tool teaches people to ignore
	// it.
	for _, f := range findings {
		if _, planted := want[f.Rule]; !planted {
			t.Errorf("the clean half was reported: %s:%d [%s] %s", f.File, f.Line, f.Rule, f.Message)
		}
	}
}

// TestTheModelFirstProjectHasNoRepositories guards the fixture itself.
//
// The tree is only a test of the re-gating while it has no repositories in it.
// Somebody adding one to make another test easier would turn the file above
// green for the wrong reason, and the rules would go quiet again with nothing
// saying so.
func TestTheModelFirstProjectHasNoRepositories(t *testing.T) {
	entries, err := os.ReadDir("testdata/orm/app")
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() == "Repositories" {
			t.Fatal("testdata/orm grew an app/Repositories; this fixture is only a test of the re-gating while it has none")
		}
	}

	// And no type named like one, which is the other half of the old gate.
	err = filepath.WalkDir("testdata/orm", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(source), "Repository struct") {
			t.Errorf("%s declares a repository type", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the fixture: %v", err)
	}
}
