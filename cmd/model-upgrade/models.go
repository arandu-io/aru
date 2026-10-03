package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"sort"
	"strings"

	"github.com/arandu-io/aru/internal/gen"
)

// models is the first pass: every constructor returning *model.Model[X]
// becomes the table of X, and X embeds the non-generic model.Model.
func (u *upgrader) models() {
	byDir := map[string][]*source{}
	for _, f := range u.files {
		byDir[f.dir] = append(byDir[f.dir], f)
	}
	dirs := make([]string, 0, len(byDir))
	for dir := range byDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	for _, dir := range dirs {
		files := byDir[dir]
		declared := map[string]bool{}
		for _, f := range files {
			for _, decl := range f.ast.Decls {
				for _, name := range topLevelNames(decl) {
					declared[name] = true
				}
			}
		}

		for _, f := range files {
			core := f.importName(modelPath)
			if core == "" {
				continue
			}
			for _, decl := range f.ast.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || fn.Body == nil {
					continue
				}
				name, generic := genericModelResult(fn, core)
				if !generic {
					continue
				}
				if name == "" {
					u.problemAt(f, f.line(fn), fn.Name.Name+" returns a generic model of a type declared in another package: "+
						"move the constructor beside the entity, then run model-upgrade again")
					continue
				}
				u.constructor(f, fn, core, name, declared)
			}
		}

		for _, f := range files {
			u.embed(f)
		}
	}
}

// genericModelResult reports whether fn returns *model.Model[X], and answers X
// when it is a type of fn's own package.
func genericModelResult(fn *ast.FuncDecl, core string) (string, bool) {
	if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
		return "", false
	}
	star, ok := fn.Type.Results.List[0].Type.(*ast.StarExpr)
	if !ok {
		return "", false
	}
	idx, ok := star.X.(*ast.IndexExpr)
	if !ok {
		return "", false
	}
	if name, ok := selectorOf(idx.X, core); !ok || name != "Model" {
		return "", false
	}
	if id, ok := idx.Index.(*ast.Ident); ok {
		return id.Name, true
	}
	return "", true
}

// spec is a constructor read back: what each setting ends up as, in the order
// the constructor applied them.
type spec struct {
	table        string
	uniqueIDs    bool
	incrementing bool
	keyType      string
	primaryKey   string
	timestamps   bool
	updatedAt    string
	updatedSet   bool
	tenantColumn string
	tenantSet    bool
	softDeletes  bool
	perPage      string
	events       []string
}

// constructor reads one constructor and replaces it with the table.
func (u *upgrader) constructor(f *source, fn *ast.FuncDecl, core, name string, declared map[string]bool) {
	s := spec{incrementing: true, keyType: "int", timestamps: true}
	fail := func(n ast.Node, message string) {
		u.problemAt(f, f.line(n), message)
		u.failed[f.pkgPath+"."+name] = true
	}

	stmts := fn.Body.List
	if len(stmts) == 0 {
		fail(fn, fn.Name.Name+" has an empty body, and a constructor of the generic model is one model.NewModel")
		return
	}

	held := ""
	switch first := stmts[0].(type) {
	case *ast.ReturnStmt:
		if len(stmts) != 1 || len(first.Results) != 1 {
			fail(first, "the constructor returns more than one model.NewModel")
			return
		}
		if !u.readNewModel(f, first.Results[0], core, name, &s, fail) {
			return
		}
	case *ast.AssignStmt:
		if first.Tok != token.DEFINE || len(first.Lhs) != 1 || len(first.Rhs) != 1 {
			fail(first, "the constructor does not start with one v := model.NewModel[...](...)")
			return
		}
		id, ok := first.Lhs[0].(*ast.Ident)
		if !ok {
			fail(first, "the constructor does not start with one v := model.NewModel[...](...)")
			return
		}
		held = id.Name
		if !u.readNewModel(f, first.Rhs[0], core, name, &s, fail) {
			return
		}
		if !u.readSettings(f, stmts[1:], held, core, name, &s, fail) {
			return
		}
	default:
		fail(first, "the constructor of "+name+" does not start with model.NewModel, so model-upgrade cannot read its table")
		return
	}

	tableVar := lowerFirst(name) + "Table"
	if declared[tableVar] {
		fail(fn, "the table of "+name+" goes in "+tableVar+", and the package already declares that name")
		return
	}
	if s.uniqueIDs && s.incrementing {
		fail(fn, name+" uses unique ids and sets Incrementing back to true, and a key cannot be both")
		return
	}

	custom := strings.Contains(string(f.src), "arandu:begin custom")
	var b strings.Builder
	fmt.Fprintf(&b, "// %s is the table of %s.\n// Its query, %s, is generated beside it by aru model:build.\n",
		tableVar, name, gen.Constructor(name))
	if fn.Doc != nil {
		b.WriteString("//\n")
		for _, c := range fn.Doc.List {
			b.WriteString(c.Text)
			b.WriteString("\n")
		}
	}
	fmt.Fprintf(&b, "var %s = %s.NewTable(%s.TableSpec{\n", tableVar, core, core)
	fmt.Fprintf(&b, "\tName: %s,\n", s.table)
	fmt.Fprintf(&b, "\tNew: func() %s.Entity { return new(%s) },\n", core, name)
	if s.primaryKey != "" {
		fmt.Fprintf(&b, "\tPrimaryKey: %q,\n", s.primaryKey)
	}
	switch {
	case s.uniqueIDs:
		if s.keyType != "string" {
			fmt.Fprintf(&b, "\tKeyType: %q,\n", s.keyType)
		}
		b.WriteString("\tUniqueIDs: true,\n")
	case !s.incrementing:
		if s.keyType != "string" {
			fmt.Fprintf(&b, "\tKeyType: %q,\n", s.keyType)
		}
		b.WriteString("\tManualKey: true,\n")
	case s.keyType != "int":
		fmt.Fprintf(&b, "\tKeyType: %q,\n", s.keyType)
	}
	if !s.timestamps {
		b.WriteString("\tNoTimestamps: true,\n")
	}
	if s.updatedSet && s.updatedAt != "updated_at" {
		if s.updatedAt == "" {
			fmt.Fprintf(&b, "\tUpdatedAtColumn: %s.NoColumn,\n", core)
		} else {
			fmt.Fprintf(&b, "\tUpdatedAtColumn: %q,\n", s.updatedAt)
		}
	}
	if s.softDeletes {
		b.WriteString("\tSoftDeletes: true,\n")
	}
	if s.tenantSet && s.tenantColumn != "tenant_id" {
		if s.tenantColumn == "" {
			b.WriteString("\tGlobal: true,\n")
		} else {
			fmt.Fprintf(&b, "\tTenantColumn: %q,\n", s.tenantColumn)
		}
	}
	if s.perPage != "" {
		fmt.Fprintf(&b, "\tPerPage: %s,\n", s.perPage)
	}
	if len(s.events) > 0 {
		fmt.Fprintf(&b, "\tEvents: map[%s.Event][]func(%s.Entity) error{\n", core, core)
		for _, e := range s.events {
			b.WriteString(e)
		}
		b.WriteString("\t},\n")
	}
	if custom {
		b.WriteString("\t// arandu:begin custom\n\t// arandu:end custom\n")
	}
	b.WriteString("})")

	from := fn.Pos()
	if fn.Doc != nil {
		from = fn.Doc.Pos()
	}
	f.replaceRange(from, fn.End(), b.String())

	e := &entity{name: name, pkgPath: f.pkgPath, oldCtor: fn.Name.Name, newCtor: gen.Constructor(name), tableVar: tableVar}
	if u.entities[f.pkgPath] == nil {
		u.entities[f.pkgPath] = map[string]*entity{}
		u.constructors[f.pkgPath] = map[string]*entity{}
	}
	u.entities[f.pkgPath][name] = e
	u.constructors[f.pkgPath][fn.Name.Name] = e
}

// readNewModel reads model.NewModel[X](table, ...) with any .UseUniqueIDs()
// chained onto it.
func (u *upgrader) readNewModel(f *source, e ast.Expr, core, name string, s *spec, fail func(ast.Node, string)) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		fail(e, "the constructor of "+name+" does not build its model with model.NewModel")
		return false
	}
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "UseUniqueIDs" && len(call.Args) == 0 {
		if !u.readNewModel(f, sel.X, core, name, s, fail) {
			return false
		}
		s.useUniqueIDs()
		return true
	}
	idx, ok := call.Fun.(*ast.IndexExpr)
	if !ok {
		fail(call, "the constructor of "+name+" calls something other than model.NewModel and UseUniqueIDs on its model")
		return false
	}
	if fn, ok := selectorOf(idx.X, core); !ok || fn != "NewModel" {
		fail(call, "the constructor of "+name+" builds its model with something other than model.NewModel")
		return false
	}
	if id, ok := idx.Index.(*ast.Ident); !ok || id.Name != name {
		fail(call, "the constructor returns model.Model["+name+"] and builds model.NewModel["+f.text(idx.Index)+"]")
		return false
	}
	if len(call.Args) != 4 {
		fail(call, "model.NewModel takes the table, the connection, the grammar and the processor, and this call passes something else")
		return false
	}
	s.table = f.text(call.Args[0])
	return true
}

func (s *spec) useUniqueIDs() {
	s.uniqueIDs = true
	s.keyType = "string"
	s.incrementing = false
}

// readSettings reads the statements after v := model.NewModel: assignments to
// the settings it knows, UseUniqueIDs, RegisterModelEvent, and return v.
func (u *upgrader) readSettings(f *source, stmts []ast.Stmt, held, core, name string, s *spec, fail func(ast.Node, string)) bool {
	for i, stmt := range stmts {
		last := i == len(stmts)-1
		switch st := stmt.(type) {
		case *ast.ReturnStmt:
			if !last || len(st.Results) != 1 || !isIdent(st.Results[0], held) {
				fail(st, "the constructor of "+name+" returns something other than "+held)
				return false
			}
			return true
		case *ast.AssignStmt:
			if st.Tok != token.ASSIGN || len(st.Lhs) != 1 || len(st.Rhs) != 1 {
				fail(st, "an assignment model-upgrade does not read: "+firstLine(f.text(st)))
				return false
			}
			sel, ok := st.Lhs[0].(*ast.SelectorExpr)
			if !ok || !isIdent(sel.X, held) {
				fail(st, "an assignment model-upgrade does not read: "+firstLine(f.text(st)))
				return false
			}
			if !s.set(f, sel.Sel.Name, st.Rhs[0], fail) {
				return false
			}
		case *ast.ExprStmt:
			call, ok := st.X.(*ast.CallExpr)
			sel, isSel := ast.Expr(nil), false
			if ok {
				if x, yes := call.Fun.(*ast.SelectorExpr); yes && isIdent(x.X, held) {
					sel, isSel = x, true
				}
			}
			if !isSel {
				fail(st, "a call model-upgrade does not read in the constructor of "+name+": "+firstLine(f.text(st)))
				return false
			}
			switch method := sel.(*ast.SelectorExpr).Sel.Name; {
			case method == "UseUniqueIDs" && len(call.Args) == 0:
				s.useUniqueIDs()
			case method == "RegisterModelEvent" && len(call.Args) == 2:
				event, ok := u.event(f, call.Args[1], core, name, fail)
				if !ok {
					return false
				}
				s.events = append(s.events, "\t\t"+f.text(call.Args[0])+": {"+event+"},\n")
			default:
				fail(st, held+"."+method+" is a setting model-upgrade does not translate into model.TableSpec")
				return false
			}
		default:
			fail(stmt, "a statement model-upgrade does not read in the constructor of "+name+": "+firstLine(f.text(stmt)))
			return false
		}
	}
	fail(stmts[len(stmts)-1], "the constructor of "+name+" does not end by returning "+held)
	return false
}

// set records one assigned setting. A value that is not a literal is refused:
// what it means for the table depends on the value, and a name has no value
// here.
func (s *spec) set(f *source, field string, value ast.Expr, fail func(ast.Node, string)) bool {
	str := func() (string, bool) {
		v, ok := stringValue(value)
		if !ok {
			fail(value, "the "+field+" assigned here is not a string literal, and model-upgrade reads the value to translate it")
		}
		return v, ok
	}
	boolean := func() (bool, bool) {
		v, ok := boolValue(value)
		if !ok {
			fail(value, "the "+field+" assigned here is not true or false, and model-upgrade reads the value to translate it")
		}
		return v, ok
	}
	var ok bool
	switch field {
	case "PrimaryKey":
		var v string
		if v, ok = str(); ok && v != "id" {
			s.primaryKey = v
		}
	case "KeyType":
		s.keyType, ok = str()
	case "Incrementing":
		s.incrementing, ok = boolean()
	case "Timestamps":
		s.timestamps, ok = boolean()
	case "UpdatedAtColumn":
		s.updatedAt, ok = str()
		s.updatedSet = true
	case "TenantColumn":
		s.tenantColumn, ok = str()
		s.tenantSet = true
	case "SoftDeletes":
		s.softDeletes, ok = boolean()
	case "PerPage":
		s.perPage, ok = f.text(value), true
	default:
		fail(value, field+" is a setting model-upgrade does not translate into model.TableSpec")
		return false
	}
	return ok
}

// event rewrites a callback over the generic model into one over the entity:
// func(m *model.Model[X]) error { ... m.Entity.Name ... } becomes
// func(e model.Entity) error { m := e.(*X); ... m.Name ... }.
func (u *upgrader) event(f *source, e ast.Expr, core, name string, fail func(ast.Node, string)) (string, bool) {
	lit, ok := e.(*ast.FuncLit)
	if !ok || lit.Type.Params == nil || len(lit.Type.Params.List) != 1 || len(lit.Type.Params.List[0].Names) > 1 {
		fail(e, "the event callback is not a function literal over the model, so model-upgrade cannot retype it")
		return "", false
	}
	param := lit.Type.Params.List[0]
	star, ok := param.Type.(*ast.StarExpr)
	idx, isIdx := ast.Expr(nil), false
	if ok {
		idx, isIdx = star.X.(*ast.IndexExpr)
	}
	if !isIdx {
		fail(e, "the event callback does not take *model.Model["+name+"]")
		return "", false
	}
	if m, ok := selectorOf(idx.(*ast.IndexExpr).X, core); !ok || m != "Model" {
		fail(e, "the event callback does not take *model.Model["+name+"]")
		return "", false
	}

	body := lit.Body
	inner := string(f.src[f.offset(body.Lbrace)+1 : f.offset(body.Rbrace)])
	if len(param.Names) == 0 || param.Names[0].Name == "_" {
		return fmt.Sprintf("func(%s.Entity) error {%s}", core, inner), true
	}
	held := param.Names[0].Name

	// m.Entity is the row; on the concrete model the row is m.
	var edits []edit
	base := f.offset(body.Lbrace) + 1
	used := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			used[x.Name] = true
		case *ast.SelectorExpr:
			if isIdent(x.X, held) && x.Sel.Name == "Entity" {
				edits = append(edits, edit{start: f.offset(x.X.End()) - base, end: f.offset(x.End()) - base})
			}
		}
		return true
	})
	rewritten, err := applyEdits([]byte(inner), edits)
	if err != nil {
		fail(e, err.Error())
		return "", false
	}
	arg := "e"
	for _, candidate := range []string{"e", "entity", "row", "anyRow"} {
		if !used[candidate] && candidate != held {
			arg = candidate
			break
		}
	}
	return fmt.Sprintf("func(%s %s.Entity) error {\n%s := %s.(*%s)\n%s}", arg, core, held, arg, name,
		strings.TrimLeft(string(rewritten), "\n")), true
}

// embed rewrites the entities of f: model.Model[X] embedded in X becomes
// model.Model, and an entity that embedded nothing gets it as its first field.
func (u *upgrader) embed(f *source) {
	entities := u.entities[f.pkgPath]
	core := f.importName(modelPath)
	for _, decl := range f.ast.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, sp := range gd.Specs {
			ts := sp.(*ast.TypeSpec)
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			e, isEntity := entities[ts.Name.Name]
			if isEntity {
				e.structFile = f.path
			}
			embedded := false
			for _, field := range st.Fields.List {
				if len(field.Names) != 0 {
					continue
				}
				typ := field.Type
				pointer := false
				if star, ok := typ.(*ast.StarExpr); ok {
					typ, pointer = star.X, true
				}
				idx, ok := typ.(*ast.IndexExpr)
				if !ok {
					continue
				}
				if m, ok := selectorOf(idx.X, core); !ok || m != "Model" {
					continue
				}
				arg, _ := idx.Index.(*ast.Ident)
				if arg == nil {
					// The model of a type from another package: the callers
					// pass names it, with what it can become.
					continue
				}
				switch {
				case arg.Name != ts.Name.Name || !isEntity:
					if !u.failed[f.pkgPath+"."+ts.Name.Name] {
						u.problemAt(f, f.line(field), ts.Name.Name+" embeds "+f.text(field.Type)+
							", and model-upgrade found no constructor of it to read: an entity embeds the model of itself, built by one constructor beside it")
					}
				case pointer:
					u.problemAt(f, f.line(field), ts.Name.Name+" embeds a pointer to the generic model; the concrete model is embedded by value")
				default:
					f.replace(idx, core+".Model")
					embedded = true
				}
			}
			if isEntity && !embedded && !u.hasProblemFor(f, ts) {
				if core == "" {
					core = "model"
				}
				f.edits = append(f.edits, edit{
					start: f.offset(st.Fields.Opening) + 1, end: f.offset(st.Fields.Opening) + 1,
					text: "\n\t" + core + ".Model\n",
				})
			}
		}
	}
}

// hasProblemFor reports whether a problem was already recorded on the struct.
func (u *upgrader) hasProblemFor(f *source, ts *ast.TypeSpec) bool {
	from, to := f.line(ts), f.fset.Position(ts.End()).Line
	for _, p := range u.problems {
		if p.file == f.rel && p.line >= from && p.line <= to {
			return true
		}
	}
	return false
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func topLevelNames(decl ast.Decl) []string {
	var out []string
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if d.Recv == nil {
			out = append(out, d.Name.Name)
		}
	case *ast.GenDecl:
		for _, sp := range d.Specs {
			switch s := sp.(type) {
			case *ast.TypeSpec:
				out = append(out, s.Name.Name)
			case *ast.ValueSpec:
				for _, n := range s.Names {
					out = append(out, n.Name)
				}
			}
		}
	}
	return out
}
