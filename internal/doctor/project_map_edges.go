package doctor

import (
	"go/ast"
	"go/token"
	"go/types"
	"regexp"
	"strconv"
	"strings"
)

// referenceUse is how a qualified name is used where it is written.
type referenceUse int

const (
	useReference referenceUse = iota
	useCall
	useComposite
)

// walkWithParents visits every node under root with the node that encloses it.
func walkWithParents(root ast.Node, visit func(n, parent ast.Node)) {
	var stack []ast.Node
	ast.Inspect(root, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		var parent ast.Node
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		}
		visit(n, parent)
		stack = append(stack, n)
		return true
	})
}

// eachDeclaration visits every top-level declaration of f with the function
// it is, or nil for a declaration that is not one.
func eachDeclaration(f *file, visit func(decl ast.Decl, fn *ast.FuncDecl)) {
	for _, decl := range f.ast.Decls {
		fn, _ := decl.(*ast.FuncDecl)
		visit(decl, fn)
	}
}

// addReferenceEdges reads every qualified name a file writes that resolves to
// a declaration of the project's own packages, and draws the edge the two
// kinds of artifact imply.
func (s *mapState) addReferenceEdges(files []*file) {
	prefix := s.p.modulePath + "/"
	for _, f := range files {
		if s.artifacts[f.rel] == nil || s.p.modulePath == "" {
			continue
		}
		if s.artifacts[f.rel].node.Generated {
			continue
		}
		eachDeclaration(f, func(decl ast.Decl, fn *ast.FuncDecl) {
			source := s.ownerOf(f, fn)
			walkWithParents(decl, func(n, parent ast.Node) {
				switch node := n.(type) {
				case *ast.SelectorExpr:
					alias, ok := node.X.(*ast.Ident)
					if !ok || f.objects().Local(alias) {
						return
					}
					importPath, imported := f.importPath(alias.Name)
					if !imported {
						return
					}
					dir, inProject := strings.CutPrefix(importPath, prefix)
					if !inProject {
						return
					}
					declaring := s.declared[dir][node.Sel.Name]
					if declaring == nil {
						return
					}
					s.reference(f, source, declaring, node.Sel.Name, useOf(node, parent), node)
				case *ast.Ident:
					// A test in the package it tests names what it uses without
					// a qualifier, and those names are the package's own.
					if !f.isTest || localName(f, node, parent) {
						return
					}
					if selector, ok := parent.(*ast.SelectorExpr); ok && selector.Sel == node {
						return
					}
					declaring := s.declared[f.dir][node.Name]
					if declaring == nil || declaring.isTest {
						return
					}
					s.reference(f, source, declaring, node.Name, useOf(node, parent), node)
				}
			})
		})
	}
}

// localName reports whether id, an unqualified name written in f, is one f
// declares rather than a name of its package's other files.
//
// It is the file's own resolution with three readings that are deliberately the
// map's and not the type checker's, which keep the edges what they have been:
//
//   - a composite literal key is looked up by its name in the scopes around
//     it, and a package-level name only once the file has declared it, whether
//     the key turns out to name a field or not
//   - the name of a method, and of an init function, in its declaration is no
//     declaration of the file
//   - a type parameter of a method's receiver, where it is declared, is none
//     either
func localName(f *file, id *ast.Ident, parent ast.Node) bool {
	objects := f.objects()
	switch p := parent.(type) {
	case *ast.KeyValueExpr:
		if p.Key == id {
			return keyInScope(f, id)
		}
	case *ast.FuncDecl:
		if p.Name == id && (p.Recv != nil || id.Name == "init") {
			return false
		}
	}
	if name, ok := objects.Object(id).(*types.TypeName); ok && name.Pos() == id.Pos() {
		if _, parameter := name.Type().(*types.TypeParam); parameter && inReceiver(f, id.Pos()) {
			return false
		}
	}
	return objects.Local(id)
}

// keyInScope reports whether a declaration of f named like the composite
// literal key id is in scope at the key: a local one declared before it, or a
// package-level one whose declaration the file has finished by then. A type
// counts from its own name on, a constant or variable from the end of its
// spec, a function from the end of its body.
func keyInScope(f *file, id *ast.Ident) bool {
	pkg := f.objects().Package()
	if pkg == nil {
		return false
	}
	scope := pkg.Scope().Innermost(id.Pos())
	if scope == nil {
		return false
	}
	_, object := scope.LookupParent(id.Name, id.Pos())
	if object == nil || object.Parent() == types.Universe {
		return false
	}
	if _, imported := object.(*types.PkgName); imported {
		return false
	}
	if object.Parent() != pkg.Scope() {
		return true
	}
	declared := token.NoPos
	for _, decl := range f.ast.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name.Pos() == object.Pos() {
				declared = d.End()
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.Pos() == object.Pos() {
						declared = s.Name.Pos()
					}
				case *ast.ValueSpec:
					for _, name := range s.Names {
						if name.Pos() == object.Pos() {
							declared = s.End()
						}
					}
				}
			}
		}
	}
	return declared.IsValid() && declared <= id.Pos()
}

// inReceiver reports whether pos falls in the receiver of one of f's methods.
func inReceiver(f *file, pos token.Pos) bool {
	for _, decl := range f.ast.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv != nil && fn.Recv.Pos() <= pos && pos < fn.Recv.End() {
			return true
		}
	}
	return false
}

func useOf(n, parent ast.Node) referenceUse {
	switch p := parent.(type) {
	case *ast.CallExpr:
		if p.Fun == n {
			return useCall
		}
	case *ast.CompositeLit:
		if p.Type == n {
			return useComposite
		}
	}
	return useReference
}

// reference draws the edge one use of a declaration implies, if it implies
// one.
func (s *mapState) reference(f *file, source string, declaring *file, name string, use referenceUse, at ast.Node) {
	target := s.targetOf(declaring)
	from := s.b.nodes[source]
	if target == nil || from == nil || target.node.ID == source || declaring.rel == f.rel {
		return
	}
	location := mapLocation(f, at)
	if from.Kind == "test" {
		if target.node.Kind != "test" {
			s.b.link(target.node.ID, source, EdgeTestedBy, location)
		}
		return
	}
	if declaring.dir == f.dir {
		return
	}
	switch target.node.Kind {
	case "policy":
		s.b.link(source, target.node.ID, EdgeAuthorizes, location)
	case "request":
		s.b.link(source, target.node.ID, EdgeValidatesWith, location)
	case "model":
		if use == useCall || from.Kind == "repository" {
			s.b.link(source, target.node.ID, EdgePersists, location)
		}
	case "repository":
		s.b.link(source, target.node.ID, EdgePersists, location)
	case "resource":
		switch from.Kind {
		case "action", "controller", "webhook":
			s.b.link(source, target.node.ID, EdgeRenders, location)
		}
	case "job", "event", "notification", "mail":
		switch from.Kind {
		case "bootstrap", "provider", "entrypoint":
			return
		}
		if from.Kind == "listener" && target.node.Kind == "event" {
			s.b.link(source, target.node.ID, EdgeListensTo, location)
			return
		}
		if use == useComposite || use == useCall && strings.HasPrefix(name, "Dispatch") || strings.HasSuffix(name, "Name") {
			s.b.link(source, target.node.ID, EdgeDispatches, location)
		}
	}
}

// renderingCalls are the context methods whose first argument is the name of
// a view.
var renderingCalls = []string{".View", ".Fragment", ".Partial"}

// viewFields are the struct fields a mail sets to the name of a view.
var viewFields = map[string]bool{"View": true, "TextView": true, "HTMLView": true}

// addStringEdges reads the names written as strings: the view an action
// renders, the view a mail renders, and the event a listener reacts to.
func (s *mapState) addStringEdges(files []*file) {
	eventNames := map[string]string{}
	for _, artifact := range s.artifacts {
		if artifact.node.Kind != "event" {
			continue
		}
		for _, decl := range artifact.f.ast.Decls {
			general, ok := decl.(*ast.GenDecl)
			if !ok || general.Tok != token.CONST {
				continue
			}
			for _, spec := range general.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range value.Names {
					if i >= len(value.Values) || !strings.HasSuffix(name.Name, "Name") {
						continue
					}
					if literal, ok := stringLiteral(value.Values[i]); ok && literal != "" {
						eventNames[literal] = artifact.node.ID
					}
				}
			}
		}
	}

	for _, f := range files {
		artifact := s.artifacts[f.rel]
		if artifact == nil || artifact.node.Generated || artifact.node.Kind == "test" {
			continue
		}
		eachDeclaration(f, func(decl ast.Decl, fn *ast.FuncDecl) {
			source := s.ownerOf(f, fn)
			ast.Inspect(decl, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.CallExpr:
					called := callName(node)
					for _, suffix := range renderingCalls {
						if !strings.HasSuffix(called, suffix) || len(node.Args) == 0 {
							continue
						}
						if name, ok := stringLiteral(node.Args[0]); ok {
							s.b.link(source, s.views[name], EdgeRenders, mapLocation(f, node.Args[0]))
						}
					}
				case *ast.KeyValueExpr:
					key, ok := node.Key.(*ast.Ident)
					if !ok || !viewFields[key.Name] {
						return true
					}
					if name, ok := stringLiteral(node.Value); ok {
						s.b.link(source, s.views[name], EdgeRenders, mapLocation(f, node.Value))
					}
				case *ast.BasicLit:
					if artifact.node.Kind != "listener" || node.Kind != token.STRING {
						return true
					}
					if value, err := strconv.Unquote(node.Value); err == nil {
						s.b.link(source, eventNames[value], EdgeListensTo, mapLocation(f, node))
					}
				}
				return true
			})
		})
	}
}

// sqlTable finds the table a CREATE TABLE or ALTER TABLE statement names.
var sqlTable = regexp.MustCompile("(?i)\\b(?:create|alter)\\s+table\\s+(?:if\\s+not\\s+exists\\s+)?[\"`]?([a-z_][a-z0-9_]*)")

// addTableEdges links each model to the migrations that declare its table,
// and puts the migration into the model's feature.
func (s *mapState) addTableEdges(files []*file) {
	tables := map[string][]string{}
	for _, artifact := range s.artifacts {
		if artifact.node.Kind != "model" || artifact.node.Generated {
			continue
		}
		ast.Inspect(artifact.f.ast, func(n ast.Node) bool {
			literal, ok := n.(*ast.CompositeLit)
			if !ok || typeExprName(literal.Type) != "TableSpec" {
				return true
			}
			for _, element := range literal.Elts {
				pair, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := pair.Key.(*ast.Ident); ok && key.Name == "Name" {
					if table, ok := stringLiteral(pair.Value); ok && table != "" {
						tables[table] = append(tables[table], artifact.node.ID)
					}
				}
			}
			return true
		})
	}

	for _, f := range files {
		artifact := s.artifacts[f.rel]
		if artifact == nil || artifact.node.Kind != "migration" {
			continue
		}
		link := func(table string, at ast.Node) {
			for _, model := range tables[table] {
				s.b.link(model, artifact.node.ID, EdgePersists, mapLocation(f, at))
				if feature := s.b.nodes[model].Feature; feature != "" && artifact.node.Feature == "" {
					artifact.node.Feature = feature
					s.b.link(feature, artifact.node.ID, EdgeContains, nil)
				}
			}
		}
		ast.Inspect(f.ast, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				called := callName(node)
				if !strings.Contains(called, "Schema().") || len(node.Args) < 2 {
					return true
				}
				if strings.HasSuffix(called, ".Create") || strings.HasSuffix(called, ".Table") {
					if table, ok := stringLiteral(node.Args[1]); ok {
						link(table, node.Args[1])
					}
				}
			case *ast.BasicLit:
				if node.Kind != token.STRING {
					return true
				}
				for _, match := range sqlTable.FindAllStringSubmatch(node.Value, -1) {
					link(strings.ToLower(match[1]), node)
				}
			}
			return true
		})
	}
}

// viewDirective finds the name an @extends or @include names.
var viewDirective = regexp.MustCompile(`@(?:extends|include)\(\s*['"]([^'"]+)['"]`)

// addViewEdges links a view to the views it extends and includes.
func (s *mapState) addViewEdges() {
	for _, view := range s.p.views {
		from := s.views[view.name]
		for number, line := range strings.Split(view.body, "\n") {
			for _, match := range viewDirective.FindAllStringSubmatchIndex(line, -1) {
				name := line[match[2]:match[3]]
				s.b.link(from, s.views[name], EdgeRenders, &MapLocation{
					File: view.rel, Line: number + 1, Column: match[0] + 1,
					EndLine: number + 1, EndColumn: match[1] + 1,
				})
			}
		}
	}
}
