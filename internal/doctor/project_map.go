package doctor

import (
	"go/ast"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// MapSchemaVersion is the schema ProjectMap answers in.
//
// It is the second one. The first is ProjectGraph, kept byte for byte for the
// clients that ask for nothing else, and a client reaches this one only by
// naming it.
const MapSchemaVersion = 2

// ProjectMap is the second schema of the editor-facing map of a project.
//
// Where ProjectGraph files every artifact under the folder it sits in and links
// them by containment alone, this reads what each file declares -- the
// interface it asserts, the method shapes it has, the generated marker on its
// first line -- and links the artifacts by what the code does with them: a
// route reaches an action, an action binds a request, a service asks a policy
// and writes through a model.
//
// Every edge kind is listed in EdgeKinds with what the analysis follows to
// draw it and what it does not, because a missing edge has to be told apart
// from an edge the analysis cannot see.
type ProjectMap struct {
	SchemaVersion int `json:"schemaVersion"`
	// Profile is the deployment profile the diagnostics were checked against,
	// read from arandu.mod.toml.
	Profile   string     `json:"profile"`
	Groups    []Group    `json:"groups"`
	Nodes     []MapNode  `json:"nodes"`
	Edges     []MapEdge  `json:"edges"`
	EdgeKinds []EdgeKind `json:"edgeKinds"`
}

// MapNode is one artifact, route, action, feature, capability, module or
// diagnostic of the map.
//
// Line and Column are where the node starts and EndLine and EndColumn where it
// ends, one-based, in the bytes of the file. A route spans its registration
// call, an action its method declaration from the name onwards, and a
// diagnostic the text of the line it reports.
type MapNode struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Detail    string `json:"detail,omitempty"`
	File      string `json:"file,omitempty"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	EndLine   int    `json:"endLine"`
	EndColumn int    `json:"endColumn"`
	// Feature is the ID of the feature node the artifact belongs to.
	Feature string `json:"feature,omitempty"`
	// Generated marks a file a tool wrote, read from the marker on its first
	// line. A generated file joins a feature and never opens one.
	Generated bool `json:"generated,omitempty"`
	// Variant is the shape of a controller -- resource, singleton, invokable
	// or plain -- and of a client, fake for the stand-in a test uses.
	Variant string `json:"variant,omitempty"`
	// NestedUnder is the parent resource a nested controller or route is
	// registered under: "projects" for "projects.tasks".
	NestedUnder string `json:"nestedUnder,omitempty"`
	// Parent is the ID of the node an action belongs to.
	Parent string `json:"parent,omitempty"`
	// Method, Pattern and Name describe a route as the router registers it.
	Method  string `json:"method,omitempty"`
	Pattern string `json:"pattern,omitempty"`
	Name    string `json:"name,omitempty"`
	// Rule, Severity and RuleDoc describe a diagnostic: the rule's name, the
	// level it reported at, and where the rule is documented -- a path inside
	// the project with a #L line fragment, or an https address when the
	// project does not carry the documentation.
	Rule     string `json:"rule,omitempty"`
	Severity string `json:"severity,omitempty"`
	RuleDoc  string `json:"ruleDoc,omitempty"`
}

// MapEdge is one typed, directed relationship, and where in the code it was
// read from.
type MapEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
	// At is the place the relationship is written: the registration of a
	// route, the call that asks a policy, the line of a test that uses the
	// artifact. Containment is written nowhere and carries none.
	At *MapLocation `json:"at,omitempty"`
}

// MapLocation is a place in a file, one-based, in bytes.
type MapLocation struct {
	File      string `json:"file"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	EndLine   int    `json:"endLine"`
	EndColumn int    `json:"endColumn"`
}

// EdgeKind states what one kind of edge means and how far the analysis reads
// to draw it.
type EdgeKind struct {
	Kind          string `json:"kind"`
	Meaning       string `json:"meaning"`
	Follows       string `json:"follows"`
	DoesNotFollow string `json:"doesNotFollow"`
}

// The edge kinds, in the order EdgeKinds lists them.
const (
	EdgeContains      = "contains"
	EdgeRoutesTo      = "routes-to"
	EdgeValidatesWith = "validates-with"
	EdgeAuthorizes    = "authorizes"
	EdgePersists      = "persists"
	EdgeRenders       = "renders"
	EdgeTestedBy      = "tested-by"
	EdgeDispatches    = "dispatches"
	EdgeListensTo     = "listens-to"
)

// mapEdgeKinds is the closed set of edge kinds, with the reach of each.
var mapEdgeKinds = []EdgeKind{
	{
		Kind:          EdgeContains,
		Meaning:       "a feature holds an artifact, and a controller holds its actions",
		Follows:       "the entity a file is named for, the file a generated file was generated from, and the methods a controller type declares with the action signature",
		DoesNotFollow: "an artifact named for no entity, which belongs to no feature",
	},
	{
		Kind:          EdgeRoutesTo,
		Meaning:       "a route reaches a controller action",
		Follows:       "Resource, Singleton, ResourceAction, Invokable, Action and the verb methods in routes/, Group prefixes held in variables of the same function, and a controller resolved through a parameter, a struct field of the routes package, a composite literal or a New constructor",
		DoesNotFollow: "a router passed between functions with a prefix already applied, a controller built in another package and handed over as an interface, a handler that is a function literal, and routes a module registers on its own",
	},
	{
		Kind:          EdgeValidatesWith,
		Meaning:       "an action or a service takes a request",
		Follows:       "any reference to a type of a request file: the variable an action binds into and the parameter a service validates",
		DoesNotFollow: "a request reached through a value of another type",
	},
	{
		Kind:          EdgeAuthorizes,
		Meaning:       "an artifact asks a policy",
		Follows:       "any reference from outside the policies to a declaration of a policy file: the field holding the policy and the action constants handed to Authorize",
		DoesNotFollow: "a policy reached through an interface value built elsewhere, and route guards",
	},
	{
		Kind:          EdgePersists,
		Meaning:       "an artifact reads or writes rows through a model or a repository, and a model is stored in the table a migration declares",
		Follows:       "a call of a function of a model file (the query constructor model:build writes), any reference from a repository to a model, any reference to a repository, and a table named in a model's TableSpec and in a migration's Schema().Create or Table call or CREATE TABLE and ALTER TABLE statement",
		DoesNotFollow: "a model used only as a value, SQL assembled at run time, and a table name held in a variable",
	},
	{
		Kind:          EdgeRenders,
		Meaning:       "an action, a mail or a view renders a view, and an action answers with a JSON resource",
		Follows:       "the literal name passed to ctx.View, ctx.Fragment or ctx.Partial, a View or TextView field set to a literal, @extends and @include, and any reference from a controller to a resource file",
		DoesNotFollow: "a view name assembled at run time",
	},
	{
		Kind:          EdgeTestedBy,
		Meaning:       "a test uses an artifact",
		Follows:       "any reference from a _test.go file to a declaration of the project's packages, and bare identifiers of a test in the package it tests",
		DoesNotFollow: "an artifact a test reaches only through HTTP requests or another artifact",
	},
	{
		Kind:          EdgeDispatches,
		Meaning:       "an artifact sends a job, an event, a notification or a mail",
		Follows:       "a call of a Dispatch function, a composite literal of the declared type and a reference to its Name constant, from outside bootstrap and outside the declaring package",
		DoesNotFollow: "a value of the type built elsewhere and handed over, and a job named by a string literal",
	},
	{
		Kind:          EdgeListensTo,
		Meaning:       "a listener reacts to an event",
		Follows:       "a string literal in the listener equal to the value of an event's Name constant, and any reference from a listener to an event file",
		DoesNotFollow: "an event name assembled at run time and the outbox wiring in bootstrap",
	},
}

// mapGroups are the sections of the map, in the order a client lists them.
var mapGroups = []Group{
	{ID: "application-features", Label: "Application Features"},
	{ID: "http", Label: "HTTP"},
	{ID: "database", Label: "Database"},
	{ID: "views", Label: "Views"},
	{ID: "async", Label: "Async"},
	{ID: "integrations", Label: "Integrations"},
	{ID: "console", Label: "Console"},
	{ID: "tests", Label: "Tests"},
	{ID: "native-screens", Label: "Native Screens"},
	{ID: "native-capabilities", Label: "Native Capabilities"},
	{ID: "community-modules", Label: "Community Modules"},
	{ID: "diagnostics", Label: "Diagnostics"},
}

// mapGroupOf is the section each node kind is listed under.
var mapGroupOf = map[string]string{
	"feature": "application-features", "policy": "application-features", "service": "application-features",
	"enum": "application-features", "rule": "application-features", "provider": "application-features",
	"application": "application-features", "bootstrap": "application-features",
	"route-file": "http", "route": "http", "controller": "http", "action": "http",
	"request": "http", "middleware": "http", "resource": "http",
	"model": "database", "migration": "database", "seeder": "database", "factory": "database",
	"repository": "database",
	"view":       "views",
	"job":        "async", "event": "async", "listener": "async", "mail": "async", "notification": "async",
	"client": "integrations", "webhook": "integrations",
	"mcp-tool": "integrations", "mcp-resource": "integrations", "mcp-prompt": "integrations",
	"command": "console", "console-route": "console", "entrypoint": "console",
	"test":              "tests",
	"native-target":     "native-screens",
	"native-screen":     "native-screens",
	"native-capability": "native-capabilities",
	"community-module":  "community-modules",
	"diagnostic":        "diagnostics",
}

// doctorSkill is where a project generated by `aru new` documents every rule
// of this package, one table row per rule.
const doctorSkill = ".agents/skills/arandu-doctor/SKILL.md"

// doctorSkillOnline is the same documentation in the project skeleton, for a
// project that does not carry the skill.
const doctorSkillOnline = "https://github.com/arandu-io/arandu/blob/main/.agents/skills/arandu-doctor/SKILL.md"

// mapBuilder collects the nodes and edges of one ProjectMap.
type mapBuilder struct {
	nodes map[string]*MapNode
	edges map[string]*MapEdge
}

func newMapBuilder() *mapBuilder {
	return &mapBuilder{nodes: map[string]*MapNode{}, edges: map[string]*MapEdge{}}
}

// add keeps the first node an ID is given and the earliest place it was seen.
func (b *mapBuilder) add(node MapNode) *MapNode {
	if existing, found := b.nodes[node.ID]; found {
		if mapBefore(node.File, node.Line, node.Column, existing.File, existing.Line, existing.Column) {
			existing.File, existing.Line, existing.Column = node.File, node.Line, node.Column
			existing.EndLine, existing.EndColumn = node.EndLine, node.EndColumn
		}
		return existing
	}
	stored := node
	b.nodes[node.ID] = &stored
	return &stored
}

// link records an edge once per kind and pair, keeping the earliest place it
// is written.
func (b *mapBuilder) link(from, to, kind string, at *MapLocation) {
	if from == "" || to == "" || from == to {
		return
	}
	if _, ok := b.nodes[from]; !ok {
		return
	}
	if _, ok := b.nodes[to]; !ok {
		return
	}
	key := from + "\x00" + to + "\x00" + kind
	if existing, found := b.edges[key]; found {
		if at != nil && (existing.At == nil || mapBefore(at.File, at.Line, at.Column, existing.At.File, existing.At.Line, existing.At.Column)) {
			existing.At = at
		}
		return
	}
	b.edges[key] = &MapEdge{From: from, To: to, Kind: kind, At: at}
}

func (b *mapBuilder) projectMap(profile Profile) ProjectMap {
	groups := make([]Group, len(mapGroups))
	index := map[string]int{}
	for i, group := range mapGroups {
		groups[i] = Group{ID: group.ID, Label: group.Label, NodeIDs: []string{}}
		index[group.ID] = i
	}
	nodes := make([]MapNode, 0, len(b.nodes))
	for _, node := range b.nodes {
		nodes = append(nodes, *node)
		if at, ok := index[mapGroupOf[node.Kind]]; ok {
			groups[at].NodeIDs = append(groups[at].NodeIDs, node.ID)
		}
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	for i := range groups {
		sort.Strings(groups[i].NodeIDs)
	}
	edges := make([]MapEdge, 0, len(b.edges))
	for _, edge := range b.edges {
		edges = append(edges, *edge)
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
	kinds := append([]EdgeKind(nil), mapEdgeKinds...)
	return ProjectMap{
		SchemaVersion: MapSchemaVersion, Profile: string(profile),
		Groups: groups, Nodes: nodes, Edges: edges, EdgeKinds: kinds,
	}
}

func mapBefore(leftFile string, leftLine, leftColumn int, rightFile string, rightLine, rightColumn int) bool {
	if leftFile != rightFile {
		return leftFile < rightFile
	}
	if leftLine != rightLine {
		return leftLine < rightLine
	}
	return leftColumn < rightColumn
}

// mapArtifact is one file of the project as the map files it.
type mapArtifact struct {
	f         *file
	node      *MapNode
	entity    string
	joinsOnly bool
}

// mapState is what the edge readers share: every artifact by file, and the
// declarations of every directory of the project by name.
type mapState struct {
	p         *project
	b         *mapBuilder
	artifacts map[string]*mapArtifact
	// declared maps a directory to the names its files declare at package
	// level and the file declaring each.
	declared map[string]map[string]*file
	// features maps a lower-cased entity to its feature node ID.
	features map[string]string
	// views maps a view name to its node ID.
	views map[string]string
	// controllerTypes maps a controller type name to the file declaring it.
	controllerTypes map[string]*file
	// actions maps "dir\x00Type.Method" to the action node ID.
	actions map[string]string
	// queryOwners maps a generated query file to the entity file it was
	// written from.
	queryOwners map[string]string
}

func buildProjectMap(p *project, findings []Finding) ProjectMap {
	files := append([]*file(nil), p.files...)
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })

	s := &mapState{
		p: p, b: newMapBuilder(), artifacts: map[string]*mapArtifact{},
		declared: map[string]map[string]*file{}, features: map[string]string{},
		views: map[string]string{}, controllerTypes: map[string]*file{},
		actions: map[string]string{}, queryOwners: map[string]string{},
	}
	for _, f := range files {
		s.declare(f)
	}
	for _, f := range files {
		if artifact, ok := s.classify(f); ok {
			s.artifacts[f.rel] = artifact
		}
	}
	s.addFeatures()
	for _, view := range p.views {
		lines := strings.Count(view.body, "\n") + 1
		id := "view:" + graphID(view.rel)
		s.b.add(MapNode{
			ID: id, Kind: "view", Label: view.name, Detail: view.rel, File: view.rel,
			Line: 1, Column: 1, EndLine: lines, EndColumn: 1,
		})
		s.views[view.name] = id
		if feature := featureForView(view.name, s.features); feature != "" {
			s.b.nodes[id].Feature = feature
			s.b.link(feature, id, EdgeContains, nil)
		}
	}
	s.addActions()
	s.addRoutes(files)
	s.addReferenceEdges(files)
	s.addStringEdges(files)
	s.addTableEdges(files)
	s.addViewEdges()
	s.addBorrowedGroups(files)
	s.addDiagnostics(findings)
	return s.b.projectMap(p.profile)
}

// declare indexes the package-level names a file declares.
func (s *mapState) declare(f *file) {
	names := s.declared[f.dir]
	if names == nil {
		names = map[string]*file{}
		s.declared[f.dir] = names
	}
	for _, decl := range f.ast.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				keepFirst(names, d.Name.Name, f)
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch sp := spec.(type) {
				case *ast.TypeSpec:
					keepFirst(names, sp.Name.Name, f)
				case *ast.ValueSpec:
					for _, name := range sp.Names {
						if name.Name != "_" {
							keepFirst(names, name.Name, f)
						}
					}
				}
			}
		}
	}
}

// keepFirst records the file declaring a name, preferring a file that is not
// a test: a test file in the package declares helpers, and the artifact is
// the file that ships.
func keepFirst(names map[string]*file, name string, f *file) {
	if existing, found := names[name]; found && (!existing.isTest || f.isTest) {
		return
	}
	names[name] = f
}

// classify files one parsed file, by what it declares before where it sits.
func (s *mapState) classify(f *file) (*mapArtifact, bool) {
	if strings.HasPrefix(f.rel, "resources/views/") || strings.HasPrefix(f.rel, "storage/framework/views/") {
		return nil, false
	}
	if isNativeTarget(f.rel) {
		return nil, false
	}

	kind, variant := anatomyOf(f)
	located, locatedOK := graphArtifactForFile(f)
	switch {
	case kind != "":
	case locatedOK && located.node.Kind == "route":
		kind = "route-file"
	case locatedOK:
		kind = located.node.Kind
	case strings.HasPrefix(f.rel, "database/factories/"):
		kind = "factory"
	default:
		return nil, false
	}
	if kind == "controller" && f.importsAny(hesapePrefix+"webhook") {
		kind = "webhook"
	}
	if f.category == "Clients" && kind != "test" {
		if f.importsAny(hesapePrefix + "http/client") {
			kind, variant = "client", ""
		} else if kind == "application" || kind == "client" {
			kind, variant = "client", "fake"
		}
	}

	line, column := firstDeclarationPosition(f)
	end := f.fset.Position(f.ast.End())
	label := strings.TrimSuffix(filepath.Base(f.rel), ".go")
	artifact := &mapArtifact{f: f, entity: f.entity}
	node := MapNode{
		ID: kind + ":" + graphID(f.rel), Kind: kind, Label: label, Detail: f.rel, File: f.rel,
		Line: line, Column: column, EndLine: max(end.Line, line), EndColumn: max(end.Column, 1),
		Variant: variant,
	}
	if ast.IsGenerated(f.ast) {
		node.Generated = true
		artifact.joinsOnly = true
	}
	if source, generated := generatedQuerySource(f); generated {
		node.Generated = true
		artifact.joinsOnly = true
		_, artifact.entity = classify(path.Join(f.dir, source))
		s.queryOwners[f.rel] = path.Join(f.dir, source)
	}
	switch kind {
	case "test":
		artifact.joinsOnly = true
		artifact.entity = strings.TrimSuffix(label, "_test")
	case "factory", "seeder":
		artifact.joinsOnly = true
		artifact.entity = strings.TrimSuffix(strings.TrimSuffix(label, "Factory"), "Seeder")
	case "migration", "route-file", "console-route", "entrypoint", "bootstrap":
		artifact.entity = ""
	}
	artifact.node = s.b.add(node)
	if kind == "controller" || kind == "webhook" {
		f.types(func(ts *ast.TypeSpec) {
			if _, taken := s.controllerTypes[ts.Name.Name]; !taken {
				s.controllerTypes[ts.Name.Name] = f
			}
		})
	}
	return artifact, true
}

// importsAny reports whether the file imports one of the paths.
func (f *file) importsAny(paths ...string) bool {
	for _, want := range paths {
		if _, found := f.imports[want]; found {
			return true
		}
	}
	return false
}

// anatomyOf reads what a file is from what it declares: the interface it
// asserts with `var _ I = T{}`, the import that only one kind of artifact
// takes, and the shape of its methods. An empty kind means the file states
// nothing a kind can be read from, and its folder decides.
func anatomyOf(f *file) (kind, variant string) {
	if f.isTest {
		return "test", ""
	}
	asserted := f.assertedInterfaces()
	for _, contract := range asserted {
		if found, ok := contractKinds[contract]; ok {
			kind = found
			break
		}
	}
	switch {
	case kind == "controller":
		return "controller", controllerVariantFromAssertions(asserted)
	case kind != "":
		return kind, ""
	}
	if f.importsAny(hesapePrefix + "database/model/factories") {
		return "factory", ""
	}
	if declaresEntity(f) {
		return "model", ""
	}
	if f.registersMigration() {
		return "migration", ""
	}
	if f.declaresAction() {
		return "controller", ""
	}
	if f.declaresMethod(func(fn *ast.FuncDecl) bool {
		return fn.Name.Name == "Validate" && fn.Type.Params.NumFields() == 0 && resultSelector(fn, "Errors")
	}) {
		return "request", ""
	}
	if f.declaresMethod(func(fn *ast.FuncDecl) bool {
		return fn.Name.Name == "Event" && fn.Type.Params.NumFields() == 1 && resultSelector(fn, "Event")
	}) {
		return "event", ""
	}
	if strings.HasPrefix(f.rel, "app/") && f.declaresFunction(func(fn *ast.FuncDecl) bool {
		return fn.Recv == nil && resultSelector(fn, "Middleware")
	}) {
		return "middleware", ""
	}
	return "", ""
}

// contractKinds maps an asserted interface, by the import path it is declared
// in and its name, to the kind of artifact that asserts it.
var contractKinds = func() map[string]string {
	out := map[string]string{}
	add := func(kind, name string, paths ...string) {
		for _, p := range paths {
			out[p+"."+name] = kind
		}
	}
	httpPaths := []string{"github.com/arandu-io/framework/http", hesapePrefix + "http", hesapePrefix + "routing"}
	add("resource", "JsonResource", httpPaths...)
	for _, name := range []string{"Indexer", "Creator", "Storer", "Shower", "Editor", "Updater", "Destroyer", "Invoker"} {
		add("controller", name, httpPaths...)
	}
	add("notification", "Notification", hesapePrefix+"notifications")
	add("mcp-tool", "Tool", "github.com/arandu-io/mcp")
	add("mcp-resource", "Resource", "github.com/arandu-io/mcp")
	add("mcp-prompt", "Prompt", "github.com/arandu-io/mcp")
	add("request", "Validatable", frameworkValidation, hesapeValidation)
	add("policy", "Policy", frameworkSecurity, hesapeAuth)
	add("job", "Handler", "github.com/arandu-io/framework/jobs", hesapePrefix+"queue")
	add("listener", "Publisher", frameworkEvents, hesapePrefix+"events")
	add("mail", "Mailable", "github.com/arandu-io/framework/mail", hesapePrefix+"mail")
	add("seeder", "Seeder", "")
	return out
}()

// assertedInterfaces answers the interfaces the file asserts with a blank
// variable, as "import/path.Name", or ".Name" for one of its own package.
func (f *file) assertedInterfaces() []string {
	var out []string
	for _, decl := range f.ast.Decls {
		general, ok := decl.(*ast.GenDecl)
		if !ok || general.Tok != token.VAR {
			continue
		}
		for _, spec := range general.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || value.Type == nil || len(value.Names) == 0 || value.Names[0].Name != "_" {
				continue
			}
			typ := value.Type
			if index, ok := typ.(*ast.IndexExpr); ok {
				typ = index.X
			}
			if index, ok := typ.(*ast.IndexListExpr); ok {
				typ = index.X
			}
			switch t := typ.(type) {
			case *ast.SelectorExpr:
				alias, ok := t.X.(*ast.Ident)
				if !ok {
					continue
				}
				if p, imported := f.importPath(alias.Name); imported {
					out = append(out, p+"."+t.Sel.Name)
				}
			case *ast.Ident:
				out = append(out, "."+t.Name)
			}
		}
	}
	return out
}

// controllerVariantFromAssertions reads the shape of a controller from the
// router interfaces it asserts: Invoker is a single action, the three a
// singleton answers alone are a singleton, and any of the four only a
// collection has make it a resource.
func controllerVariantFromAssertions(asserted []string) string {
	seen := map[string]bool{}
	for _, contract := range asserted {
		if i := strings.LastIndexByte(contract, '.'); i >= 0 {
			seen[contract[i+1:]] = true
		}
	}
	switch {
	case seen["Invoker"]:
		return "invokable"
	case seen["Indexer"] || seen["Creator"] || seen["Storer"] || seen["Destroyer"]:
		return "resource"
	case seen["Shower"] || seen["Editor"] || seen["Updater"]:
		return "singleton"
	}
	return ""
}

// registersMigration reports whether the file registers a migration with the
// migrations package, which is what makes a type a migration whatever folder
// it is in.
func (f *file) registersMigration() bool {
	found := false
	f.calls(func(call *ast.CallExpr, name string) {
		local, fn, ok := strings.Cut(name, ".")
		if !ok || fn != "Register" {
			return
		}
		if p, imported := f.importPath(local); imported && strings.HasSuffix(p, "database/migrations") {
			found = true
		}
	})
	return found
}

// isActionMethod reports whether fn has the signature a router dispatches to:
// a method taking one pointer to a Context and returning one error.
func isActionMethod(fn *ast.FuncDecl) bool {
	if fn.Recv == nil || !fn.Name.IsExported() || fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
		return false
	}
	param := fn.Type.Params.List[0]
	if len(param.Names) > 1 {
		return false
	}
	star, ok := param.Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	switch t := star.X.(type) {
	case *ast.SelectorExpr:
		if t.Sel.Name != "Context" {
			return false
		}
	case *ast.Ident:
		if t.Name != "Context" {
			return false
		}
	default:
		return false
	}
	results := fn.Type.Results
	if results == nil || len(results.List) != 1 {
		return false
	}
	ident, ok := results.List[0].Type.(*ast.Ident)
	return ok && ident.Name == "error"
}

func (f *file) declaresAction() bool {
	return f.declaresMethod(isActionMethod)
}

func (f *file) declaresMethod(match func(*ast.FuncDecl) bool) bool {
	return f.declaresFunction(func(fn *ast.FuncDecl) bool { return fn.Recv != nil && match(fn) })
}

func (f *file) declaresFunction(match func(*ast.FuncDecl) bool) bool {
	found := false
	f.functions(func(fn *ast.FuncDecl) {
		if !found && match(fn) {
			found = true
		}
	})
	return found
}

// resultSelector reports whether fn returns exactly one value whose type is a
// qualified name ending in name: validation.Errors, events.Event.
func resultSelector(fn *ast.FuncDecl, name string) bool {
	results := fn.Type.Results
	if results == nil || len(results.List) != 1 || len(results.List[0].Names) > 1 {
		return false
	}
	selector, ok := results.List[0].Type.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == name
}

// featureOpeners are the kinds of artifact that name an entity of the
// application: a model is one, and a service, a policy, a controller and a
// repository are each written for one. Every other kind joins the feature its
// name points at and never opens one -- a job called SendInvoice is part of
// Invoice, and a feature called SendInvoice beside it would list one file.
var featureOpeners = map[string]bool{
	"model": true, "service": true, "policy": true, "controller": true, "repository": true,
}

// addFeatures opens one feature per entity named by a file that can open one,
// and puts every artifact into the feature of its entity.
func (s *mapState) addFeatures() {
	type place struct {
		label  string
		node   *MapNode
		weight int
	}
	opened := map[string]place{}
	for _, artifact := range s.artifacts {
		if artifact.entity == "" || artifact.joinsOnly || !featureOpeners[artifact.node.Kind] {
			continue
		}
		key := strings.ToLower(artifact.entity)
		candidate := place{label: artifact.entity, node: artifact.node, weight: featureLocationWeight(artifact.node.Kind)}
		current, found := opened[key]
		if !found || candidate.weight < current.weight ||
			candidate.weight == current.weight && mapBefore(candidate.node.File, candidate.node.Line, candidate.node.Column, current.node.File, current.node.Line, current.node.Column) {
			opened[key] = candidate
		}
	}
	for key, feature := range opened {
		id := "feature:" + graphID(feature.label)
		s.features[key] = id
		s.b.add(MapNode{
			ID: id, Kind: "feature", Label: feature.label, Detail: "Application feature",
			File: feature.node.File, Line: feature.node.Line, Column: feature.node.Column,
			EndLine: feature.node.Line, EndColumn: feature.node.Column,
		})
	}

	// Any other artifact joins the feature its name is, or the longest one its
	// name holds as a whole word: PurchaseOrderTenantScope_test is about
	// PurchaseOrder, SendInvoice about Invoice, and InvoiceResource about
	// Invoice -- and Notebook is not about Note.
	labels := make([]string, 0, len(opened))
	for _, feature := range opened {
		labels = append(labels, feature.label)
	}
	sort.Slice(labels, func(i, j int) bool {
		if len(labels[i]) != len(labels[j]) {
			return len(labels[i]) > len(labels[j])
		}
		return labels[i] < labels[j]
	})
	for _, artifact := range s.artifacts {
		if artifact.entity == "" {
			continue
		}
		id := s.features[strings.ToLower(artifact.entity)]
		if id == "" {
			for _, label := range labels {
				if holdsWord(artifact.entity, label) {
					id = s.features[strings.ToLower(label)]
					break
				}
			}
		}
		if id == "" {
			continue
		}
		artifact.node.Feature = id
		s.b.link(id, artifact.node.ID, EdgeContains, nil)
	}
}

// holdsWord reports whether name contains word where a word of a Go name
// begins and ends: at the start or after a lower-case letter, and before the
// end, an upper-case letter or an underscore.
func holdsWord(name, word string) bool {
	if word == "" {
		return false
	}
	for from := 0; ; {
		at := strings.Index(name[from:], word)
		if at < 0 {
			return false
		}
		at += from
		end := at + len(word)
		startsWord := at == 0 || name[at-1] == '_' || name[at-1] >= 'a' && name[at-1] <= 'z'
		if end < len(name) && name[end] == 's' {
			// The plural is the same word: Invoices is about Invoice.
			if end+1 == len(name) || name[end+1] == '_' || name[end+1] >= 'A' && name[end+1] <= 'Z' {
				end++
			}
		}
		endsWord := end == len(name) || name[end] == '_' || name[end] >= 'A' && name[end] <= 'Z'
		if startsWord && endsWord {
			return true
		}
		from = at + 1
	}
}

// ownerOf is the node a reference made inside fn belongs to: the action when
// fn is one, and the file's artifact otherwise.
func (s *mapState) ownerOf(f *file, fn *ast.FuncDecl) string {
	if fn != nil && isActionMethod(fn) {
		if id := s.actions[f.dir+"\x00"+receiverType(fn)+"."+fn.Name.Name]; id != "" {
			return id
		}
	}
	if artifact := s.artifacts[f.rel]; artifact != nil {
		return artifact.node.ID
	}
	return ""
}

// targetOf is the node a declaration of file f stands for: the entity file of
// a generated query file, and the file's own artifact otherwise.
func (s *mapState) targetOf(f *file) *mapArtifact {
	if owner, generated := s.queryOwners[f.rel]; generated {
		if artifact := s.artifacts[owner]; artifact != nil {
			return artifact
		}
	}
	return s.artifacts[f.rel]
}

// addActions puts one node per controller action into the map, and reads the
// shape of a controller no assertion and no route has named: a controller
// whose only action is Invoke is invokable, and any other is plain.
func (s *mapState) addActions() {
	rels := make([]string, 0, len(s.artifacts))
	for rel := range s.artifacts {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		artifact := s.artifacts[rel]
		if artifact.node.Kind != "controller" && artifact.node.Kind != "webhook" {
			continue
		}
		f := artifact.f
		var declared []string
		f.functions(func(fn *ast.FuncDecl) {
			if !isActionMethod(fn) {
				return
			}
			typeName := receiverType(fn)
			name := f.fset.Position(fn.Name.Pos())
			end := f.fset.Position(fn.End())
			id := "action:" + graphID(f.rel+"#"+typeName+"."+fn.Name.Name)
			s.b.add(MapNode{
				ID: id, Kind: "action", Label: typeName + "." + fn.Name.Name, Detail: f.rel,
				File: f.rel, Line: name.Line, Column: name.Column, EndLine: end.Line, EndColumn: end.Column,
				Parent: artifact.node.ID, Feature: artifact.node.Feature,
			})
			declared = append(declared, fn.Name.Name)
			s.actions[f.dir+"\x00"+typeName+"."+fn.Name.Name] = id
			s.b.link(artifact.node.ID, id, EdgeContains, nil)
			if artifact.node.Feature != "" {
				s.b.link(artifact.node.Feature, id, EdgeContains, nil)
			}
		})
		if artifact.node.Variant == "" && artifact.node.Kind == "controller" {
			artifact.node.Variant = "plain"
			if len(declared) == 1 && declared[0] == "Invoke" {
				artifact.node.Variant = "invokable"
			}
		}
	}
}

// addBorrowedGroups takes the native and community sections from the reading
// schema 1 already does of them, which is the same reading for both schemas.
func (s *mapState) addBorrowedGroups(files []*file) {
	borrowed := newGraphBuilder()
	addNativeScreens(borrowed, files)
	addNativeCapabilities(borrowed, files)
	addCommunityModules(borrowed, s.p, files)
	for _, node := range borrowed.nodes {
		s.b.add(MapNode{
			ID: node.ID, Kind: node.Kind, Label: node.Label, Detail: node.Detail, File: node.File,
			Line: node.Line, Column: node.Column, EndLine: node.Line, EndColumn: node.Column,
		})
	}
}

// addDiagnostics puts every finding into the map with the place it reports,
// the rule it carries and where that rule is documented.
//
// A finding names a line and not a column, so the node spans the text of that
// line: from its first character that is not blank to its last. That is the
// statement the rule read, and it is the range an editor underlines.
func (s *mapState) addDiagnostics(findings []Finding) {
	docLines := ruleDocLines(s.p.skills.local["arandu-doctor"])
	sources := map[string][]string{}
	seen := map[string]int{}
	for _, finding := range findings {
		base := "diagnostic:" + graphID(finding.Rule) + ":" + graphID(finding.File) + ":" + strconv.Itoa(finding.Line)
		seen[base]++
		id := base
		if seen[base] > 1 {
			id += ":" + strconv.Itoa(seen[base])
		}
		lines, read := sources[finding.File]
		if !read {
			if body, err := os.ReadFile(filepath.Join(s.p.root, filepath.FromSlash(finding.File))); err == nil {
				lines = strings.Split(string(body), "\n")
			}
			sources[finding.File] = lines
		}
		column, endColumn := lineExtent(lines, finding.Line)
		doc := doctorSkillOnline
		if at, documented := docLines[finding.Rule]; documented {
			doc = doctorSkill + "#L" + strconv.Itoa(at)
		}
		s.b.add(MapNode{
			ID: id, Kind: "diagnostic", Label: finding.Message, Detail: finding.Why,
			File: finding.File, Line: max(finding.Line, 1), Column: column,
			EndLine: max(finding.Line, 1), EndColumn: endColumn,
			Rule: finding.Rule, Severity: finding.Severity.String(), RuleDoc: doc,
		})
	}
}

// lineExtent answers the first and one past the last byte column of the text
// on a one-based line, or 1 and 1 for a line that is not there.
func lineExtent(lines []string, line int) (int, int) {
	if line < 1 || line > len(lines) {
		return 1, 1
	}
	text := strings.TrimRight(strings.TrimSuffix(lines[line-1], "\r"), " \t")
	start := len(text) - len(strings.TrimLeft(text, " \t"))
	if start == len(text) {
		return 1, 1
	}
	return start + 1, len(text) + 1
}

// ruleDocLines maps each rule the doctor skill documents to the one-based line
// of its row: a table row whose first cell is the rule's name in backticks.
func ruleDocLines(skill []byte) map[string]int {
	out := map[string]int{}
	for i, line := range strings.Split(string(skill), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "| `")
		if !ok {
			continue
		}
		name, _, ok := strings.Cut(rest, "`")
		if ok && name != "" {
			if _, found := out[name]; !found {
				out[name] = i + 1
			}
		}
	}
	return out
}

// mapLocation is where a node of f begins and ends.
func mapLocation(f *file, n ast.Node) *MapLocation {
	start := f.fset.Position(n.Pos())
	end := f.fset.Position(n.End())
	return &MapLocation{File: f.rel, Line: start.Line, Column: start.Column, EndLine: end.Line, EndColumn: end.Column}
}
