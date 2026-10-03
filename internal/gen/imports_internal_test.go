package gen

import (
	"strings"
	"testing"
)

// TestTidyImportsKeepsAMajorVersionImportTheFileUses pins that an import whose
// last path element is a major-version suffix binds the element before it:
// math/rand/v2 binds rand, and a file that calls rand.N keeps the import.
func TestTidyImportsKeepsAMajorVersionImportTheFileUses(t *testing.T) {
	src := []byte("package p\n\nimport (\n\t\"math/rand/v2\"\n\t\"strings\"\n)\n\nfunc F() int { return rand.N(10) }\n")
	out, err := TidyImports(src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"math/rand/v2"`) {
		t.Fatalf("the used math/rand/v2 import was dropped:\n%s", out)
	}
	if strings.Contains(string(out), `"strings"`) {
		t.Fatalf("the unused strings import was kept:\n%s", out)
	}
}

func TestAssumedImportNameSkipsTheMajorVersionSuffix(t *testing.T) {
	for path, want := range map[string]string{
		"math/rand/v2":           "rand",
		"github.com/acme/lib/v3": "lib",
		"github.com/acme/v2lib":  "v2lib",
		"example.test/app/v1":    "v1",
		"gopkg.in/yaml.v3":       "yaml.v3",
		"strings":                "strings",
	} {
		if got := AssumedImportName(path); got != want {
			t.Errorf("AssumedImportName(%q) = %q, want %q", path, got, want)
		}
	}
}
