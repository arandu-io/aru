package doctor

import (
	"go/ast"
	"go/token"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// requestsDir is where a request type lives.
const requestsDir = "app/Http/Requests/"

// requestFile reports whether the file declares requests: a file of
// app/Http/Requests, tests excluded.
func requestFile(f *file) bool {
	return !f.isTest && strings.HasPrefix(f.rel, requestsDir)
}

// handRule is a rule a request's Validate wrote by hand: the field it checks,
// the rule that already says the same, what the hand-written check was, and where.
type handRule struct {
	field string
	rule  string
	what  string
	node  ast.Node
}

// A request declares its field rules in Rules, and does not write them by
// hand in Validate.
//
// Reason. Rules is the first step of validation and ctx.Bind runs it over the
// raw input: the sentence comes out in the language of the request, with the
// field named for a person, and the same declaration is what the API schema
// reads. A Validate that checks emptiness, length, format, number or membership
// with errs.Add writes the same restriction a second time, in English and only
// once the service is reached, and the two drift apart. Validate is for what
// spans fields or asks the data.
//
// Scope. Every type declared in app/Http/Requests, tests excluded, that has a
// Validate method and no Rules method. The check reads the whole package of
// requests: a Rules declared in another file of it is a Rules.
//
// Severity. A warning until 2026-12-15, an error after it.
//
// Positive: an Add call guarded by an empty-string or length comparison (`== ""`,
// `len(x) == 0`, `len(x) > 255`), by a regular expression that spells an email,
// a uuid or a url, by a rejected membership (`!slices.Contains`) or by a loop that
// compares the field with each case; a `_, err := strconv.Atoi(x)` that only
// asks whether the text is a number; a call of validation.Required, NotZero,
// MinLen, MaxLen or Email. One finding per field and rule.
//
// Negative: a comparison of one field with another, a date that must come after
// another, a lookup in the database, a check of the policy, a parse whose value
// is used, a pattern the doctor cannot name.
//
// Known false positive: a length compared with a literal for a reason of the
// domain, a code that must have exactly eight characters, is read as max.
//
// Limit. Local to the Validate method: a helper it calls is not followed, and a
// failure added without an Add call, by building the Errors map, is not seen.
//
// Correction: declare the field in Rules, `"email": "required|email|max:255"`,
// and keep in Validate only what spans fields.
func requestValidatesByHand(p *project) []Finding {
	if blindToRequests(p) {
		return nil
	}

	declaresRules := map[string]bool{}
	patterns := map[string]string{}
	for _, f := range p.files {
		if !requestFile(f) {
			continue
		}
		f.functions(func(fn *ast.FuncDecl) {
			if fn.Name.Name == "Rules" {
				declaresRules[receiverType(fn)] = true
			}
		})
		for name, pattern := range f.packagePatterns() {
			patterns[name] = pattern
		}
	}

	var out []Finding
	for _, f := range p.files {
		if !requestFile(f) {
			continue
		}
		tags := f.formTags()
		f.functions(func(fn *ast.FuncDecl) {
			recv := receiverType(fn)
			if fn.Name.Name != "Validate" || fn.Body == nil || recv == "" || declaresRules[recv] {
				return
			}
			if fn.Type.Params != nil && fn.Type.Params.NumFields() != 0 {
				return
			}
			seen := map[string]bool{}
			for _, found := range f.handWrittenRules(fn, patterns, tags[recv]) {
				key := found.field + "|" + found.rule
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, Finding{
					Rule: "request-validates-by-hand", Severity: Warning,
					File: f.rel, Line: f.line(found.node),
					Message: recv + ".Validate checks \"" + found.field + "\" by hand (" + found.what +
						"); the rule is `" + found.rule + "`, declared in " + recv + ".Rules()",
					Why: "Rules is the first step of validation: ctx.Bind runs it over the raw input, says the failure in the " +
						"language of the request, and the same declaration is what the API schema reads. The same " +
						"restriction written in Validate comes out in English, only once the service is reached, and drifts " +
						"from the Rules and the schema. Declare the field in Rules() and keep Validate for what spans " +
						"fields or asks the data." + promotion,
				})
			}
		})
	}
	return out
}

// blindToRequests reports whether a file of app/Http/Requests did not parse. A
// Rules declared in it would be missed, and the rule concludes from a Rules that
// is absent.
func blindToRequests(p *project) bool {
	for _, u := range p.unreadable {
		if strings.HasPrefix(u.rel, requestsDir) {
			return true
		}
	}
	return false
}

// packagePatterns answers the regular expressions the file holds in
// package-level variables, by the name of the variable.
func (f *file) packagePatterns() map[string]string {
	out := map[string]string{}
	for _, decl := range f.ast.Decls {
		general, ok := decl.(*ast.GenDecl)
		if !ok || general.Tok != token.VAR {
			continue
		}
		for _, spec := range general.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range value.Names {
				if i >= len(value.Values) {
					break
				}
				if pattern, ok := f.compiledPattern(value.Values[i]); ok {
					out[name.Name] = pattern
				}
			}
		}
	}
	return out
}

// compiledPattern answers the literal of regexp.MustCompile("...") and
// regexp.Compile("...").
func (f *file) compiledPattern(e ast.Expr) (string, bool) {
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return "", false
	}
	if _, name, ok := f.importedName(call.Fun, isPath("regexp")); !ok || (name != "MustCompile" && name != "Compile") {
		return "", false
	}
	return stringLiteral(call.Args[0])
}

// formTags answers, per request type, the form name of each field: the field
// Reference of StoreInvoice reads `form:"reference"`.
func (f *file) formTags() map[string]map[string]string {
	out := map[string]map[string]string{}
	f.types(func(ts *ast.TypeSpec) {
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return
		}
		names := map[string]string{}
		for _, field := range st.Fields.List {
			if field.Tag == nil {
				continue
			}
			tag, err := strconv.Unquote(field.Tag.Value)
			if err != nil {
				continue
			}
			value := formTagOf(tag)
			if value == "" {
				continue
			}
			for _, name := range field.Names {
				names[name.Name] = value
			}
		}
		out[ts.Name.Name] = names
	})
	return out
}

var formTag = regexp.MustCompile(`(?:^|\s)(?:form|json):"([^",]+)`)

// formTagOf answers the input name a struct tag declares.
func formTagOf(tag string) string {
	if m := formTag.FindStringSubmatch(tag); m != nil {
		return m[1]
	}
	return ""
}

// handWrittenRules reads the Validate method for the checks a rule already makes.
func (f *file) handWrittenRules(fn *ast.FuncDecl, patterns map[string]string, tags map[string]string) []handRule {
	var out []handRule
	fieldOf := func(e ast.Expr) string { return requestFieldName(e, tags) }

	var stack []ast.Node
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		stack = append(stack, n)

		switch node := n.(type) {
		case *ast.CallExpr:
			if found, ok := f.helperRule(node); ok {
				out = append(out, found)
				return true
			}
			field, isAdd := addedField(node)
			if !isAdd {
				return true
			}
			for i := len(stack) - 2; i >= 0; i-- {
				enclosing, ok := stack[i].(*ast.IfStmt)
				if !ok || !within(enclosing.Body, node) {
					continue
				}
				for _, check := range f.classify(enclosing.Cond, patterns) {
					name := field
					if name == "" {
						name = fieldOf(check.subject)
					}
					out = append(out, handRule{field: name, rule: check.rule, what: check.what, node: node})
				}
			}
		case *ast.AssignStmt:
			if found, ok := f.parseOnly(node, fieldOf); ok {
				out = append(out, found)
			}
		case *ast.RangeStmt:
			if found, ok := membershipLoop(node, fieldOf); ok {
				out = append(out, found)
			}
		}
		return true
	})
	return out
}

// within reports whether the node sits inside the block.
func within(block *ast.BlockStmt, n ast.Node) bool {
	return block != nil && n.Pos() >= block.Pos() && n.End() <= block.End()
}

// addedField answers the field an Errors.Add call names, when it is one: a
// method called Add whose first argument is a string literal and whose second
// is the message.
func addedField(call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Add" || len(call.Args) != 2 {
		return "", false
	}
	field, ok := stringLiteral(call.Args[0])
	return field, ok
}

// helperRule reads a call of the validation package's own helpers, which are the
// hand-written form of a rule.
func (f *file) helperRule(call *ast.CallExpr) (handRule, bool) {
	_, name, ok := f.importedName(call.Fun, func(path string) bool {
		return path == frameworkValidation || path == hesapeValidation
	})
	if !ok || len(call.Args) < 3 {
		return handRule{}, false
	}
	field, literal := stringLiteral(call.Args[1])
	if !literal {
		return handRule{}, false
	}
	switch name {
	case "Required", "NotZero":
		return handRule{field: field, rule: "required", what: "validation." + name, node: call}, true
	case "Email":
		return handRule{field: field, rule: "email", what: "validation.Email", node: call}, true
	case "MaxLen", "MinLen":
		if len(call.Args) != 4 {
			return handRule{}, false
		}
		n, ok := intLiteral(call.Args[3])
		if !ok {
			return handRule{}, false
		}
		word := "max"
		if name == "MinLen" {
			word = "min"
		}
		return handRule{field: field, rule: word + ":" + strconv.Itoa(n), what: "validation." + name, node: call}, true
	}
	return handRule{}, false
}

// check is one test a condition makes on a value, and the rule that makes it.
type check struct {
	rule    string
	what    string
	subject ast.Expr
}

// classify reads a condition for the tests a rule already makes. Both sides of
// && and || are read, so a guard around the check does not hide it.
func (f *file) classify(cond ast.Expr, patterns map[string]string) []check {
	switch c := cond.(type) {
	case *ast.ParenExpr:
		return f.classify(c.X, patterns)
	case *ast.BinaryExpr:
		if c.Op == token.LAND || c.Op == token.LOR {
			return append(f.classify(c.X, patterns), f.classify(c.Y, patterns)...)
		}
		return comparison(c)
	case *ast.UnaryExpr:
		if c.Op != token.NOT {
			return nil
		}
		call, ok := c.X.(*ast.CallExpr)
		if !ok {
			return nil
		}
		if _, name, ok := f.importedName(call.Fun, isPath("slices")); ok && name == "Contains" && len(call.Args) == 2 {
			return []check{{rule: "in", what: "a list the value must be one of", subject: call.Args[1]}}
		}
		if kind, subject, ok := f.matches(call, patterns); ok {
			return []check{{rule: kind, what: "a regular expression for " + kind, subject: subject}}
		}
	}
	return nil
}

// comparison reads `x == ""`, `len(x) == 0`, `len(x) > 255` and their relatives.
func comparison(c *ast.BinaryExpr) []check {
	if s, ok := stringLiteral(c.Y); ok && s == "" && c.Op == token.EQL {
		return []check{{rule: "required", what: "empty text", subject: unwrapped(c.X)}}
	}
	if s, ok := stringLiteral(c.X); ok && s == "" && c.Op == token.EQL {
		return []check{{rule: "required", what: "empty text", subject: unwrapped(c.Y)}}
	}
	subject, ok := lengthOf(c.X)
	if !ok {
		return nil
	}
	n, ok := intLiteral(c.Y)
	if !ok {
		return nil
	}
	switch {
	case (c.Op == token.EQL && n == 0) || (c.Op == token.LSS && n == 1) || (c.Op == token.LEQ && n == 0):
		return []check{{rule: "required", what: "no length", subject: subject}}
	case c.Op == token.GTR:
		return []check{{rule: "max:" + strconv.Itoa(n), what: "a length over " + strconv.Itoa(n), subject: subject}}
	case c.Op == token.GEQ:
		return []check{{rule: "max:" + strconv.Itoa(n-1), what: "a length of " + strconv.Itoa(n) + " or more", subject: subject}}
	case c.Op == token.LSS && n > 1:
		return []check{{rule: "min:" + strconv.Itoa(n), what: "a length under " + strconv.Itoa(n), subject: subject}}
	case c.Op == token.LEQ && n > 0:
		return []check{{rule: "min:" + strconv.Itoa(n+1), what: "a length of " + strconv.Itoa(n) + " or less", subject: subject}}
	}
	return nil
}

// unwrapped answers the value a text comparison reads through strings.TrimSpace.
func unwrapped(e ast.Expr) ast.Expr {
	if call, ok := e.(*ast.CallExpr); ok && len(call.Args) == 1 {
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "TrimSpace" {
			return call.Args[0]
		}
	}
	return e
}

// lengthOf answers the value whose length an expression reads: len(x),
// len(strings.TrimSpace(x)), utf8.RuneCountInString(x), len([]rune(x)).
func lengthOf(e ast.Expr) (ast.Expr, bool) {
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return nil, false
	}
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		if fun.Name != "len" {
			return nil, false
		}
		arg := call.Args[0]
		if conv, ok := arg.(*ast.CallExpr); ok && len(conv.Args) == 1 {
			if array, ok := conv.Fun.(*ast.ArrayType); ok && array.Len == nil {
				return unwrapped(conv.Args[0]), true
			}
		}
		return unwrapped(arg), true
	case *ast.SelectorExpr:
		if fun.Sel.Name == "RuneCountInString" {
			return unwrapped(call.Args[0]), true
		}
	}
	return nil, false
}

// matches reads `re.MatchString(x)` and `regexp.MustCompile("...").MatchString(x)`
// for the kind of value the expression spells: email, uuid or url.
func (f *file) matches(call *ast.CallExpr, patterns map[string]string) (kind string, subject ast.Expr, ok bool) {
	sel, isSel := call.Fun.(*ast.SelectorExpr)
	if !isSel || sel.Sel.Name != "MatchString" || len(call.Args) != 1 {
		return "", nil, false
	}
	var name, pattern string
	switch x := sel.X.(type) {
	case *ast.Ident:
		name, pattern = x.Name, patterns[x.Name]
	case *ast.CallExpr:
		pattern, _ = f.compiledPattern(x)
	}
	kind = patternKind(name, pattern)
	return kind, call.Args[0], kind != ""
}

// patternKind names what a regular expression recognises, by the pattern and by
// the name of the variable that holds it. A pattern it cannot name is a rule of
// the domain, and is not read.
func patternKind(name, pattern string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "email") || strings.Contains(pattern, "@"):
		return "email"
	case strings.Contains(lower, "uuid") || strings.Contains(pattern, "[0-9a-f]{8}") || strings.Contains(pattern, "[0-9a-fA-F]{8}"):
		return "uuid"
	case strings.Contains(lower, "url") || strings.Contains(pattern, "https?") || strings.Contains(pattern, "://"):
		return "url"
	}
	return ""
}

// parseOnly reads `_, err := strconv.Atoi(x)`: a parse that asks whether the
// text is a number and keeps nothing of the number. A parse whose value is kept
// is a comparison of the domain.
func (f *file) parseOnly(assign *ast.AssignStmt, fieldOf func(ast.Expr) string) (handRule, bool) {
	if len(assign.Lhs) != 2 || len(assign.Rhs) != 1 {
		return handRule{}, false
	}
	if blank, ok := assign.Lhs[0].(*ast.Ident); !ok || blank.Name != "_" {
		return handRule{}, false
	}
	call, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok || len(call.Args) == 0 {
		return handRule{}, false
	}
	_, name, ok := f.importedName(call.Fun, isPath("strconv"))
	if !ok {
		return handRule{}, false
	}
	rule := map[string]string{
		"Atoi": "integer", "ParseInt": "integer", "ParseUint": "integer",
		"ParseFloat": "numeric", "ParseBool": "boolean",
	}[name]
	if rule == "" {
		return handRule{}, false
	}
	return handRule{field: fieldOf(call.Args[0]), rule: rule, what: "strconv." + name + " that keeps no value", node: assign}, true
}

// membershipLoop reads a loop that compares the field with each case of a list:
//
//	for _, c := range []string{"a", "b"} { if c == r.Kind { found = true } }
func membershipLoop(loop *ast.RangeStmt, fieldOf func(ast.Expr) string) (handRule, bool) {
	value, ok := loop.Value.(*ast.Ident)
	if !ok || value.Name == "_" || loop.Body == nil || len(loop.Body.List) != 1 {
		return handRule{}, false
	}
	branch, ok := loop.Body.List[0].(*ast.IfStmt)
	if !ok || branch.Else != nil || branch.Init != nil {
		return handRule{}, false
	}
	compare, ok := branch.Cond.(*ast.BinaryExpr)
	if !ok || compare.Op != token.EQL {
		return handRule{}, false
	}
	var subject ast.Expr
	switch {
	case isIdent(compare.X, value.Name):
		subject = compare.Y
	case isIdent(compare.Y, value.Name):
		subject = compare.X
	default:
		return handRule{}, false
	}
	if _, listed := loop.X.(*ast.CompositeLit); !listed {
		if _, named := loop.X.(*ast.Ident); !named {
			return handRule{}, false
		}
	}
	return handRule{field: fieldOf(subject), rule: "in", what: "a loop over the allowed values", node: loop}, true
}

// isIdent reports whether the expression is the identifier.
func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}

// intLiteral answers the value of an integer literal.
func intLiteral(e ast.Expr) (int, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.INT {
		return 0, false
	}
	n, err := strconv.Atoi(lit.Value)
	return n, err == nil
}

// requestFieldName answers the name of the input a field is read from: the form
// tag of the field the expression selects, and otherwise the field's name in
// snake case.
func requestFieldName(e ast.Expr, tags map[string]string) string {
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
			continue
		case *ast.CallExpr:
			if len(x.Args) == 1 {
				e = x.Args[0]
				continue
			}
		}
		break
	}
	var name string
	switch x := e.(type) {
	case *ast.SelectorExpr:
		name = x.Sel.Name
	case *ast.Ident:
		name = x.Name
	default:
		return "?"
	}
	if tag := tags[name]; tag != "" {
		return tag
	}
	return snake(name)
}

// snake spells a Go field name as an input name: SupplierEmail is supplier_email.
func snake(name string) string {
	var b strings.Builder
	for i, r := range name {
		if unicode.IsUpper(r) && i > 0 {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
