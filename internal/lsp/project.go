package lsp

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/arandu-io/aru/internal/kyse"
)

// project is what the server knows about the tree it was initialized on.
//
// Everything it answers comes off the disk of that tree. Nothing is fetched,
// and no build is run: an editor asks on every keystroke, and a question that
// costs a network round trip or a compile is one the person experiences as the
// editor hanging.
//
// The caches below are revalidated by stat rather than by a timer, because a
// generated package changes when the generator runs and no notification of that
// reaches this process.
type project struct {
	root string

	module   *moduleFile
	moduleAt fileStamp

	packages map[string]*packageIndex

	// assetStamps is what every Go file of the tree looked like when it was
	// last read for RegisterAsset calls, and assetsByFile is what those reads
	// found. Keeping them apart is what lets the walk re-read only the files
	// that changed instead of the whole tree.
	assetStamps  map[string]fileStamp
	assetsByFile map[string][]string
}

func newProject(root string) *project {
	return &project{
		root:         root,
		packages:     map[string]*packageIndex{},
		assetStamps:  map[string]fileStamp{},
		assetsByFile: map[string][]string{},
	}
}

// fileStamp is what a file looked like when it was last read.
//
// Size and modification time together are what the Go toolchain itself uses to
// decide a cached answer is stale, and they cost one stat.
type fileStamp struct {
	size    int64
	modTime int64
	found   bool
}

func stampFile(path string) fileStamp {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{size: info.Size(), modTime: info.ModTime().UnixNano(), found: true}
}

// moduleFile is the part of a go.mod this server reads: what the tree is
// called, what it requires, and what it replaces.
type moduleFile struct {
	path     string
	versions map[string]string
	replaced map[string]string
}

// readModule reads go.mod, reusing the last read while the file is unchanged.
//
// The parse is deliberately small. What is needed is a module path, a version
// per requirement and the local replacements, and a full go.mod reader would be
// a dependency this module does not take.
func (p *project) readModule() *moduleFile {
	path := filepath.Join(p.root, "go.mod")
	stamp := stampFile(path)
	if p.module != nil && stamp == p.moduleAt {
		return p.module
	}
	body, err := os.ReadFile(path)
	if err != nil {
		p.module, p.moduleAt = nil, stamp
		return nil
	}
	p.module, p.moduleAt = parseModule(string(body)), stamp
	return p.module
}

func parseModule(source string) *moduleFile {
	module := &moduleFile{versions: map[string]string{}, replaced: map[string]string{}}
	block := ""
	for _, line := range strings.Split(source, "\n") {
		if at := strings.Index(line, "//"); at >= 0 {
			line = line[:at]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == ")" {
			block = ""
			continue
		}
		if block == "" {
			keyword, rest, found := strings.Cut(line, " ")
			if !found {
				continue
			}
			rest = strings.TrimSpace(rest)
			if rest == "(" {
				block = keyword
				continue
			}
			module.record(keyword, rest)
			continue
		}
		module.record(block, line)
	}
	return module
}

func (m *moduleFile) record(keyword, rest string) {
	fields := strings.Fields(rest)
	switch keyword {
	case "module":
		if len(fields) >= 1 {
			m.path = fields[0]
		}
	case "require":
		if len(fields) >= 2 {
			m.versions[fields[0]] = fields[1]
		}
	case "replace":
		// `old => new` and `old v1 => new v2` both end with the replacement,
		// and only a replacement that is a directory is usable here: a module
		// swapped for another module is still found through the cache.
		at := -1
		for i, field := range fields {
			if field == "=>" {
				at = i
			}
		}
		if at < 0 || at+1 >= len(fields) || len(fields) == 0 {
			return
		}
		target := fields[at+1]
		if strings.HasPrefix(target, ".") || strings.HasPrefix(target, "/") || filepath.IsAbs(target) {
			m.replaced[fields[0]] = target
		}
	}
}

// packageDir maps an import path to the directory that holds it, or reports
// that it is not on this disk.
//
// Three places are looked in, in the order the toolchain resolves them: the
// tree itself, its vendor directory, and the module cache. The cache is read
// directly rather than through `go list`, because starting the toolchain for
// every go-to-definition would cost more than the whole rest of the answer.
func (p *project) packageDir(importPath string) (string, bool) {
	module := p.readModule()
	if module == nil || importPath == "" {
		return "", false
	}

	if module.path != "" {
		if rest, ok := underImportPath(importPath, module.path); ok {
			return existingDir(filepath.Join(p.root, filepath.FromSlash(rest)))
		}
	}
	for prefix, target := range module.replaced {
		if rest, ok := underImportPath(importPath, prefix); ok {
			dir := target
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(p.root, filepath.FromSlash(dir))
			}
			return existingDir(filepath.Join(dir, filepath.FromSlash(rest)))
		}
	}
	if dir, ok := existingDir(filepath.Join(p.root, "vendor", filepath.FromSlash(importPath))); ok {
		return dir, true
	}

	cache := moduleCache()
	if cache == "" {
		return "", false
	}
	for prefix, version := range module.versions {
		rest, ok := underImportPath(importPath, prefix)
		if !ok {
			continue
		}
		at := filepath.Join(cache, filepath.FromSlash(escapeModulePath(prefix)+"@"+version), filepath.FromSlash(rest))
		if dir, ok := existingDir(at); ok {
			return dir, true
		}
	}
	return "", false
}

// underImportPath reports whether the import path is the prefix or lies inside
// it, and returns what is left over.
//
// Comparing on the slash boundary is what keeps `example.com/kyseless` from
// resolving through a requirement on `example.com/kyse`.
func underImportPath(importPath, prefix string) (string, bool) {
	if importPath == prefix {
		return "", true
	}
	if rest, found := strings.CutPrefix(importPath, prefix+"/"); found {
		return rest, true
	}
	return "", false
}

func existingDir(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return path, true
}

func moduleCache() string {
	if cache := os.Getenv("GOMODCACHE"); cache != "" {
		return cache
	}
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		return filepath.Join(strings.Split(gopath, string(os.PathListSeparator))[0], "pkg", "mod")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "go", "pkg", "mod")
}

// escapeModulePath is the module cache's spelling of a path: an upper-case
// letter becomes an exclamation mark and its lower-case form.
//
// The cache has to name modules on a filesystem that does not distinguish case,
// so `Sirupsen` and `sirupsen` would be one directory without it.
func escapeModulePath(importPath string) string {
	var out strings.Builder
	for _, r := range importPath {
		if r >= 'A' && r <= 'Z' {
			out.WriteByte('!')
			out.WriteRune(r - 'A' + 'a')
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// packageIndex is what one directory of Go declares, as the editor needs it.
type packageIndex struct {
	name    string
	symbols map[string]symbol
	names   []string
	stamp   string
}

// symbol is one exported declaration and where a person should be sent for it.
type symbol struct {
	name string
	// param is the type of the single parameter an exported function takes,
	// empty when it takes none or several. For a kyse component that is its
	// props struct, which is the component's whole contract.
	param string
	file  string
	line  int
	// doc is the first line of the declaration's doc comment.
	doc string
}

// indexPackage reads the exported functions of a directory, reusing the last
// read while nothing in the directory has changed.
//
// The stamp covers every file's name, size and time rather than the directory's
// own timestamp: editing a file in place leaves the directory untouched on
// every filesystem this runs on, and a component gains and loses props by
// exactly that edit.
func (p *project) indexPackage(dir string) *packageIndex {
	entries, err := os.ReadDir(dir)
	if err != nil {
		delete(p.packages, dir)
		return nil
	}

	var stamp strings.Builder
	sources := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !indexableGoFile(name) {
			continue
		}
		sources = append(sources, name)
		stamp.WriteString(name)
		if info, err := entry.Info(); err == nil {
			fmt.Fprintf(&stamp, ":%d:%d", info.Size(), info.ModTime().UnixNano())
		}
		stamp.WriteString("\n")
	}

	if cached, found := p.packages[dir]; found && cached.stamp == stamp.String() {
		return cached
	}

	index := &packageIndex{symbols: map[string]symbol{}, stamp: stamp.String()}
	for _, name := range sources {
		readGoFile(filepath.Join(dir, name), index)
	}
	for name := range index.symbols {
		index.names = append(index.names, name)
	}
	sort.Strings(index.names)
	p.packages[dir] = index
	return index
}

// indexableGoFile reports whether a file is Go this server should parse.
//
// A `.kyse.go` ends in `.go` and is not Go: it carries markup below the package
// clause, and go/parser refuses it. The compiler skips it by build tag, and the
// declarations it contains are in the generated file beside it.
func indexableGoFile(name string) bool {
	return strings.HasSuffix(name, ".go") &&
		!strings.HasSuffix(name, "_test.go") &&
		!strings.HasSuffix(name, ".kyse.go")
}

// readGoFile adds one file's exported functions to the index.
//
// Positions are read unadjusted. A generated view carries `//line` directives
// pointing back at the source it came from, and go/parser applies them -- which
// is right for a compiler error and wrong here, because the path in a directive
// is written relative to the module root and joining it to the file's own
// directory names somewhere that does not exist. Where the person should
// actually be sent is decided below, from the source file itself.
func readGoFile(path string, index *packageIndex) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution|parser.ParseComments)
	if err != nil {
		return
	}
	if index.name == "" {
		index.name = file.Name.Name
	}
	for _, decl := range file.Decls {
		function, ok := decl.(*ast.FuncDecl)
		if !ok || function.Recv != nil || !function.Name.IsExported() {
			continue
		}
		at := fset.PositionFor(function.Name.Pos(), false)
		index.symbols[function.Name.Name] = symbol{
			name:  function.Name.Name,
			param: soleParameterType(function),
			file:  path,
			line:  at.Line,
			doc:   firstDocLine(function.Doc),
		}
	}
}

// soleParameterType names the type of a function that takes exactly one
// argument, which is the shape every kyse component has.
func soleParameterType(function *ast.FuncDecl) string {
	params := function.Type.Params
	if params == nil || len(params.List) != 1 || len(params.List[0].Names) > 1 {
		return ""
	}
	name, ok := params.List[0].Type.(*ast.Ident)
	if !ok {
		return ""
	}
	return name.Name
}

func firstDocLine(doc *ast.CommentGroup) string {
	if doc == nil {
		return ""
	}
	for _, line := range strings.Split(doc.Text(), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// location is where a symbol is declared, as the editor should open it.
type location struct {
	file string
	line int
}

// declarationOf resolves one exported name of a package to the file a person
// should be shown.
//
// A generated component is not that file. `aru view:build` writes the function
// into a `.go` whose header says not to edit it, from a `.kyse.go` beside it
// that holds the props and the markup -- so when that source exists, the answer
// is the line its props are declared on. Sending someone to generated output
// means the first thing they read is an instruction not to change it.
func (p *project) declarationOf(dir, name string) (location, bool) {
	index := p.indexPackage(dir)
	if index == nil {
		return location{}, false
	}
	found, ok := index.symbols[name]
	if !ok {
		return location{}, false
	}

	source := strings.TrimSuffix(found.file, ".go") + ".kyse.go"
	if _, err := os.Stat(source); err != nil {
		return location{file: found.file, line: found.line}, true
	}
	return location{file: source, line: kyseDeclarationLine(source, found.param)}, true
}

// kyseDeclarationLine finds where a type is declared inside a view's `@go`
// blocks.
//
// The view is read with the compiler's own parser rather than a second one:
// a block records the line its body starts on, so the Nth line of the body is
// the (Line+N-1)th of the file, and the offset of the declaration within it is
// the whole of the arithmetic.
//
// A view whose props cannot be found still resolves, to its first line. The
// file is the answer to "where does this component live"; the line only decides
// where the cursor lands in it.
func kyseDeclarationLine(source, typeName string) int {
	if typeName == "" {
		return 1
	}
	body, err := os.ReadFile(source)
	if err != nil {
		return 1
	}
	file, err := kyse.Parse(source, string(body))
	if err != nil {
		return 1
	}
	for _, block := range file.Go {
		for offset, line := range strings.Split(block.Body, "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "type "+typeName) {
				continue
			}
			rest := strings.TrimPrefix(trimmed, "type "+typeName)
			if rest == "" || strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, "\t") {
				return block.Line + offset
			}
		}
	}
	return 1
}
