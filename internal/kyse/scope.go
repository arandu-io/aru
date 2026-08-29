package kyse

import (
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

// checkScopes reads the Go this generator just wrote and refuses it when a name
// in it can mean something other than what the code that emitted it meant.
//
// # What it is for
//
// A view and the file compiled from it share one Go scope. The generator writes
// names into it -- a writer, an error, the temporaries an escape needs, the
// packages every call goes through -- and the view writes names into it too, in
// the binding of a loop and in the body of an `@go` block. When the two
// collide the file still parses and still formats, so nothing else in this
// package can notice: what changed is which declaration a name reaches, and
// nothing asks that question until something type-checks the result. Until
// then the command writes the file, prints how many views it compiled, and
// exits 0.
//
// The generator keeps them apart by writing every name of its own under a
// prefix no view may use. That is the mechanism, and it was only ever as good
// as the list of names somebody remembered to apply it to. The list was applied
// to the variables and not to the imports, and the second half failed exactly
// like the first: `@foreach(.Steps as view)` renamed the package every escape
// is called through, and the view was reported for `view.TextURL undefined
// (type Step has no field or method TextURL)` against a file whose header says
// DO NOT EDIT. Seven shapes did it, and all seven builds said they had
// succeeded.
//
// So this consults no list. It reads the bytes that are about to be written and
// checks the discipline on them, which is what makes it hold for the names
// nobody has added yet:
//
//   - every import the file declares answers either to a name in the reserved
//     namespace or to one the view itself wrote in its own import block, so a
//     package the generator brought in and forgot to rename is refused here
//     rather than shadowed later;
//   - every local name the generated code introduces is either in the reserved
//     namespace or a word that appears in the view's own source, so a
//     temporary invented without the prefix is refused the same way;
//   - nothing the view writes reaches into the reserved namespace, which
//     `loopBinding` enforces for a loop binding and nothing enforced for an
//     `@go` block;
//   - and every reserved name the code reads is a reserved name something
//     declared.
//
// # What it is not
//
// It is not a type check, and choosing not to be one is the point. Type-
// checking the output means resolving what it imports -- the native runtime,
// component libraries, the project's own packages -- which needs a warm module
// cache, costs seconds per view, and turns any compile error anywhere in the
// project into a failure of `aru view:build`. A build step that is slow, that
// fails on an aeroplane, or that reports somebody else's error is a worse tool
// than the silent one it replaced. This is a parse and a walk of one small
// file: no import is read, nothing is fetched, and it runs on every save.
//
// The cost of drawing the line there is honest and worth stating: a generated
// file can still fail to type-check for a reason that is not a name -- a wrong
// argument count, a value of the wrong type -- and the Go compiler is what
// reports those. What cannot happen any more is the failure that pointed at the
// view's own line and blamed the author for it.
func checkScopes(src []byte, f *File, generated string) error {
	fset := token.NewFileSet()
	parsed, err := goparser.ParseFile(fset, generated, src, goparser.SkipObjectResolution)
	if err != nil {
		// Unreachable in practice -- the caller has already run the output
		// through gofmt, which parses it. Reported rather than ignored, because
		// a check that quietly does nothing is the defect this file closes.
		return fmt.Errorf("kyse: the Go generated from %s does not parse -- this is a bug in the generator: %w", f.Path, err)
	}

	imports, readable := viewImports(f)
	// The names the view did not write in an import block but did call as
	// packages. They are bound in the output for exactly that reason, so they
	// answer here the same way the view's own imports do -- see
	// republishedPackages, and viewPackageRefs for how narrowly the question is
	// asked.
	for _, name := range publishedImports(f) {
		imports[name] = true
	}
	c := &scopeCheck{fset: fset, file: f, own: viewIdents(f), imports: imports, checkImports: readable, seen: map[string]bool{}}
	c.checkNamespace()
	c.push()
	c.walkFile(parsed)
	if len(c.problems) > 0 {
		return fmt.Errorf("%s", strings.Join(c.problems, "\n"))
	}
	return nil
}

type scopeCheck struct {
	fset *token.FileSet
	file *File
	// own are the words the view's own source contains, which is what tells a
	// name the person wrote from a name the generator invented.
	own map[string]bool
	// imports are the names the view owns: the ones its own import block binds,
	// and the ones it calls as packages in its own source.
	imports map[string]bool
	// checkImports is false when the view's own import block could not be read,
	// which is the one state in which the output's imports say nothing: a name
	// there may be the view's and there is no way to tell. Saying nothing is
	// the right answer then -- the block that would not parse is refused by
	// gofmt one step later, and a guess here would refuse a view for the
	// compiler's mistake.
	checkImports bool
	// scopes[0] is the file scope: imports and package-level declarations.
	scopes   []map[string]bool
	problems []string
	seen     map[string]bool
}

func (c *scopeCheck) push() { c.scopes = append(c.scopes, map[string]bool{}) }
func (c *scopeCheck) pop()  { c.scopes = c.scopes[:len(c.scopes)-1] }

// report records one finding, at the position the `//line` directives map it
// to -- the view's own line wherever the generator marked one, so the reader
// is put where they are standing rather than in a file they never opened.
func (c *scopeCheck) report(pos token.Pos, message string) {
	at := c.fset.Position(pos)
	line := fmt.Sprintf("%s:%d: %s", at.Filename, at.Line, message)
	if c.seen[line] {
		return
	}
	c.seen[line] = true
	c.problems = append(c.problems, line)
}

// bug is the wording every finding here carries.
//
// The view is not at fault and must not be told to change: keeping the
// namespace clear is the generator's job, and a message asking the author to
// rename their loop variable would turn a defect into a permanent feature. What
// the author is given is a way to keep working while it is fixed.
const bug = " -- this is a bug in the view compiler, not in the view"

// checkNamespace refuses a view that writes a name in the reserved namespace.
//
// loopBinding already refuses one as a loop binding, which is where a person
// would most plausibly write one. This is the rest of the surface: an `@go`
// block is Go the person wrote, copied in verbatim, and a variable declared
// there under the prefix would shadow the generator's in the whole function.
//
// The view's markup is left out on purpose. Text is not code -- a CSS class
// named for this compiler is somebody's stylesheet, not a declaration -- so
// only the Go the view contributes is read.
func (c *scopeCheck) checkNamespace() {
	var found []string
	for name := range c.own {
		if strings.HasPrefix(name, hygienePrefix) {
			found = append(found, name)
		}
	}
	if len(found) == 0 {
		return
	}
	sort.Strings(found)
	c.problems = append(c.problems, fmt.Sprintf(
		"%s: this view writes %s, and a name beginning with %q belongs to the view compiler\n"+
			"    The generated Go writes its writer, its data, its temporaries and its imports under that prefix.\n"+
			"    Any other name works, including a single letter and the name of a package.",
		c.file.Path, strings.Join(quoteAll(found), ", "), hygienePrefix))
}

// declare records a name and, for a local one, checks who could have written it.
//
// A name is the generator's business unless the view's source contains the
// word. That is a deliberately generous test -- a view that happens to mention
// `item` anywhere buys `item` for its loops -- and generous is the right
// direction: it can only cost a finding, never invent one, and the finding it
// makes is unambiguous. A name nothing in the view mentions was invented by the
// generator, and an invented name outside the reserved namespace is one a view
// can take away tomorrow.
func (c *scopeCheck) declare(id *ast.Ident, local bool) {
	if id == nil || id.Name == "_" {
		return
	}
	c.scopes[len(c.scopes)-1][id.Name] = true
	if !local || strings.HasPrefix(id.Name, hygienePrefix) || id.Name == viewData || c.own[id.Name] {
		return
	}
	c.report(id.Pos(), fmt.Sprintf(
		"the Go generated from this view binds %q, which is neither in the compiler's %q namespace nor a name this view wrote%s\n"+
			"    A view that binds the same name would rename it, and the view would be reported for a type error in code it did not write.",
		id.Name, hygienePrefix, bug))
}

// use checks a reference in the reserved namespace, which nothing but a
// declaration in the same namespace may answer.
func (c *scopeCheck) use(id *ast.Ident) {
	if id == nil || !strings.HasPrefix(id.Name, hygienePrefix) {
		return
	}
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if c.scopes[i][id.Name] {
			return
		}
	}
	c.report(id.Pos(), fmt.Sprintf("the Go generated from this view reads %q, which nothing declares%s", id.Name, bug))
}

// checkImport is the half that was open: a package name is an ordinary
// identifier in the file that imports it, so an import the generator brought in
// under its own name is a name a loop binding can take.
//
// The test is not a list of packages. It is where the name came from: either
// the reserved namespace, or the view itself -- its own import block, or a
// package its own source calls by that name. Anything else is an import this
// generator added and did not rename, whichever package it is and whenever it
// was added.
//
// The second half of "the view itself" is what lets one path be imported twice:
// the generator calls it under the reserved name, the view calls it under the
// plain one, and the plain one is bound only where the view asked for it. What
// the rule keeps is its shape -- every name in the output belongs to one side or
// the other, and neither can take the other's.
func (c *scopeCheck) checkImport(spec *ast.ImportSpec) {
	path, err := strconv.Unquote(spec.Path.Value)
	if err != nil {
		path = spec.Path.Value
	}
	name := importLocalName(spec.Name, path)
	c.scopes[0][name] = true
	// Only a name a view could bind is a name a view could take. Anything else
	// is not reachable by the failure this guards against, and asking about it
	// only produces noise: a raw-quoted path that spans lines is one string in
	// the view and another in the output, because gofmt indents the block it
	// lands in, and the two spellings compared unequal.
	if !c.checkImports || !validGoIdent(name) || strings.HasPrefix(name, hygienePrefix) || c.imports[name] {
		return
	}
	c.report(spec.Pos(), fmt.Sprintf(
		"the Go generated from this view imports %s under the name %q, which is outside the compiler's %q namespace%s\n"+
			"    A view that binds %q in an @foreach or @forelse renames the package, and every call the generator makes through it is reported against the view.",
		spec.Path.Value, name, hygienePrefix, bug, name))
}

func (c *scopeCheck) walkFile(f *ast.File) {
	// The file scope first and whole: Go resolves a package-level name from
	// anywhere in the file, so a body walked before the declaration below it
	// would report a name that is perfectly in scope.
	for _, imp := range f.Imports {
		c.checkImport(imp)
	}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				c.declare(d.Name, false)
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.ValueSpec:
					for _, name := range s.Names {
						c.declare(name, false)
					}
				case *ast.TypeSpec:
					c.declare(s.Name, false)
				}
			}
		}
	}

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			c.push()
			c.walkFieldList(d.Recv, true)
			c.walkFieldList(d.Type.TypeParams, true)
			c.walkFieldList(d.Type.Params, true)
			c.walkFieldList(d.Type.Results, true)
			c.walkStmtsInPlace(d.Body)
			c.pop()
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				c.walkSpecBody(spec)
			}
		}
	}
}

// walkSpecBody walks what a declaration refers to without declaring its names:
// at file scope they are declared already, and inside a body walkStmt declares
// them in order.
func (c *scopeCheck) walkSpecBody(spec ast.Spec) {
	switch s := spec.(type) {
	case *ast.ValueSpec:
		c.walkExpr(s.Type)
		for _, v := range s.Values {
			c.walkExpr(v)
		}
	case *ast.TypeSpec:
		c.push()
		c.walkFieldList(s.TypeParams, true)
		c.walkExpr(s.Type)
		c.pop()
	}
}

// walkFieldList walks the types of a list, and declares the names when they are
// parameters rather than fields.
//
// A struct field and a method name are declarations in no scope this walk can
// see: they are reached through a value, which is the selector walkExpr skips.
func (c *scopeCheck) walkFieldList(list *ast.FieldList, declareNames bool) {
	if list == nil {
		return
	}
	for _, field := range list.List {
		c.walkExpr(field.Type)
		if !declareNames {
			continue
		}
		for _, name := range field.Names {
			c.declare(name, true)
		}
	}
}

// walkStmtsInPlace walks a block in the scope already open, for a function body
// whose parameters share that scope.
func (c *scopeCheck) walkStmtsInPlace(block *ast.BlockStmt) {
	if block == nil {
		return
	}
	for _, stmt := range block.List {
		c.walkStmt(stmt)
	}
}

func (c *scopeCheck) walkBlock(block *ast.BlockStmt) {
	if block == nil {
		return
	}
	c.push()
	c.walkStmtsInPlace(block)
	c.pop()
}

func (c *scopeCheck) walkStmt(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case nil:
		return

	case *ast.DeclStmt:
		decl, ok := s.Decl.(*ast.GenDecl)
		if !ok {
			return
		}
		for _, spec := range decl.Specs {
			// The body first and the names after, because `var x = x` reads the
			// x above it.
			c.walkSpecBody(spec)
			switch v := spec.(type) {
			case *ast.ValueSpec:
				for _, name := range v.Names {
					c.declare(name, true)
				}
			case *ast.TypeSpec:
				c.declare(v.Name, true)
			}
		}

	case *ast.AssignStmt:
		for _, rhs := range s.Rhs {
			c.walkExpr(rhs)
		}
		if s.Tok == token.DEFINE {
			for _, lhs := range s.Lhs {
				if id, ok := lhs.(*ast.Ident); ok {
					c.declare(id, true)
					continue
				}
				c.walkExpr(lhs)
			}
			return
		}
		for _, lhs := range s.Lhs {
			c.walkExpr(lhs)
		}

	case *ast.ExprStmt:
		c.walkExpr(s.X)
	case *ast.SendStmt:
		c.walkExpr(s.Chan)
		c.walkExpr(s.Value)
	case *ast.IncDecStmt:
		c.walkExpr(s.X)
	case *ast.GoStmt:
		c.walkExpr(s.Call)
	case *ast.DeferStmt:
		c.walkExpr(s.Call)
	case *ast.ReturnStmt:
		for _, r := range s.Results {
			c.walkExpr(r)
		}
	case *ast.LabeledStmt:
		// A label is a name in no value scope.
		c.walkStmt(s.Stmt)
	case *ast.BlockStmt:
		c.walkBlock(s)

	case *ast.IfStmt:
		c.push()
		c.walkStmt(s.Init)
		c.walkExpr(s.Cond)
		c.walkBlock(s.Body)
		c.walkStmt(s.Else)
		c.pop()

	case *ast.ForStmt:
		c.push()
		c.walkStmt(s.Init)
		c.walkExpr(s.Cond)
		c.walkStmt(s.Post)
		c.walkBlock(s.Body)
		c.pop()

	case *ast.RangeStmt:
		c.push()
		// The subject is evaluated before the binding exists, which is what
		// lets @foreach(.Steps as steps) mean the field and then the element.
		c.walkExpr(s.X)
		if s.Tok == token.DEFINE {
			if id, ok := s.Key.(*ast.Ident); ok {
				c.declare(id, true)
			}
			if id, ok := s.Value.(*ast.Ident); ok {
				c.declare(id, true)
			}
		} else {
			c.walkExpr(s.Key)
			c.walkExpr(s.Value)
		}
		c.walkBlock(s.Body)
		c.pop()

	case *ast.SwitchStmt:
		c.push()
		c.walkStmt(s.Init)
		c.walkExpr(s.Tag)
		c.walkBlock(s.Body)
		c.pop()

	case *ast.TypeSwitchStmt:
		c.push()
		c.walkStmt(s.Init)
		c.walkStmt(s.Assign)
		c.walkBlock(s.Body)
		c.pop()

	case *ast.SelectStmt:
		c.walkBlock(s.Body)

	case *ast.CaseClause:
		c.push()
		for _, e := range s.List {
			c.walkExpr(e)
		}
		for _, st := range s.Body {
			c.walkStmt(st)
		}
		c.pop()

	case *ast.CommClause:
		c.push()
		c.walkStmt(s.Comm)
		for _, st := range s.Body {
			c.walkStmt(st)
		}
		c.pop()
	}
}

func (c *scopeCheck) walkExpr(expr ast.Expr) {
	switch e := expr.(type) {
	case nil:
		return

	case *ast.Ident:
		c.use(e)

	case *ast.SelectorExpr:
		// Only the left side is a name in scope. The selector is a field or a
		// method, reached through the value.
		c.walkExpr(e.X)

	case *ast.CallExpr:
		c.walkExpr(e.Fun)
		for _, a := range e.Args {
			c.walkExpr(a)
		}

	case *ast.FuncLit:
		c.push()
		c.walkFieldList(e.Type.Params, true)
		c.walkFieldList(e.Type.Results, true)
		c.walkStmtsInPlace(e.Body)
		c.pop()

	case *ast.CompositeLit:
		c.walkExpr(e.Type)
		for _, elt := range e.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				c.walkExpr(elt)
				continue
			}
			// A bare name left of the colon is a field name in every literal
			// this compiler emits or copies, and a field name is not a
			// reference. Skipping it can only cost a finding.
			if _, bare := kv.Key.(*ast.Ident); !bare {
				c.walkExpr(kv.Key)
			}
			c.walkExpr(kv.Value)
		}

	case *ast.ParenExpr:
		c.walkExpr(e.X)
	case *ast.StarExpr:
		c.walkExpr(e.X)
	case *ast.UnaryExpr:
		c.walkExpr(e.X)
	case *ast.BinaryExpr:
		c.walkExpr(e.X)
		c.walkExpr(e.Y)
	case *ast.KeyValueExpr:
		c.walkExpr(e.Key)
		c.walkExpr(e.Value)
	case *ast.IndexExpr:
		c.walkExpr(e.X)
		c.walkExpr(e.Index)
	case *ast.IndexListExpr:
		c.walkExpr(e.X)
		for _, i := range e.Indices {
			c.walkExpr(i)
		}
	case *ast.SliceExpr:
		c.walkExpr(e.X)
		c.walkExpr(e.Low)
		c.walkExpr(e.High)
		c.walkExpr(e.Max)
	case *ast.TypeAssertExpr:
		c.walkExpr(e.X)
		c.walkExpr(e.Type)
	case *ast.Ellipsis:
		c.walkExpr(e.Elt)
	case *ast.ArrayType:
		c.walkExpr(e.Len)
		c.walkExpr(e.Elt)
	case *ast.MapType:
		c.walkExpr(e.Key)
		c.walkExpr(e.Value)
	case *ast.ChanType:
		c.walkExpr(e.Value)
	case *ast.StructType:
		c.walkFieldList(e.Fields, false)
	case *ast.InterfaceType:
		c.walkFieldList(e.Methods, false)
	case *ast.FuncType:
		// A function type written as a type -- `func(io.Writer) error` in the
		// map a layout is handed -- names nothing.
		c.walkFieldList(e.TypeParams, false)
		c.walkFieldList(e.Params, false)
		c.walkFieldList(e.Results, false)
	}
}

// viewIdents are the words the view's own Go contains: its `@go` blocks, its
// import lines, and the arguments of every directive -- which is where a loop
// binding and the three clauses of an `@for` are written.
//
// Markup is left out. A word in a paragraph is not a declaration, and reading
// prose as one would hand the view every name in the language.
func viewIdents(f *File) map[string]bool {
	out := map[string]bool{}
	for _, block := range f.Go {
		addIdents(out, block.Body)
	}
	for _, line := range f.Imports {
		addIdents(out, line)
	}
	var walk func([]Node)
	walk = func(nodes []Node) {
		for _, n := range nodes {
			if n.Kind != Text {
				addIdents(out, n.Body)
			}
			walk(n.Children)
		}
	}
	walk(f.Body)
	for _, s := range f.Sections {
		walk(s.Nodes)
	}
	return out
}

// addIdents collects every identifier-shaped run in the text.
//
// It is a scan and not a parse, on purpose: what it is asked is whether the
// person could have written this word, and a word inside a string literal or a
// comment is still a word they wrote. Over-collecting costs a finding; parsing
// a fragment that is not a whole Go file would cost the answer.
func addIdents(out map[string]bool, text string) {
	start := -1
	for i := 0; i <= len(text); i++ {
		part := i < len(text) && (isIdentByte(text[i]) || (start >= 0 && isDigit(text[i])))
		switch {
		case part && start < 0:
			start = i
		case !part && start >= 0:
			out[text[start:i]] = true
			start = -1
		}
	}
}

func isIdentByte(c byte) bool { return isASCIILetter(c) || c == '_' }

// viewImports are the names the view's own import block binds, which are the
// only names outside the reserved namespace an import in the output may use.
//
// The lines are read by the Go parser rather than scanned for quotes, because a
// path may be written either way a Go string may be written. Scanning for a
// double quote missed a raw-quoted one entirely and then reported the view's
// own import as a package the generator had smuggled in -- a check refusing the
// thing it was written to defend.
//
// The second result says whether they could be read at all. A block that does
// not parse is nobody's answer to give here.
func viewImports(f *File) (map[string]bool, bool) {
	out := map[string]bool{}
	if len(f.Imports) == 0 {
		return out, true
	}
	src := "package p\nimport (\n" + strings.Join(f.Imports, "\n") + "\n)\n"
	parsed, err := goparser.ParseFile(token.NewFileSet(), "", src, goparser.SkipObjectResolution)
	if err != nil {
		return out, false
	}
	for _, spec := range parsed.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return out, false
		}
		out[importLocalName(spec.Name, path)] = true
	}
	return out, true
}

// importLocalName is the name a file calls an import by.
//
// Without an alias it is the last element of the path, which is right for every
// import either side of this compiler writes and wrong only for a package whose
// clause disagrees with its directory. Being wrong there costs a finding this
// check would otherwise make, never one it makes wrongly.
func importLocalName(alias *ast.Ident, path string) string {
	if alias != nil {
		return alias.Name
	}
	if at := strings.LastIndex(path, "/"); at >= 0 {
		return path[at+1:]
	}
	return path
}

func quoteAll(names []string) []string {
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = fmt.Sprintf("%q", name)
	}
	return out
}
