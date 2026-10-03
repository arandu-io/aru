package kyse_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/kyse"
)

// {!! !!} writes what a value's type says is markup, and a string that arrived
// as data stops the build at the line of the view.
//
// Both halves are built with the Go toolchain rather than read, because the
// property is a type error and only a type checker sees one.
func TestRawOutputTakesOnlyMarkup(t *testing.T) {
	const accepted = `//go:build kyse

package views

import "html/template"

@go
type Profile struct {
	Card template.HTML
}

func Badge() template.HTML { return "<b>new</b>" }
@endgo

<div>{!! .Card !!}</div>
<div>{!! Badge() !!}</div>
<div>{!! "<hr>" !!}</div>
`
	const refused = `//go:build kyse

package views

@go
type Comment struct {
	Body string
}
@endgo

<p>{!! .Body !!}</p>
`

	build := func(t *testing.T, name, dataType, source string) (string, error) {
		t.Helper()
		root := t.TempDir()
		writeStubModule(t, root)
		path := "resources/views/" + name + ".kyse.go"
		file, err := kyse.Parse(path, source)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		out, err := kyse.Generate(file, name, dataType, "views/"+name+".go")
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		writeFile(t, filepath.Join(root, "views", name+".go"), string(out))
		cmd := goCommand(t, root, "build", "./...")
		got, err := cmd.CombinedOutput()
		return string(got), err
	}

	t.Run("markup, a function returning markup and a constant", func(t *testing.T) {
		if out, err := build(t, "profile", "Profile", accepted); err != nil {
			t.Fatalf("a value typed as markup does not build: %v\n%s", err, out)
		}
	})

	t.Run("a string field", func(t *testing.T) {
		out, err := build(t, "comment", "Comment", refused)
		if err == nil {
			t.Fatal("a string written raw built, and a visitor's text would reach the page as markup")
		}
		if !strings.Contains(out, "resources/views/comment.kyse.go:11") || !strings.Contains(out, ".HTML value") {
			t.Errorf("the type error does not name the view's line and the type it wants:\n%s", out)
		}
	})
}
