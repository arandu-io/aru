package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"sort"
)

// heldConstructors refuses a constructor's result kept in a variable and used
// in a way that is right for the generic model and wrong for the query that
// replaces it.
//
// The generic constructor returned a model, and every chain started on the
// model opened a query of its own, so starting two chains on one variable was
// two queries. The concrete constructor returns one query, and a query is
// mutable: the second chain carries the clauses of the first, compiles, and
// reads the wrong rows. Nothing in the result says so, which is why the tool
// stops instead of rewriting the variable's type and moving on.
//
// What it lets through is the one shape that means the same before and after:
// a local variable read exactly once, as the receiver of a method call, and
// not inside a loop or a function literal that its binding is outside of. That
// read starts the only chain the variable will ever have. Every other read --
// a second chain, an argument, a return, a copy into another name, a capture
// that may run again -- is refused, and so is a package variable, which every
// caller shares. Counting reads is the whole rule because what a reader does
// with a variable it was handed is not visible from here.
func (u *upgrader) heldConstructors(f *source) {
	for _, decl := range f.ast.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			if d.Tok != token.VAR {
				continue
			}
			for _, sp := range d.Specs {
				vs := sp.(*ast.ValueSpec)
				if len(vs.Names) != len(vs.Values) {
					continue
				}
				for i, name := range vs.Names {
					if call, e := u.heldCall(f, vs.Values[i]); e != nil && name.Name != "_" {
						u.problemAt(f, f.line(name), name.Name+" holds "+firstLine(f.text(call))+
							" in a package variable, which every use of it shares"+u.heldReason(f, call, e))
					}
				}
			}
		case *ast.FuncDecl:
			if d.Body == nil {
				continue
			}
			// The parameters and the top level of the body are one scope.
			vars := new([]*heldVar)
			w := &heldWalker{u: u, f: f, vars: vars, scope: &heldScope{names: map[string]*heldVar{}}}
			for _, list := range []*ast.FieldList{d.Recv, d.Type.Params, d.Type.Results} {
				w.declareFields(list)
			}
			w.walk(stmts(d.Body.List)...)
			u.judgeHeld(f, *vars)
		}
	}
}

// heldCall answers e, unwrapped of parentheses, when it is a call of a
// constructor the first pass rewrote, and the entity it builds.
func (u *upgrader) heldCall(f *source, e ast.Expr) (ast.Expr, *entity) {
	for {
		paren, ok := e.(*ast.ParenExpr)
		if !ok {
			break
		}
		e = paren.X
	}
	return e, u.constructorCalled(f, e)
}

// heldReason is what every refusal of a held constructor ends with: what the
// constructor returns now, and what to write instead.
func (u *upgrader) heldReason(f *source, call ast.Expr, e *entity) string {
	qualifier := ""
	if sel, ok := call.(*ast.CallExpr).Fun.(*ast.SelectorExpr); ok {
		qualifier = f.text(sel.X) + "."
	}
	return ": the constructor now returns one mutable *" + qualifier + e.name + "Query, not a model that " +
		"opens a new query for every chain, so each use would carry the clauses of the ones before it. " +
		"Start each query at the constructor"
}

// judgeHeld records a problem for every variable bound to a constructor whose
// reads are not the one safe shape.
func (u *upgrader) judgeHeld(f *source, vars []*heldVar) {
	for _, v := range vars {
		if len(v.bindings) == 0 || len(v.reads) == 0 {
			continue
		}
		sort.Slice(v.reads, func(i, j int) bool { return v.reads[i].id.Pos() < v.reads[j].id.Pos() })
		first, read := v.bindings[0], v.reads[0]
		how := ""
		switch {
		case len(v.reads) > 1:
			how = fmt.Sprintf("is used again at line %d", f.origLine(f.line(v.reads[1].id)))
		case !read.chain:
			how = fmt.Sprintf("is used at line %d as a value rather than as the start of one chain",
				f.origLine(f.line(read.id)))
		default:
			for _, b := range v.bindings {
				if !within(read.loops, b.loops) {
					how = fmt.Sprintf("is used at line %d inside a loop or a function literal it was not bound in, "+
						"which can run that chain more than once", f.origLine(f.line(read.id)))
					break
				}
			}
		}
		if how != "" {
			u.problemAt(f, f.line(first.name), v.name+" holds "+firstLine(f.text(first.call))+" and "+how+
				u.heldReason(f, first.call, first.entity))
		}
	}
}

// within reports whether every loop and function literal around a read is
// also around the binding: the read then runs at most once per binding.
// Both lists run from the outermost inwards, so it is a prefix test.
func within(read, binding []ast.Node) bool {
	if len(read) > len(binding) {
		return false
	}
	for i := range read {
		if read[i] != binding[i] {
			return false
		}
	}
	return true
}

// heldVar is one local variable of a function, with where a constructor was
// assigned to it and where it is read.
type heldVar struct {
	name     string
	bindings []heldBinding
	reads    []heldRead
}

// heldBinding is one assignment of a constructor's result to a variable.
type heldBinding struct {
	name   *ast.Ident
	call   ast.Expr
	entity *entity
	loops  []ast.Node
}

// heldRead is one read of a variable.
type heldRead struct {
	id *ast.Ident
	// chain reports whether the read is the receiver of a method call.
	chain bool
	loops []ast.Node
}

// heldScope is one lexical scope of a function body.
type heldScope struct {
	parent *heldScope
	names  map[string]*heldVar
}

func (s *heldScope) lookup(name string) *heldVar {
	for ; s != nil; s = s.parent {
		if v, ok := s.names[name]; ok {
			return v
		}
	}
	return nil
}

// heldWalker resolves the identifiers of one function body to its local
// variables, the way the compiler's scopes do, without type information: a
// name declared in an inner scope shadows the outer one, and the right side
// of a short declaration is read before its names exist. loops are the loops
// and function literals around the node being walked, outermost first.
type heldWalker struct {
	u     *upgrader
	f     *source
	scope *heldScope
	loops []ast.Node
	// vars is every variable the function declares, shared by the walkers of
	// its nested scopes.
	vars *[]*heldVar
}

// nested is a walker for a scope inside w's, inside one more loop when loop is
// not nil.
func (w *heldWalker) nested(loop ast.Node) *heldWalker {
	loops := w.loops
	if loop != nil {
		loops = append(append([]ast.Node(nil), w.loops...), loop)
	}
	return &heldWalker{u: w.u, f: w.f, loops: loops, vars: w.vars,
		scope: &heldScope{parent: w.scope, names: map[string]*heldVar{}}}
}

func (w *heldWalker) declare(id *ast.Ident) *heldVar {
	if id == nil || id.Name == "_" {
		return nil
	}
	v := &heldVar{name: id.Name}
	w.scope.names[id.Name] = v
	*w.vars = append(*w.vars, v)
	return v
}

func (w *heldWalker) declareFields(list *ast.FieldList) {
	if list == nil {
		return
	}
	for _, field := range list.List {
		for _, name := range field.Names {
			w.declare(name)
		}
	}
}

func (w *heldWalker) read(id *ast.Ident, chain bool) {
	if v := w.scope.lookup(id.Name); v != nil {
		v.reads = append(v.reads, heldRead{id: id, chain: chain, loops: w.loops})
	}
}

// bind records value as assigned to v when it is a constructor call.
func (w *heldWalker) bind(v *heldVar, id *ast.Ident, value ast.Expr) {
	if v == nil {
		return
	}
	if call, e := w.u.heldCall(w.f, value); e != nil {
		v.bindings = append(v.bindings, heldBinding{name: id, call: call, entity: e, loops: w.loops})
	}
}

// walk walks each node with w. An absent optional part -- the Init of an if,
// the Else -- is a nil interface and is skipped.
func (w *heldWalker) walk(nodes ...ast.Node) {
	for _, n := range nodes {
		if n != nil {
			ast.Walk(w, n)
		}
	}
}

func exprs(list []ast.Expr) []ast.Node {
	out := make([]ast.Node, 0, len(list))
	for _, e := range list {
		out = append(out, e)
	}
	return out
}

func stmts(list []ast.Stmt) []ast.Node {
	out := make([]ast.Node, 0, len(list))
	for _, s := range list {
		out = append(out, s)
	}
	return out
}

// Visit walks one node. A node that opens a scope, declares a name or holds a
// read whose position matters is walked by hand and answers nil; the rest
// descend with w.
func (w *heldWalker) Visit(n ast.Node) ast.Visitor {
	switch n := n.(type) {
	case nil:
		return nil

	case *ast.Ident:
		w.read(n, false)
		return nil

	case *ast.CallExpr:
		if sel, ok := n.Fun.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok {
				w.read(id, true)
			} else {
				w.walk(sel.X)
			}
			w.walk(exprs(n.Args)...)
			return nil
		}
		return w

	case *ast.SelectorExpr:
		w.walk(n.X)
		return nil

	case *ast.KeyValueExpr:
		// A bare name before the colon is a field of the literal, not a read.
		if _, field := n.Key.(*ast.Ident); !field {
			w.walk(n.Key)
		}
		w.walk(n.Value)
		return nil

	case *ast.FuncLit:
		inner := w.nested(n)
		inner.declareFields(n.Type.Params)
		inner.declareFields(n.Type.Results)
		inner.walk(stmts(n.Body.List)...)
		return nil

	case *ast.Field:
		// A name in a field list is a declaration or a struct field.
		w.walk(n.Type)
		return nil

	case *ast.BlockStmt:
		w.nested(nil).walk(stmts(n.List)...)
		return nil

	case *ast.IfStmt:
		inner := w.nested(nil)
		inner.walk(n.Init, n.Cond, n.Body, n.Else)
		return nil

	case *ast.ForStmt:
		// The init runs once; the condition, the post statement and the body
		// run on every iteration.
		inner := w.nested(nil)
		inner.walk(n.Init)
		inner.nested(n).walk(n.Cond, n.Post, n.Body)
		return nil

	case *ast.RangeStmt:
		// The ranged expression is evaluated once, before the names exist.
		w.walk(n.X)
		loop := w.nested(n)
		if n.Tok == token.DEFINE {
			for _, e := range []ast.Expr{n.Key, n.Value} {
				if id, ok := e.(*ast.Ident); ok {
					loop.declare(id)
				}
			}
		} else {
			loop.walk(n.Key, n.Value)
		}
		loop.walk(n.Body)
		return nil

	case *ast.SwitchStmt:
		inner := w.nested(nil)
		inner.walk(n.Init, n.Tag)
		inner.walk(stmts(n.Body.List)...)
		return nil

	case *ast.TypeSwitchStmt:
		inner := w.nested(nil)
		inner.walk(n.Init)
		var bound *ast.Ident
		switch a := n.Assign.(type) {
		case *ast.AssignStmt:
			if len(a.Lhs) == 1 && len(a.Rhs) == 1 {
				bound, _ = a.Lhs[0].(*ast.Ident)
				inner.walk(a.Rhs[0])
			}
		case *ast.ExprStmt:
			inner.walk(a.X)
		}
		for _, s := range n.Body.List {
			clause := s.(*ast.CaseClause)
			c := inner.nested(nil)
			c.declare(bound)
			c.walk(exprs(clause.List)...)
			c.walk(stmts(clause.Body)...)
		}
		return nil

	case *ast.CaseClause:
		c := w.nested(nil)
		c.walk(exprs(n.List)...)
		c.walk(stmts(n.Body)...)
		return nil

	case *ast.SelectStmt:
		w.walk(n.Body)
		return nil

	case *ast.CommClause:
		c := w.nested(nil)
		c.walk(n.Comm)
		c.walk(stmts(n.Body)...)
		return nil

	case *ast.LabeledStmt:
		w.walk(n.Stmt)
		return nil

	case *ast.BranchStmt:
		return nil

	case *ast.AssignStmt:
		w.walk(exprs(n.Rhs)...)
		paired := len(n.Lhs) == len(n.Rhs)
		for i, lhs := range n.Lhs {
			id, isIdent := lhs.(*ast.Ident)
			if !isIdent {
				w.walk(lhs)
				continue
			}
			var v *heldVar
			switch n.Tok {
			case token.DEFINE:
				if existing, ok := w.scope.names[id.Name]; ok {
					v = existing
				} else {
					v = w.declare(id)
				}
			case token.ASSIGN:
				v = w.scope.lookup(id.Name)
			default:
				w.read(id, false)
				continue
			}
			if paired {
				w.bind(v, id, n.Rhs[i])
			}
		}
		return nil

	case *ast.DeclStmt:
		gd, ok := n.Decl.(*ast.GenDecl)
		if !ok {
			return nil
		}
		for _, sp := range gd.Specs {
			switch s := sp.(type) {
			case *ast.ValueSpec:
				w.walk(exprs(s.Values)...)
				for i, name := range s.Names {
					v := w.declare(name)
					if gd.Tok == token.VAR && len(s.Values) == len(s.Names) {
						w.bind(v, name, s.Values[i])
					}
				}
			case *ast.TypeSpec:
				w.declare(s.Name)
			}
		}
		return nil
	}
	return w
}
