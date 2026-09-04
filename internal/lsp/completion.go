package lsp

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	completionItemKindFunction = 3
	completionItemKindFile     = 17
)

// assetCall is the expression whose argument view.URL will accept, and the
// reason completing it is worth the walk below: an unregistered name is not a
// broken link, it is a panic, so a typo takes the page down.
const assetCall = "view.URL("

// memberCompletionAt reads the qualifier of a `package.` the cursor sits after.
//
// The partially typed member is not needed: the editor filters the list it is
// given. What matters is that the qualifier resolves, because a list drawn from
// the wrong package is a list of names that do not compile.
func memberCompletionAt(source string, at position) (string, bool) {
	prefix, ok := sourcePrefixAtUTF16Position(source, at)
	if !ok || !insideGoExpression(source, len(prefix)) {
		return "", false
	}
	end := len(prefix)
	for end > 0 && identifierByte(prefix[end-1]) {
		end--
	}
	if end == 0 || prefix[end-1] != '.' {
		return "", false
	}
	qualifier := identifierBefore(prefix, end-1)
	if qualifier == "" {
		return "", false
	}
	// A field access reaches through a value, not a package: `.Page.Title` and
	// `feature.Title` are both spelled this way and neither names a package.
	if end-1-len(qualifier) > 0 && prefix[end-2-len(qualifier)] == '.' {
		return "", false
	}
	return qualifier, true
}

// assetCompletionAt reports whether the cursor is inside the string view.URL
// takes.
//
// Two conditions, and both are needed. The call has to be somewhere a view
// writes Go, because `view.URL("…")` typed into a paragraph is a sentence about
// the call and not the call. And the string has to still be open at the cursor:
// once it is closed the name is written, and offering the list again would
// replace an argument that is already right.
func assetCompletionAt(source string, at position) bool {
	prefix, ok := sourcePrefixAtUTF16Position(source, at)
	if !ok || !insideGoExpression(source, len(prefix)) {
		return false
	}
	line := prefix
	if start := strings.LastIndexByte(prefix, '\n'); start >= 0 {
		line = prefix[start+1:]
	}
	call := strings.LastIndex(line, assetCall)
	if call < 0 {
		return false
	}
	rest := strings.TrimLeft(line[call+len(assetCall):], " \t")
	return strings.HasPrefix(rest, `"`) && !strings.Contains(rest[1:], `"`)
}

// memberCompletionItems offers what a package the view imported declares.
func (p *project) memberCompletionItems(source, qualifier string) []completionItem {
	if p == nil {
		return nil
	}
	importPath, declared := viewImports(source)[qualifier]
	if !declared {
		return nil
	}
	dir, found := p.packageDir(importPath)
	if !found {
		return nil
	}
	index := p.indexPackage(dir)
	if index == nil {
		return nil
	}

	items := make([]completionItem, 0, len(index.names))
	for _, name := range index.names {
		found := index.symbols[name]
		item := completionItem{
			Label:         name,
			Kind:          completionItemKindFunction,
			Documentation: found.doc,
			InsertText:    name,
		}
		// A component's props type is its whole signature, and seeing it in the
		// popup is what tells a caller which struct literal to write next.
		if found.param != "" {
			item.Detail = found.param
		}
		items = append(items, item)
	}
	return items
}

// assetCompletionItems offers every name view.URL will answer to.
func (p *project) assetCompletionItems() []completionItem {
	if p == nil {
		return nil
	}
	names := p.assetNames()
	items := make([]completionItem, 0, len(names))
	for _, name := range names.sorted() {
		items = append(items, completionItem{
			Label:      name,
			Kind:       completionItemKindFile,
			Detail:     names[name],
			InsertText: name,
		})
	}
	return items
}

// assetSources maps every registered asset name to where it came from, which
// is what the popup shows beside it: a name a person does not recognise is
// answered by the file that put it there.
type assetSources map[string]string

func (a assetSources) sorted() []string {
	out := make([]string, 0, len(a))
	for name := range a {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// assetNames is the set of names view.URL accepts in this project.
//
// Two sources, and neither is written down here. The framework's own are read
// off the `//go:embed` line of the view package it resolves to, because that
// directive is the list of files compiled into the binary -- the directory
// beside it also holds sources that are not embedded, and offering one of those
// would suggest the exact argument that panics. The project's own are the
// literal names it passes to RegisterAsset.
func (p *project) assetNames() assetSources {
	names := assetSources{}
	if dir, found := p.packageDir(viewPackagePath); found {
		for _, name := range embeddedAssetNames(dir) {
			names[name] = "framework asset"
		}
	}
	for file, registered := range p.registeredAssets() {
		for _, name := range registered {
			names[name] = p.relativeTo(file)
		}
	}
	return names
}

// viewPackagePath is where the framework's embedded assets are declared.
const viewPackagePath = "github.com/arandu-io/hesape/view"

func embeddedAssetNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || !indexableGoFile(entry.Name()) {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil || !bytes.Contains(body, []byte("go:embed")) {
			continue
		}
		for _, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "//"))
			rest, found := strings.CutPrefix(trimmed, "go:embed ")
			if !found {
				continue
			}
			for _, pattern := range strings.Fields(rest) {
				if base := path.Base(pattern); base != "" && strings.Contains(base, ".") {
					names = append(names, base)
				}
			}
		}
	}
	return names
}

func (p *project) relativeTo(file string) string {
	rel, err := filepath.Rel(p.root, file)
	if err != nil {
		return file
	}
	return filepath.ToSlash(rel)
}

// registeredAssets returns, per file, the names that file registers.
//
// The walk visits every Go file of the tree and reads only the ones whose bytes
// have changed since the last answer -- a stat each, and a parse of the few
// that mention the call. Reading them all every time costs 150ms on a tree of
// thirteen hundred files, which a person experiences as the editor stalling;
// stat-only it is under ten.
func (p *project) registeredAssets() map[string][]string {
	seen := map[string]bool{}
	filepath.WalkDir(p.root, func(at string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if skippedAssetDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !indexableGoFile(entry.Name()) {
			return nil
		}
		seen[at] = true

		info, err := entry.Info()
		if err != nil {
			return nil
		}
		stamp := fileStamp{size: info.Size(), modTime: info.ModTime().UnixNano(), found: true}
		if p.assetStamps[at] == stamp {
			return nil
		}
		p.assetStamps[at] = stamp
		if names := readRegisteredAssets(at); len(names) > 0 {
			p.assetsByFile[at] = names
		} else {
			delete(p.assetsByFile, at)
		}
		return nil
	})

	for at := range p.assetStamps {
		if !seen[at] {
			delete(p.assetStamps, at)
			delete(p.assetsByFile, at)
		}
	}
	return p.assetsByFile
}

// skippedAssetDir names the directories no application registers an asset from.
//
// `storage` is build output and `vendor` is somebody else's module; the rest
// are not this tree's code at all. Walking them would cost the most time and
// find the least.
func skippedAssetDir(name string) bool {
	switch name {
	case ".git", "vendor", "node_modules", "testdata", "storage":
		return true
	}
	return false
}

// readRegisteredAssets reads the string literals one file passes to
// RegisterAsset.
//
// The byte check comes first so that the parse happens for the handful of files
// that mention the call rather than for the whole tree. A name built from a
// variable is not read: what it will be is not decidable here, and guessing
// would put a name in the list that view.URL rejects.
func readRegisteredAssets(path string) []string {
	body, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(body, []byte("RegisterAsset")) {
		return nil
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, body, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}

	var names []string
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "RegisterAsset" {
			return true
		}
		literal, ok := call.Args[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		if name, err := strconv.Unquote(literal.Value); err == nil && name != "" {
			names = append(names, name)
		}
		return true
	})
	return names
}
