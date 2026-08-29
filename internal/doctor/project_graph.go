package doctor

import (
	"encoding/base64"
	"go/ast"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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

func addCommunityModules(builder *graphBuilder, p *project, files []*file) {
	for _, f := range files {
		if f.isTest || !strings.HasPrefix(f.rel, "bootstrap/") {
			continue
		}
		ast.Inspect(f.ast, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := callName(call)
			if name != "Register" && !strings.HasSuffix(name, ".Register") {
				return true
			}
			for _, argument := range call.Args {
				alias, at, ok := registeredImportAlias(argument)
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

func aliasName(alias *ast.Ident, at ast.Node, ok bool) (string, ast.Node, bool) {
	if !ok || alias == nil {
		return "", nil, false
	}
	return alias.Name, at, true
}

func importPathForAlias(f *file, alias string) string {
	for importPath, local := range f.imports {
		if local == alias {
			return importPath
		}
	}
	return ""
}

func isExternalModule(importPath, modulePath string) bool {
	if importPath == "" || strings.HasPrefix(importPath, "github.com/arandu-io/") {
		return false
	}
	if modulePath != "" && (importPath == modulePath || strings.HasPrefix(importPath, modulePath+"/")) {
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
