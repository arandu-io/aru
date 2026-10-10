package doctor

import (
	"go/ast"
	"go/token"
	"strings"
)

// sinkKind is where a value is written out whole: a log line, or a JSON
// document. Both print every field the value has unless its type says
// otherwise, which is what sensitiveFieldNeedsRedaction asks about.
type sinkKind uint8

const (
	logSink sinkKind = 1 << iota
	jsonSink
)

// sinkReach is one place a type was seen reaching a sink.
type sinkReach struct {
	kind sinkKind
	file string
	line int
	call string
}

// sinkReaches answers, for the project's own types, where a value of each one
// is handed to a log or a JSON sink.
//
// byType is keyed by the directory and name of the type ("app/Models.User").
// byName is keyed by a lower-case type name, for an identifier whose type the
// function does not declare but whose name is the type's -- `charge` for
// Charge, `charges` for a slice of them. A parse without type checking cannot
// do better, and a name that matches is evidence rather than proof, which the
// rule reading this says.
func sinkReaches(p *project) (byType, byName map[string][]sinkReach) {
	byType = map[string][]sinkReach{}
	byName = map[string][]sinkReach{}
	for _, f := range p.files {
		if f.isTest {
			continue
		}
		f.functions(func(fn *ast.FuncDecl) {
			if fn.Body == nil {
				return
			}
			env := declaredTypes(fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.AssignStmt:
					learnAssigned(env, x)
				case *ast.RangeStmt:
					learnRanged(env, x)
				case *ast.DeclStmt:
					learnDeclared(env, x)
				case *ast.CallExpr:
					kind, ok := f.sinkCall(x)
					if !ok {
						return true
					}
					reach := sinkReach{kind: kind, file: f.rel, line: f.line(x), call: callName(x)}
					for _, arg := range x.Args {
						if ref, ok := typeOfValue(env, arg); ok {
							if dir, known := p.typeDir(f, ref.qual); known {
								key := dir + "." + ref.name
								byType[key] = append(byType[key], reach)
							}
							continue
						}
						if id, ok := unwrapIdent(arg); ok {
							name := strings.ToLower(id.Name)
							byName[name] = append(byName[name], reach)
							if trimmed := strings.TrimSuffix(name, "s"); trimmed != name && trimmed != "" {
								byName[trimmed] = append(byName[trimmed], reach)
							}
						}
					}
				}
				return true
			})
		})
	}
	return byType, byName
}

// sinkCall reports whether a call writes its arguments out whole, and to
// which kind of sink.
//
// Log: any function of log/slog, log, the hesape log package or the
// observability package (Dump among them); the fmt functions that format a
// value -- Print, Fprint, Sprint and Errorf, whose result is text somebody
// logs; and the logging methods on a receiver whose name says it is a logger.
// JSON: json.Marshal and MarshalIndent, and an Encode method, which is how an
// encoder writes a value.
func (f *file) sinkCall(call *ast.CallExpr) (sinkKind, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return 0, false
	}
	method := sel.Sel.Name
	if head, ok := sel.X.(*ast.Ident); ok && !f.objects().Local(head) {
		if path, imported := f.importPath(head.Name); imported {
			switch {
			case path == "log/slog" || path == "log" || path == "github.com/arandu-io/hesape/log" ||
				strings.HasSuffix(path, "/observability"):
				return logSink, true
			case path == "fmt":
				if strings.HasPrefix(method, "Print") || strings.HasPrefix(method, "Fprint") ||
					strings.HasPrefix(method, "Sprint") || method == "Errorf" {
					return logSink, true
				}
				return 0, false
			case path == "encoding/json":
				if method == "Marshal" || method == "MarshalIndent" {
					return jsonSink, true
				}
				return 0, false
			}
		}
	}
	if method == "Encode" {
		return jsonSink, true
	}
	if loggingMethods[method] {
		receiver := strings.ToLower(exprName(sel.X))
		if i := strings.LastIndex(receiver, "."); i >= 0 {
			receiver = receiver[i+1:]
		}
		if strings.Contains(receiver, "log") {
			return logSink, true
		}
	}
	return 0, false
}

// loggingMethods are the method names a logger writes a line with.
var loggingMethods = map[string]bool{
	"Debug": true, "Info": true, "Warn": true, "Error": true, "Log": true, "LogAttrs": true, "With": true,
	"DebugContext": true, "InfoContext": true, "WarnContext": true, "ErrorContext": true,
	"Print": true, "Printf": true, "Println": true,
}

// typeRef is a type named in source: its package qualifier, empty for the
// file's own package, and its name.
type typeRef struct {
	qual string
	name string
}

// typeDir answers the project directory of the package a qualifier names in
// f, or f's own directory for an empty one. A package outside the module
// answers false: its types are not the project's to redact.
func (p *project) typeDir(f *file, qual string) (string, bool) {
	if qual == "" {
		return f.dir, true
	}
	path, ok := f.importPath(qual)
	if !ok || p.modulePath == "" || !strings.HasPrefix(path, p.modulePath+"/") {
		return "", false
	}
	return strings.TrimPrefix(path, p.modulePath+"/"), true
}

// refOfType reads the named type out of a type expression, through a pointer,
// a slice or an array: *models.User, []User and [4]User all name User.
func refOfType(e ast.Expr) (typeRef, bool) {
	switch t := e.(type) {
	case *ast.Ident:
		return typeRef{name: t.Name}, true
	case *ast.SelectorExpr:
		if q, ok := t.X.(*ast.Ident); ok {
			return typeRef{qual: q.Name, name: t.Sel.Name}, true
		}
	case *ast.StarExpr:
		return refOfType(t.X)
	case *ast.ArrayType:
		return refOfType(t.Elt)
	}
	return typeRef{}, false
}

// declaredTypes are the types a function's signature gives its names: the
// receiver, the parameters and the named results.
func declaredTypes(fn *ast.FuncDecl) map[string]typeRef {
	env := map[string]typeRef{}
	bind := func(list *ast.FieldList) {
		if list == nil {
			return
		}
		for _, field := range list.List {
			ref, ok := refOfType(field.Type)
			if !ok {
				continue
			}
			for _, name := range field.Names {
				env[name.Name] = ref
			}
		}
	}
	bind(fn.Recv)
	bind(fn.Type.Params)
	bind(fn.Type.Results)
	return env
}

// learnAssigned records `x := T{…}`, `x := &T{…}` and `x := []T{…}`.
func learnAssigned(env map[string]typeRef, a *ast.AssignStmt) {
	if a.Tok != token.DEFINE || len(a.Lhs) != len(a.Rhs) {
		return
	}
	for i, lhs := range a.Lhs {
		id, ok := lhs.(*ast.Ident)
		if !ok {
			continue
		}
		if ref, ok := typeOfValue(env, a.Rhs[i]); ok {
			env[id.Name] = ref
		}
	}
}

// learnRanged records the value of `for _, x := range xs` when xs is known.
func learnRanged(env map[string]typeRef, r *ast.RangeStmt) {
	value, ok := r.Value.(*ast.Ident)
	if !ok || r.Tok != token.DEFINE {
		return
	}
	if ref, ok := typeOfValue(env, r.X); ok {
		env[value.Name] = ref
	}
}

// learnDeclared records `var x T`.
func learnDeclared(env map[string]typeRef, d *ast.DeclStmt) {
	gen, ok := d.Decl.(*ast.GenDecl)
	if !ok || gen.Tok != token.VAR {
		return
	}
	for _, spec := range gen.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok || vs.Type == nil {
			continue
		}
		if ref, ok := refOfType(vs.Type); ok {
			for _, name := range vs.Names {
				env[name.Name] = ref
			}
		}
	}
}

// typeOfValue answers the named type of an expression when the source says
// it: a composite literal, the address of one, or a name the function bound.
func typeOfValue(env map[string]typeRef, e ast.Expr) (typeRef, bool) {
	switch x := e.(type) {
	case *ast.CompositeLit:
		if x.Type != nil {
			return refOfType(x.Type)
		}
	case *ast.UnaryExpr:
		if x.Op == token.AND {
			return typeOfValue(env, x.X)
		}
	case *ast.StarExpr:
		return typeOfValue(env, x.X)
	case *ast.ParenExpr:
		return typeOfValue(env, x.X)
	case *ast.IndexExpr:
		return typeOfValue(env, x.X)
	case *ast.Ident:
		ref, ok := env[x.Name]
		return ref, ok
	}
	return typeRef{}, false
}

// unwrapIdent answers the identifier an argument is, through an address-of,
// a dereference or parentheses.
func unwrapIdent(e ast.Expr) (*ast.Ident, bool) {
	switch x := e.(type) {
	case *ast.Ident:
		return x, x.Name != "nil" && x.Name != "_"
	case *ast.UnaryExpr:
		if x.Op == token.AND {
			return unwrapIdent(x.X)
		}
	case *ast.StarExpr:
		return unwrapIdent(x.X)
	case *ast.ParenExpr:
		return unwrapIdent(x.X)
	}
	return nil, false
}

// sensitiveWords are the words of a field name that say it holds a secret or
// a personal document number. They are matched as whole words of the name,
// singular or plural, so Undocumented and documented are not documents; two
// adjacent words are matched joined, so APIKey and api_key both read apikey.
var sensitiveWords = map[string]bool{
	"password": true, "passwd": true, "secret": true, "token": true, "apikey": true,
	"creditcard": true, "document": true, "cpf": true, "cnpj": true,
}

// sensitiveName reports whether a field name carries one of sensitiveWords.
func sensitiveName(name string) bool {
	words := camelWords(name)
	for i, w := range words {
		w = strings.ToLower(w)
		candidates := []string{w}
		if i+1 < len(words) {
			candidates = append(candidates, w+strings.ToLower(words[i+1]))
		}
		for _, c := range candidates {
			if sensitiveWords[c] || sensitiveWords[strings.TrimSuffix(c, "s")] {
				return true
			}
		}
	}
	return false
}

// cannotHoldASecret reports whether a field's type is a number, a boolean or
// a time: MaxTokens int counts tokens, it is not one.
func cannotHoldASecret(t ast.Expr) bool {
	switch x := t.(type) {
	case *ast.Ident:
		switch x.Name {
		case "bool", "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64",
			"uintptr", "float32", "float64", "complex64", "complex128", "rune":
			return true
		}
	case *ast.SelectorExpr:
		if q, ok := x.X.(*ast.Ident); ok && q.Name == "time" {
			return x.Sel.Name == "Time" || x.Sel.Name == "Duration"
		}
	case *ast.StarExpr:
		return cannotHoldASecret(x.X)
	}
	return false
}

// hiddenFromJSON reports whether a field's tag keeps it out of JSON.
func hiddenFromJSON(field *ast.Field) bool {
	if field.Tag == nil {
		return false
	}
	tag := strings.Trim(field.Tag.Value, "`")
	for _, part := range strings.Fields(tag) {
		if strings.HasPrefix(part, `json:"-"`) {
			return true
		}
	}
	return false
}
