package doctor

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// parseFunction reads one function declaration out of a source line.
func parseFunction(t *testing.T, source string) *ast.FuncDecl {
	t.Helper()

	parsed, err := parser.ParseFile(token.NewFileSet(), "screen.go", "package main\n"+source+"\n", 0)
	if err != nil {
		t.Fatalf("%s does not parse: %v", source, err)
	}
	for _, declaration := range parsed.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok {
			return function
		}
	}
	t.Fatalf("%s declares no function", source)
	return nil
}

// TestTheNativeTargetIsItsOwnGroup keeps the files that draw a window out of
// the heading about the terminal.
//
// The target is a command, so the rule that puts everything under cmd/ with
// the console commands would file five files that open a window beside the
// ones that print to it.
func TestTheNativeTargetIsItsOwnGroup(t *testing.T) {
	for _, rel := range []string{
		"cmd/native/main.go",
		"cmd/native/sign_in.go",
		"cmd/native",
	} {
		if !isNativeTarget(rel) {
			t.Errorf("%s is not recognised as the native target", rel)
		}
	}

	for _, rel := range []string{
		"cmd/worker/main.go",
		"cmd/native-worker/main.go",
		"cmd/natively/main.go",
		"main.go",
		"internal/cmd/native/main.go",
	} {
		if isNativeTarget(rel) {
			t.Errorf("%s was taken for the native target", rel)
		}
	}
}

// TestAScreenIsAFunctionThatAnswersDimensions fixes what this reads to find a
// screen.
//
// The signature is the contract of the drawing side. A name convention would
// find whatever somebody happened to call a function and miss the one they
// called something else, and the person whose screen is missing from the tree
// has no way to know why.
func TestAScreenIsAFunctionThatAnswersDimensions(t *testing.T) {
	screens := []string{
		"func layoutHome(c ayra.Context) ayra.Dimensions { return ayra.Dimensions{} }",
		"func settings(c ayra.Context) Dimensions { return Dimensions{} }",
		"func (a *App) layoutSignIn(c ayra.Context, st status) ayra.Dimensions { return ayra.Dimensions{} }",
	}
	for _, source := range screens {
		if !drawsAScreen(parseFunction(t, source)) {
			t.Errorf("not read as a screen: %s", source)
		}
	}

	others := []string{
		"func main() {}",
		"func greeting(name string) string { return name }",
		"func measure(c ayra.Context) (ayra.Dimensions, error) { return ayra.Dimensions{}, nil }",
		"func nothing(c ayra.Context) {}",
	}
	for _, source := range others {
		if drawsAScreen(parseFunction(t, source)) {
			t.Errorf("read as a screen, and is not: %s", source)
		}
	}
}

// TestTheScreenLabelDropsTheRepeatedPrefix keeps a list of rows whose first six
// characters are identical.
func TestTheScreenLabelDropsTheRepeatedPrefix(t *testing.T) {
	for name, want := range map[string]string{
		"layoutHome":     "Home",
		"layoutSignIn":   "SignIn",
		"layoutSettings": "Settings",
		"catalogue":      "catalogue",
		"layout":         "layout",
	} {
		if got := screenLabel(name); got != want {
			t.Errorf("%s is labelled %q, want %q", name, got, want)
		}
	}
}

// TestTheTargetIsLabelledByWhatItIs keeps the row somebody looks for from
// being called main.go.
func TestTheTargetIsLabelledByWhatItIs(t *testing.T) {
	node := nativeTargetNode("cmd/native/main.go")

	if strings.Contains(node.Label, ".go") {
		t.Errorf("the target is labelled %q, which is a filename", node.Label)
	}
	if node.File != "cmd/native/main.go" {
		t.Errorf("the target does not open its own file: %q", node.File)
	}
	if node.Kind != "native-target" {
		t.Errorf("the target is a %q", node.Kind)
	}
}

// TestTheGraphDeclaresTheNativeGroup keeps the group from being emitted by the
// code without being declared, which the editor refuses as an unknown group.
func TestTheGraphDeclaresTheNativeGroup(t *testing.T) {
	var found bool
	for _, group := range graphGroups {
		if group.ID == "native-screens" {
			found = true
			if group.Label == "" {
				t.Error("the native group has no label")
			}
		}
	}
	if !found {
		t.Error("nothing declares the native-screens group, and the editor refuses a node in a group it was not told about")
	}
}
