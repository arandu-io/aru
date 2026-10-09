// Package catalog answers, for every exported symbol of the framework, the
// one import path a project should name it by.
//
// The framework carries bridge packages: old import paths kept so an
// application written against them goes on compiling, whose symbols point at
// the component that now owns them. Most of what a bridge exports is a Go
// alias -- `type Grant = auth.Grant` -- or a function that only calls through,
// and for those the owner's path is the one to import. Some of it is not: a
// type the bridge declares, or a function that does more than forward, is the
// framework's own, and its path stays the framework's until something with the
// same responsibility exists elsewhere. Which of the two a symbol is cannot be
// decided by its package, because one package holds both.
//
// It can be decided by its declaration, and without running anything: an
// alias is syntax. So the catalog is read from the framework's source, the
// version a project requires, rather than kept as a list that would drift from
// it on the next release.
package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/arandu-io/aru/internal/buildcache"
	"github.com/arandu-io/aru/internal/gen"
	"github.com/arandu-io/aru/internal/gomod"
)

// Framework is the module the catalog describes.
const Framework = "github.com/arandu-io/framework"

// Kind is what a symbol is declared as.
type Kind string

// The four declarations a package-level name can have.
const (
	Type  Kind = "type"
	Func  Kind = "func"
	Var   Kind = "var"
	Const Kind = "const"
)

// How says why a symbol's canonical path is the one the catalog gives.
type How string

const (
	// Declared is a symbol the framework defines: a defined type, a function
	// whose body does more than call through, a value computed here. Its
	// canonical path is the framework's.
	Declared How = "declared"
	// Alias is `type X = p.Y`, `var X = p.Y` or `const X = p.Y`: the name is
	// another package's symbol under a second spelling.
	Alias How = "alias"
	// Forward is a function whose whole body calls another package's function
	// with its own parameters, in order, and whose signature names no type the
	// framework declares -- Go has no alias form for a function, so this is
	// the alias written out.
	Forward How = "forward"
)

// Symbol is one exported name of a framework package and where to import it
// from.
type Symbol struct {
	// Package and Name are the framework's spelling.
	Package string `json:"package"`
	Name    string `json:"name"`
	Kind    Kind   `json:"kind"`
	How     How    `json:"how"`
	// Canonical and CanonicalName are the path and name a project writes.
	// For a Declared symbol they are Package and Name.
	Canonical     string `json:"canonical"`
	CanonicalName string `json:"canonicalName"`
	// File and Line are where the framework declares it, File relative to
	// the module root.
	File string `json:"file"`
	Line int    `json:"line"`
}

// Moved reports whether the canonical spelling is not the framework's.
func (s Symbol) Moved() bool {
	return s.Canonical != s.Package || s.CanonicalName != s.Name
}

// Package is one importable package of the framework.
type Package struct {
	Path string `json:"path"`
	// Bridge is the import path the package's own documentation names when it
	// says it is a bridge and what to import instead, or empty when it does not
	// say so. It is what the documentation promises; the symbols say, one by
	// one, how much of the package that promise covers.
	Bridge  string   `json:"bridge,omitempty"`
	Symbols []Symbol `json:"symbols"`
}

// Catalog is every exported symbol of one version of the framework.
type Catalog struct {
	Module string `json:"module"`
	// Version is the version a project requires, or empty when the catalog was
	// read from a directory -- a replace, or a call to Read -- that no version
	// names.
	Version  string    `json:"version,omitempty"`
	Dir      string    `json:"-"`
	Packages []Package `json:"packages"`

	index map[string]map[string]Symbol
}

// Lookup answers the entry for name in the framework package at path.
func (c *Catalog) Lookup(pkg, name string) (Symbol, bool) {
	if c == nil {
		return Symbol{}, false
	}
	s, ok := c.index[pkg][name]
	return s, ok
}

// Covers reports whether the catalog read the package at path. A package it
// did not read -- one the framework does not have at this version, or one
// left out of a vendor directory -- is one nothing can be said about.
func (c *Catalog) Covers(pkg string) bool {
	if c == nil {
		return false
	}
	_, ok := c.index[pkg]
	return ok
}

// ErrNotRequired is returned by ForProject when the project's go.mod does not
// require the framework, which is a project the catalog has nothing to say
// about rather than one it failed to read.
var ErrNotRequired = errors.New("go.mod does not require " + Framework)

// NotOnDisk is returned by ForProject when the framework is required and its
// source is in no place the toolchain would read it from.
type NotOnDisk struct {
	Version string
}

func (e *NotOnDisk) Error() string {
	return fmt.Sprintf("%s %s is not in the module cache, a vendor directory or a replaced directory: "+
		"run go mod download %s", Framework, e.Version, Framework)
}

// ForProject reads the catalog of the framework version the project rooted at
// root requires, from wherever its source already sits.
//
// It reads go.mod and the disk and starts nothing: a replace naming a
// directory, the vendor directory and the module cache are looked in, in that
// order. A framework that is required and not on disk answers *NotOnDisk.
func ForProject(root string) (*Catalog, error) {
	body, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}
	mod := gomod.Parse(string(body))
	version, required := mod.Versions[Framework]
	if !required {
		return nil, ErrNotRequired
	}
	if pinned, ok := mod.Pinned(Framework); ok {
		version = pinned
	}
	dir, found := mod.Dir(root, Framework)
	_, replaced := mod.Replaced[Framework]
	switch {
	case !found && replaced:
		// A download would fetch the version, and the build reads the
		// directory: a catalog of the one is not an answer about the other.
		return nil, fmt.Errorf("go.mod replaces %s with %s, which is not a directory", Framework, dir)
	case !found:
		return nil, &NotOnDisk{Version: version}
	}
	// The path is the one go.mod requires rather than one read off the
	// directory: a vendor directory holds no go.mod, and a replacement that
	// declared another path would not build.
	c, err := read(dir, Framework)
	if err != nil {
		return nil, err
	}
	// A directory replace is a working tree, and the version go.mod names
	// beside it is not what was read.
	if !replaced {
		c.Version = version
	}
	return c, nil
}

// Fetch is ForProject for a caller that may start the toolchain: when the
// required version is not on disk, it runs `go mod download` for that version
// in root and reads the source from where the download reports it put it.
//
// The doctor never calls this. It reads what is on disk and starts nothing,
// so on a machine that never built the project it has no catalog, and says
// nothing about imports rather than guessing.
func Fetch(root string) (*Catalog, error) {
	c, err := ForProject(root)
	var missing *NotOnDisk
	if !errors.As(err, &missing) {
		return c, err
	}

	module := Framework + "@" + missing.Version
	cmd := buildcache.Command("mod", "download", "-json", module)
	cmd.Dir = root
	out, runErr := cmd.Output()
	var downloaded struct {
		Dir   string
		Error string
	}
	_ = json.Unmarshal(out, &downloaded)
	switch {
	case downloaded.Error != "":
		return nil, fmt.Errorf("go mod download %s: %s", module, downloaded.Error)
	case runErr != nil || downloaded.Dir == "":
		return nil, fmt.Errorf("go mod download %s did not report where it put the source: %v", module, runErr)
	}
	c, err = Read(downloaded.Dir)
	if err != nil {
		return nil, err
	}
	c.Version = missing.Version
	return c, nil
}

// bridgeSentence is what a bridge package says about itself in its package
// documentation.
var bridgeSentence = regexp.MustCompile(`This package is a bridge\. It is removed in v1\.0\.0; import (\S+) directly\.`)

// BridgeTarget answers the import path a package's documentation sends its
// reader to, from the sentence every bridge carries.
func BridgeTarget(doc string) (string, bool) {
	m := bridgeSentence.FindStringSubmatch(strings.Join(strings.Fields(doc), " "))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// source is one package as parsed, before anything is resolved.
type source struct {
	path  string
	name  string
	doc   string
	files []*parsedFile
}

type parsedFile struct {
	rel     string
	ast     *ast.File
	imports map[string]string // local name -> import path
}

// decl is one exported declaration and what it points at, before the chain is
// followed.
type decl struct {
	pkg, name string
	kind      Kind
	how       How
	target    ref
	file      string
	line      int
	// fn and in are kept for a forward, whose signature is checked once the
	// types it names are resolved.
	fn *ast.FuncDecl
	in *parsedFile
}

type ref struct{ pkg, name string }

// Read parses the module rooted at dir and catalogs every exported symbol of
// every package a project can import from it.
//
// Test files, testdata, internal packages, commands and nested modules are not
// importable by a project and are skipped; files the build would exclude on
// this platform are skipped for the same reason the compiler skips them.
func Read(dir string) (*Catalog, error) {
	module, err := modulePath(dir)
	if err != nil {
		return nil, err
	}
	return read(dir, module)
}

func read(dir, module string) (*Catalog, error) {
	// A walk does not follow a link it is handed as its root, so a module
	// reached through one -- a replace naming a link, a checkout shared into a
	// worktree -- would read as empty.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	fset := token.NewFileSet()
	sources := map[string]*source{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			name := d.Name()
			if p != dir && (name == "testdata" || name == "vendor" || name == "internal" ||
				strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			if p != dir {
				if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		if match, err := build.Default.MatchFile(filepath.Dir(p), name); err != nil || !match {
			return nil
		}
		parsed, err := parser.ParseFile(fset, p, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("catalog: %w", err)
		}
		if parsed.Name.Name == "main" {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		pkgPath := module
		if relDir := filepath.ToSlash(filepath.Dir(rel)); relDir != "." {
			pkgPath = module + "/" + relDir
		}
		src := sources[pkgPath]
		if src == nil {
			src = &source{path: pkgPath, name: parsed.Name.Name}
			sources[pkgPath] = src
		}
		if parsed.Doc != nil && src.doc == "" {
			if _, bridge := BridgeTarget(parsed.Doc.Text()); bridge {
				src.doc = parsed.Doc.Text()
			}
		}
		src.files = append(src.files, &parsedFile{rel: filepath.ToSlash(rel), ast: parsed})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("catalog: %s holds no package of %s", dir, module)
	}

	// The name an unnamed import binds is the imported package's clause. For a
	// package of this module it is read off the source; for any other it is the
	// convention, which a package that breaks it is imported by name to avoid.
	clauses := map[string]string{}
	for p, src := range sources {
		clauses[p] = src.name
	}
	decls := map[ref]*decl{}
	for _, src := range sources {
		for _, f := range src.files {
			f.imports = importNames(f.ast, clauses)
			for _, d := range declare(src.path, f, fset) {
				key := ref{d.pkg, d.name}
				if _, twice := decls[key]; twice {
					continue
				}
				decls[key] = d
			}
		}
	}

	r := &resolver{module: module, decls: decls, done: map[ref]ref{}, how: map[ref]How{}}
	c := &Catalog{Module: module, Dir: dir, index: map[string]map[string]Symbol{}}
	paths := make([]string, 0, len(sources))
	for p := range sources {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		pkg := Package{Path: p}
		pkg.Bridge, _ = BridgeTarget(sources[p].doc)
		c.index[p] = map[string]Symbol{}
		for key, d := range decls {
			if key.pkg != p {
				continue
			}
			canonical := r.resolve(key)
			s := Symbol{
				Package: d.pkg, Name: d.name, Kind: d.kind, How: r.how[key],
				Canonical: canonical.pkg, CanonicalName: canonical.name,
				File: d.file, Line: d.line,
			}
			pkg.Symbols = append(pkg.Symbols, s)
			c.index[p][d.name] = s
		}
		sort.Slice(pkg.Symbols, func(i, j int) bool { return pkg.Symbols[i].Name < pkg.Symbols[j].Name })
		c.Packages = append(c.Packages, pkg)
	}
	return c, nil
}

// modulePath reads the module line of dir/go.mod.
func modulePath(dir string) (string, error) {
	body, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("catalog: %w", err)
	}
	module := gomod.Parse(string(body)).Path
	if module == "" {
		return "", fmt.Errorf("catalog: %s/go.mod declares no module path", dir)
	}
	return module, nil
}

// importNames maps the name each import of a file binds to its path.
func importNames(f *ast.File, clauses map[string]string) map[string]string {
	out := map[string]string{}
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		name := gen.AssumedImportName(p)
		if clause, known := clauses[p]; known {
			name = clause
		}
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if name == "_" || name == "." {
			continue
		}
		out[name] = p
	}
	return out
}

// declare reads the exported package-level declarations of one file.
func declare(pkg string, f *parsedFile, fset *token.FileSet) []*decl {
	var out []*decl
	at := func(n ast.Node) int { return fset.Position(n.Pos()).Line }
	add := func(name *ast.Ident, kind Kind, how How, target ref) *decl {
		d := &decl{pkg: pkg, name: name.Name, kind: kind, how: how, target: target, file: f.rel, line: at(name), in: f}
		out = append(out, d)
		return d
	}

	for _, node := range f.ast.Decls {
		switch d := node.(type) {
		case *ast.FuncDecl:
			if d.Recv != nil || !d.Name.IsExported() {
				continue
			}
			if target, ok := forwards(pkg, d, f.imports); ok {
				add(d.Name, Func, Forward, target).fn = d
				continue
			}
			add(d.Name, Func, Declared, ref{})

		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if !s.Name.IsExported() {
						continue
					}
					if target, ok := aliases(pkg, s, f.imports); ok {
						add(s.Name, Type, Alias, target)
						continue
					}
					add(s.Name, Type, Declared, ref{})

				case *ast.ValueSpec:
					kind := Var
					if d.Tok == token.CONST {
						kind = Const
					}
					for i, name := range s.Names {
						if !name.IsExported() {
							continue
						}
						// A declared type, or a value list that does not pair
						// one value with each name, makes this a value of its
						// own: a conversion, an iota, a tuple from a call.
						if s.Type == nil && len(s.Values) == len(s.Names) {
							if target, ok := reference(pkg, s.Values[i], f.imports); ok {
								add(name, kind, Alias, target)
								continue
							}
						}
						add(name, kind, Declared, ref{})
					}
				}
			}
		}
	}
	return out
}

// reference reads an expression that names one package-level symbol: an
// identifier of this package or a selector on an import.
func reference(pkg string, e ast.Expr, imports map[string]string) (ref, bool) {
	switch x := e.(type) {
	case *ast.Ident:
		return ref{pkg, x.Name}, true
	case *ast.SelectorExpr:
		id, ok := x.X.(*ast.Ident)
		if !ok {
			return ref{}, false
		}
		p, imported := imports[id.Name]
		if !imported {
			return ref{}, false
		}
		return ref{p, x.Sel.Name}, true
	case *ast.ParenExpr:
		return reference(pkg, x.X, imports)
	}
	return ref{}, false
}

// aliases reads `type X = p.Y` and its generic form, `type X[T any] = p.Y[T]`,
// where the parameters pass through unchanged and in order. An alias of an
// instantiation, a pointer or a composite names no single symbol.
func aliases(pkg string, s *ast.TypeSpec, imports map[string]string) (ref, bool) {
	if !s.Assign.IsValid() {
		return ref{}, false
	}
	base, args := unindex(s.Type)
	if !sameNames(fieldNames(s.TypeParams), args) {
		return ref{}, false
	}
	return reference(pkg, base, imports)
}

// forwards reads a function whose body is one call to another function with
// the function's own parameters, in order, and with its own type parameters if
// it names any -- returned when there are results and discarded when there are
// none.
func forwards(pkg string, fn *ast.FuncDecl, imports map[string]string) (ref, bool) {
	if fn.Body == nil || len(fn.Body.List) != 1 {
		return ref{}, false
	}
	var call *ast.CallExpr
	switch stmt := fn.Body.List[0].(type) {
	case *ast.ReturnStmt:
		if fn.Type.Results == nil || len(stmt.Results) != 1 {
			return ref{}, false
		}
		call, _ = stmt.Results[0].(*ast.CallExpr)
	case *ast.ExprStmt:
		if fn.Type.Results != nil {
			return ref{}, false
		}
		call, _ = stmt.X.(*ast.CallExpr)
	}
	if call == nil {
		return ref{}, false
	}

	base, typeArgs := unindex(call.Fun)
	if len(typeArgs) > 0 && !sameNames(fieldNames(fn.Type.TypeParams), typeArgs) {
		return ref{}, false
	}

	params := fieldNames(fn.Type.Params)
	if len(params) != len(call.Args) {
		return ref{}, false
	}
	for i, arg := range call.Args {
		id, ok := arg.(*ast.Ident)
		if !ok || id.Name != params[i] || id.Name == "_" {
			return ref{}, false
		}
	}
	if variadic(fn.Type) != call.Ellipsis.IsValid() {
		return ref{}, false
	}
	return reference(pkg, base, imports)
}

// unindex splits an instantiation into the generic and its type arguments.
func unindex(e ast.Expr) (ast.Expr, []ast.Expr) {
	switch x := e.(type) {
	case *ast.IndexExpr:
		return x.X, []ast.Expr{x.Index}
	case *ast.IndexListExpr:
		return x.X, x.Indices
	}
	return e, nil
}

// fieldNames lists the names a field list declares, one per name, with an
// unnamed field as the empty string.
func fieldNames(fields *ast.FieldList) []string {
	if fields == nil {
		return nil
	}
	var out []string
	for _, field := range fields.List {
		if len(field.Names) == 0 {
			out = append(out, "")
			continue
		}
		for _, name := range field.Names {
			out = append(out, name.Name)
		}
	}
	return out
}

// sameNames reports whether exprs are exactly the identifiers names, in order.
func sameNames(names []string, exprs []ast.Expr) bool {
	if len(names) != len(exprs) {
		return false
	}
	for i, e := range exprs {
		id, ok := e.(*ast.Ident)
		if !ok || id.Name != names[i] || names[i] == "" {
			return false
		}
	}
	return true
}

func variadic(fn *ast.FuncType) bool {
	if fn.Params == nil || len(fn.Params.List) == 0 {
		return false
	}
	_, dots := fn.Params.List[len(fn.Params.List)-1].Type.(*ast.Ellipsis)
	return dots
}

// resolver follows each declaration to the symbol it ends at.
type resolver struct {
	module string
	decls  map[ref]*decl
	done   map[ref]ref
	how    map[ref]How
}

// resolve answers the canonical spelling of an exported declaration.
//
// A chain is followed while it stays inside the framework -- kernel.Kernel is
// foundation.Application, which foundation declares -- and stops at the first
// symbol that is the framework's own or outside it. A target a project cannot
// import (an internal package, an unexported name) makes the declaration the
// framework's own, because there is nothing else to write.
func (r *resolver) resolve(key ref) ref {
	if out, ok := r.done[key]; ok {
		return out
	}
	// Recorded before following, so a cycle -- which does not compile, and is
	// guarded against anyway -- ends at the declaration itself.
	r.done[key], r.how[key] = key, Declared

	d := r.decls[key]
	if d == nil || d.how == Declared {
		return key
	}
	target := d.target
	inside := r.inside(target.pkg)
	switch {
	case !token.IsExported(target.name):
		return key
	case inside && isInternal(target.pkg):
		return key
	case inside:
		if r.decls[target] == nil {
			return key
		}
		target = r.resolve(target)
	}
	if d.how == Forward && !r.inside(target.pkg) && !r.transparent(d) {
		return key
	}
	r.done[key], r.how[key] = target, d.how
	return target
}

// transparent reports whether a forward's signature names only what the
// function it calls could also name: predeclared types, its own type
// parameters, and types that resolve outside the framework.
//
// A function outside the framework cannot mention a type the framework
// declares, so a forward whose signature does is translating between the two
// rather than calling through -- the envelope, not the alias.
func (r *resolver) transparent(d *decl) bool {
	typeParams := map[string]bool{}
	for _, name := range fieldNames(d.fn.Type.TypeParams) {
		typeParams[name] = true
	}
	return r.fieldsLeave(d, d.fn.Type.Params, typeParams) && r.fieldsLeave(d, d.fn.Type.Results, typeParams)
}

// fieldsLeave reports whether every type a field list names resolves outside
// the framework. The names of the fields are not types and are not read.
func (r *resolver) fieldsLeave(d *decl, fields *ast.FieldList, typeParams map[string]bool) bool {
	if fields == nil {
		return true
	}
	for _, field := range fields.List {
		if !r.typeLeaves(d, field.Type, typeParams) {
			return false
		}
	}
	return true
}

// typeLeaves walks one type expression of a forward's signature.
func (r *resolver) typeLeaves(d *decl, e ast.Expr, typeParams map[string]bool) bool {
	switch x := e.(type) {
	case *ast.Ident:
		return typeParams[x.Name] || predeclared[x.Name] || r.leaves(ref{d.pkg, x.Name})
	case *ast.SelectorExpr:
		id, ok := x.X.(*ast.Ident)
		if !ok {
			return false
		}
		p, imported := d.in.imports[id.Name]
		if !imported {
			return false
		}
		return !r.inside(p) || r.leaves(ref{p, x.Sel.Name})
	case *ast.StarExpr:
		return r.typeLeaves(d, x.X, typeParams)
	case *ast.ParenExpr:
		return r.typeLeaves(d, x.X, typeParams)
	case *ast.Ellipsis:
		return r.typeLeaves(d, x.Elt, typeParams)
	case *ast.ArrayType:
		return r.typeLeaves(d, x.Elt, typeParams)
	case *ast.MapType:
		return r.typeLeaves(d, x.Key, typeParams) && r.typeLeaves(d, x.Value, typeParams)
	case *ast.ChanType:
		return r.typeLeaves(d, x.Value, typeParams)
	case *ast.FuncType:
		return r.fieldsLeave(d, x.Params, typeParams) && r.fieldsLeave(d, x.Results, typeParams)
	case *ast.StructType:
		return r.fieldsLeave(d, x.Fields, typeParams)
	case *ast.InterfaceType:
		return r.fieldsLeave(d, x.Methods, typeParams)
	case *ast.IndexExpr:
		return r.typeLeaves(d, x.X, typeParams) && r.typeLeaves(d, x.Index, typeParams)
	case *ast.IndexListExpr:
		for _, index := range x.Indices {
			if !r.typeLeaves(d, index, typeParams) {
				return false
			}
		}
		return r.typeLeaves(d, x.X, typeParams)
	}
	return false
}

// leaves reports whether a framework type resolves to something outside the
// framework.
func (r *resolver) leaves(t ref) bool {
	if r.decls[t] == nil {
		return false
	}
	return !r.inside(r.resolve(t).pkg)
}

func (r *resolver) inside(pkg string) bool {
	_, ok := gomod.Under(pkg, r.module)
	return ok
}

func isInternal(pkg string) bool {
	return strings.HasSuffix(pkg, "/internal") || strings.Contains(pkg, "/internal/")
}

// predeclared are the type names a signature can use without declaring them.
var predeclared = map[string]bool{
	"any": true, "bool": true, "byte": true, "comparable": true, "complex64": true, "complex128": true,
	"error": true, "float32": true, "float64": true, "int": true, "int8": true, "int16": true,
	"int32": true, "int64": true, "rune": true, "string": true, "uint": true, "uint8": true,
	"uint16": true, "uint32": true, "uint64": true, "uintptr": true,
}

// Short is a path written relative to the framework or hesape module, the way
// a person reading a table wants it: framework/security, hesape/auth.
func Short(p string) string {
	if rest, ok := strings.CutPrefix(p, "github.com/arandu-io/"); ok {
		return rest
	}
	return p
}
