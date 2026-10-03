package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/arandu-io/aru/internal/gen"
)

// The import paths whose generic surface the tool rewrites.
const (
	modelPath     = "github.com/arandu-io/hesape/database/model"
	factoriesPath = "github.com/arandu-io/hesape/database/model/factories"
)

// source is one Go file of the module, as text and as the tree of that text.
//
// Every pass reads the tree, writes edits against the text, and parses the
// result again for the next pass. Editing the text rather than the tree is what
// keeps the comments and the layout of a file somebody wrote: the printer
// places a comment by position, and a tree with nodes moved or inserted prints
// them wherever the arithmetic lands.
type source struct {
	path    string // absolute
	rel     string // slashed, relative to the module root
	dir     string // slashed, relative to the module root
	pkgPath string // import path of the package
	src     []byte
	orig    []byte
	fset    *token.FileSet
	ast     *ast.File
	edits   []edit
	// names maps the import path of each package of the module to its
	// package clause, which is the name an unnamed import of it binds.
	names map[string]string
}

// edit replaces src[start:end] with text.
type edit struct {
	start, end int
	text       string
}

// upgrader holds the module and what the passes learned about it.
type upgrader struct {
	root       string
	modulePath string
	files      []*source
	problems   []problem

	// entities are the rewritten entities by package path and type name.
	entities map[string]map[string]*entity
	// constructors maps a package path and a constructor's old name to the
	// entity it built: models.Users and the factories' UserFactory alike.
	constructors map[string]map[string]*entity
	// factories maps the factories package path and an old factory
	// constructor's name to its entity.
	factories map[string]map[string]*entity
	// failed are the entities, by package path and name, whose declaration
	// was refused: their callers are not reported a second time.
	failed map[string]bool
}

// entity is one model the tool rewrote.
type entity struct {
	name     string
	pkgPath  string
	oldCtor  string
	newCtor  string
	tableVar string
	// structFile is the file declaring the struct, as it was read.
	structFile string
	// factoryPkg is the package of its typed factory, when it has one.
	factoryPkg string
}

// load reads every Go file of the module rooted at root that the patterns
// select. A file that does not parse stops the run: it may hold a model, and an
// upgrade that skipped it would leave its callers rewritten for a constructor
// that still exists.
func load(root, wd string, patterns []string) (*upgrader, error) {
	modulePath := readModulePath(root)
	if modulePath == "" {
		return nil, fmt.Errorf("model-upgrade: %s has no module line", filepath.Join(root, "go.mod"))
	}
	u := &upgrader{
		root: root, modulePath: modulePath,
		entities:     map[string]map[string]*entity{},
		constructors: map[string]map[string]*entity{},
		factories:    map[string]map[string]*entity{},
		failed:       map[string]bool{},
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (name == "vendor" || name == "testdata" || name == "node_modules" ||
				strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			if path != root {
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, ".kyse.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !selected(root, wd, rel, patterns) {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		pkgPath := modulePath
		if dir != "." {
			pkgPath += "/" + dir
		}
		f := &source{path: path, rel: rel, dir: dir, pkgPath: pkgPath, src: body, orig: body}
		if err := f.parse(); err != nil {
			return fmt.Errorf("model-upgrade: %v", err)
		}
		u.files = append(u.files, f)
		return nil
	})
	sort.Slice(u.files, func(i, j int) bool { return u.files[i].rel < u.files[j].rel })
	names := map[string]string{}
	for _, f := range u.files {
		if !strings.HasSuffix(f.ast.Name.Name, "_test") {
			names[f.pkgPath] = f.ast.Name.Name
		}
	}
	for _, f := range u.files {
		f.names = names
	}
	return u, err
}

// upgrade runs the passes and returns the files whose text changed. It writes
// nothing; when it records a problem, the caller writes nothing either.
func (u *upgrader) upgrade() []*source {
	// A problem in one pass does not stop the next: the run writes nothing
	// either way, and one run that names every place is worth more than a
	// run per place. Only a rewrite that does not parse stops it, because
	// every pass after it would read a tree that is not the text.
	for _, pass := range []func(){u.models, u.factoryFiles, u.callers, u.tidy} {
		pass()
		if !u.apply() {
			return nil
		}
	}
	u.leftovers()

	var changed []*source
	for _, f := range u.files {
		if !bytes.Equal(f.src, f.orig) {
			changed = append(changed, f)
		}
	}
	return changed
}

// apply writes every file's edits into its text, formats it and parses it
// again. It reports false when a result does not parse, which is a defect of
// this tool and is said as one.
func (u *upgrader) apply() bool {
	ok := true
	for _, f := range u.files {
		if len(f.edits) == 0 {
			continue
		}
		out, err := applyEdits(f.src, f.edits)
		f.edits = nil
		if err != nil {
			u.problemAt(f, 1, err.Error())
			ok = false
			continue
		}
		formatted, err := format.Source(out)
		if err != nil {
			u.problemAt(f, 1, "the rewritten file does not parse, which is a defect of model-upgrade: "+err.Error())
			ok = false
			continue
		}
		f.src = formatted
		if err := f.parse(); err != nil {
			u.problemAt(f, 1, err.Error())
			ok = false
		}
	}
	return ok
}

func (f *source) parse() error {
	f.fset = token.NewFileSet()
	file, err := parser.ParseFile(f.fset, f.rel, f.src, parser.ParseComments)
	if err != nil {
		return err
	}
	f.ast = file
	return nil
}

// offset is the byte offset of pos in the file's text.
func (f *source) offset(pos token.Pos) int { return f.fset.Position(pos).Offset }

// line is the line of n.
func (f *source) line(n ast.Node) int { return f.fset.Position(n.Pos()).Line }

// text is the source of n.
func (f *source) text(n ast.Node) string { return string(f.src[f.offset(n.Pos()):f.offset(n.End())]) }

// replace records an edit of the text n spans.
func (f *source) replace(n ast.Node, text string) {
	f.edits = append(f.edits, edit{start: f.offset(n.Pos()), end: f.offset(n.End()), text: text})
}

// replaceRange records an edit of the text between two positions.
func (f *source) replaceRange(from, to token.Pos, text string) {
	f.edits = append(f.edits, edit{start: f.offset(from), end: f.offset(to), text: text})
}

// importName answers the name f imports path under, or "" when it does not.
func (f *source) importName(path string) string {
	for _, imp := range f.ast.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p == path {
			return f.bound(imp, p)
		}
	}
	return ""
}

// importsByName maps each name f binds by import to the path.
func (f *source) importsByName() map[string]string {
	out := map[string]string{}
	for _, imp := range f.ast.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		out[f.bound(imp, p)] = p
	}
	return out
}

// bound is the name an import binds: its alias, the package clause of a
// package of this module, or the name the path binds by convention.
func (f *source) bound(imp *ast.ImportSpec, path string) string {
	if imp.Name != nil {
		return imp.Name.Name
	}
	if name, ok := f.names[path]; ok {
		return name
	}
	return gen.AssumedImportName(path)
}

// problemAt records a problem at a line of f's current text, reported at the
// line of the file as it was read: a person opens the file they have, not the
// one the earlier passes have been rewriting in memory.
func (u *upgrader) problemAt(f *source, line int, message string) {
	u.problems = append(u.problems, problem{file: f.rel, line: f.origLine(line), message: message})
}

// origLine maps a line of the current text to the line of the original it
// came from, by the longest common subsequence of the two texts' lines. A line
// the rewrite wrote is reported at the original line before it.
func (f *source) origLine(line int) int {
	if bytes.Equal(f.src, f.orig) {
		return line
	}
	old := strings.Split(string(f.orig), "\n")
	cur := strings.Split(string(f.src), "\n")
	n, m := len(old), len(cur)
	// lcs[i][j] is the length of the longest common subsequence of old[i:]
	// and cur[j:].
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case old[i] == cur[j]:
				lcs[i][j] = lcs[i+1][j+1] + 1
			case lcs[i+1][j] >= lcs[i][j+1]:
				lcs[i][j] = lcs[i+1][j]
			default:
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	mapped, last := 1, 0
	for i, j := 0, 0; i < n && j < m; {
		switch {
		case old[i] == cur[j]:
			last = i + 1
			if j+1 == line {
				return last
			}
			i, j = i+1, j+1
		case lcs[i+1][j] >= lcs[i][j+1]:
			i++
		default:
			if j+1 == line {
				return max(last, mapped)
			}
			j++
		}
	}
	return max(last, mapped)
}

// applyEdits writes the edits into src, refusing two that overlap.
func applyEdits(src []byte, edits []edit) ([]byte, error) {
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	var buf bytes.Buffer
	at := 0
	for _, e := range edits {
		if e.start < at {
			return nil, fmt.Errorf("two rewrites of one span of text at byte %d, which is a defect of model-upgrade", e.start)
		}
		buf.Write(src[at:e.start])
		buf.WriteString(e.text)
		at = e.end
	}
	buf.Write(src[at:])
	return buf.Bytes(), nil
}

// readModulePath answers the module line of go.mod, or empty.
func readModulePath(root string) string {
	body, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(body), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// selectorOf reports whether e is pkg.name, and answers name.
func selectorOf(e ast.Expr, pkg string) (string, bool) {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || pkg == "" {
		return "", false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || id.Name != pkg {
		return "", false
	}
	return sel.Sel.Name, true
}

// stringValue answers the value of a string literal.
func stringValue(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	return v, err == nil
}

// boolValue answers the value of the identifiers true and false.
func boolValue(e ast.Expr) (bool, bool) {
	id, ok := e.(*ast.Ident)
	if !ok || (id.Name != "true" && id.Name != "false") {
		return false, false
	}
	return id.Name == "true", true
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
