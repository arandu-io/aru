package lsp

import (
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// targetKind is what the cursor was found to be sitting on.
type targetKind int

const (
	targetNone targetKind = iota
	// targetQualified is `package.Name` written where Go is written.
	targetQualified
	// targetViewName is the argument of @extends or @include.
	targetViewName
)

// cursorTarget is the thing under the cursor, named but not yet resolved.
//
// Deciding what was clicked and deciding where it lives are kept apart because
// only the second one touches the disk: a cursor on ordinary markup is answered
// without a single stat.
type cursorTarget struct {
	kind      targetKind
	qualifier string
	name      string
	viewName  string
}

// targetAt reads what the cursor is on.
//
// The two contexts are the two ways a view names something declared elsewhere.
// A directive argument is a view name, and a qualified identifier inside an
// interpolation, a directive's arguments or an `@go` block is a package member.
// Everywhere else -- text, a tag, an attribute value, a comment -- is markup,
// and markup names nothing.
func targetAt(source string, at position) cursorTarget {
	prefix, ok := sourcePrefixAtUTF16Position(source, at)
	if !ok {
		return cursorTarget{}
	}
	offset := len(prefix)

	if target, found := viewNameTargetAt(source, offset); found {
		return target
	}
	if !insideGoExpression(source, offset) {
		return cursorTarget{}
	}
	return qualifiedTargetAt(source, offset)
}

// viewNameTargetAt reads the quoted argument of @extends or @include when the
// cursor is inside its parentheses.
//
// The cursor has to be within the parentheses rather than anywhere on the line:
// `@include('parts.row', components.RowProps{…})` names two different things,
// and the second one is resolved as an identifier a few lines below.
func viewNameTargetAt(source string, offset int) (cursorTarget, bool) {
	lineStart := strings.LastIndexByte(source[:offset], '\n') + 1
	lineEnd := strings.IndexByte(source[lineStart:], '\n')
	if lineEnd < 0 {
		lineEnd = len(source)
	} else {
		lineEnd += lineStart
	}
	line := source[lineStart:lineEnd]

	indent := len(line) - len(strings.TrimLeft(line, " \t"))
	for _, directive := range []string{"@extends(", "@include("} {
		if !strings.HasPrefix(line[indent:], directive) {
			continue
		}
		open := lineStart + indent + len(directive)
		close := strings.LastIndexByte(line, ')')
		if close < 0 {
			return cursorTarget{}, false
		}
		close += lineStart
		if offset < open || offset > close {
			return cursorTarget{}, false
		}
		name := strings.TrimSpace(strings.SplitN(source[open:close], ",", 2)[0])
		name = strings.Trim(name, `'"`)
		if name == "" {
			return cursorTarget{}, false
		}
		return cursorTarget{kind: targetViewName, viewName: name}, true
	}
	return cursorTarget{}, false
}

// qualifiedTargetAt reads `package.Name` around the offset.
//
// It expands in both directions, because an editor sends where the pointer was
// and that is as often the start or the end of a word as its middle, and it
// answers for either half: clicking `components` and clicking `Button` are the
// same question about the same call.
func qualifiedTargetAt(source string, offset int) cursorTarget {
	start, end := offset, offset
	for end < len(source) && identifierByte(source[end]) {
		end++
	}
	for start > 0 && identifierByte(source[start-1]) {
		start--
	}
	word := source[start:end]
	if word == "" {
		return cursorTarget{}
	}

	if start > 0 && source[start-1] == '.' {
		qualifier := identifierBefore(source, start-1)
		return exportedTarget(qualifier, word)
	}
	if end < len(source) && source[end] == '.' {
		member := identifierAfter(source, end+1)
		return exportedTarget(word, member)
	}
	return cursorTarget{}
}

// exportedTarget accepts the pair only when it can name a package member.
//
// An unexported name is not reachable from a view at all, so resolving one
// would mean opening a file for a call that does not compile.
func exportedTarget(qualifier, name string) cursorTarget {
	if qualifier == "" || name == "" || !exportedName(name) {
		return cursorTarget{}
	}
	return cursorTarget{kind: targetQualified, qualifier: qualifier, name: name}
}

func exportedName(name string) bool {
	return name != "" && name[0] >= 'A' && name[0] <= 'Z'
}

func identifierByte(value byte) bool {
	return value == '_' ||
		value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9'
}

func identifierBefore(source string, end int) string {
	start := end
	for start > 0 && identifierByte(source[start-1]) {
		start--
	}
	return source[start:end]
}

func identifierAfter(source string, start int) string {
	end := start
	for end < len(source) && identifierByte(source[end]) {
		end++
	}
	return source[start:end]
}

// insideGoExpression reports whether the offset falls where a view writes Go.
//
// Three places do: an interpolation, the arguments of a directive, and an `@go`
// block. A kyse comment is checked before both interpolation forms because it
// opens with the same two braces as the escaped one -- the same precedence the
// compiler's own reader uses, and getting it the other way round would treat a
// note somebody wrote as code.
//
// This walks the buffer rather than the parse tree because the buffer is a
// document being typed. kyse.Parse reports every problem it finds and returns
// no tree at all when it finds one, and a view is unparsable for most of the
// time a person spends editing it -- which is exactly when the editor is asking.
func insideGoExpression(source string, offset int) bool {
	if offset < 0 || offset > len(source) {
		return false
	}
	if insideGoBlock(source, offset) {
		return true
	}
	if insideDirectiveArguments(source, offset) {
		return true
	}

	for at := 0; at < len(source); {
		rest := source[at:]
		switch {
		case strings.HasPrefix(rest, "{{--"):
			end := strings.Index(rest, "--}}")
			if end < 0 {
				return false
			}
			at += end + 4
		case strings.HasPrefix(rest, "{!!"):
			end := strings.Index(rest[3:], "!!}")
			if end < 0 {
				return false
			}
			if offset > at+3 && offset <= at+3+end {
				return true
			}
			at += 3 + end + 3
		case strings.HasPrefix(rest, "{{"):
			end := strings.Index(rest[2:], "}}")
			if end < 0 {
				return false
			}
			if offset > at+2 && offset <= at+2+end {
				return true
			}
			at += 2 + end + 2
		default:
			at++
		}
	}
	return false
}

// insideGoBlock reports whether the offset is in an `@go … @endgo` body, which
// is Go copied verbatim into the generated file.
func insideGoBlock(source string, offset int) bool {
	open := false
	at := 0
	for at < len(source) {
		end := strings.IndexByte(source[at:], '\n')
		if end < 0 {
			end = len(source)
		} else {
			end += at
		}
		switch strings.TrimSpace(source[at:end]) {
		case "@go":
			open = true
		case "@endgo":
			if offset <= end {
				return false
			}
			open = false
		default:
			if open && offset >= at && offset <= end {
				return true
			}
		}
		at = end + 1
	}
	return false
}

// insideDirectiveArguments reports whether the offset is between the
// parentheses of a directive, which hold a Go expression: `@if(len(.Rows) > 0)`
// and `@include('parts.row', components.RowProps{…})` both do.
func insideDirectiveArguments(source string, offset int) bool {
	lineStart := strings.LastIndexByte(source[:offset], '\n') + 1
	lineEnd := strings.IndexByte(source[lineStart:], '\n')
	if lineEnd < 0 {
		lineEnd = len(source)
	} else {
		lineEnd += lineStart
	}
	line := source[lineStart:lineEnd]

	indent := len(line) - len(strings.TrimLeft(line, " \t"))
	if indent >= len(line) || line[indent] != '@' {
		return false
	}
	open := strings.IndexByte(line, '(')
	closed := strings.LastIndexByte(line, ')')
	if open < 0 || closed < open {
		return false
	}
	return offset > lineStart+open && offset <= lineStart+closed
}

// viewImports maps the name a view refers to a package by to its import path.
//
// It reads the import block the same way the compiler's reader does -- from the
// package clause to the first line that is neither an import nor blank -- and
// for the same reason it walks the buffer rather than the tree: the block is
// still there and still correct while the markup below it is halfway through a
// keystroke.
func viewImports(source string) map[string]string {
	imports := map[string]string{}
	block := false
	seenPackage := false
	for _, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case block:
			if trimmed == ")" {
				return imports
			}
			recordImport(imports, trimmed)
		case trimmed == "" || strings.HasPrefix(trimmed, "//"):
			continue
		case strings.HasPrefix(trimmed, "package "):
			seenPackage = true
		case !seenPackage:
			continue
		case trimmed == "import (":
			block = true
		case strings.HasPrefix(trimmed, "import "):
			recordImport(imports, strings.TrimSpace(strings.TrimPrefix(trimmed, "import")))
		default:
			return imports
		}
	}
	return imports
}

func recordImport(imports map[string]string, spec string) {
	if spec == "" {
		return
	}
	fields := strings.Fields(spec)
	quoted := fields[len(fields)-1]
	importPath, err := strconv.Unquote(quoted)
	if err != nil || importPath == "" {
		return
	}
	name := path.Base(importPath)
	if len(fields) > 1 {
		name = fields[0]
	}
	if name == "_" || name == "." {
		return
	}
	imports[name] = importPath
}

// definitionsFor resolves what the cursor is on to the files it is declared in.
//
// An empty result is the answer to a name this tree does not declare, and it is
// the answer on purpose: an editor that is handed a guess opens the wrong file
// and says nothing about having guessed.
func (p *project) definitionsFor(source string, at position) []protocolLocation {
	if p == nil {
		return []protocolLocation{}
	}
	target := targetAt(source, at)

	var found location
	var ok bool
	switch target.kind {
	case targetViewName:
		found, ok = p.viewLocation(target.viewName)
	case targetQualified:
		found, ok = p.memberLocation(viewImports(source), target)
	default:
		return []protocolLocation{}
	}
	if !ok {
		return []protocolLocation{}
	}

	uri, err := fileURIFromPath(found.file, nativeFilePathStyle())
	if err != nil {
		return []protocolLocation{}
	}
	line := max(found.line-1, 0)
	return []protocolLocation{{
		URI: uri,
		Range: protocolRange{
			Start: position{Line: line},
			End:   position{Line: line},
		},
	}}
}

// viewLocation maps a dotted view name to the file that declares it.
//
// The name is the path under resources/views with dots for separators, which is
// the same mapping the compiler uses to name a view, so `layouts.app` is
// `resources/views/layouts/app.kyse.go` and nothing else has to be searched.
func (p *project) viewLocation(name string) (location, bool) {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return location{}, false
	}
	rel := filepath.FromSlash(strings.ReplaceAll(name, ".", "/")) + ".kyse.go"
	file := filepath.Join(p.root, "resources", "views", rel)
	if !stampFile(file).found {
		return location{}, false
	}
	return location{file: file, line: 1}, true
}

// memberLocation resolves `package.Name` through the view's own import block.
//
// The import block is what decides which package a qualifier means, so a view
// that imports nothing resolves nothing -- which is correct, and is what keeps
// a word in a paragraph from opening a file because some package elsewhere
// happens to declare it.
func (p *project) memberLocation(imports map[string]string, target cursorTarget) (location, bool) {
	importPath, declared := imports[target.qualifier]
	if !declared {
		return location{}, false
	}
	dir, found := p.packageDir(importPath)
	if !found {
		return location{}, false
	}
	// A package answers to the name in its clause unless the view renamed it on
	// import. Checking it is what refuses a qualifier that only looks right
	// because the last element of the path happens to match.
	index := p.indexPackage(dir)
	if index == nil {
		return location{}, false
	}
	if path.Base(importPath) == target.qualifier && index.name != "" && index.name != target.qualifier {
		return location{}, false
	}
	return p.declarationOf(dir, target.name)
}
