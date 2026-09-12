package pack

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// linkedSymbols are the variables of the runtime package that a build writes
// the application's identity into, at link time.
//
// They are not called from here and never appear in these sources as code: each
// is a name in a -X flag, which is a string the linker matches against the
// symbol table. That is the whole of the coupling, and it is why this list
// exists -- three strings in three different files were the only record that
// the packager requires anything of that package at all.
var linkedSymbols = []string{"ID", "extraArgs", "schemesURI"}

// verifyLinkedSymbols refuses a runtime package that cannot receive what the
// linker will be told to write into it.
//
// The check exists because the linker does not do it. `go tool link -X` over a
// name that is absent, that is a constant, or that is not a package-level
// string writes nothing and reports nothing: the build succeeds, the artifact
// installs, the application opens, and its identifier is empty. On a phone that
// identifier is what an upgrade is matched against, so the fault surfaces as a
// second copy of the application installed beside the first -- on somebody
// else's device, weeks later.
//
// Every Go file of the package is read, build constraints included, and that is
// deliberate rather than an oversight: one of these three is declared in a file
// only Windows compiles, and the flag naming it is only passed on Windows. What
// this proves is therefore that the declaration exists, not that this target
// compiles it -- which is the question the linker is about to fail to ask.
func verifyLinkedSymbols(pkgPath, dir string) error {
	declared, err := packageVariables(dir)
	if err != nil {
		return fmt.Errorf("reading the runtime package %s: %w", pkgPath, err)
	}

	var problems []string
	for _, symbol := range linkedSymbols {
		found, ok := declared[symbol]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s is not declared there", symbol))
		case found.constant:
			problems = append(problems, fmt.Sprintf("%s is a constant, and the linker can only write to a variable", symbol))
		case found.kind != "string":
			problems = append(problems, fmt.Sprintf("%s is %s, and the linker can only write to a string", symbol, found.kind))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf(
		"the runtime package %s cannot be told what this application is: %s. Nothing downstream reports this -- the artifact would build, install, open, and carry no identifier",
		pkgPath, strings.Join(problems, "; "))
}

// declaration is what one package-level name was declared as.
type declaration struct {
	constant bool
	kind     string
}

// packageVariables answers the package-level names a directory declares.
//
// A directory and not a loaded package: the graph selects the files of one
// target, and the question here is about files of several.
func packageVariables(dir string) (map[string]declaration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	fset := token.NewFileSet()
	declared := make(map[string]declaration)
	read := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		read++
		collectDeclarations(file, declared)
	}
	if read == 0 {
		// Every statement below is true of a package with no files in it, so a
		// directory that turned out to hold nothing would be reported as one
		// that declares everything correctly.
		return nil, fmt.Errorf("%s holds no Go source", dir)
	}
	return declared, nil
}

// collectDeclarations records the top-level var and const names of one file.
//
// Top level only. A name inside a function is a different symbol, and the
// linker cannot reach it.
func collectDeclarations(file *ast.File, into map[string]declaration) {
	for _, decl := range file.Decls {
		general, ok := decl.(*ast.GenDecl)
		if !ok || (general.Tok != token.VAR && general.Tok != token.CONST) {
			continue
		}
		for _, spec := range general.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for index, named := range value.Names {
				into[named.Name] = declaration{
					constant: general.Tok == token.CONST,
					kind:     declaredKind(value, index),
				}
			}
		}
	}
}

// declaredKind answers the type of one name of a declaration, as written or as
// the literal beside it gives it away.
//
// Syntax rather than types: loading a package for type information means
// building its dependencies for a target this machine may not have a toolchain
// for, and what is being asked is whether three names are strings.
func declaredKind(value *ast.ValueSpec, index int) string {
	if value.Type != nil {
		return typeName(value.Type)
	}
	if index >= len(value.Values) {
		return "of a type that cannot be read here"
	}
	switch literal := value.Values[index].(type) {
	case *ast.BasicLit:
		switch literal.Kind {
		case token.STRING:
			return "string"
		case token.INT:
			return "int"
		case token.FLOAT:
			return "float64"
		case token.CHAR:
			return "rune"
		}
	case *ast.Ident:
		if literal.Name == "true" || literal.Name == "false" {
			return "bool"
		}
	}
	return "of a type that cannot be read here"
}

// typeName answers a written type as text, for the shapes a refusal has to be
// able to say out loud.
func typeName(expr ast.Expr) string {
	switch written := expr.(type) {
	case *ast.Ident:
		return written.Name
	case *ast.StarExpr:
		return "*" + typeName(written.X)
	case *ast.ArrayType:
		return "[]" + typeName(written.Elt)
	case *ast.SelectorExpr:
		return typeName(written.X) + "." + written.Sel.Name
	}
	return "of a type that cannot be read here"
}
