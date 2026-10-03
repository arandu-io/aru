package doctor

import (
	"go/ast"

	"github.com/arandu-io/aru/internal/modelbuild"
)

// The helpers below serve modelCoreStaysInTheModels, in rules.go: they read
// where the model core is reached from, by name rather than by type.

// coreCallsIn reports, inside one function, a method called on what the
// function holds as a *model.Builder or a *model.Table, or on the result of a
// Base() call.
func coreCallsIn(typ *ast.FuncType, body *ast.BlockStmt, core string, report func(ast.Node, string)) {
	held := map[string]string{}
	hold := func(fields *ast.FieldList) {
		if fields == nil {
			return
		}
		for _, field := range fields.List {
			if kind := coreType(field.Type, core); kind != "" {
				for _, name := range field.Names {
					held[name.Name] = kind
				}
			}
		}
	}
	hold(typ.Params)
	hold(typ.Results)

	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncLit:
			coreCallsIn(x.Type, x.Body, core, report)
			return false
		case *ast.ValueSpec:
			if kind := coreType(x.Type, core); kind != "" {
				for _, name := range x.Names {
					held[name.Name] = kind
				}
			}
			for i, value := range x.Values {
				if i < len(x.Names) && isBaseCall(value) {
					held[x.Names[i].Name] = "Builder"
				}
			}
		case *ast.AssignStmt:
			for i, value := range x.Rhs {
				if id, ok := x.Lhs[min(i, len(x.Lhs)-1)].(*ast.Ident); ok && isBaseCall(value) {
					held[id.Name] = "Builder"
				}
			}
		case *ast.CallExpr:
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if isBaseCall(sel.X) {
				report(x, "the core builder's "+sel.Sel.Name+" is called through Base()")
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && held[id.Name] != "" {
				report(x, "the core "+held[id.Name]+"'s "+sel.Sel.Name+" is called on "+id.Name)
			}
		}
		return true
	})
}

// coreType answers "Builder" or "Table" for *model.Builder and *model.Table
// spelled with the name the core is imported under, and "" otherwise.
func coreType(e ast.Expr, core string) string {
	if core == "" || e == nil {
		return ""
	}
	if star, ok := e.(*ast.StarExpr); ok {
		e = star.X
	}
	for _, kind := range []string{"Builder", "Table"} {
		if isSelector(e, core, kind) {
			return kind
		}
	}
	return ""
}

// isBaseCall reports a call of a method named Base with no argument, which on
// a generated query is the accessor of its core builder.
func isBaseCall(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Base"
}

// declaresEntity reports whether f declares a struct embedding the model core
// by value and without a type argument.
func declaresEntity(f *file) bool {
	core := f.imports[modelbuild.ModelPath]
	if core == "" {
		return false
	}
	declared := false
	f.types(func(ts *ast.TypeSpec) {
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return
		}
		for _, field := range st.Fields.List {
			if len(field.Names) == 0 && isSelector(field.Type, core, "Model") {
				declared = true
			}
		}
	})
	return declared
}

func isSelector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg && sel.Sel.Name == name
}
