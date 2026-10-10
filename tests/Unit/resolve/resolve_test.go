package resolve_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/arandu-io/aru/internal/resolve"
)

// source declares one of each thing a reader of a single file has to tell
// apart: an import and a local that shadows it, a name of this file and one of
// another, a predeclared name, the variable a type switch declares, and a type
// embedded in a struct.
const source = `package services

import "github.com/arandu-io/framework/security"

type local struct{ Grant int }

type embeds struct {
	local
	Elsewhere
}

var top = 1

func Use(g security.Grant, v any) int {
	n := top
	{
		security := local{}
		n += security.Grant
	}
	switch kind := v.(type) {
	case int:
		n += kind
	}
	return n + len(Other) + security.Level
}
`

// idents answers every identifier of f spelled name, in source order.
func idents(f *ast.File, name string) []*ast.Ident {
	var out []*ast.Ident
	ast.Inspect(f, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			out = append(out, id)
		}
		return true
	})
	return out
}

func check(t *testing.T) (*ast.File, *resolve.File) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "use.go", source, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	return f, resolve.Check(fset, f)
}

func TestAnImportIsNotLocalAndTheVariableShadowingItIs(t *testing.T) {
	f, r := check(t)
	security := idents(f, "security")
	// The parameter's type, the local's declaration, its use, and the
	// package again after the block.
	if len(security) != 4 {
		t.Fatalf("found %d identifiers spelled security, want 4", len(security))
	}
	for i, want := range []bool{false, true, true, false} {
		if got := r.Local(security[i]); got != want {
			t.Errorf("security #%d at %v: Local = %t, want %t", i+1, security[i].Pos(), got, want)
		}
	}
	if r.Object(security[1]) != r.Object(security[2]) {
		t.Error("the local and its use answer different objects")
	}
	if _, ok := r.Declaration(r.Object(security[2])).(*ast.AssignStmt); !ok {
		t.Errorf("the local is declared by %T, want the short variable declaration", r.Declaration(r.Object(security[2])))
	}
}

func TestANameOfThisFileIsLocalAndOneOfAnotherIsNot(t *testing.T) {
	f, r := check(t)
	top := idents(f, "top")
	if !r.Local(top[1]) {
		t.Error("a use of a package-level variable of this file is not local")
	}
	if _, ok := r.Declaration(r.Object(top[1])).(*ast.ValueSpec); !ok {
		t.Errorf("top is declared by %T, want its var spec", r.Declaration(r.Object(top[1])))
	}
	for _, name := range []string{"Other", "len"} {
		if id := idents(f, name)[0]; r.Local(id) || r.Object(id) != nil {
			t.Errorf("%s resolves within the file", name)
		}
	}
	if g := idents(f, "g")[0]; r.Declaration(r.Object(g)) == nil {
		t.Error("a parameter has no declaring field")
	}
}

func TestTheVariableATypeSwitchDeclaresIsLocal(t *testing.T) {
	f, r := check(t)
	kind := idents(f, "kind")
	if !r.Local(kind[0]) {
		t.Error("the variable in the type switch header is not local")
	}
	if !r.Local(kind[1]) {
		t.Error("its use in a case is not local")
	}
	if r.Local(f.Name) {
		t.Error("the package clause's name is local")
	}
}

func TestAnEmbeddedFieldAnswersTheTypeItNames(t *testing.T) {
	f, r := check(t)
	local := idents(f, "local")
	embedded := local[1]
	if r.Object(embedded) == nil || r.Object(embedded) != r.Object(local[0]) {
		t.Errorf("the embedded local answers %v, want the type declared above", r.Object(embedded))
	}
	if elsewhere := idents(f, "Elsewhere")[0]; r.Local(elsewhere) {
		t.Error("a type of another file embedded here resolves within the file")
	}
}
