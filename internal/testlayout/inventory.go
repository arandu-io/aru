package testlayout

import (
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// InventoryVersion is the version of the inventory's JSON form. It changes
// only when a field is removed, renamed or changes meaning; a field added
// leaves it where it is, so a reader ignores what it does not know.
const InventoryVersion = 1

// OtherLayer names the tests outside the layer directories: a test beside the
// code, named _internal_test.go, or a test under a directory of tests/ that is
// not a layer. go test ./... runs them like any other.
const OtherLayer = "Other"

// Name is the layer's name, the last element of its directory: Unit, Feature,
// Contract, E2E or Conformance.
func (c Category) Name() string { return path.Base(c.Dir) }

// Inventory is every test of a module, read from its sources without
// compiling or running anything.
type Inventory struct {
	// Version is InventoryVersion.
	Version int `json:"version"`
	// Module is the module path go.mod declares.
	Module string `json:"module"`
	// Layers is every layer a test can be in, in the order the tests are
	// sorted: the layers of Categories, then OtherLayer.
	Layers []Layer `json:"layers"`
	// Tests is every top-level test, sorted by layer, file and line.
	Tests []Test `json:"tests"`
	// Unreadable is every test file the parser refused. Its tests are not in
	// Tests, so a reader that shows a tree can say why a file is missing.
	Unreadable []Unreadable `json:"unreadable"`
}

// Layer is one layer as the inventory names it.
type Layer struct {
	// Name is Unit, Feature, Contract, E2E, Conformance or Other.
	Name string `json:"name"`
	// Dir is the layer's directory relative to the module root, with forward
	// slashes, or empty for OtherLayer, which has none.
	Dir string `json:"dir"`
	// Tag is the build tag every file of the layer opens with, or empty.
	Tag string `json:"tag"`
}

// Test is one top-level test function: func TestX(t *testing.T) in a
// _test.go file.
type Test struct {
	// Layer is the name of the layer the file is in.
	Layer string `json:"layer"`
	// Package is the import path go test reports for the file's package.
	Package string `json:"package"`
	// File is the file relative to the module root, with forward slashes.
	File string `json:"file"`
	// Line is the 1-based line of the func keyword.
	Line int `json:"line"`
	// Name is the function's name, which is what -run matches.
	Name string `json:"name"`
	// Constraint is the expression of the file's //go:build line, or empty.
	Constraint string `json:"constraint"`
	// Tags is what -tags needs for go test to compile the file: every tag the
	// constraint names that is not negated and is not an operating system, an
	// architecture, a compiler, cgo or a Go version, which the machine
	// satisfies rather than the command line.
	Tags []string `json:"tags"`
	// Subtests is every t.Run whose name is a string literal, depth first in
	// source order.
	Subtests []Subtest `json:"subtests"`
}

// Subtest is one t.Run call with a literal name.
type Subtest struct {
	// Name is the full name go test gives it, TestX/name/inner, with the
	// spaces the literal holds turned into underscores and a repeated name
	// numbered, the way go test -run and -json spell it.
	Name string `json:"name"`
	// Line is the 1-based line of the call.
	Line int `json:"line"`
}

// Unreadable is a test file the parser refused.
type Unreadable struct {
	// File is the file relative to the module root, with forward slashes.
	File string `json:"file"`
	// Error is what the parser said.
	Error string `json:"error"`
}

// List reads every test of the module at root.
//
// The tree is walked the way go test ./... walks it: a directory named
// testdata, vendor, or starting with a dot or an underscore is skipped, and so
// is a directory holding a go.mod of its own, which is another module. A test
// is what go test runs: a function with no receiver whose name is Test or Test
// followed by anything but a lower-case letter, taking one *testing.T.
// A subtest is listed only when its name is a string literal, because any
// other name is known only once the test runs.
func List(root string) (Inventory, error) {
	module, err := ModulePath(root)
	if err != nil {
		return Inventory{}, err
	}
	files, err := testFiles(root)
	if err != nil {
		return Inventory{}, err
	}

	inv := Inventory{Version: InventoryVersion, Module: module, Tests: []Test{}, Unreadable: []Unreadable{}}
	order := map[string]int{}
	for i, c := range Categories() {
		inv.Layers = append(inv.Layers, Layer{Name: c.Name(), Dir: c.Dir, Tag: c.Tag})
		order[c.Name()] = i
	}
	inv.Layers = append(inv.Layers, Layer{Name: OtherLayer})
	order[OtherLayer] = len(inv.Layers) - 1

	for _, rel := range files {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(rel)), nil,
			parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			inv.Unreadable = append(inv.Unreadable, Unreadable{File: rel, Error: err.Error()})
			continue
		}
		expr, tags := buildConstraint(file)
		testing := testingName(file)
		if testing == "" {
			continue
		}
		pkg := module
		if dir := path.Dir(rel); dir != "." {
			pkg = module + "/" + dir
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			param, ok := testParam(fn, testing)
			if !ok {
				continue
			}
			inv.Tests = append(inv.Tests, Test{
				Layer:      layerOf(rel),
				Package:    pkg,
				File:       rel,
				Line:       fset.Position(fn.Pos()).Line,
				Name:       fn.Name.Name,
				Constraint: expr,
				Tags:       append([]string{}, tags...),
				Subtests:   subtests(fset, fn.Name.Name, param, testing, fn.Body),
			})
		}
	}

	sort.SliceStable(inv.Tests, func(i, j int) bool {
		a, b := inv.Tests[i], inv.Tests[j]
		if order[a.Layer] != order[b.Layer] {
			return order[a.Layer] < order[b.Layer]
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	return inv, nil
}

// layerOf is the name of the layer the slash path rel is in.
func layerOf(rel string) string {
	for _, c := range Categories() {
		if strings.HasPrefix(rel, c.Dir+"/") {
			return c.Name()
		}
	}
	return OtherLayer
}

// testFiles is every _test.go file go test ./... would read under root, as a
// slash path relative to root, in lexical order.
func testFiles(root string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p == root {
				return nil
			}
			name := d.Name()
			if name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, "_test.go") || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		found = append(found, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", root, err)
	}
	return found, nil
}

// buildConstraint is the expression of the file's //go:build line and the
// tags -tags has to name for it to hold.
//
// Only a line above the package clause is a constraint; the same text further
// down is a comment.
func buildConstraint(file *ast.File) (string, []string) {
	for _, group := range file.Comments {
		if group.Pos() >= file.Package {
			break
		}
		for _, c := range group.List {
			if !constraint.IsGoBuild(c.Text) {
				continue
			}
			expr, err := constraint.Parse(c.Text)
			if err != nil {
				return strings.TrimSpace(strings.TrimPrefix(c.Text, "//go:build")), []string{}
			}
			tags := []string{}
			collectTags(expr, false, &tags)
			return expr.String(), tags
		}
	}
	return "", []string{}
}

// collectTags appends to tags every tag of e that is not negated and that the
// command line, rather than the machine, has to supply.
func collectTags(e constraint.Expr, negated bool, tags *[]string) {
	switch x := e.(type) {
	case *constraint.TagExpr:
		if negated || platformTag(x.Tag) {
			return
		}
		for _, t := range *tags {
			if t == x.Tag {
				return
			}
		}
		*tags = append(*tags, x.Tag)
	case *constraint.NotExpr:
		collectTags(x.X, !negated, tags)
	case *constraint.AndExpr:
		collectTags(x.X, negated, tags)
		collectTags(x.Y, negated, tags)
	case *constraint.OrExpr:
		collectTags(x.X, negated, tags)
		collectTags(x.Y, negated, tags)
	}
}

// goVersionTag is the release tag every toolchain from that version on sets.
var goVersionTag = regexp.MustCompile(`^go1\.[0-9]+$`)

// platformTags are the tags the toolchain sets from the machine and the build
// configuration: operating systems, architectures, compilers and cgo. Passing
// one to -tags is never what a test asks for.
var platformTags = setOf(strings.Fields(`aix android darwin dragonfly freebsd hurd illumos ios js linux nacl netbsd
		openbsd plan9 solaris wasip1 windows zos unix
		386 amd64 amd64p32 arm armbe arm64 arm64be loong64 mips mipsle mips64 mips64le mips64p32 mips64p32le
		ppc ppc64 ppc64le riscv riscv64 s390 s390x sparc sparc64 wasm
		cgo gc gccgo`))

func setOf(words []string) map[string]bool {
	set := make(map[string]bool, len(words))
	for _, w := range words {
		set[w] = true
	}
	return set
}

func platformTag(tag string) bool { return platformTags[tag] || goVersionTag.MatchString(tag) }

// testingName is the name the file refers to the testing package by, or empty
// when it does not import it -- and then nothing in it can be a test.
func testingName(file *ast.File) string {
	for _, imp := range file.Imports {
		if p, err := strconv.Unquote(imp.Path.Value); err != nil || p != "testing" {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return "testing"
	}
	return ""
}

// testParam reports whether go test runs fn as a test, and the name of its
// *testing.T parameter, which is empty or _ when the body cannot call Run.
func testParam(fn *ast.FuncDecl, testing string) (string, bool) {
	if fn.Recv != nil || fn.Type.Results != nil || fn.Type.TypeParams != nil || !named(fn.Name.Name, "Test") {
		return "", false
	}
	return tParam(fn.Type, testing)
}

// tParam is the name of the single *testing.T parameter of a function type.
func tParam(ft *ast.FuncType, testing string) (string, bool) {
	if ft.Params == nil || len(ft.Params.List) != 1 || len(ft.Params.List[0].Names) > 1 {
		return "", false
	}
	field := ft.Params.List[0]
	star, ok := field.Type.(*ast.StarExpr)
	if !ok || !isTestingT(star.X, testing) {
		return "", false
	}
	if len(field.Names) == 0 {
		return "", true
	}
	return field.Names[0].Name, true
}

// isTestingT reports whether e names testing.T under the file's name for the
// package, which is the bare T after a dot import.
func isTestingT(e ast.Expr, testing string) bool {
	if testing == "." {
		id, ok := e.(*ast.Ident)
		return ok && id.Name == "T"
	}
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "T" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == testing
}

// subtests is every t.Run in body called on the parameter t with a literal
// name, depth first, named under parent the way go test names it.
func subtests(fset *token.FileSet, parent, t, testing string, body *ast.BlockStmt) []Subtest {
	out := []Subtest{}
	seen := map[string]int{}
	var walk func(parent, t string, node ast.Node)
	walk = func(parent, t string, node ast.Node) {
		if t == "" || t == "_" {
			return
		}
		ast.Inspect(node, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Run" {
				return true
			}
			recv, ok := sel.X.(*ast.Ident)
			if !ok || recv.Name != t {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				// The name is known only when the test runs, and so are the
				// names of everything nested under it.
				return false
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				return false
			}
			name := uniqueName(seen, parent, rewriteName(value))
			out = append(out, Subtest{Name: name, Line: fset.Position(call.Pos()).Line})
			if fl, ok := call.Args[1].(*ast.FuncLit); ok {
				if inner, ok := tParam(fl.Type, testing); ok {
					walk(name, inner, fl.Body)
				}
			}
			return false
		})
	}
	walk(parent, t, body)
	return out
}

// uniqueName is the name go test gives the subtest sub of parent: parent/sub
// the first time, then parent/sub#01, parent/sub#02 for each repeat, and
// parent/#00 for an empty name. A name written with a #NN of its own that a
// repeat already took is numbered again, as go test numbers it.
func uniqueName(seen map[string]int, parent, sub string) string {
	base := parent + "/" + sub
	for {
		n := seen[base]
		seen[base] = n + 1
		if n == 0 && sub != "" {
			prefix, nn := subtestNumber(base)
			if len(prefix) < len(base) && nn < seen[prefix] {
				continue
			}
			return base
		}
		name := fmt.Sprintf("%s#%02d", base, n)
		if seen[name] != 0 {
			continue
		}
		return name
	}
}

// subtestNumber splits a name ending in a #NN that go test could have written
// into what precedes it and the number; any other name is returned whole with
// zero.
func subtestNumber(s string) (string, int) {
	i := strings.LastIndex(s, "#")
	if i < 0 {
		return s, 0
	}
	prefix, suffix := s[:i], s[i+1:]
	if len(suffix) < 2 || (len(suffix) > 2 && suffix[0] == '0') {
		return s, 0
	}
	if suffix == "00" && !strings.HasSuffix(prefix, "/") {
		return s, 0
	}
	n, err := strconv.ParseInt(suffix, 10, 32)
	if err != nil || n < 0 {
		return s, 0
	}
	return prefix, int(n)
}

// rewriteName is a subtest's name as go test spells it: each space becomes an
// underscore and each character that does not print becomes its Go escape.
func rewriteName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case nameSpace(r):
			b.WriteByte('_')
		case !strconv.IsPrint(r):
			q := strconv.QuoteRune(r)
			b.WriteString(q[1 : len(q)-1])
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// nameSpace reports whether go test turns r into an underscore in a subtest's
// name: the ASCII spaces, NEL, NBSP and the Unicode space separators.
func nameSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x85, 0xA0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}
