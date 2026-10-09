package lsp

import (
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/arandu-io/aru/internal/doctor"
)

// Navigation along the edges of the map.
//
// Go source has a language server of its own, and every identifier in it is
// answered there. What this adds is what the types cannot say: that a string
// is the name of a route, that a registration in routes/ reaches a method, and
// that a policy is asked by this line of a service. So definition answers only
// on a string literal, where the Go server answers nothing, and references on
// a declaration answers the places the map read an edge from -- the
// registration that reaches an action, the call that asks a policy, the test
// that uses a model -- which are references in the sense the map has and the
// type checker does not.

// symbolKind values of the protocol this server answers with.
const (
	symbolKindMethod = 6
	symbolKindEvent  = 24
)

// documentSymbol is one entry of a document's outline.
type documentSymbol struct {
	Name           string        `json:"name"`
	Detail         string        `json:"detail,omitempty"`
	Kind           int           `json:"kind"`
	Range          protocolRange `json:"range"`
	SelectionRange protocolRange `json:"selectionRange"`
}

// symbolInformation is one answer to a workspace symbol query.
type symbolInformation struct {
	Name          string           `json:"name"`
	Kind          int              `json:"kind"`
	Location      protocolLocation `json:"location"`
	ContainerName string           `json:"containerName,omitempty"`
}

type referenceParams struct {
	TextDocument *textDocumentIdentifier `json:"textDocument"`
	Position     *completionPosition     `json:"position"`
	Context      *struct {
		IncludeDeclaration bool `json:"includeDeclaration"`
	} `json:"context"`
}

type documentSymbolParams struct {
	TextDocument *textDocumentIdentifier `json:"textDocument"`
}

type workspaceSymbolParams struct {
	Query *string `json:"query"`
}

// projectMap answers the map of the tree, or nil when there is no tree or it
// cannot be analyzed. Navigation that cannot read the map answers nothing.
func (p *project) projectMap() *doctor.ProjectMap {
	if p == nil {
		return nil
	}
	analysis, err := p.analysis()
	if err != nil || analysis.Map == nil {
		return nil
	}
	return analysis.Map
}

// relative answers the path of a document under the project root, with
// slashes, and false for a document outside it.
func (p *project) relative(uri string) (string, bool) {
	if p == nil {
		return "", false
	}
	path, err := pathFromFileURI(uri, nativeFilePathStyle())
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(p.root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// byteColumnAt answers the one-based line and byte column of a protocol
// position in a buffer.
func byteColumnAt(source string, at position) (int, int, bool) {
	prefix, ok := sourcePrefixAtUTF16Position(source, at)
	if !ok {
		return 0, 0, false
	}
	lineStart := strings.LastIndexByte(prefix, '\n') + 1
	return at.Line + 1, len(prefix) - lineStart + 1, true
}

// spans reports whether a one-based line and column falls inside a node.
func spans(node doctor.MapNode, line, column int) bool {
	if line < node.Line || line > node.EndLine {
		return false
	}
	if line == node.Line && column < node.Column {
		return false
	}
	return line != node.EndLine || column <= node.EndColumn
}

// locationOf turns a place of the map into one the protocol opens.
func locationOf(lines *sourceLines, file string, line, column, endLine, endColumn int) (protocolLocation, bool) {
	uri, err := lines.uri(file)
	if err != nil {
		return protocolLocation{}, false
	}
	return protocolLocation{URI: uri, Range: protocolRange{
		Start: lines.position(file, line, column),
		End:   lines.position(file, endLine, endColumn),
	}}, true
}

func nodeLocation(lines *sourceLines, node doctor.MapNode) (protocolLocation, bool) {
	return locationOf(lines, node.File, node.Line, node.Column, node.EndLine, node.EndColumn)
}

// routeNameCalls are the methods whose first argument names a route.
var routeNameCalls = map[string]bool{"URL": true, "RedirectRoute": true, "Route": true, "Name": true}

// mapDefinitionsInGoSource answers a string under the cursor that the map
// knows: a route name passed to ctx.URL, ctx.RedirectRoute or Name opens the
// action the route reaches, and any string of a registration in routes/ opens
// the actions that registration reaches.
func (p *project) mapDefinitionsInGoSource(uri, source string, at position) []protocolLocation {
	literal, method, ok := stringArgumentAt(source, at)
	if !ok {
		return []protocolLocation{}
	}
	m := p.projectMap()
	if m == nil {
		return []protocolLocation{}
	}

	routes := map[string]bool{}
	if routeNameCalls[method] {
		for _, node := range m.Nodes {
			if node.Kind == "route" && node.Name == literal {
				routes[node.ID] = true
			}
		}
	}
	if rel, inside := p.relative(uri); inside && strings.HasPrefix(rel, "routes/") {
		if line, column, ok := byteColumnAt(source, at); ok {
			for _, node := range m.Nodes {
				if node.Kind == "route" && node.File == rel && spans(node, line, column) {
					routes[node.ID] = true
				}
			}
		}
	}
	if len(routes) == 0 {
		return []protocolLocation{}
	}

	nodes := nodesByID(m)
	lines := newSourceLines(p.root)
	seen := map[string]bool{}
	var out []protocolLocation
	for _, edge := range m.Edges {
		if edge.Kind != doctor.EdgeRoutesTo || !routes[edge.From] || seen[edge.To] {
			continue
		}
		seen[edge.To] = true
		if target, found := nodes[edge.To]; found {
			if location, ok := locationOf(lines, target.File, target.Line, target.Column, target.Line, target.Column); ok {
				out = append(out, location)
			}
		}
	}
	sortLocations(out)
	return emptyIfNil(out)
}

// stringArgumentAt reads the string literal under the cursor and, when it is
// the first argument of a method call, the name of the method.
func stringArgumentAt(source string, at position) (string, string, bool) {
	prefix, ok := sourcePrefixAtUTF16Position(source, at)
	if !ok {
		return "", "", false
	}
	offset := len(prefix)
	fileSet := token.NewFileSet()
	file := fileSet.AddFile("", -1, len(source))
	var reader scanner.Scanner
	reader.Init(file, []byte(source), nil, 0)

	var window [3]scannedToken
	for {
		pos, kind, literal := reader.Scan()
		if kind == token.EOF {
			return "", "", false
		}
		start := file.Offset(pos)
		if kind == token.STRING && offset > start && offset < start+len(literal) {
			value, err := strconv.Unquote(literal)
			if err != nil {
				return "", "", false
			}
			method := ""
			if window[0].kind == token.PERIOD && window[1].kind == token.IDENT && window[2].kind == token.LPAREN {
				method = window[1].literal
			}
			return value, method, true
		}
		window[0], window[1], window[2] = window[1], window[2], scannedToken{kind: kind, literal: literal}
	}
}

// referencesAt answers the places the map read an edge of the declaration
// under the cursor from, outside the declaration's own file: for an action the
// registrations that reach it, for a request the actions and services that
// take it, for a policy the services that ask it, for a model the services
// that write through it, the migrations of its table and its tests, and for a
// view the actions that render it and the views that extend it.
func (p *project) referencesAt(uri, source string, at position, includeDeclaration bool) []protocolLocation {
	m := p.projectMap()
	rel, inside := p.relative(uri)
	if m == nil || !inside {
		return []protocolLocation{}
	}
	node, found := p.declarationAt(m, rel, source, at)
	if !found {
		return []protocolLocation{}
	}

	members := map[string]bool{node.ID: true}
	for _, candidate := range m.Nodes {
		if candidate.Parent == node.ID {
			members[candidate.ID] = true
		}
	}
	lines := newSourceLines(p.root)
	seen := map[string]bool{}
	var out []protocolLocation
	add := func(location protocolLocation) {
		key := location.URI + ":" + strconv.Itoa(location.Range.Start.Line) + ":" + strconv.Itoa(location.Range.Start.Character)
		if !seen[key] {
			seen[key] = true
			out = append(out, location)
		}
	}
	if includeDeclaration {
		if location, ok := nodeLocation(lines, node); ok {
			add(location)
		}
	}
	for _, edge := range m.Edges {
		if edge.At == nil || edge.At.File == node.File || !members[edge.From] && !members[edge.To] {
			continue
		}
		if location, ok := locationOf(lines, edge.At.File, edge.At.Line, edge.At.Column, edge.At.EndLine, edge.At.EndColumn); ok {
			add(location)
		}
	}
	sortLocations(out)
	return emptyIfNil(out)
}

// declarationAt finds the node of the map the cursor names: the view a view
// document is, or the one its @extends or @include names under the cursor;
// in Go, the action a method name declares, or the artifact of the file whose
// type or function name the cursor is on.
func (p *project) declarationAt(m *doctor.ProjectMap, rel, source string, at position) (doctor.MapNode, bool) {
	if strings.HasSuffix(rel, ".kyse.go") {
		target := rel
		if named := targetAt(source, at); named.kind == targetViewName {
			if found, ok := p.viewLocation(named.viewName); ok {
				if named, inside := p.relativePath(found.file); inside {
					target = named
				}
			}
		}
		for _, node := range m.Nodes {
			if node.Kind == "view" && node.File == target {
				return node, true
			}
		}
		return doctor.MapNode{}, false
	}

	prefix, ok := sourcePrefixAtUTF16Position(source, at)
	if !ok {
		return doctor.MapNode{}, false
	}
	offset := len(prefix)
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, rel, source, parser.SkipObjectResolution)
	if parsed == nil {
		return doctor.MapNode{}, false
	}
	_ = err // a file mid-edit still declares what parsed before the error
	covers := func(identifier *ast.Ident) bool {
		start := fileSet.Position(identifier.Pos()).Offset
		return offset >= start && offset <= start+len(identifier.Name)
	}

	label, onFile := "", false
	for _, decl := range parsed.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !covers(d.Name) {
				continue
			}
			if d.Recv != nil {
				label = receiverName(d) + "." + d.Name.Name
			} else {
				onFile = true
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				if typed, ok := spec.(*ast.TypeSpec); ok && covers(typed.Name) {
					onFile = true
				}
			}
		}
	}
	if label != "" {
		for _, node := range m.Nodes {
			if node.File == rel && node.Kind == "action" && node.Label == label {
				return node, true
			}
		}
		// A method that is not an action -- a service's Create, a helper of
		// the controller -- stands for the artifact of its file.
		onFile = true
	}
	if onFile {
		for _, node := range m.Nodes {
			if node.File == rel && fileArtifact(node) {
				return node, true
			}
		}
	}
	return doctor.MapNode{}, false
}

// relativePath answers an absolute path under the project root with slashes.
func (p *project) relativePath(path string) (string, bool) {
	rel, err := filepath.Rel(p.root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// fileArtifact reports whether a node stands for a whole file.
func fileArtifact(node doctor.MapNode) bool {
	switch node.Kind {
	case "action", "route", "feature", "diagnostic", "native-capability", "community-module", "native-screen", "native-target":
		return false
	}
	return true
}

func receiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	typ := fn.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	if index, ok := typ.(*ast.IndexExpr); ok {
		typ = index.X
	}
	if identifier, ok := typ.(*ast.Ident); ok {
		return identifier.Name
	}
	return ""
}

// documentSymbols answers the routes a routes file registers and the actions
// a controller declares, as the outline of the document.
func (p *project) documentSymbols(uri string) []documentSymbol {
	m := p.projectMap()
	rel, inside := p.relative(uri)
	out := []documentSymbol{}
	if m == nil || !inside {
		return out
	}
	reaching := routesReaching(m)
	lines := newSourceLines(p.root)
	for _, node := range m.Nodes {
		if node.File != rel || node.Kind != "route" && node.Kind != "action" {
			continue
		}
		location, ok := nodeLocation(lines, node)
		if !ok {
			continue
		}
		symbol := documentSymbol{Name: node.Label, Kind: symbolKindEvent, Detail: node.Name, Range: location.Range}
		symbol.SelectionRange = protocolRange{Start: location.Range.Start, End: location.Range.Start}
		if node.Kind == "action" {
			symbol.Kind = symbolKindMethod
			symbol.Detail = strings.Join(reaching[node.ID], ", ")
		}
		out = append(out, symbol)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Range.Start, out[j].Range.Start
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Character != b.Character {
			return a.Character < b.Character
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// workspaceSymbols answers every route and action whose label, route name or
// pattern holds the query, ignoring case. An empty query answers all of them.
func (p *project) workspaceSymbols(query string) []symbolInformation {
	m := p.projectMap()
	out := []symbolInformation{}
	if m == nil {
		return out
	}
	query = strings.ToLower(query)
	reaching := routesReaching(m)
	nodes := nodesByID(m)
	lines := newSourceLines(p.root)
	for _, node := range m.Nodes {
		if node.Kind != "route" && node.Kind != "action" {
			continue
		}
		haystack := strings.ToLower(node.Label + " " + node.Name + " " + node.Pattern)
		if query != "" && !strings.Contains(haystack, query) {
			continue
		}
		location, ok := nodeLocation(lines, node)
		if !ok {
			continue
		}
		symbol := symbolInformation{Name: node.Label, Kind: symbolKindEvent, Location: location, ContainerName: node.Name}
		if node.Kind == "action" {
			symbol.Kind = symbolKindMethod
			symbol.ContainerName = nodes[node.Parent].Label
			if routes := reaching[node.ID]; len(routes) > 0 {
				symbol.ContainerName += " (" + strings.Join(routes, ", ") + ")"
			}
		}
		out = append(out, symbol)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Location.URI < out[j].Location.URI
	})
	return out
}

// routesReaching maps each action to the labels of the routes that reach it.
func routesReaching(m *doctor.ProjectMap) map[string][]string {
	nodes := nodesByID(m)
	out := map[string][]string{}
	for _, edge := range m.Edges {
		if edge.Kind == doctor.EdgeRoutesTo {
			out[edge.To] = append(out[edge.To], nodes[edge.From].Label)
		}
	}
	for id := range out {
		sort.Strings(out[id])
	}
	return out
}

func nodesByID(m *doctor.ProjectMap) map[string]doctor.MapNode {
	out := make(map[string]doctor.MapNode, len(m.Nodes))
	for _, node := range m.Nodes {
		out[node.ID] = node
	}
	return out
}

func sortLocations(locations []protocolLocation) {
	sort.SliceStable(locations, func(i, j int) bool {
		a, b := locations[i], locations[j]
		if a.URI != b.URI {
			return a.URI < b.URI
		}
		if a.Range.Start.Line != b.Range.Start.Line {
			return a.Range.Start.Line < b.Range.Start.Line
		}
		return a.Range.Start.Character < b.Range.Start.Character
	})
}

func emptyIfNil(locations []protocolLocation) []protocolLocation {
	if locations == nil {
		return []protocolLocation{}
	}
	return locations
}
