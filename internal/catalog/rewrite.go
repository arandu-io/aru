package catalog

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ast/astutil"

	"github.com/arandu-io/aru/internal/resolve"
)

// Rewrite answers the Go source src with every symbol a framework bridge only
// re-exports named by its canonical path instead, and reports whether
// anything changed.
//
// Only a symbol the catalog classifies as Alias or Forward moves. A symbol the
// framework declares -- Router, SessionStore -- stays where it is, and so does
// the bridge import while anything still names one through it. A name the
// catalog does not know is treated as one that stays. The output is printed by
// go/format, so it is gofmt-clean, and a second run over it changes nothing:
// what is left on a bridge is what the framework declares.
func (c *Catalog) Rewrite(filename string, src []byte) ([]byte, bool, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, false, err
	}

	type use struct {
		sel    *ast.SelectorExpr
		symbol Symbol
	}
	type bridge struct {
		spec  *ast.ImportSpec
		path  string
		local string
		moved []use
		stays bool
	}
	var bridges []*bridge
	for _, spec := range file.Imports {
		p, _ := strconv.Unquote(spec.Path.Value)
		if !c.Covers(p) {
			continue
		}
		local := importLocal(spec, p)
		if local == "_" || local == "." {
			continue
		}
		bridges = append(bridges, &bridge{spec: spec, path: p, local: local})
	}
	if len(bridges) == 0 {
		return src, false, nil
	}

	byLocal := map[string]*bridge{}
	for _, b := range bridges {
		byLocal[b.local] = b
	}
	objects := resolve.Check(fset, file)
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		// An identifier the file resolves is a local value that shadows the
		// import, not the package.
		if !ok || objects.Local(id) {
			return true
		}
		b, ok := byLocal[id.Name]
		if !ok {
			return true
		}
		symbol, known := c.Lookup(b.path, sel.Sel.Name)
		if !known || !symbol.Moved() {
			b.stays = true
			return true
		}
		b.moved = append(b.moved, use{sel: sel, symbol: symbol})
		return true
	})

	changed := false
	for _, b := range bridges {
		if len(b.moved) > 0 {
			changed = true
		}
	}
	if !changed {
		return src, false, nil
	}

	// The names a new import may not take: every identifier of the file other
	// than a selected field, less the qualifiers about to be rewritten away
	// from a bridge that goes.
	goes := func(b *bridge) bool { return len(b.moved) > 0 && !b.stays }
	rewritten := map[*ast.Ident]bool{}
	for _, b := range bridges {
		if !goes(b) {
			continue
		}
		for _, u := range b.moved {
			rewritten[u.sel.X.(*ast.Ident)] = true
		}
	}
	taken := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			ast.Inspect(x.X, func(m ast.Node) bool {
				if id, ok := m.(*ast.Ident); ok && !rewritten[id] {
					taken[id.Name] = true
				}
				return true
			})
			return false
		case *ast.ImportSpec:
			return false
		case *ast.Ident:
			if !rewritten[x] {
				taken[x.Name] = true
			}
		}
		return true
	})
	locals := map[string]string{}
	for _, spec := range file.Imports {
		p, _ := strconv.Unquote(spec.Path.Value)
		local := importLocal(spec, p)
		if b, ok := byLocal[local]; ok && b.spec == spec && goes(b) {
			continue
		}
		taken[local] = true
		if local != "_" && local != "." {
			locals[p] = local
		}
	}

	for _, b := range bridges {
		for _, u := range b.moved {
			target := u.symbol.Canonical
			local, ok := locals[target]
			if !ok {
				local = freeName(path.Base(target), taken)
				taken[local] = true
				locals[target] = local
				if local == path.Base(target) {
					astutil.AddImport(fset, file, target)
				} else {
					astutil.AddNamedImport(fset, file, local, target)
				}
			}
			u.sel.X.(*ast.Ident).Name = local
			u.sel.Sel.Name = u.symbol.CanonicalName
		}
		if goes(b) {
			name := ""
			if b.spec.Name != nil {
				name = b.spec.Name.Name
			}
			astutil.DeleteNamedImport(fset, file, name, b.path)
		}
	}

	var out bytes.Buffer
	if err := format.Node(&out, fset, file); err != nil {
		return nil, false, err
	}
	return out.Bytes(), !bytes.Equal(out.Bytes(), src), nil
}

// importLocal is the name an import is referred to by in its file.
func importLocal(spec *ast.ImportSpec, importPath string) string {
	if spec.Name != nil {
		return spec.Name.Name
	}
	return path.Base(importPath)
}

// freeName answers name, or the first of hname, hesapename, name2, name3…
// that nothing in the file uses.
func freeName(name string, taken map[string]bool) string {
	for _, candidate := range []string{name, "h" + name, "hesape" + name} {
		if !taken[candidate] {
			return candidate
		}
	}
	for i := 2; ; i++ {
		candidate := name + strconv.Itoa(i)
		if !taken[candidate] {
			return candidate
		}
	}
}

// viewImportLine is one import line of a view's header.
type viewImportLine struct {
	index  int // 0-indexed line
	local  string
	path   string
	named  bool
	block  bool
	indent string
}

// RewriteView is Rewrite for a .kyse.go source, which is not Go below its
// header and is therefore rewritten as text: the import lines above the first
// directive, and every `local.Name` below them. What moves and what stays is
// decided the same way, by the catalog.
func (c *Catalog) RewriteView(src string) (string, bool) {
	lines := strings.Split(src, "\n")
	imports, bodyStart := viewHeaderImports(lines)
	if len(imports) == 0 {
		return src, false
	}
	body := strings.Join(lines[bodyStart:], "\n")

	locals := map[string]string{}
	taken := map[string]bool{}
	for _, imp := range imports {
		taken[imp.local] = true
		locals[imp.path] = imp.local
	}

	type plan struct {
		imp   viewImportLine
		moved map[string]Symbol
		stays bool
	}
	var plans []*plan
	for _, imp := range imports {
		if !c.Covers(imp.path) || imp.local == "_" || imp.local == "." {
			continue
		}
		pl := &plan{imp: imp, moved: map[string]Symbol{}}
		for _, m := range selectorPattern(imp.local).FindAllStringSubmatch(body, -1) {
			symbol, known := c.Lookup(imp.path, m[2])
			if !known || !symbol.Moved() {
				pl.stays = true
				continue
			}
			pl.moved[m[2]] = symbol
		}
		if len(pl.moved) > 0 {
			plans = append(plans, pl)
		}
	}
	if len(plans) == 0 {
		return src, false
	}

	// A word of the body is taken, unless it is the qualifier of a bridge that
	// goes entirely.
	words := regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`).FindAllString(body, -1)
	for _, w := range words {
		taken[w] = true
	}
	for _, pl := range plans {
		if !pl.stays {
			delete(taken, pl.imp.local)
		}
	}

	added := map[int][]string{} // after line index: specs to insert
	removed := map[int]bool{}
	replaced := map[int]string{}
	for _, pl := range plans {
		names := make([]string, 0, len(pl.moved))
		for name := range pl.moved {
			names = append(names, name)
		}
		sort.Strings(names)
		reuse := !pl.stays
		for _, name := range names {
			symbol := pl.moved[name]
			local, ok := locals[symbol.Canonical]
			if !ok {
				local = freeName(path.Base(symbol.Canonical), taken)
				taken[local] = true
				locals[symbol.Canonical] = local
				spec := strconv.Quote(symbol.Canonical)
				if local != path.Base(symbol.Canonical) {
					spec = local + " " + spec
				}
				if reuse {
					// The bridge's line becomes the first new import.
					replaced[pl.imp.index] = viewSpecLine(pl.imp, spec)
					reuse = false
				} else {
					added[pl.imp.index] = append(added[pl.imp.index], viewSpecLine(pl.imp, spec))
				}
			}
			body = selectorPattern(pl.imp.local, name).ReplaceAllString(body, "${1}"+local+"."+symbol.CanonicalName)
		}
		if reuse {
			removed[pl.imp.index] = true
		}
	}

	var out []string
	for i, line := range lines[:bodyStart] {
		switch {
		case replaced[i] != "":
			out = append(out, replaced[i])
		case removed[i]:
		default:
			out = append(out, line)
		}
		out = append(out, added[i]...)
	}
	result := strings.Join(out, "\n") + "\n" + body
	return result, result != src
}

// viewSpecLine writes an import spec the way the line it sits beside is
// written: inside a block, indented; alone, as an import declaration.
func viewSpecLine(beside viewImportLine, spec string) string {
	if beside.block {
		return beside.indent + spec
	}
	return beside.indent + "import " + spec
}

// selectorPattern matches `local.Name`, or `local.<one of names>`, where the
// qualifier is not itself the tail of a longer selector. The qualifier is
// group 1's following text; group 2 is the name.
func selectorPattern(local string, names ...string) *regexp.Regexp {
	name := `[A-Z][A-Za-z0-9_]*`
	if len(names) > 0 {
		quoted := make([]string, len(names))
		for i, n := range names {
			quoted[i] = regexp.QuoteMeta(n)
		}
		name = strings.Join(quoted, "|")
	}
	return regexp.MustCompile(`(^|[^\w.])` + regexp.QuoteMeta(local) + `\.(` + name + `)\b`)
}

// viewHeaderImports reads the import lines above the first directive of a
// view, and answers the index of the line the body starts at: the first line
// opening with @, or the end of the file.
func viewHeaderImports(lines []string) ([]viewImportLine, int) {
	var out []viewImportLine
	inBlock := false
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		indent := raw[:len(raw)-len(strings.TrimLeft(raw, " \t"))]
		switch {
		case strings.HasPrefix(line, "@"):
			return out, i
		case inBlock && line == ")":
			inBlock = false
		case inBlock:
			if imp, ok := parseViewSpec(line); ok {
				imp.index, imp.block, imp.indent = i, true, indent
				out = append(out, imp)
			}
		case line == "import (":
			inBlock = true
		case strings.HasPrefix(line, "import "):
			if imp, ok := parseViewSpec(strings.TrimPrefix(line, "import ")); ok {
				imp.index, imp.indent = i, indent
				out = append(out, imp)
			}
		}
	}
	return out, len(lines)
}

// parseViewSpec reads `name "path"` or `"path"`, a trailing comment allowed.
func parseViewSpec(spec string) (viewImportLine, bool) {
	if i := strings.Index(spec, "//"); i >= 0 {
		spec = strings.TrimSpace(spec[:i])
	}
	fields := strings.Fields(spec)
	var imp viewImportLine
	var quoted string
	switch len(fields) {
	case 1:
		quoted = fields[0]
	case 2:
		imp.local, imp.named, quoted = fields[0], true, fields[1]
	default:
		return imp, false
	}
	p, err := strconv.Unquote(quoted)
	if err != nil || p == "" {
		return imp, false
	}
	imp.path = p
	if imp.local == "" {
		imp.local = path.Base(p)
	}
	return imp, true
}
