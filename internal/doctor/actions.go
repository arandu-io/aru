package doctor

import (
	"go/ast"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

// The two module paths that declare the action type. Both are matched because
// both are reachable: the framework re-exports what hesape declares, and a
// project imports whichever it happened to import.
//
// The names are data rather than references. They are the strings an import
// spells, and matching them is the only way to tell this type from any other
// type called Action in the tree.
const (
	frameworkSecurity = "github.com/arandu-io/framework/security"
	hesapeAuth        = "github.com/arandu-io/hesape/auth"
)

// The two module paths that declare the rule set type, matched for the same
// reason and in the same way: the framework re-exports what hesape declares.
const (
	frameworkValidation = "github.com/arandu-io/framework/validation"
	hesapeValidation    = "github.com/arandu-io/hesape/validation"
)

// Action is one action the source states.
//
// It carries where it is written, because the catalogue is the code read back:
// a person looking at an entry has to be able to open the line that produced
// it, and a line number is what separates a derived catalogue from a list
// somebody typed.
type Action struct {
	// Const is the constant that names it, qualified by the package it is
	// declared in: "policies.InvoiceDelete". Empty for an action written as a
	// conversion where it is used, which is readable and is not named.
	Const string
	// Value is the string it holds, in "module.verb" form.
	Value string
	// Doc is the first sentence of the constant's comment, or empty. A panel
	// that lists permissions shows it beside the identifier, and it is written
	// where the constant is rather than in a second file that would disagree.
	Doc string
	// File is the path relative to the root that was read, and Line is where
	// the action is written in it.
	File string
	Line int
}

// Actions reads the actions the tree rooted at dir states.
//
// It parses and never runs, which is the whole of why it can answer at all: a
// catalogue assembled at boot would depend on every module having been
// registered, and a panel administers permissions of modules the application
// has not wired. It also answers about a tree that does not compile.
//
// What it reads is the tree it is given. Actions declared by a dependency are
// in that dependency's source and not here, so a project's answer covers the
// project -- run it in the module to see the module's own.
//
// The result is ordered by value, then by where it is written, so two runs over
// the same tree read the same way and a diff between them means something.
func Actions(dir string) ([]Action, error) {
	files, _, err := parseProject(dir)
	if err != nil {
		return nil, err
	}

	var out []Action
	for _, f := range files {
		if f.isTest {
			continue
		}
		out = append(out, f.actions()...)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Value != out[j].Value {
			return out[i].Value < out[j].Value
		}
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}

// actions reads what one file states.
//
// Two shapes reach the catalogue and they are not the same thing. A constant of
// the action type is the declared form, and it is what a module offers a panel.
// A conversion written where the action is used -- security.Action("invoice.view")
// passed straight to Can -- is not named, and it is still read: leaving it out
// would produce a catalogue that is missing an action the code enforces, which
// is the divergence the whole arrangement exists to prevent.
func (f *file) actions() []Action {
	spellings := f.actionTypeSpellings()
	if len(spellings) == 0 {
		return nil
	}

	var out []Action
	declared := map[ast.Node]bool{}

	for _, decl := range f.ast.Decls {
		group, ok := decl.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}
		for _, s := range group.Specs {
			spec, ok := s.(*ast.ValueSpec)
			if !ok || !spellings[exprName(spec.Type)] {
				continue
			}
			for i, name := range spec.Names {
				if i >= len(spec.Values) {
					continue
				}
				value, literal := stringValue(spec.Values[i])
				if !literal {
					continue
				}
				declared[spec.Values[i]] = true
				out = append(out, Action{
					Const: f.pkg + "." + name.Name,
					Value: value,
					Doc:   firstSentence(docOf(spec, group)),
					File:  f.rel,
					Line:  f.line(name),
				})
			}
		}
	}

	// The conversions, minus the ones that were the value of a constant just
	// read: `const X security.Action = "x"` has no conversion in it, but
	// `const X = security.Action("x")` does, and listing it twice would say the
	// project declares two actions where it declares one.
	for _, call := range f.actionConversions(spellings) {
		if declared[call] || len(call.Args) != 1 {
			continue
		}
		value, literal := stringValue(call.Args[0])
		if !literal {
			continue
		}
		out = append(out, Action{
			Value: value,
			File:  f.rel,
			Line:  f.line(call),
		})
	}
	return out
}

// actionTypeSpellings answers how this file can spell the action type.
//
// A file that imports neither module cannot name it, and a rule reading such a
// file has nothing to say -- which is why this returns a set rather than a
// fixed pair: the local name is the import's, and an aliased import is what
// made a list of fixed strings silently stop matching once before.
func (f *file) actionTypeSpellings() map[string]bool {
	out := map[string]bool{}
	for _, path := range []string{frameworkSecurity, hesapeAuth} {
		local, imported := f.imports[path]
		if !imported || local == "_" {
			continue
		}
		if local == "." {
			out["Action"] = true
			continue
		}
		out[local+".Action"] = true
	}
	return out
}

// actionConversions collects the calls that turn something into the action
// type: security.Action(x).
//
// A conversion is a CallExpr like any other, so what identifies it is the name
// of what is being called being the type. Nothing else in either package is
// spelled that way.
func (f *file) actionConversions(spellings map[string]bool) []*ast.CallExpr {
	var out []*ast.CallExpr
	ast.Inspect(f.ast, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if ok && len(call.Args) == 1 && spellings[exprName(call.Fun)] {
			out = append(out, call)
		}
		return true
	})
	return out
}

// stringValue reads an expression a catalogue can print, and reports whether it
// could.
//
// A quoted string is one, and so is a concatenation of them -- Go folds that at
// compile time and it is as fixed as a single literal. Anything else is a value
// this cannot state, which is the same question the rule asks and the reason
// the two share this function rather than deciding it twice.
func stringValue(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		text, err := strconv.Unquote(x.Value)
		if err != nil {
			return "", false
		}
		return text, true
	case *ast.ParenExpr:
		return stringValue(x.X)
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		left, okLeft := stringValue(x.X)
		right, okRight := stringValue(x.Y)
		if !okLeft || !okRight {
			return "", false
		}
		return left + right, true
	}
	return "", false
}

// docOf reads the comment above a constant, falling back to the group's own
// when the constants are declared together under one.
func docOf(spec *ast.ValueSpec, group *ast.GenDecl) string {
	if spec.Doc != nil {
		return spec.Doc.Text()
	}
	if spec.Comment != nil {
		return spec.Comment.Text()
	}
	if len(group.Specs) == 1 && group.Doc != nil {
		return group.Doc.Text()
	}
	return ""
}

// firstSentence is the comment's opening line, without the identifier Go
// convention puts in front of it.
func firstSentence(doc string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(doc), "\n")
	if _, rest, cut := strings.Cut(line, " is "); cut {
		line = rest
	}
	return strings.TrimSpace(line)
}

// actionUse is one place an action value is made, with the function it is made
// inside.
type actionUse struct {
	// at is the node a finding points at: the conversion, or the name being
	// declared.
	at ast.Node
	// value is what the action is made out of.
	value ast.Expr
	// scope is the function it sits in, and nil at package level. It is what
	// tells a parameter and a local variable apart from anything else spelled
	// the same way.
	scope *ast.FuncDecl
}

// actionUses collects the two shapes that make an action value.
//
// A conversion, security.Action(x), is the one that reads as construction. A
// declaration with the type written out, `var a security.Action = x`, is the
// same act with the type on the left instead, and leaving it out would let the
// question be stepped around by moving the type across the equals sign.
//
// A declaration whose value is itself a conversion is left to the conversion,
// so one act produces one answer.
func (f *file) actionUses(spellings map[string]bool) []actionUse {
	var out []actionUse
	for _, decl := range f.ast.Decls {
		if fn, isFunc := decl.(*ast.FuncDecl); isFunc {
			out = append(out, actionUsesIn(fn, fn, spellings)...)
			continue
		}
		out = append(out, actionUsesIn(decl, nil, spellings)...)
	}
	return out
}

func actionUsesIn(root ast.Node, scope *ast.FuncDecl, spellings map[string]bool) []actionUse {
	var out []actionUse
	converted := map[ast.Expr]bool{}

	ast.Inspect(root, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if len(x.Args) == 1 && spellings[exprName(x.Fun)] {
				converted[x] = true
				out = append(out, actionUse{at: x, value: x.Args[0], scope: scope})
			}
		case *ast.ValueSpec:
			if x.Type == nil || !spellings[exprName(x.Type)] {
				return true
			}
			for i, name := range x.Names {
				if i < len(x.Values) && !converted[x.Values[i]] {
					out = append(out, actionUse{at: name, value: x.Values[i], scope: scope})
				}
			}
		}
		return true
	})
	return out
}

// assembledFrom names what an action was built out of, and reports whether that
// could be said at all.
//
// It answers only where it can point at the thing: a call, a concatenation with
// something that is not a literal, a parameter, a variable of the same function,
// a field, an element. Everything else comes back false, including a bare
// identifier that is neither -- it may be a constant declared in another file of
// the same package, the doctor reads one file at a time, and guessing there
// would invent findings in the projects that keep their actions where they
// belong.
func assembledFrom(f *file, scope *ast.FuncDecl, e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return assembledFrom(f, scope, x.X)
	case *ast.CallExpr:
		if name := exprName(x.Fun); name != "" {
			return "what " + name + " returned", true
		}
		return "a function result", true
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		if _, folded := stringValue(x); folded {
			return "", false
		}
		return "a concatenation", true
	case *ast.IndexExpr:
		return "an element of " + exprName(x.X), true
	case *ast.SelectorExpr:
		// A qualified name is a constant of another package as often as it is a
		// field, and only the imports tell them apart.
		if base, isIdent := x.X.(*ast.Ident); isIdent {
			if _, imported := f.importPath(base.Name); imported {
				return "", false
			}
		}
		return "the field " + exprName(x), true
	case *ast.Ident:
		if scope == nil {
			return "", false
		}
		if isParameter(scope, x.Name) {
			return "the parameter " + x.Name, true
		}
		if isLocalVariable(scope, x.Name) {
			return "the variable " + x.Name, true
		}
	}
	return "", false
}

// isParameter reports whether name is one of the function's inputs or its
// receiver.
func isParameter(fn *ast.FuncDecl, name string) bool {
	lists := []*ast.FieldList{fn.Recv}
	if fn.Type != nil {
		lists = append(lists, fn.Type.Params)
	}
	for _, list := range lists {
		if list == nil {
			continue
		}
		for _, field := range list.List {
			for _, ident := range field.Names {
				if ident.Name == name {
					return true
				}
			}
		}
	}
	return false
}

// isLocalVariable reports whether the function assigns name with := or declares
// it with var.
//
// A constant declared inside the function is deliberately not one: `const a
// security.Action = "invoice.view"` is exactly the shape being asked for, and
// reading it as a variable would report the fix.
func isLocalVariable(fn *ast.FuncDecl, name string) bool {
	if fn.Body == nil {
		return false
	}
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			if x.Tok != token.DEFINE {
				return true
			}
			for _, lhs := range x.Lhs {
				if ident, ok := lhs.(*ast.Ident); ok && ident.Name == name {
					found = true
				}
			}
		case *ast.GenDecl:
			if x.Tok != token.VAR {
				return true
			}
			for _, s := range x.Specs {
				spec, ok := s.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, ident := range spec.Names {
					if ident.Name == name {
						found = true
					}
				}
			}
		case *ast.RangeStmt:
			for _, key := range []ast.Expr{x.Key, x.Value} {
				if ident, ok := key.(*ast.Ident); ok && ident.Name == name && x.Tok == token.DEFINE {
					found = true
				}
			}
		}
		return !found
	})
	return found
}
