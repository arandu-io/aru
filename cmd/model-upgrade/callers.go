package main

import (
	"go/ast"
	"strings"

	"golang.org/x/tools/go/ast/astutil"

	"github.com/arandu-io/aru/internal/gen"
)

// callers is the third pass: the types and the calls of every file that names
// a rewritten model.
func (u *upgrader) callers() {
	for _, f := range u.files {
		u.callersIn(f)
	}
}

func (u *upgrader) callersIn(f *source) {
	imports := f.importsByName()
	core := f.importName(modelPath)
	hesape := f.importName(factoriesPath)
	appFactories := u.modulePath + "/database/factories"

	// entityOf answers the entity a type argument names, and how to qualify
	// the generated names next to it: "" inside its package, "models." outside.
	entityOf := func(arg ast.Expr) (*entity, string, string) {
		switch t := arg.(type) {
		case *ast.Ident:
			return u.entities[f.pkgPath][t.Name], "", f.pkgPath + "." + t.Name
		case *ast.SelectorExpr:
			if pkg, ok := t.X.(*ast.Ident); ok {
				path := imports[pkg.Name]
				return u.entities[path][t.Sel.Name], pkg.Name + ".", path + "." + t.Sel.Name
			}
		}
		return nil, "", ""
	}

	astutil.Apply(f.ast, func(c *astutil.Cursor) bool {
		switch n := c.Node().(type) {
		case *ast.IndexExpr:
			kind, generic := "", false
			if name, ok := selectorOf(n.X, core); ok {
				kind, generic = name, true
			} else if name, ok := selectorOf(n.X, hesape); ok && name == "Factory" {
				kind, generic = "Factory", true
			}
			if !generic {
				return true
			}
			e, qualifier, key := entityOf(n.Index)
			if e == nil {
				if !u.failed[key] && (kind == "Builder" || kind == "Collection" || kind == "Factory") {
					u.problemAt(f, f.line(n), f.text(n)+" is over a type that is not an entity model-upgrade rewrote")
				}
				return true
			}
			switch kind {
			case "Builder":
				f.replace(n, qualifier+e.name+"Query")
			case "Collection":
				f.replace(n, qualifier+e.name+"Collection")
			case "Factory":
				if e.factoryPkg == "" {
					u.problemAt(f, f.line(n), f.text(n)+" names the factory of "+e.name+
						", and model-upgrade found no factory constructor of it to rewrite")
					return true
				}
				name := e.name + "Factory"
				if f.pkgPath != e.factoryPkg {
					alias := f.importName(appFactories)
					if alias == "" {
						if _, taken := imports["factories"]; taken {
							u.problemAt(f, f.line(n), f.text(n)+" becomes *factories."+name+
								", and this file already binds the name factories to another package")
							return true
						}
						alias = "factories"
					}
					name = alias + "." + name
				}
				if _, pointer := c.Parent().(*ast.StarExpr); !pointer {
					name = "*" + name
				}
				f.replace(n, name)
			case "Model":
				u.problemAt(f, f.line(n), f.text(n)+" outside the struct of the entity has no concrete equivalent "+
					"model-upgrade can choose: a constructor's result is now *"+qualifier+e.name+"Query, a row is *"+
					qualifier+e.name+", and a type embedding the model is a design to rewrite by hand")
			}
			return false

		case *ast.CallExpr:
			u.call(f, n)

		case *ast.SelectorExpr:
			// A reference to a constructor whose name changed, called or not.
			if pkg, ok := n.X.(*ast.Ident); ok {
				if e := u.constructors[imports[pkg.Name]][n.Sel.Name]; e != nil && e.oldCtor != e.newCtor {
					f.replace(n.Sel, e.newCtor)
				}
			}

		case *ast.Ident:
			if e := u.constructors[f.pkgPath][n.Name]; e != nil && e.oldCtor != e.newCtor {
				if _, isSel := c.Parent().(*ast.SelectorExpr); !isSel || c.Name() == "X" {
					f.replace(n, e.newCtor)
				}
			}
		}
		return true
	}, nil)

	for _, decl := range f.ast.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
			u.rowsFromNewInstance(f, fn.Body)
		}
	}
	u.heldConstructors(f)
}

// call rewrites one call: a chain off a constructor, and a factory
// constructor whose name changed.
func (u *upgrader) call(f *source, call *ast.CallExpr) {
	imports := f.importsByName()
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		if id, isIdent := call.Fun.(*ast.Ident); isIdent {
			if e := u.factories[f.pkgPath][id.Name]; e != nil {
				f.replace(id, e.newCtor)
			}
		}
		return
	}

	if pkg, isPkg := sel.X.(*ast.Ident); isPkg {
		if e := u.factories[imports[pkg.Name]][sel.Sel.Name]; e != nil {
			f.replace(sel.Sel, e.newCtor)
			return
		}
		if hesape := f.importName(factoriesPath); pkg.Name == hesape && hesape != "" {
			switch sel.Sel.Name {
			case "For", "Has", "ForParent":
				u.problemAt(f, f.line(call), hesape+"."+sel.Sel.Name+" is the generic factory, and the typed factory "+
					"model:build renders has no "+sel.Sel.Name+": write it over the typed factories by hand")
				return
			}
		}
	}

	rooted := u.constructorCalled(f, sel.X) != nil
	switch sel.Sel.Name {
	case "NewQuery", "Query":
		if len(call.Args) != 0 {
			return
		}
		if rooted {
			f.replaceRange(sel.X.End(), call.End(), "")
		} else if sel.Sel.Name == "NewQuery" {
			u.problemAt(f, f.line(call), "NewQuery is called on "+firstLine(f.text(sel.X))+
				", which is not a call of a constructor: model-upgrade drops it only where it can see the query starts at one")
		}
	case "NewInstance":
		if !rooted {
			u.problemAt(f, f.line(call), "NewInstance is called on "+firstLine(f.text(sel.X))+
				", which is not a call of a constructor: model-upgrade rewrites it only where it can see the query starts at one")
			return
		}
		if len(call.Args) != 2 || !isIdent(call.Args[0], "nil") || !isIdent(call.Args[1], "false") {
			u.problemAt(f, f.line(call), "NewInstance with attributes or an existing row has no one-line equivalent: "+
				"write it as New() and Fill, or as a query's FirstOrNew")
			return
		}
		f.replaceRange(sel.Sel.Pos(), call.End(), "New()")
	}
}

// rowsFromNewInstance drops .Entity from what a rewritten NewInstance
// returned: instance, err := models.Notes(db).NewInstance(nil, false) is now
// New(), which returns the row itself, so instance.Entity is instance.
func (u *upgrader) rowsFromNewInstance(f *source, body *ast.BlockStmt) {
	held := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Rhs) != 1 || len(as.Lhs) == 0 {
			return true
		}
		call, ok := as.Rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "NewInstance" || u.constructorCalled(f, sel.X) == nil {
			return true
		}
		if id, ok := as.Lhs[0].(*ast.Ident); ok && id.Name != "_" {
			held[id.Name] = true
		}
		return true
	})
	if len(held) == 0 {
		return
	}
	ast.Inspect(body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Entity" {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && held[id.Name] {
			f.replaceRange(sel.X.End(), sel.End(), "")
		}
		return true
	})
}

// tidy is the fourth pass: the imports of every rewritten file are what it
// uses, from what it imported before and the two names the rewrite writes.
func (u *upgrader) tidy() {
	known := []byte("package known\n\nimport (\n\tmodel \"" + modelPath + "\"\n\tfactories \"" +
		u.modulePath + "/database/factories\"\n)\n")
	for _, f := range u.files {
		if string(f.src) == string(f.orig) {
			continue
		}
		candidates := [][]byte{f.orig}
		if !strings.HasSuffix(f.pkgPath, "/database/factories") {
			candidates = append(candidates, known)
		} else {
			candidates = append(candidates, []byte("package known\n\nimport model \""+modelPath+"\"\n"))
		}
		out, err := gen.TidyImports(f.src, candidates...)
		if err != nil {
			u.problemAt(f, 1, "the rewritten file does not parse, which is a defect of model-upgrade: "+err.Error())
			continue
		}
		if string(out) != string(f.src) {
			f.edits = append(f.edits, edit{start: 0, end: len(f.src), text: string(out)})
		}
	}
}

// leftovers reports what is still generic once every pass has run: a generic
// of the model layer the passes do not translate -- a relation constructor
// over a type argument, ModelOf, a scope type -- and a NewModel outside a
// constructor.
func (u *upgrader) leftovers() {
	for _, f := range u.files {
		core := f.importName(modelPath)
		hesape := f.importName(factoriesPath)
		imports := f.importsByName()
		// failed reports a type argument naming an entity whose declaration
		// was already refused: what is left of it is that one problem.
		failed := func(arg ast.Expr) bool {
			switch t := arg.(type) {
			case *ast.Ident:
				return u.failed[f.pkgPath+"."+t.Name]
			case *ast.SelectorExpr:
				if pkg, ok := t.X.(*ast.Ident); ok {
					return u.failed[imports[pkg.Name]+"."+t.Sel.Name]
				}
			}
			return false
		}
		ast.Inspect(f.ast, func(n ast.Node) bool {
			var x ast.Expr
			switch e := n.(type) {
			case *ast.IndexExpr:
				if failed(e.Index) {
					return true
				}
				x = e.X
			case *ast.IndexListExpr:
				x = e.X
			case *ast.CallExpr:
				if idx, ok := e.Fun.(*ast.IndexExpr); ok && !failed(idx.Index) {
					if name, ok := selectorOf(idx.X, core); ok && name == "NewModel" {
						u.leftover(f, e, "model.NewModel outside a constructor model-upgrade can read")
					}
				}
				return true
			default:
				return true
			}
			if name, ok := selectorOf(x, core); ok {
				u.leftover(f, n, "model."+name+" with a type argument has no concrete equivalent model-upgrade writes")
			} else if name, ok := selectorOf(x, hesape); ok {
				u.leftover(f, n, "factories."+name+" with a type argument has no concrete equivalent model-upgrade writes")
			}
			return true
		})
	}
}

// leftover records a problem unless one was already recorded on the line.
func (u *upgrader) leftover(f *source, n ast.Node, message string) {
	line := f.origLine(f.line(n))
	for _, p := range u.problems {
		if p.file == f.rel && p.line == line {
			return
		}
	}
	u.problems = append(u.problems, problem{file: f.rel, line: line, message: message})
}
