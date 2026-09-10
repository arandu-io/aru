package doctor

import (
	"encoding/base64"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/arandu-io/aru/internal/gomod"
)

// Analysis is one Doctor load, with the findings and project graph derived
// from the same parsed project.
type Analysis struct {
	Findings []Finding    `json:"findings"`
	Graph    ProjectGraph `json:"graph"`
}

// ProjectGraph is the stable editor-facing map of an Arandu project.
type ProjectGraph struct {
	SchemaVersion int     `json:"schemaVersion"`
	Groups        []Group `json:"groups"`
	Nodes         []Node  `json:"nodes"`
	Edges         []Edge  `json:"edges"`
}

// Group is one ordered navigation section and the IDs of the nodes it contains.
type Group struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	NodeIDs []string `json:"nodeIds"`
}

// Node is one project artifact, capability, module or diagnostic.
type Node struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Detail string `json:"detail"`
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Level  string `json:"level"`
}

// Edge describes one directed relationship between two graph nodes.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

var graphGroups = []Group{
	{ID: "application-features", Label: "Application Features"},
	{ID: "http", Label: "HTTP"},
	{ID: "database", Label: "Database"},
	{ID: "views", Label: "Views"},
	{ID: "async", Label: "Async"},
	{ID: "console", Label: "Console"},
	{ID: "native-screens", Label: "Native Screens"},
	{ID: "native-capabilities", Label: "Native Capabilities"},
	{ID: "community-modules", Label: "Community Modules"},
	{ID: "diagnostics", Label: "Diagnostics"},
}

type graphArtifact struct {
	node    Node
	group   string
	feature string
}

type graphBuilder struct {
	groups     []Group
	groupIndex map[string]int
	nodes      map[string]Node
	edges      map[string]Edge
}

func newGraphBuilder() *graphBuilder {
	groups := make([]Group, len(graphGroups))
	index := make(map[string]int, len(graphGroups))
	for i, group := range graphGroups {
		groups[i] = Group{ID: group.ID, Label: group.Label, NodeIDs: []string{}}
		index[group.ID] = i
	}
	return &graphBuilder{
		groups: groups, groupIndex: index,
		nodes: map[string]Node{}, edges: map[string]Edge{},
	}
}

func (b *graphBuilder) addNode(group string, node Node) {
	if existing, found := b.nodes[node.ID]; found {
		if locationBefore(node, existing) {
			b.nodes[node.ID] = node
		}
		return
	}
	b.nodes[node.ID] = node
	at := b.groupIndex[group]
	b.groups[at].NodeIDs = append(b.groups[at].NodeIDs, node.ID)
}

// has reports whether a node with this ID is already in the graph.
func (b *graphBuilder) has(id string) bool {
	_, found := b.nodes[id]
	return found
}

func (b *graphBuilder) addEdge(from, to string) {
	edge := Edge{From: from, To: to, Kind: "contains"}
	b.edges[from+"\x00"+to] = edge
}

func (b *graphBuilder) graph() ProjectGraph {
	nodes := make([]Node, 0, len(b.nodes))
	for _, node := range b.nodes {
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	for i := range b.groups {
		sort.Strings(b.groups[i].NodeIDs)
	}

	edges := make([]Edge, 0, len(b.edges))
	for _, edge := range b.edges {
		edges = append(edges, edge)
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		if edges[i].To != edges[j].To {
			return edges[i].To < edges[j].To
		}
		return edges[i].Kind < edges[j].Kind
	})
	return ProjectGraph{SchemaVersion: 1, Groups: b.groups, Nodes: nodes, Edges: edges}
}

func buildProjectGraph(p *project, findings []Finding) ProjectGraph {
	builder := newGraphBuilder()
	files := append([]*file(nil), p.files...)
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })

	artifacts := make([]graphArtifact, 0, len(files))
	for _, f := range files {
		if f.isTest {
			continue
		}
		if artifact, ok := graphArtifactForFile(f); ok {
			artifacts = append(artifacts, artifact)
		}
	}

	features := collectFeatures(artifacts)
	featureIDs := addFeatureNodes(builder, features)
	for _, artifact := range artifacts {
		builder.addNode(artifact.group, artifact.node)
		if id := featureIDs[strings.ToLower(artifact.feature)]; id != "" {
			builder.addEdge(id, artifact.node.ID)
		}
	}
	for _, view := range p.views {
		node := Node{
			ID: "view:" + graphID(view.rel), Kind: "view", Label: view.name,
			Detail: view.rel, File: view.rel, Line: 1, Column: 1,
		}
		builder.addNode("views", node)
		if featureID := featureForView(view.name, featureIDs); featureID != "" {
			builder.addEdge(featureID, node.ID)
		}
	}
	addNativeScreens(builder, files)
	addNativeCapabilities(builder, files)
	addCommunityModules(builder, p, files)
	addDiagnosticNodes(builder, findings)
	return builder.graph()
}

type featurePlace struct {
	label          string
	file           string
	line           int
	column         int
	locationWeight int
}

func collectFeatures(artifacts []graphArtifact) map[string]featurePlace {
	features := map[string]featurePlace{}
	for _, artifact := range artifacts {
		if artifact.feature == "" {
			continue
		}
		key := strings.ToLower(artifact.feature)
		candidate := featurePlace{
			label: artifact.feature, file: artifact.node.File,
			line: artifact.node.Line, column: artifact.node.Column,
			locationWeight: featureLocationWeight(artifact.node.Kind),
		}
		current, found := features[key]
		if !found || betterFeaturePlace(candidate, current) {
			features[key] = candidate
		}
	}
	return features
}

func betterFeaturePlace(candidate, current featurePlace) bool {
	if candidate.locationWeight != current.locationWeight {
		return candidate.locationWeight < current.locationWeight
	}
	return locationBefore(
		Node{File: candidate.file, Line: candidate.line, Column: candidate.column},
		Node{File: current.file, Line: current.line, Column: current.column},
	)
}

func addFeatureNodes(builder *graphBuilder, features map[string]featurePlace) map[string]string {
	keys := make([]string, 0, len(features))
	for key := range features {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	ids := make(map[string]string, len(features))
	for _, key := range keys {
		feature := features[key]
		id := "feature:" + graphID(feature.label)
		ids[key] = id
		builder.addNode("application-features", Node{
			ID: id, Kind: "feature", Label: feature.label,
			Detail: "Application feature", File: feature.file,
			Line: feature.line, Column: feature.column,
		})
	}
	return ids
}

func graphArtifactForFile(f *file) (graphArtifact, bool) {
	if strings.HasPrefix(f.rel, "resources/views/") || strings.HasPrefix(f.rel, "storage/framework/views/") {
		return graphArtifact{}, false
	}
	line, column := firstDeclarationPosition(f)
	label := strings.TrimSuffix(filepath.Base(f.rel), ".go")
	artifact := graphArtifact{
		node:    Node{Label: label, Detail: f.rel, File: f.rel, Line: line, Column: column},
		feature: f.entity,
	}

	if strings.HasPrefix(f.rel, "app/") {
		switch f.category {
		case "Controllers":
			artifact.group, artifact.node.Kind = "http", "controller"
		case "Middleware":
			artifact.group, artifact.node.Kind = "http", "middleware"
		case "Requests":
			artifact.group, artifact.node.Kind = "http", "request"
		case "Models":
			artifact.group, artifact.node.Kind = "database", "model"
		case "Repositories":
			artifact.group, artifact.node.Kind = "database", "repository"
		case "Jobs":
			artifact.group, artifact.node.Kind = "async", "job"
		case "Events":
			artifact.group, artifact.node.Kind = "async", "event"
		case "Listeners":
			artifact.group, artifact.node.Kind = "async", "listener"
		case "Mail":
			artifact.group, artifact.node.Kind = "async", "mail"
		case "Commands":
			artifact.group, artifact.node.Kind = "console", "command"
		case "Policies":
			artifact.group, artifact.node.Kind = "application-features", "policy"
		case "Services":
			artifact.group, artifact.node.Kind = "application-features", "service"
		case "Enums":
			artifact.group, artifact.node.Kind = "application-features", "enum"
		case "Rules":
			artifact.group, artifact.node.Kind = "application-features", "rule"
		case "Providers":
			artifact.group, artifact.node.Kind = "application-features", "provider"
		default:
			artifact.group, artifact.node.Kind = "application-features", "application"
		}
	} else {
		switch {
		case strings.HasPrefix(f.rel, "database/migrations/"):
			artifact.group, artifact.node.Kind = "database", "migration"
		case strings.HasPrefix(f.rel, "database/seeders/"):
			artifact.group, artifact.node.Kind = "database", "seeder"
		case f.rel == "routes/console.go":
			artifact.group, artifact.node.Kind = "console", "console-route"
		case strings.HasPrefix(f.rel, "routes/"):
			artifact.group, artifact.node.Kind = "http", "route"
		case isNativeTarget(f.rel):
			// The native target is a command, and listing it beside the
			// console commands would put five files that draw a window under a
			// heading about the terminal. It has a group of its own.
			return graphArtifact{}, false
		case f.rel == "main.go" || strings.HasPrefix(f.rel, "cmd/"):
			artifact.group, artifact.node.Kind = "console", "entrypoint"
		case strings.HasPrefix(f.rel, "bootstrap/"):
			artifact.group, artifact.node.Kind = "application-features", "bootstrap"
		default:
			return graphArtifact{}, false
		}
	}
	artifact.node.ID = artifact.node.Kind + ":" + graphID(f.rel)
	return artifact, true
}

func firstDeclarationPosition(f *file) (int, int) {
	position := f.fset.Position(f.ast.Name.Pos())
	for _, declaration := range f.ast.Decls {
		if general, ok := declaration.(*ast.GenDecl); ok && general.Tok == token.IMPORT {
			continue
		}
		position = f.fset.Position(declaration.Pos())
		break
	}
	return max(position.Line, 1), max(position.Column, 1)
}

func featureLocationWeight(kind string) int {
	switch kind {
	case "model":
		return 0
	case "service":
		return 1
	case "policy":
		return 2
	case "controller":
		return 3
	}
	return 4
}

func featureForView(name string, features map[string]string) string {
	segment := strings.ToLower(strings.Split(name, ".")[0])
	if id := features[segment]; id != "" {
		return id
	}
	if strings.HasSuffix(segment, "s") {
		return features[strings.TrimSuffix(segment, "s")]
	}
	return ""
}

func addNativeCapabilities(builder *graphBuilder, files []*file) {
	const prefix = "github.com/arandu-io/hesape/"
	for _, f := range files {
		if f.isTest {
			continue
		}
		for _, spec := range f.ast.Imports {
			importPath := strings.Trim(spec.Path.Value, `"`)
			if !strings.HasPrefix(importPath, prefix) {
				continue
			}
			component := strings.Split(strings.TrimPrefix(importPath, prefix), "/")[0]
			if component == "" {
				continue
			}
			position := f.fset.Position(spec.Pos())
			builder.addNode("native-capabilities", Node{
				ID: "native-capability:" + graphID(component), Kind: "native-capability",
				Label: component, Detail: prefix + component, File: f.rel,
				Line: max(position.Line, 1), Column: max(position.Column, 1),
			})
		}
	}
}

// addCommunityModules puts every Arandu module of the community this project
// depends on into the graph.
//
// Two ways in, and both are needed. A module registered on the kernel is wired
// into the application and is found by reading bootstrap. A module used as a
// library -- a service constructed from it, a catalogue built with it, a
// registry it hands out -- is never registered anywhere, and reading only
// bootstrap answered that a project depending on two of them had none.
//
// What separates such a module from any other Go dependency is that it carries
// an arandu.mod.toml at its root. That is the ecosystem's own declaration, and
// it is what the package skeleton ships: a name heuristic on the import path
// would be a guess that rots the first time somebody names a module something
// else.
func addCommunityModules(builder *graphBuilder, p *project, files []*file) {
	addRegisteredCommunityModules(builder, p, files)
	addDependedCommunityModules(builder, p, files)
}

func addRegisteredCommunityModules(builder *graphBuilder, p *project, files []*file) {
	for _, f := range files {
		if f.isTest || !strings.HasPrefix(f.rel, "bootstrap/") {
			continue
		}
		built := builtModules(f)
		ast.Inspect(f.ast, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := callName(call)
			if name != "Register" && !strings.HasSuffix(name, ".Register") {
				return true
			}
			if !kernelRegisterCall(f, call) {
				return true
			}
			for _, argument := range call.Args {
				alias, at, ok := registeredImportAlias(argument)
				if !ok {
					alias, at, ok = built.lookup(argument)
				}
				if !ok {
					continue
				}
				importPath := importPathForAlias(f, alias)
				if !isExternalModule(importPath, p.modulePath) {
					continue
				}
				position := f.fset.Position(at.Pos())
				builder.addNode("community-modules", Node{
					ID: "community-module:" + graphID(importPath), Kind: "community-module",
					Label: importPath, Detail: "Registered in bootstrap", File: f.rel,
					Line: max(position.Line, 1), Column: max(position.Column, 1),
				})
			}
			return false
		})
	}
}

// addDependedCommunityModules reads the imports of the project and keeps the
// ones answered by a module that declares itself an Arandu module.
//
// The manifest is read from where the module actually sits: the tree itself for
// a local replace, and the module cache otherwise. A module that is required and
// not downloaded contributes nothing rather than a node pointing at a file that
// is not there -- the graph is drawn from what is on this disk, and saying
// nothing is the honest answer to not knowing.
func addDependedCommunityModules(builder *graphBuilder, p *project, files []*file) {
	manifest := readGoMod(p.root)
	if manifest == nil {
		return
	}

	seen := make(map[string]bool)
	for _, f := range files {
		if f.isTest {
			continue
		}
		for _, spec := range f.ast.Imports {
			importPath := strings.Trim(spec.Path.Value, "\"")
			if !isExternalModule(importPath, p.modulePath) {
				continue
			}
			modulePath, ok := owningModule(manifest, importPath)
			if !ok || seen[modulePath] {
				continue
			}
			seen[modulePath] = true
			if builder.has("community-module:" + graphID(modulePath)) {
				// Registered on the kernel, and already in the graph saying so.
				// That is the more precise answer of the two: it says the module
				// is wired into the application, not merely on the require list.
				continue
			}
			if !declaresAranduManifest(manifest, p.root, modulePath) {
				continue
			}
			position := f.fset.Position(spec.Pos())
			builder.addNode("community-modules", Node{
				ID: "community-module:" + graphID(modulePath), Kind: "community-module",
				Label: modulePath, Detail: "Required in go.mod", File: f.rel,
				Line: max(position.Line, 1), Column: max(position.Column, 1),
			})
		}
	}
}

// readGoMod reads the project's go.mod, or nil when there is none to read.
func readGoMod(root string) *gomod.File {
	body, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil
	}
	return gomod.Parse(string(body))
}

// owningModule answers which required module provides an import path.
//
// The longest requirement wins, because a module and a nested module of it are
// both prefixes of the same import and only the longer one owns the package.
func owningModule(manifest *gomod.File, importPath string) (string, bool) {
	best := ""
	for modulePath := range manifest.Versions {
		if _, ok := gomod.Under(importPath, modulePath); !ok {
			continue
		}
		if len(modulePath) > len(best) {
			best = modulePath
		}
	}
	return best, best != ""
}

// declaresAranduManifest reports whether a module carries an arandu.mod.toml.
func declaresAranduManifest(manifest *gomod.File, root, modulePath string) bool {
	if target, replaced := manifest.Replaced[modulePath]; replaced {
		dir := target
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(root, filepath.FromSlash(dir))
		}
		return fileExists(filepath.Join(dir, "arandu.mod.toml"))
	}

	version, required := manifest.Versions[modulePath]
	cache := gomod.Cache()
	if !required || cache == "" {
		return false
	}
	at := filepath.Join(cache, filepath.FromSlash(gomod.EscapePath(modulePath)+"@"+version), "arandu.mod.toml")
	return fileExists(at)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func kernelRegisterCall(f *file, call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Register" {
		return false
	}
	return kernelExpression(f, selector.X)
}

// fluentKernelMethods are the kernel methods that answer the kernel.
//
// There are two, and the set is written out rather than inferred because
// inferring it means accepting any method call on a kernel as a kernel -- which
// would count a module registered on whatever k.Tasks() or k.Recorder()
// returns. Register and Use are the whole of the fluent surface.
var fluentKernelMethods = map[string]bool{"Register": true, "Use": true}

func kernelExpression(f *file, expression ast.Expr) bool {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Obj != nil && kernelDeclaration(f, value.Obj.Decl, value)
	case *ast.CallExpr:
		selector, ok := value.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		if fluentKernelMethods[selector.Sel.Name] {
			// The wiring is written as one chain -- k.Use(...).Register(...) --
			// so the receiver of the Register call is the Use call, not the
			// identifier. Both methods answer the kernel itself and nothing
			// else does, which is what keeps this closed: a receiver like
			// k.Tasks() is a call on a kernel too, and a module registered on
			// whatever that returns is not a module of this application.
			return kernelExpression(f, selector.X)
		}
		if selector.Sel.Name != "New" {
			return false
		}
		alias, ok := selector.X.(*ast.Ident)
		return ok && importPathForAlias(f, alias.Name) == "github.com/arandu-io/framework/kernel"
	case *ast.ParenExpr:
		return kernelExpression(f, value.X)
	case *ast.UnaryExpr:
		return value.Op == token.AND && kernelExpression(f, value.X)
	case *ast.CompositeLit:
		return kernelType(f, value.Type)
	}
	return false
}

func kernelDeclaration(f *file, declaration any, identifier *ast.Ident) bool {
	switch value := declaration.(type) {
	case *ast.Field:
		return kernelType(f, value.Type)
	case *ast.AssignStmt:
		for index, left := range value.Lhs {
			declared, ok := left.(*ast.Ident)
			if !ok || declared.Obj != identifier.Obj || len(value.Lhs) != len(value.Rhs) {
				continue
			}
			return kernelExpression(f, value.Rhs[index])
		}
	case *ast.ValueSpec:
		if value.Type != nil && kernelType(f, value.Type) {
			return true
		}
		for index, name := range value.Names {
			if name.Obj != identifier.Obj || len(value.Names) != len(value.Values) {
				continue
			}
			return kernelExpression(f, value.Values[index])
		}
	}
	return false
}

func kernelType(f *file, expression ast.Expr) bool {
	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Kernel" {
		return false
	}
	alias, ok := selector.X.(*ast.Ident)
	return ok && importPathForAlias(f, alias.Name) == "github.com/arandu-io/framework/kernel"
}

func registeredImportAlias(expression ast.Expr) (string, ast.Node, bool) {
	switch value := expression.(type) {
	case *ast.CallExpr:
		selector, ok := value.Fun.(*ast.SelectorExpr)
		if !ok {
			return "", nil, false
		}
		alias, ok := selector.X.(*ast.Ident)
		return aliasName(alias, value, ok)
	case *ast.CompositeLit:
		selector, ok := value.Type.(*ast.SelectorExpr)
		if !ok {
			return "", nil, false
		}
		alias, ok := selector.X.(*ast.Ident)
		return aliasName(alias, value, ok)
	case *ast.UnaryExpr:
		return registeredImportAlias(value.X)
	}
	return "", nil, false
}

// builtModules maps a local variable to the package alias whose constructor
// produced it.
//
// A module is registered by the identifier, not by the call, whenever its
// constructor returns an error -- and every module the package skeleton
// produces does, because a module that could be registered half-wired is a
// module whose first request reports the missing half. Go has no way to spell
// a two-value call inside a variadic argument list, so the shape is two
// statements:
//
//	fleetModule, err := fleet.New(fleet.Config{...}, db, sessions)
//	...
//	k.Register(fleetModule)
//
// Reading only the call form saw an identifier with no selector on it and
// counted nothing, which is how a project registering community modules
// reported none of them. The lookup is per file, and bootstrap/app.go is where
// both statements are: a module built in one file and registered in another is
// not a shape the wiring takes, and guessing across files would name a variable
// that happens to repeat.
type moduleAliases map[string]string

func builtModules(f *file) moduleAliases {
	built := make(moduleAliases)
	ast.Inspect(f.ast, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, left := range assign.Lhs {
			name, ok := left.(*ast.Ident)
			if !ok || name.Name == "_" || i >= len(assign.Rhs) {
				continue
			}
			if alias, _, ok := registeredImportAlias(assign.Rhs[i]); ok {
				built[name.Name] = alias
			}
		}
		return true
	})
	return built
}

// lookup answers the alias a registered identifier was built from, and where to
// point at it: the registration, not the construction, because the graph is
// about what this application wires in.
func (b moduleAliases) lookup(expression ast.Expr) (string, ast.Node, bool) {
	identifier, ok := expression.(*ast.Ident)
	if !ok {
		return "", nil, false
	}
	alias, ok := b[identifier.Name]
	if !ok {
		return "", nil, false
	}
	return alias, identifier, true
}

func aliasName(alias *ast.Ident, at ast.Node, ok bool) (string, ast.Node, bool) {
	if !ok || alias == nil {
		return "", nil, false
	}
	return alias.Name, at, true
}

func importPathForAlias(f *file, alias string) string {
	for _, spec := range f.ast.Imports {
		importPath := strings.Trim(spec.Path.Value, `"`)
		local := filepath.Base(importPath)
		if spec.Name != nil {
			local = spec.Name.Name
		}
		if local == alias {
			return importPath
		}
	}
	return ""
}

func isExternalModule(importPath, modulePath string) bool {
	if importPath == "" || modulePath == "" || strings.HasPrefix(importPath, "github.com/arandu-io/") {
		return false
	}
	if importPath == modulePath || strings.HasPrefix(importPath, modulePath+"/") {
		return false
	}
	return strings.Contains(strings.Split(importPath, "/")[0], ".")
}

func addDiagnosticNodes(builder *graphBuilder, findings []Finding) {
	seenIDs := make(map[string]int)
	for _, finding := range findings {
		baseID := "diagnostic:" + graphID(finding.Rule) + ":" + graphID(finding.File) + ":" + strconv.Itoa(finding.Line)
		seenIDs[baseID]++
		id := baseID
		if seenIDs[baseID] > 1 {
			id += ":" + strconv.Itoa(seenIDs[baseID])
		}
		builder.addNode("diagnostics", Node{
			ID: id, Kind: "diagnostic", Label: finding.Message, Detail: finding.Why,
			File: finding.File, Line: finding.Line, Column: 1,
			Level: finding.Severity.String(),
		})
	}
}

func locationBefore(left, right Node) bool {
	if left.File != right.File {
		return left.File < right.File
	}
	if left.Line != right.Line {
		return left.Line < right.Line
	}
	return left.Column < right.Column
}

func graphID(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}
