package doctor

import (
	"go/ast"
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
