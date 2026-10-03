package doctor

import (
	"go/ast"
	"go/token"
	"strings"
)

// reachesApplicationData reports whether a method reaches the rows of this
// application, by any of the routes that exist.
//
// It replaces isRepository, which asked where the code was filed and what its
// receiver was called. That question had a right answer while every path to a
// table went through a type named InvoiceRepository in app/Repositories. It
// stops having one the moment the model is the path: a project with no
// app/Repositories and no type ending in Repository answers false for its
// entire data layer -- and this predicate gates four authorization rules at
// once, so a method it does not see is a method where none of them apply.
//
// A rule that finds nothing does not fail. It passes. That is the failure mode
// this function exists to prevent, and it is why the fixture tree under
// testdata/orm has no app/Repositories in it at all.
//
// The four routes:
//
//  1. the file is in app/Repositories -- unchanged, and still right;
//  2. the receiver is named like a repository -- unchanged, and still right for
//     the project that files one elsewhere;
//  3. the body calls database/sql -- QueryContext and its neighbours;
//  4. the method hands its Grant to something, in a file that reaches the model.
//
// The fourth is the new one and it is deliberately not a list of method names.
// A list would have to be kept level with a package in another repository, and
// the day it fell behind, four authorization rules would go quiet for whatever
// was added. What it asks instead is structural: this method received a Grant
// and passed it on, in a file that imports the model layer. A method that does
// that is on the path to a row, whatever the method it called is called this
// month.
func reachesApplicationData(f *file, fn *ast.FuncDecl) bool {
	if f.category == "Repositories" {
		return true
	}
	if looksLikeRepository(receiverType(fn)) {
		return true
	}
	// The request path has its own two rules -- handler-reaches-data and
	// controller-reaches-repository -- and they say something more useful than
	// "this controller receives no Grant". Reporting both would be the same
	// finding twice, in the words of the less helpful one.
	if _, onRequestPath := requestPath(f); onRequestPath {
		return false
	}
	if reachesTheDatabase(fn) {
		return true
	}
	return reachesTheModel(f, fn)
}

// reachesTheModel reports whether fn opens a query through the model layer and
// hands its Grant to something.
//
// Both halves are needed, and the first one is narrower than it first looks.
// Importing app/Models is not the signal: a service names a model type in its
// return, and a view struct names one in a field, and neither reaches a row. A
// method that hands its Grant onward is not the signal either: the service that
// passes it to the repository below is correct code, and reporting it is a rule
// firing on something right, which is how a tool teaches people to ignore it.
//
// What is the signal is a call ON the model package -- models.Users(db), or
// anything reached through the model library -- by a method that also hands its
// Grant somewhere. That pair is a query, and a query is where the authorization
// has to be.
func reachesTheModel(f *file, fn *ast.FuncDecl) bool {
	if !callsTheModelPackage(f, fn) {
		return false
	}
	name := grantParameter(fn)
	if name == "" {
		return false
	}
	return passesIdentifier(fn, name)
}

// callsTheModelPackage reports whether the body calls something on a package
// imported from the model layer.
//
// It reads the file's own import names, so an aliased import is followed and a
// package that merely shares a last path segment is not.
func callsTheModelPackage(f *file, fn *ast.FuncDecl) bool {
	names := f.modelPackageNames()
	if len(names) == 0 || fn.Body == nil {
		return false
	}

	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return !found
		}
		if root := rootIdentifier(call.Fun); root != "" && names[root] {
			found = true
			return false
		}
		return !found
	})
	return found
}

// modelPackageNames answers the local names the model layer was imported under.
func (f *file) modelPackageNames() map[string]bool {
	out := map[string]bool{}
	for path, name := range f.imports {
		lowered := strings.ToLower(path)
		for _, candidate := range modelImports {
			if strings.Contains(lowered, candidate) {
				out[name] = true
			}
		}
	}
	return out
}

// rootIdentifier walks a call target back to the identifier it starts from:
// models.Users(db).Where(...).Get(...) roots at "models".
func rootIdentifier(expr ast.Expr) string {
	for {
		switch e := expr.(type) {
		case *ast.Ident:
			return e.Name
		case *ast.SelectorExpr:
			expr = e.X
		case *ast.CallExpr:
			expr = e.Fun
		case *ast.IndexExpr:
			expr = e.X
		case *ast.IndexListExpr:
			expr = e.X
		default:
			return ""
		}
	}
}

// modelImports are the import paths that mean "this file can reach a row
// through the model": the application's own entities, and the library under
// them.
//
// framework/data and hesape/database are deliberately not here. A service that
// imports data.Query for a sort field, or hands its Grant to the repository
// below, is correct code -- and the repository is what the first two routes
// already see. Including them reported the service for not checking a Grant it
// was correctly passing on, which is a rule firing on correct code, which is
// how a tool teaches people to ignore it.
var modelImports = []string{
	"/app/models",
	"/database/model",
}

// grantParameter returns the name the Grant parameter was given, or "" when
// there is none.
//
// A Grant received as "_" is not a Grant this method can pass on, so it answers
// "" for that too -- and grant-not-received is the rule that has something to
// say about it.
func grantParameter(fn *ast.FuncDecl) string {
	if fn.Type.Params == nil {
		return ""
	}
	for _, p := range fn.Type.Params.List {
		sel, ok := p.Type.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Grant" {
			continue
		}
		for _, name := range p.Names {
			if name.Name != "_" {
				return name.Name
			}
		}
	}
	return ""
}

// passesIdentifier reports whether the body hands name to a call.
//
// It looks at arguments only. A Grant that is merely read -- auth.Tenant(g) is
// itself a call and would count, which is right: reading the tenant off a Grant
// to build a query is reaching data as much as handing it to a builder is.
func passesIdentifier(fn *ast.FuncDecl, name string) bool {
	if fn.Body == nil {
		return false
	}
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return !found
		}
		for _, arg := range call.Args {
			if ident, ok := arg.(*ast.Ident); ok && ident.Name == name {
				found = true
				return false
			}
		}
		return !found
	})
	return found
}

// rowSource is what readsRows can say about the value a call is made on.
type rowSource int

const (
	// sourceUnknown is a receiver doctor could not follow to a type. The rule
	// asking keeps judging by the method name, so a shape this cannot read
	// leaves the rule as it was rather than quiet.
	sourceUnknown rowSource = iota
	// sourceRows is the data layer: a model, a query builder, a repository, or
	// a type the project declares itself.
	sourceRows
	// sourceNotRows is a type from another module that is not the data layer:
	// a filesystem disk, a cache store, a client.
	sourceNotRows
)

// readsRows reports whether a method call is made on something that holds
// rows, judged by the type of what it is called on.
//
// The method name cannot say it. Get(ctx, g, key) is a repository answering
// one entity and a filesystem.Disk answering a file, and both are spelled the
// same way down to the arguments. The type can: it is followed from the call
// back to a field of the receiver, a parameter, a local assigned from one of
// those, or the package a chain was opened on, and its import path decides.
//
// It reads declarations, never types, so what it cannot follow -- a value
// returned by a method, a field of a parameter -- answers sourceUnknown.
func readsRows(p *project, f *file, fn *ast.FuncDecl, call *ast.CallExpr) rowSource {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return sourceUnknown
	}
	return exprSource(p, f, fn, sel.X, 0)
}

// exprSource follows an expression to the type it is a value of, and answers
// what that type reads from.
//
// A chain answers for its base: models.Charges(db).Where(...) is the models
// package, and s.storage.Disk("r2") is whatever s.storage holds.
func exprSource(p *project, f *file, fn *ast.FuncDecl, e ast.Expr, depth int) rowSource {
	// Deep enough for a local assigned from a local assigned from a field, and
	// a bound on a definition that refers to itself.
	if depth > 4 {
		return sourceUnknown
	}
	switch x := e.(type) {
	case *ast.ParenExpr:
		return exprSource(p, f, fn, x.X, depth)
	case *ast.StarExpr:
		return exprSource(p, f, fn, x.X, depth)
	case *ast.UnaryExpr:
		return exprSource(p, f, fn, x.X, depth)
	case *ast.IndexExpr:
		return exprSource(p, f, fn, x.X, depth)
	case *ast.CallExpr:
		return exprSource(p, f, fn, x.Fun, depth)
	case *ast.SelectorExpr:
		id, ok := x.X.(*ast.Ident)
		if !ok {
			return exprSource(p, f, fn, x.X, depth)
		}
		if id.Name == receiverName(fn) {
			if declared, typ, ok := fieldOf(p, f, receiverType(fn), x.Sel.Name); ok {
				return typeSource(p, declared, typ)
			}
			return sourceUnknown
		}
		if bindsName(fn, id.Name) {
			// A field of a parameter or a local: the type of the field is in a
			// declaration this does not go looking for.
			return sourceUnknown
		}
		if path, ok := f.importPath(id.Name); ok {
			return pathSource(p, path, x.Sel.Name)
		}
		return sourceUnknown
	case *ast.Ident:
		if x.Name == receiverName(fn) {
			// A method of the type itself, which the project declares.
			return sourceRows
		}
		if typ, ok := parameterType(fn, x.Name); ok {
			return typeSource(p, f, typ)
		}
		if typ, value, ok := localDefinition(fn, x.Name); ok {
			if typ != nil {
				return typeSource(p, f, typ)
			}
			return exprSource(p, f, fn, value, depth+1)
		}
		if path, ok := f.importPath(x.Name); ok {
			return pathSource(p, path, "")
		}
	}
	return sourceUnknown
}

// typeSource answers what a declared type reads from. decl is the file the
// type expression is written in, because its imports are what the package name
// in front of the type refers to.
func typeSource(p *project, decl *file, typ ast.Expr) rowSource {
	for {
		switch t := typ.(type) {
		case *ast.StarExpr:
			typ = t.X
			continue
		case *ast.ParenExpr:
			typ = t.X
			continue
		case *ast.IndexExpr:
			typ = t.X
			continue
		case *ast.IndexListExpr:
			typ = t.X
			continue
		case *ast.Ident:
			// Declared in this package, so it is the project's own.
			return sourceRows
		case *ast.SelectorExpr:
			pkg, ok := t.X.(*ast.Ident)
			if !ok {
				return sourceUnknown
			}
			path, ok := decl.importPath(pkg.Name)
			if !ok {
				return sourceUnknown
			}
			return pathSource(p, path, t.Sel.Name)
		}
		return sourceUnknown
	}
}

// rowPackages are the import paths whose types hold rows: the application's
// models and repositories, and the libraries under them -- the model and query
// builders, the database package around them, and the data.Repository
// contract.
var rowPackages = []string{
	"/app/models",
	"/app/repositories",
	"/database/model",
	"/database/query",
	"/hesape/database",
	"/framework/data",
}

// pathSource answers what a type from an import path reads from.
//
// A type the project declares answers rows whatever package it is in: a
// service written by hand that hands out one entity is a read of a row, and is
// what this rule was first written for. A type named like a repository answers
// rows wherever it comes from. Anything else from another module is not the
// data layer -- a filesystem disk, a cache store -- and its Get is not a row.
func pathSource(p *project, path, typeName string) rowSource {
	lowered := strings.ToLower(path)
	for _, candidate := range rowPackages {
		if strings.Contains(lowered, candidate) {
			return sourceRows
		}
	}
	if strings.HasSuffix(typeName, "Repository") {
		return sourceRows
	}
	if p.modulePath != "" && (path == p.modulePath || strings.HasPrefix(path, p.modulePath+"/")) {
		return sourceRows
	}
	return sourceNotRows
}

// receiverName is the name a method gives its receiver, or "" when it gives
// none.
func receiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 || len(fn.Recv.List[0].Names) == 0 {
		return ""
	}
	if name := fn.Recv.List[0].Names[0].Name; name != "_" {
		return name
	}
	return ""
}

// fieldOf finds a field of a struct type declared in the same package as f,
// and answers the file that declares it with the field's type.
func fieldOf(p *project, f *file, typeName, field string) (*file, ast.Expr, bool) {
	if typeName == "" {
		return nil, nil, false
	}
	for _, candidate := range p.files {
		if candidate.dir != f.dir || candidate.isTest {
			continue
		}
		var found ast.Expr
		candidate.types(func(ts *ast.TypeSpec) {
			if found != nil || ts.Name.Name != typeName {
				return
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				return
			}
			for _, fld := range st.Fields.List {
				for _, name := range fld.Names {
					if name.Name == field {
						found = fld.Type
					}
				}
			}
		})
		if found != nil {
			return candidate, found, true
		}
	}
	return nil, nil, false
}

// parameterType is the declared type of a parameter of fn.
func parameterType(fn *ast.FuncDecl, name string) (ast.Expr, bool) {
	if fn.Type == nil || fn.Type.Params == nil {
		return nil, false
	}
	for _, field := range fn.Type.Params.List {
		for _, n := range field.Names {
			if n.Name == name {
				return field.Type, true
			}
		}
	}
	return nil, false
}

// localDefinition finds where a local of fn is first given a value: a `var`
// with a type, or a `:=` or `var` from one expression. It answers the type when
// one is written, and the expression otherwise.
//
// A local assigned from a call that answers two values -- `file, err :=
// disk.Get(...)` -- has the type of the call's result, which no declaration in
// view says, so it is not found.
func localDefinition(fn *ast.FuncDecl, name string) (ast.Expr, ast.Expr, bool) {
	if fn.Body == nil {
		return nil, nil, false
	}
	var typ, value ast.Expr
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		switch s := n.(type) {
		case *ast.AssignStmt:
			if s.Tok != token.DEFINE || len(s.Lhs) != len(s.Rhs) {
				return true
			}
			for i, lhs := range s.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Name == name {
					value, found = s.Rhs[i], true
					return false
				}
			}
		case *ast.ValueSpec:
			for i, ident := range s.Names {
				if ident.Name != name {
					continue
				}
				switch {
				case s.Type != nil:
					typ, found = s.Type, true
				case len(s.Values) == len(s.Names):
					value, found = s.Values[i], true
				}
				return false
			}
		}
		return true
	})
	return typ, value, found
}

// bindsName reports whether fn declares name as a parameter or a local, which
// is what keeps a local called like an imported package from being read as
// the package.
func bindsName(fn *ast.FuncDecl, name string) bool {
	if _, ok := parameterType(fn, name); ok {
		return true
	}
	if fn.Body == nil {
		return false
	}
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		switch s := n.(type) {
		case *ast.AssignStmt:
			if s.Tok == token.DEFINE {
				for _, lhs := range s.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && id.Name == name {
						found = true
					}
				}
			}
		case *ast.ValueSpec:
			for _, ident := range s.Names {
				if ident.Name == name {
					found = true
				}
			}
		}
		return !found
	})
	return found
}
