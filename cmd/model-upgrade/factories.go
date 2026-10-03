package main

import (
	"go/ast"
	"go/token"
	"regexp"
	"strings"

	"github.com/arandu-io/aru/internal/gen"
)

// customBlock matches a custom block, markers included, with its body in the
// first group.
var customBlock = regexp.MustCompile(`(?s)// arandu:begin custom[^\n]*\n(.*?)[ \t]*// arandu:end custom`)

// factoryFiles is the second pass: a factory written against the generic
// model becomes the typed factory model:build renders.
//
// The definition, every other declaration of the file and its custom block move
// into the new custom block, in that order, because from now on model:build
// rewrites everything outside it on every build.
func (u *upgrader) factoryFiles() {
	for _, f := range u.files {
		hesape := f.importName(factoriesPath)
		if hesape == "" || strings.HasSuffix(f.rel, "_test.go") {
			continue
		}
		var found []*ast.FuncDecl
		for _, decl := range f.ast.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && genericFactoryResult(fn, hesape) != nil {
				found = append(found, fn)
			}
		}
		switch len(found) {
		case 0:
			continue
		case 1:
			u.factoryFile(f, found[0], hesape)
		default:
			u.problemAt(f, f.line(found[1]), "a second factory in one file: model:build renders one factory per file, "+
				"database/factories/<Entity>Factory.go, so move it to a file of its own first")
		}
	}
}

// genericFactoryResult answers the type argument of a *factories.Factory[T]
// result, or nil.
func genericFactoryResult(fn *ast.FuncDecl, hesape string) ast.Expr {
	if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
		return nil
	}
	star, ok := fn.Type.Results.List[0].Type.(*ast.StarExpr)
	if !ok {
		return nil
	}
	idx, ok := star.X.(*ast.IndexExpr)
	if !ok {
		return nil
	}
	if name, ok := selectorOf(idx.X, hesape); !ok || name != "Factory" {
		return nil
	}
	return idx.Index
}

// factoryFile rewrites one factory file whole.
func (u *upgrader) factoryFile(f *source, fn *ast.FuncDecl, hesape string) {
	imports := f.importsByName()
	arg := genericFactoryResult(fn, hesape)
	sel, ok := arg.(*ast.SelectorExpr)
	var e *entity
	var modelsPath string
	if ok {
		if pkg, isPkg := sel.X.(*ast.Ident); isPkg {
			modelsPath = imports[pkg.Name]
			e = u.entities[modelsPath][sel.Sel.Name]
		}
	}
	if e == nil {
		if !ok || !u.failed[modelsPath+"."+sel.Sel.Name] {
			u.problemAt(f, f.line(fn), fn.Name.Name+" builds "+f.text(arg)+
				", which is not an entity model-upgrade rewrote: rewrite its model first, in the same run")
		}
		return
	}

	// return factories.For(models.Notes(db), func(f faker.Faker) models.Note { ... })
	var lit *ast.FuncLit
	if len(fn.Body.List) == 1 {
		if ret, isRet := fn.Body.List[0].(*ast.ReturnStmt); isRet && len(ret.Results) == 1 {
			if call, isCall := ret.Results[0].(*ast.CallExpr); isCall && len(call.Args) == 2 {
				if name, isFor := selectorOf(call.Fun, hesape); isFor && name == "For" && u.isConstructorCall(f, call.Args[0]) {
					lit, _ = call.Args[1].(*ast.FuncLit)
				}
			}
		}
	}
	if lit == nil || lit.Type.Params == nil || len(lit.Type.Params.List) != 1 || len(lit.Type.Params.List[0].Names) > 1 {
		u.problemAt(f, f.line(fn), fn.Name.Name+" is not one return factories.For(models.<Constructor>(db), func(f faker.Faker) "+
			e.name+" {...}), which is the shape model-upgrade moves into the typed factory")
		return
	}
	param := lit.Type.Params.List[0]
	paramName := "f"
	if len(param.Names) == 1 {
		paramName = param.Names[0].Name
	}

	src := string(f.src)
	blocks := customBlock.FindAllStringSubmatchIndex(src, -1)
	insideBlock := func(n ast.Node) bool {
		start, end := f.offset(n.Pos()), f.offset(n.End())
		for _, b := range blocks {
			if start >= b[0] && end <= b[1] {
				return true
			}
		}
		return false
	}

	var parts []string
	for _, decl := range f.ast.Decls {
		if decl == fn {
			continue
		}
		if gd, isGen := decl.(*ast.GenDecl); isGen && gd.Tok == token.IMPORT {
			continue
		}
		if insideBlock(decl) {
			continue
		}
		from := decl.Pos()
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Doc != nil {
				from = d.Doc.Pos()
			}
		case *ast.GenDecl:
			if d.Doc != nil {
				from = d.Doc.Pos()
			}
		}
		parts = append(parts, string(f.src[f.offset(from):f.offset(decl.End())]))
	}
	parts = append(parts, "// define"+e.name+" is the default state: every row the factory makes starts here,\n"+
		"// and a State changes the part a test cares about.\n"+
		"func define"+e.name+"("+paramName+" "+f.text(param.Type)+") models."+e.name+" "+f.text(lit.Body))
	for _, b := range blocks {
		if body := strings.TrimSpace(src[b[2]:b[3]]); body != "" {
			parts = append(parts, body)
		}
	}

	tenant := true
	if e.structFile != "" {
		if _, hasTenant, err := gen.FieldsFromModel(e.structFile, e.name); err == nil {
			tenant = hasTenant
		}
	}
	rendered, err := gen.RenderFactory(gen.FactorySpec{Entity: e.name, Tenant: tenant, ModelsImport: modelsPath})
	if err != nil {
		u.problemAt(f, f.line(fn), err.Error())
		return
	}
	out := string(rendered.Content)
	block := customBlock.FindStringSubmatchIndex(out)
	if block == nil {
		u.problemAt(f, 1, "the rendered factory has no custom block, which is a defect of model-upgrade")
		return
	}
	out = out[:block[2]] + strings.Join(parts, "\n\n") + "\n" + out[block[3]:]
	f.edits = append(f.edits, edit{start: 0, end: len(f.src), text: out})

	if u.factories[f.pkgPath] == nil {
		u.factories[f.pkgPath] = map[string]*entity{}
	}
	u.factories[f.pkgPath][fn.Name.Name] = e
	e.factoryPkg = f.pkgPath
}

// isConstructorCall reports whether e calls a constructor the first pass
// rewrote: models.Users(db), or Users(db) inside the models package.
func (u *upgrader) isConstructorCall(f *source, e ast.Expr) bool {
	return u.constructorCalled(f, e) != nil
}

// constructorCalled answers the entity whose constructor e calls, or nil.
func (u *upgrader) constructorCalled(f *source, e ast.Expr) *entity {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return nil
	}
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return u.constructors[f.pkgPath][fun.Name]
	case *ast.SelectorExpr:
		pkg, ok := fun.X.(*ast.Ident)
		if !ok {
			return nil
		}
		return u.constructors[f.importsByName()[pkg.Name]][fun.Sel.Name]
	}
	return nil
}
