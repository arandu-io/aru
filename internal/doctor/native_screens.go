package doctor

import (
	"go/ast"
	"strings"
)

// nativeTargetDir is where a project's native target lives, and the one place
// this looks for it.
//
// The module that publishes the target writes it here, so a project that has
// one has it here. A search would find a directory somebody named the same
// thing by accident and call it an application.
const nativeTargetDir = "cmd/native"

// isNativeTarget reports whether a file belongs to the native target.
func isNativeTarget(rel string) bool {
	return rel == nativeTargetDir || strings.HasPrefix(rel, nativeTargetDir+"/")
}

// addNativeScreens puts a project's native target, and every screen in it,
// into the graph.
//
// A screen is a function that answers dimensions: it is handed the room it has
// and reports how much of it it took. That signature is the whole contract of
// the drawing side, which is why it is what this reads -- a name convention
// would find whatever somebody happened to call a function, and miss the one
// they called something else.
func addNativeScreens(builder *graphBuilder, files []*file) {
	for _, f := range files {
		if f.isTest || !isNativeTarget(f.rel) {
			continue
		}

		// The entry point becomes the target itself, so a project that has
		// published one shows it before the first screen is written -- which
		// is exactly when somebody is looking for confirmation that the
		// publish worked.
		if declaresMain(f) {
			builder.addNode("native-screens", nativeTargetNode(f.rel))
		}

		for _, declaration := range f.ast.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || !drawsAScreen(function) {
				continue
			}
			position := f.fset.Position(function.Pos())
			builder.addNode("native-screens", Node{
				ID: "native-screen:" + graphID(f.rel+":"+function.Name.Name), Kind: "native-screen",
				Label: screenLabel(function.Name.Name), Detail: f.rel,
				File: f.rel, Line: max(position.Line, 1), Column: max(position.Column, 1),
			})
		}
	}
}

// declaresMain reports whether a file carries the program's entry point.
func declaresMain(f *file) bool {
	for _, declaration := range f.ast.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Recv == nil && function.Name.Name == "main" {
			return true
		}
	}
	return false
}

// drawsAScreen reports whether a function has the shape of a screen: one that
// answers dimensions.
//
// It reads the name of the returned type and not the package it came from,
// because the file may import that package under any alias, and the alias is
// the caller's choice rather than a fact about the function.
func drawsAScreen(function *ast.FuncDecl) bool {
	if function.Type.Results == nil || len(function.Type.Results.List) != 1 {
		return false
	}

	switch returned := function.Type.Results.List[0].Type.(type) {
	case *ast.SelectorExpr:
		return returned.Sel.Name == "Dimensions"
	case *ast.Ident:
		return returned.Name == "Dimensions"
	}
	return false
}

// screenLabel turns a function name into what a person reads in the tree.
//
// The prefix goes because every one of them carries it: a list of layoutHome,
// layoutSignIn and layoutSettings is a list where the first six characters are
// noise, and the eye has to skip them on every row.
func screenLabel(name string) string {
	if trimmed := strings.TrimPrefix(name, "layout"); trimmed != name && trimmed != "" {
		return trimmed
	}
	return name
}

// nativeTargetNode is the target itself.
//
// It is labelled by what it is rather than by the file, because "main.go" in a
// tree of them says nothing, and this is the row somebody looks for to know
// the publish landed.
func nativeTargetNode(rel string) Node {
	return Node{
		ID: "native-target:" + graphID(rel), Kind: "native-target",
		Label: "Native application", Detail: rel, File: rel, Line: 1, Column: 1,
	}
}
