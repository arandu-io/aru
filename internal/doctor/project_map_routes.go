package doctor

import (
	"go/ast"
	"go/token"
	"go/types"
	"net/http"
	"strconv"
	"strings"

	"github.com/arandu-io/aru/internal/gen"
)

// routeRegistration is one call that registers routes, read from routes/.
type routeRegistration struct {
	f    *file
	call *ast.CallExpr
	// method is the registering method: Resource, Singleton, ResourceAction,
	// Invokable, Action, Get, Post, Put, Patch or Delete.
	method string
	prefix string
	name   string
}

// resourceActions are the seven actions Resource looks for, in the order it
// registers them, with the method and the path each answers at.
var resourceActions = []struct {
	action, verb, method string
	member               bool
	suffix               string
}{
	{"Index", "index", http.MethodGet, false, ""},
	{"Create", "create", http.MethodGet, false, "/create"},
	{"Store", "store", http.MethodPost, false, ""},
	{"Show", "show", http.MethodGet, true, ""},
	{"Edit", "edit", http.MethodGet, true, "/edit"},
	{"Update", "update", http.MethodPut, true, ""},
	{"Destroy", "destroy", http.MethodDelete, true, ""},
}

// singletonActions are the three a singleton answers, all at its one path.
var singletonActions = []struct {
	action, verb, method, suffix string
}{
	{"Show", "show", http.MethodGet, ""},
	{"Edit", "edit", http.MethodGet, "/edit"},
	{"Update", "update", http.MethodPut, ""},
}

// verbMethods are the router methods named after the one HTTP method they
// register.
var verbMethods = map[string]string{
	"Get": http.MethodGet, "Post": http.MethodPost, "Put": http.MethodPut,
	"Patch": http.MethodPatch, "Delete": http.MethodDelete,
}

// addRoutes reads every route registration in routes/ and puts one node per
// route into the map, each reaching the action the router would call.
//
// A Resource or a Singleton registers only the actions its controller
// declares, the way the router does; an update registers twice, under PUT and
// PATCH, as two routes with one name. A registration whose controller cannot
// be resolved still lists its routes when the call itself says which they are
// -- an Action, a verb, a ResourceAction -- and lists none for a Resource or a
// Singleton, whose routes are decided by a controller this cannot see.
func (s *mapState) addRoutes(files []*file) {
	for _, f := range files {
		if f.isTest || !strings.HasPrefix(f.rel, "routes/") || f.rel == "routes/console.go" {
			continue
		}
		for _, registration := range routeRegistrations(f, s.objectsOf(f)) {
			s.addRegistration(registration)
		}
	}
}

// routeRegistrations finds the registering calls of one file, with the prefix
// of the group each is made on and the name chained onto it.
func routeRegistrations(f *file, objects *fileObjects) []routeRegistration {
	names := map[*ast.CallExpr]string{}
	ast.Inspect(f.ast, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Name" {
			return true
		}
		inner, ok := selector.X.(*ast.CallExpr)
		if !ok {
			return true
		}
		if name, ok := stringLiteral(call.Args[0]); ok {
			names[inner] = name
		}
		return true
	})

	var out []routeRegistration
	for _, decl := range f.ast.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		prefixes := map[types.Object]string{}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.AssignStmt:
				for i, left := range node.Lhs {
					identifier, ok := left.(*ast.Ident)
					if !ok || i >= len(node.Rhs) {
						continue
					}
					declared := objects.object(identifier)
					if declared == nil {
						continue
					}
					if prefix, router := routerPrefix(node.Rhs[i], objects, prefixes); router {
						prefixes[declared] = prefix
					}
				}
			case *ast.CallExpr:
				selector, ok := node.Fun.(*ast.SelectorExpr)
				if !ok || !registersRoutes(selector.Sel.Name, node) {
					return true
				}
				prefix, _ := routerPrefix(selector.X, objects, prefixes)
				out = append(out, routeRegistration{
					f: f, call: node, method: selector.Sel.Name, prefix: prefix, name: names[node],
				})
			}
			return true
		})
	}
	return out
}

// registersRoutes reports whether a call of a method by this name, with these
// arguments, is a route registration. The arguments decide as much as the
// name: Get with a pattern is a route, and Get with a key is a map lookup.
func registersRoutes(method string, call *ast.CallExpr) bool {
	literal := func(i int) bool {
		if i >= len(call.Args) {
			return false
		}
		_, ok := stringLiteral(call.Args[i])
		return ok
	}
	switch method {
	case "Resource", "Singleton":
		return len(call.Args) == 2 && literal(0)
	case "ResourceAction":
		return len(call.Args) >= 4 && literal(1) && literal(2)
	case "Invokable":
		return len(call.Args) == 3 && literal(1)
	case "Action":
		return len(call.Args) >= 3 && literal(0) && literal(1)
	}
	if _, verb := verbMethods[method]; verb && len(call.Args) >= 2 {
		pattern, ok := stringLiteral(call.Args[0])
		return ok && strings.HasPrefix(pattern, "/")
	}
	return false
}

// routerPrefix answers the path prefix of a router expression, and whether
// the expression is one: a parameter or variable is, and so is a Group or a
// ForModule call on one. The prefix of a router that arrived as a parameter is
// unknown here and read as empty.
func routerPrefix(expr ast.Expr, objects *fileObjects, prefixes map[types.Object]string) (string, bool) {
	switch e := expr.(type) {
	case *ast.Ident:
		if declared := objects.object(e); declared != nil {
			if prefix, found := prefixes[declared]; found {
				return prefix, true
			}
		}
		return "", true
	case *ast.ParenExpr:
		return routerPrefix(e.X, objects, prefixes)
	case *ast.CallExpr:
		selector, ok := e.Fun.(*ast.SelectorExpr)
		if !ok {
			return "", false
		}
		switch selector.Sel.Name {
		case "Group":
			base, ok := routerPrefix(selector.X, objects, prefixes)
			if !ok || len(e.Args) == 0 {
				return "", false
			}
			group, literal := stringLiteral(e.Args[0])
			if !literal {
				return base, true
			}
			return joinPattern(base, group), true
		case "ForModule":
			return routerPrefix(selector.X, objects, prefixes)
		}
	}
	return "", false
}

// joinPattern puts a group prefix in front of a pattern, with one slash
// between them.
func joinPattern(prefix, pattern string) string {
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix == "" {
		return pattern
	}
	if pattern == "" || pattern == "/" {
		return prefix
	}
	if !strings.HasPrefix(pattern, "/") {
		pattern = "/" + pattern
	}
	return prefix + pattern
}

// resourcePaths answers the route name, the collection path and the member
// path of a resource name, nested shallow when the name is dotted -- the rule
// the router applies, with the parameter named by the same inflector.
func resourcePaths(name string) (string, string, string, string) {
	name = strings.Trim(name, "/")
	if !strings.Contains(name, ".") {
		return name, "/" + name, "/" + name + "/{id}", ""
	}
	segments := strings.Split(name, ".")
	var collection strings.Builder
	for i, segment := range segments {
		collection.WriteString("/" + segment)
		if i < len(segments)-1 {
			collection.WriteString("/{" + gen.ResourceParameter(segment) + "}")
		}
	}
	last := segments[len(segments)-1]
	return name, collection.String(), "/" + last + "/{" + gen.ResourceParameter(last) + "}",
		strings.Join(segments[:len(segments)-1], ".")
}

// addRegistration turns one registration into route nodes.
func (s *mapState) addRegistration(r routeRegistration) {
	at := mapLocation(r.f, r.call)
	switch r.method {
	case "Resource", "Singleton":
		resource, _ := stringLiteral(r.call.Args[0])
		controller := s.resolveController(r.f, r.call.Args[1])
		if controller == nil {
			return
		}
		name, collection, member, parent := resourcePaths(resource)
		dir := controller.f.dir
		typeName := s.controllerTypeName(r.f, r.call.Args[1])
		node := s.artifacts[controller.f.rel].node
		node.Variant = strings.ToLower(r.method)
		if parent != "" {
			node.NestedUnder = parent
		}
		if r.method == "Resource" {
			for _, a := range resourceActions {
				action := s.actions[dir+"\x00"+typeName+"."+a.action]
				if action == "" {
					continue
				}
				pattern := collection + a.suffix
				if a.member {
					pattern = member + a.suffix
				}
				s.addRoute(r, at, a.method, joinPattern(r.prefix, pattern), name+"."+a.verb, action, parent)
				if a.action == "Update" {
					s.addRoute(r, at, http.MethodPatch, joinPattern(r.prefix, pattern), name+"."+a.verb, action, parent)
				}
			}
			return
		}
		for _, a := range singletonActions {
			action := s.actions[dir+"\x00"+typeName+"."+a.action]
			if action == "" {
				continue
			}
			pattern := joinPattern(r.prefix, collection+a.suffix)
			s.addRoute(r, at, a.method, pattern, name+"."+a.verb, action, parent)
			if a.action == "Update" {
				s.addRoute(r, at, http.MethodPatch, pattern, name+"."+a.verb, action, parent)
			}
		}
	case "ResourceAction":
		method, _ := stringLiteral(r.call.Args[0])
		if method == "" {
			method = httpMethodConstant(r.call.Args[0])
		}
		resource, _ := stringLiteral(r.call.Args[1])
		verb, _ := stringLiteral(r.call.Args[2])
		name, _, member, parent := resourcePaths(resource)
		verb = strings.Trim(verb, "/")
		s.addRoute(r, at, strings.ToUpper(method), joinPattern(r.prefix, member+"/"+verb), name+"."+verb,
			s.resolveHandler(r.f, r.call.Args[3]), parent)
	case "Invokable":
		method, _ := stringLiteral(r.call.Args[0])
		if method == "" {
			method = httpMethodConstant(r.call.Args[0])
		}
		pattern, _ := stringLiteral(r.call.Args[1])
		action := ""
		if controller := s.resolveController(r.f, r.call.Args[2]); controller != nil {
			typeName := s.controllerTypeName(r.f, r.call.Args[2])
			action = s.actions[controller.f.dir+"\x00"+typeName+".Invoke"]
			s.artifacts[controller.f.rel].node.Variant = "invokable"
		}
		s.addRoute(r, at, strings.ToUpper(method), joinPattern(r.prefix, pattern), r.name, action, "")
	case "Action":
		method, _ := stringLiteral(r.call.Args[0])
		pattern, _ := stringLiteral(r.call.Args[1])
		s.addRoute(r, at, strings.ToUpper(method), joinPattern(r.prefix, pattern), r.name,
			s.resolveHandler(r.f, r.call.Args[2]), "")
	default:
		pattern, _ := stringLiteral(r.call.Args[0])
		s.addRoute(r, at, verbMethods[r.method], joinPattern(r.prefix, pattern), r.name,
			s.resolveHandler(r.f, r.call.Args[1]), "")
	}
}

// httpMethodConstant reads http.MethodPost as POST, for the registrations that
// take the method as their first argument.
func httpMethodConstant(expr ast.Expr) string {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	method, ok := strings.CutPrefix(selector.Sel.Name, "Method")
	if !ok {
		return ""
	}
	return strings.ToUpper(method)
}

// addRoute puts one route into the map and links it to the action it reaches
// and to the feature of that action.
func (s *mapState) addRoute(r routeRegistration, at *MapLocation, method, pattern, name, action, parent string) {
	if method == "" {
		method = "ANY"
	}
	base := "route:" + graphID(method+" "+pattern)
	id := base
	for n := 2; ; n++ {
		existing, taken := s.b.nodes[id]
		if !taken || existing.File == at.File && existing.Line == at.Line && existing.Column == at.Column {
			break
		}
		id = base + ":" + strconv.Itoa(n)
	}
	label := method + " " + pattern
	node := s.b.add(MapNode{
		ID: id, Kind: "route", Label: label, Detail: name, File: at.File,
		Line: at.Line, Column: at.Column, EndLine: at.EndLine, EndColumn: at.EndColumn,
		Method: method, Pattern: pattern, Name: name, NestedUnder: parent,
	})
	if routeFile := s.artifacts[r.f.rel]; routeFile != nil {
		s.b.link(routeFile.node.ID, id, EdgeContains, nil)
	}
	if action == "" {
		return
	}
	s.b.link(id, action, EdgeRoutesTo, at)
	if target := s.b.nodes[action]; target != nil && target.Feature != "" {
		node.Feature = target.Feature
		s.b.link(target.Feature, id, EdgeContains, nil)
	}
}

// resolveHandler answers the action a handler expression names: d.Note.Show,
// notes.Show, ctrl.Index. A function literal and a function of another kind
// answer nothing.
func (s *mapState) resolveHandler(f *file, expr ast.Expr) string {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	controller := s.resolveController(f, selector.X)
	if controller == nil {
		return ""
	}
	typeName := s.controllerTypeName(f, selector.X)
	return s.actions[controller.f.dir+"\x00"+typeName+"."+selector.Sel.Name]
}

// resolveController answers the controller artifact an expression holds.
func (s *mapState) resolveController(f *file, expr ast.Expr) *mapArtifact {
	typeName := s.controllerTypeName(f, expr)
	if typeName == "" {
		return nil
	}
	declaring := s.controllerTypes[typeName]
	if declaring == nil {
		return nil
	}
	return s.artifacts[declaring.rel]
}

// controllerTypeName answers the name of the controller type an expression
// holds, read from what is written in this file: the declared type of a
// parameter or a variable, the field of a struct the routes package declares,
// a composite literal, or a constructor named New plus the type.
func (s *mapState) controllerTypeName(f *file, expr ast.Expr) string {
	name := s.expressionType(f, expr, 0)
	if _, known := s.controllerTypes[name]; known {
		return name
	}
	return ""
}

func (s *mapState) expressionType(f *file, expr ast.Expr, depth int) string {
	if depth > 8 {
		return ""
	}
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return s.expressionType(f, e.X, depth+1)
	case *ast.StarExpr:
		return s.expressionType(f, e.X, depth+1)
	case *ast.UnaryExpr:
		if e.Op == token.AND {
			return s.expressionType(f, e.X, depth+1)
		}
	case *ast.CompositeLit:
		return typeExprName(e.Type)
	case *ast.CallExpr:
		called := typeExprName(e.Fun)
		if constructed, ok := strings.CutPrefix(called, "New"); ok {
			return constructed
		}
	case *ast.Ident:
		objects := s.objectsOf(f)
		declared := objects.object(e)
		if declared == nil {
			return ""
		}
		switch decl := objects.decls[declared].(type) {
		case *ast.Field:
			return typeExprName(decl.Type)
		case *ast.ValueSpec:
			if decl.Type != nil {
				return typeExprName(decl.Type)
			}
			for i, name := range decl.Names {
				if objects.object(name) == declared && i < len(decl.Values) {
					return s.expressionType(f, decl.Values[i], depth+1)
				}
			}
		case *ast.AssignStmt:
			for i, left := range decl.Lhs {
				if identifier, ok := left.(*ast.Ident); ok && objects.object(identifier) == declared && len(decl.Lhs) == len(decl.Rhs) {
					return s.expressionType(f, decl.Rhs[i], depth+1)
				}
			}
		}
	case *ast.SelectorExpr:
		owner := s.expressionType(f, e.X, depth+1)
		if owner == "" {
			return ""
		}
		if field := s.structField(f.dir, owner, e.Sel.Name); field != nil {
			return typeExprName(field)
		}
	}
	return ""
}

// structField answers the declared type of a field of a struct declared in
// dir, or nil.
func (s *mapState) structField(dir, typeName, field string) ast.Expr {
	declaring := s.declared[dir][typeName]
	if declaring == nil {
		return nil
	}
	var found ast.Expr
	declaring.types(func(ts *ast.TypeSpec) {
		if ts.Name.Name != typeName {
			return
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return
		}
		for _, f := range st.Fields.List {
			for _, name := range f.Names {
				if name.Name == field {
					found = f.Type
				}
			}
		}
	})
	return found
}

// typeExprName is the bare name of a type or function expression:
// *controllers.NoteController is NoteController.
func typeExprName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.StarExpr:
		return typeExprName(e.X)
	case *ast.SelectorExpr:
		return e.Sel.Name
	case *ast.Ident:
		return e.Name
	case *ast.IndexExpr:
		return typeExprName(e.X)
	}
	return ""
}
