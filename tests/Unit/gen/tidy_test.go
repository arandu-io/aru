package gen_test

import (
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/gen"
)

// TestTidyImportsReadsAShadowingLocalAsNoUse pins that a selector on a local
// variable named like an import is no use of the import: the variable is what
// the file names there, so an import used nowhere else goes, and one the file
// also names as a package stays.
func TestTidyImportsReadsAShadowingLocalAsNoUse(t *testing.T) {
	src := []byte(`package p

import (
	"strings"
	"bytes"
)

type builder struct{ Len int }

func F() int {
	strings := builder{}
	return strings.Len
}

func G() *bytes.Buffer {
	bytes := new(bytes.Buffer)
	return bytes
}
`)
	out, err := gen.TidyImports(src)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `"strings"`) {
		t.Errorf("an import named only through a shadowing local was kept:\n%s", out)
	}
	if !strings.Contains(string(out), `"bytes"`) {
		t.Errorf("an import the file names as a package was dropped:\n%s", out)
	}
}
