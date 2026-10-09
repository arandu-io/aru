package lsp_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/doctor"
	"github.com/arandu-io/aru/internal/kyse"
	"github.com/arandu-io/aru/internal/lsp"
	"github.com/arandu-io/aru/tests"
)

// exchange is what one session answered: the result or the error of each
// request by its id, and every notification, in order.
type exchange struct {
	results       map[string]json.RawMessage
	errors        map[string]int
	notifications []struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
}

// session initializes the server on root, sends the messages, shuts it down
// and answers what came back.
func session(t *testing.T, root, initializationOptions string, options lsp.Options, messages ...string) exchange {
	t.Helper()
	params := fmt.Sprintf(`{"rootUri":%q}`, fileURI(root))
	if initializationOptions != "" {
		params = fmt.Sprintf(`{"rootUri":%q,"initializationOptions":%s}`, fileURI(root), initializationOptions)
	}
	all := append([]string{`{"jsonrpc":"2.0","id":"initialize","method":"initialize","params":` + params + `}`}, messages...)
	all = append(all, `{"jsonrpc":"2.0","id":"shutdown","method":"shutdown"}`, `{"jsonrpc":"2.0","method":"exit"}`)
	var output bytes.Buffer
	if err := lsp.ServeWith(bytes.NewReader(frames(all...)), &output, options); err != nil {
		t.Fatalf("serve: %v", err)
	}
	got := exchange{results: map[string]json.RawMessage{}, errors: map[string]int{}}
	for _, body := range readFrames(t, output.Bytes()) {
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &message); err != nil {
			t.Fatalf("decode message: %v", err)
		}
		if message.Method != "" {
			got.notifications = append(got.notifications, struct {
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}{message.Method, message.Params})
			continue
		}
		var id string
		_ = json.Unmarshal(message.ID, &id)
		if message.Error != nil {
			got.errors[id] = message.Error.Code
			continue
		}
		got.results[id] = message.Result
	}
	return got
}

func fileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String()
}

// mapProject is the generated project, with an empty module cache so the
// answers are about it and not about the machine.
func mapProject(t *testing.T) string {
	t.Helper()
	t.Setenv("GOMODCACHE", t.TempDir())
	return tests.MapProject(t)
}

func readSource(t *testing.T, root, rel string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// at renders a position request on a document of the project.
func at(method, id, root, rel string, line, character int, extra string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%q,"method":%q,"params":{"textDocument":{"uri":%q},"position":{"line":%d,"character":%d}%s}}`,
		id, method, fileURI(filepath.Join(root, filepath.FromSlash(rel))), line, character, extra)
}

// placesOf renders locations as "rel:line" with zero-based lines.
func placesOf(t *testing.T, root string, raw json.RawMessage) []string {
	t.Helper()
	var locations []protocolLocation
	if err := json.Unmarshal(raw, &locations); err != nil {
		t.Fatalf("decode locations: %v: %s", err, raw)
	}
	var out []string
	for _, location := range locations {
		parsed, err := url.Parse(location.URI)
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(root, parsed.Path)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s:%d", filepath.ToSlash(rel), location.Range.Start.Line))
	}
	return out
}

// lineOf answers the zero-based line of the first line of a file holding
// needle.
func lineOf(t *testing.T, root, rel, needle string) int {
	t.Helper()
	line, _ := offsetOf(t, readSource(t, root, rel), needle)
	return line
}

func TestInitializeAdvertisesTheMapCapabilities(t *testing.T) {
	root := t.TempDir()
	for _, options := range []string{"", `{"doctorDiagnostics":true}`} {
		got := session(t, root, options, lsp.Options{})
		var initialize struct {
			Capabilities struct {
				ReferencesProvider      bool `json:"referencesProvider"`
				DocumentSymbolProvider  bool `json:"documentSymbolProvider"`
				WorkspaceSymbolProvider bool `json:"workspaceSymbolProvider"`
				TextDocumentSync        struct {
					Save bool `json:"save"`
				} `json:"textDocumentSync"`
				Experimental struct {
					Schemas           []int `json:"aranduProjectGraphSchemas"`
					Catalog           bool  `json:"aranduCatalog"`
					DoctorDiagnostics bool  `json:"aranduDoctorDiagnostics"`
				} `json:"experimental"`
			} `json:"capabilities"`
		}
		if err := json.Unmarshal(got.results["initialize"], &initialize); err != nil {
			t.Fatal(err)
		}
		c := initialize.Capabilities
		if !c.ReferencesProvider || !c.DocumentSymbolProvider || !c.WorkspaceSymbolProvider || !c.TextDocumentSync.Save {
			t.Errorf("initialize does not advertise the navigation of the map: %+v", c)
		}
		if !reflect.DeepEqual(c.Experimental.Schemas, []int{1, 2}) || !c.Experimental.Catalog {
			t.Errorf("experimental capabilities = %+v", c.Experimental)
		}
		if c.Experimental.DoctorDiagnostics != (options != "") {
			t.Errorf("with options %q the doctor diagnostics read %v", options, c.Experimental.DoctorDiagnostics)
		}
	}
}

// TestProjectGraphAnswersSchemaTwoOnlyWhenAskedFor: the second schema in the
// protocol's terms, and a refusal for a schema this server does not have.
func TestProjectGraphAnswersSchemaTwoOnlyWhenAskedFor(t *testing.T) {
	root := mapProject(t)
	got := session(t, root, "", lsp.Options{},
		`{"jsonrpc":"2.0","id":"two","method":"arandu/projectGraph","params":{"schemaVersion":2}}`,
		`{"jsonrpc":"2.0","id":"three","method":"arandu/projectGraph","params":{"schemaVersion":3}}`,
		`{"jsonrpc":"2.0","id":"one","method":"arandu/projectGraph","params":{"schemaVersion":1}}`,
	)
	if got.errors["three"] != -32602 {
		t.Errorf("schema 3 answered %d, want -32602", got.errors["three"])
	}
	var one struct {
		SchemaVersion int `json:"schemaVersion"`
	}
	if err := json.Unmarshal(got.results["one"], &one); err != nil || one.SchemaVersion != 1 {
		t.Errorf("schemaVersion 1 answered schema %d: %v", one.SchemaVersion, err)
	}

	var m doctor.ProjectMap
	if err := json.Unmarshal(got.results["two"], &m); err != nil {
		t.Fatalf("decode schema 2: %v", err)
	}
	if m.SchemaVersion != 2 || m.Profile != "performance" || len(m.EdgeKinds) != 9 {
		t.Fatalf("schema %d, profile %q, %d edge kinds", m.SchemaVersion, m.Profile, len(m.EdgeKinds))
	}
	for _, node := range m.Nodes {
		if node.File == "" {
			continue
		}
		if !strings.HasPrefix(node.File, fileURI(root)+"/") {
			t.Errorf("node %s points at %q, not a URI under the root", node.ID, node.File)
		}
		if node.Kind == "diagnostic" && (node.Rule == "" || node.RuleDoc == "" || node.Severity == "") {
			t.Errorf("diagnostic %s lost its rule: %+v", node.ID, node)
		}
	}
	wantLine := lineOf(t, root, "routes/web.go", `app.Resource("invoices"`)
	found := false
	for _, node := range m.Nodes {
		if node.Kind == "route" && node.Label == "GET /app/invoices" {
			found = true
			if node.Line != wantLine || node.Column != 1 || node.Name != "invoices.index" {
				t.Errorf("route GET /app/invoices sits at %d:%d named %q, want %d:1 invoices.index", node.Line, node.Column, node.Name, wantLine)
			}
		}
	}
	if !found {
		t.Error("schema 2 lists no route GET /app/invoices")
	}
	for _, edge := range m.Edges {
		if edge.At != nil && !strings.HasPrefix(edge.At.File, "file://") {
			t.Errorf("edge %s -> %s was read from %q, not a URI", edge.From, edge.To, edge.At.File)
		}
	}
}

func TestCatalogAnswersTheDirectivesAndTheCommandsItWasGiven(t *testing.T) {
	commands := []lsp.Command{{Name: "doctor", Usage: "aru doctor [--strict]", Description: "check", Flags: []string{"--strict"}}}
	got := session(t, t.TempDir(), "", lsp.Options{Commands: commands},
		`{"jsonrpc":"2.0","id":"catalog","method":"arandu/catalog"}`)
	var catalog struct {
		Directives []struct {
			Name     string `json:"name"`
			Kind     string `json:"kind"`
			ClosedBy string `json:"closedBy"`
			Closes   string `json:"closes"`
		} `json:"directives"`
		Commands []lsp.Command `json:"commands"`
	}
	if err := json.Unmarshal(got.results["catalog"], &catalog); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(catalog.Commands, commands) {
		t.Errorf("commands = %+v, want what the server was given", catalog.Commands)
	}
	if len(catalog.Directives) != len(kyse.Directives()) {
		t.Errorf("%d directives, the compiler knows %d", len(catalog.Directives), len(kyse.Directives()))
	}
	kinds := map[string]string{}
	for _, d := range catalog.Directives {
		kinds[d.Name] = d.Kind + ":" + d.ClosedBy + d.Closes
	}
	for name, want := range map[string]string{"if": "block:endif", "endif": "end:if", "csrf": "inline:", "go": "block:endgo"} {
		if kinds[name] != want {
			t.Errorf("directive %s is %q, want %q", name, kinds[name], want)
		}
	}
}

// TestDefinitionOpensWhatARouteStringReaches: a route name passed to ctx.URL
// opens the action the route runs, and a string of a registration opens the
// actions the registration reaches -- the places the Go server cannot answer.
func TestDefinitionOpensWhatARouteStringReaches(t *testing.T) {
	root := mapProject(t)
	controller := "app/Http/Controllers/InvoiceController.go"
	routesLine, routesCharacter := offsetOf(t, readSource(t, root, "routes/web.go"), `"invoices"`)
	urlLine, urlCharacter := offsetOf(t, readSource(t, root, controller), `"invoices.create"`)
	nameLine, nameCharacter := offsetOf(t, readSource(t, root, "routes/web.go"), `"exports.store"`)
	got := session(t, root, "", lsp.Options{},
		at("textDocument/definition", "resource", root, "routes/web.go", routesLine, routesCharacter+3, ""),
		at("textDocument/definition", "url", root, controller, urlLine, urlCharacter+3, ""),
		at("textDocument/definition", "name", root, "routes/web.go", nameLine, nameCharacter+3, ""),
	)

	var want []string
	for _, action := range []string{"Index", "Create", "Store", "Show", "Edit", "Update", "Destroy"} {
		want = append(want, fmt.Sprintf("%s:%d", controller, lineOf(t, root, controller, ") "+action+"(ctx")))
	}
	sort.Strings(want)
	if got := placesOf(t, root, got.results["resource"]); !reflect.DeepEqual(sorted(got), want) {
		t.Errorf("the resource registration opens %v, want the seven actions %v", got, want)
	}
	if got := placesOf(t, root, got.results["url"]); !reflect.DeepEqual(got, []string{fmt.Sprintf("%s:%d", controller, lineOf(t, root, controller, ") Create(ctx"))}) {
		t.Errorf("invoices.create opens %v", got)
	}
	export := "app/Http/Controllers/ExportController.go"
	if got := placesOf(t, root, got.results["name"]); !reflect.DeepEqual(got, []string{fmt.Sprintf("%s:%d", export, lineOf(t, root, export, ") Invoke(ctx"))}) {
		t.Errorf("exports.store opens %v", got)
	}
}

func sorted(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

// TestReferencesWalkTheEdges: from a declaration, the places the map read an
// edge of it from in other files.
func TestReferencesWalkTheEdges(t *testing.T) {
	root := mapProject(t)
	controller := "app/Http/Controllers/InvoiceController.go"
	policy := "app/Policies/InvoicePolicy.go"
	request := "app/Http/Requests/InvoiceRequest.go"
	showLine, showCharacter := offsetOf(t, readSource(t, root, controller), "Show(ctx")
	policyLine, policyCharacter := offsetOf(t, readSource(t, root, policy), "type InvoicePolicy")
	requestLine, requestCharacter := offsetOf(t, readSource(t, root, request), "type InvoiceRequest")
	got := session(t, root, "", lsp.Options{},
		at("textDocument/references", "show", root, controller, showLine, showCharacter+1, `,"context":{"includeDeclaration":true}`),
		at("textDocument/references", "policy", root, policy, policyLine, policyCharacter+6, `,"context":{"includeDeclaration":false}`),
		at("textDocument/references", "request", root, request, requestLine, requestCharacter+6, `,"context":{"includeDeclaration":false}`),
		at("textDocument/references", "layout", root, "resources/views/layouts/app.kyse.go", 0, 0, `,"context":{"includeDeclaration":false}`),
		at("textDocument/references", "index", root, "resources/views/invoices/index.kyse.go", 0, 0, `,"context":{"includeDeclaration":false}`),
	)

	show := placesOf(t, root, got.results["show"])
	for _, want := range []string{
		fmt.Sprintf("%s:%d", controller, showLine),
		fmt.Sprintf("routes/web.go:%d", lineOf(t, root, "routes/web.go", `app.Resource("invoices"`)),
	} {
		if !contains(show, want) {
			t.Errorf("references of Show = %v, missing %s", show, want)
		}
	}
	// What Show itself reaches -- the view it renders -- is written inside it,
	// and is not a place that refers to it.
	for _, place := range show {
		if strings.HasPrefix(place, controller+":") && place != fmt.Sprintf("%s:%d", controller, showLine) {
			t.Errorf("references of Show include %s, a line of its own file", place)
		}
	}
	policyPlaces := placesOf(t, root, got.results["policy"])
	for _, want := range []string{"app/Services/InvoiceService.go:", "tests/Unit/Invoice_test.go:"} {
		if !hasPrefix(policyPlaces, want) {
			t.Errorf("references of InvoicePolicy = %v, missing one in %s", policyPlaces, want)
		}
	}
	if hasPrefix(policyPlaces, policy+":") {
		t.Errorf("references of InvoicePolicy include its own file without being asked: %v", policyPlaces)
	}
	requestPlaces := placesOf(t, root, got.results["request"])
	for _, want := range []string{controller + ":", "app/Services/InvoiceService.go:"} {
		if !hasPrefix(requestPlaces, want) {
			t.Errorf("references of InvoiceRequest = %v, missing one in %s", requestPlaces, want)
		}
	}
	layout := placesOf(t, root, got.results["layout"])
	for _, view := range []string{"index", "show", "create", "edit"} {
		if !hasPrefix(layout, "resources/views/invoices/"+view+".kyse.go:") {
			t.Errorf("references of the layout = %v, missing the %s view", layout, view)
		}
	}
	index := placesOf(t, root, got.results["index"])
	if want := fmt.Sprintf("%s:%d", controller, lineOf(t, root, controller, `ctx.View("invoices.index"`)); !contains(index, want) {
		t.Errorf("references of invoices.index = %v, want %s", index, want)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func TestDocumentAndWorkspaceSymbolsListRoutesAndActions(t *testing.T) {
	root := mapProject(t)
	controller := "app/Http/Controllers/InvoiceController.go"
	got := session(t, root, "", lsp.Options{},
		fmt.Sprintf(`{"jsonrpc":"2.0","id":"routes","method":"textDocument/documentSymbol","params":{"textDocument":{"uri":%q}}}`, fileURI(filepath.Join(root, "routes", "web.go"))),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":"actions","method":"textDocument/documentSymbol","params":{"textDocument":{"uri":%q}}}`, fileURI(filepath.Join(root, filepath.FromSlash(controller)))),
		`{"jsonrpc":"2.0","id":"close","method":"workspace/symbol","params":{"query":"close"}}`,
		`{"jsonrpc":"2.0","id":"bad","method":"workspace/symbol","params":{}}`,
	)
	type symbol struct {
		Name   string `json:"name"`
		Detail string `json:"detail"`
		Kind   int    `json:"kind"`
		Range  struct {
			Start struct {
				Line int `json:"line"`
			} `json:"start"`
		} `json:"range"`
	}
	var routes, actions []symbol
	if err := json.Unmarshal(got.results["routes"], &routes); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got.results["actions"], &actions); err != nil {
		t.Fatal(err)
	}
	if len(routes) != 24 {
		t.Errorf("routes/web.go outlines %d routes, want 24", len(routes))
	}
	for _, route := range routes {
		if route.Kind != 24 || route.Detail == "" && route.Name != "GET /health" {
			t.Errorf("route symbol %+v", route)
		}
	}
	var names []string
	for _, action := range actions {
		names = append(names, action.Name)
		if action.Kind != 6 {
			t.Errorf("action %s has kind %d, want 6", action.Name, action.Kind)
		}
		if action.Name == "InvoiceController.Index" && action.Detail != "GET /app/invoices" {
			t.Errorf("Index is reached by %q, want GET /app/invoices", action.Detail)
		}
	}
	if want := []string{"InvoiceController.Index", "InvoiceController.Show", "InvoiceController.Create", "InvoiceController.Store", "InvoiceController.Edit", "InvoiceController.Update", "InvoiceController.Destroy"}; !reflect.DeepEqual(names, want) {
		t.Errorf("the controller outlines %v, want %v in source order", names, want)
	}

	var found []struct {
		Name          string `json:"name"`
		Kind          int    `json:"kind"`
		ContainerName string `json:"containerName"`
	}
	if err := json.Unmarshal(got.results["close"], &found); err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, symbol := range found {
		labels = append(labels, symbol.Name)
	}
	if want := []string{"POST /tasks/{task}/close", "TaskController.Close"}; !reflect.DeepEqual(labels, want) {
		t.Errorf("workspace symbols for close = %v, want %v", labels, want)
	}
	if got.errors["bad"] != -32602 {
		t.Errorf("a workspace symbol request without a query answered %d, want -32602", got.errors["bad"])
	}
}

// TestDoctorFindingsArePublishedToAClientThatAsks: with the option, the
// findings arrive as diagnostics carrying the rule as their code, its
// documentation, and the range of the text of the line; a document opened
// afterwards keeps them; without the option nothing is published, because
// the client draws them from the map already.
func TestDoctorFindingsArePublishedToAClientThatAsks(t *testing.T) {
	root := mapProject(t)
	quiet := session(t, root, "", lsp.Options{}, `{"jsonrpc":"2.0","method":"initialized","params":{}}`)
	if len(quiet.notifications) != 0 {
		t.Fatalf("a client that did not ask received %d notifications", len(quiet.notifications))
	}

	opened := ""
	asked := session(t, root, `{"doctorDiagnostics":true}`, lsp.Options{}, `{"jsonrpc":"2.0","method":"initialized","params":{}}`)
	type published struct {
		URI         string `json:"uri"`
		Diagnostics []struct {
			Range struct {
				Start position `json:"start"`
				End   position `json:"end"`
			} `json:"range"`
			Severity        int    `json:"severity"`
			Source          string `json:"source"`
			Code            string `json:"code"`
			CodeDescription struct {
				Href string `json:"href"`
			} `json:"codeDescription"`
			Message string `json:"message"`
		} `json:"diagnostics"`
	}
	count := 0
	for _, notification := range asked.notifications {
		var params published
		if err := json.Unmarshal(notification.Params, &params); err != nil {
			t.Fatal(err)
		}
		parsed, _ := url.Parse(params.URI)
		lines := strings.Split(readSource(t, root, mustRel(t, root, parsed.Path)), "\n")
		for _, d := range params.Diagnostics {
			count++
			if opened == "" {
				opened = params.URI
			}
			if d.Source != "aru doctor" || d.Code == "" || d.CodeDescription.Href == "" || d.Severity < 1 || d.Severity > 2 {
				t.Errorf("finding %+v is missing its rule or its documentation", d)
			}
			text := lines[d.Range.Start.Line]
			first := len(text) - len(strings.TrimLeft(text, " \t"))
			if d.Range.Start.Character != first || d.Range.End.Character != len(strings.TrimRight(text, " \t\r")) {
				t.Errorf("%s at line %d spans %d-%d of %q", d.Code, d.Range.Start.Line, d.Range.Start.Character, d.Range.End.Character, text)
			}
		}
	}
	if count == 0 {
		t.Fatal("no finding was published; the project has findings")
	}

	path, _ := url.Parse(opened)
	text := readSource(t, root, mustRel(t, root, path.Path))
	reopened := session(t, root, `{"doctorDiagnostics":true}`, lsp.Options{},
		`{"jsonrpc":"2.0","method":"initialized","params":{}}`,
		fmt.Sprintf(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":%q,"languageId":"go","version":1,"text":%q}}}`, opened, text),
	)
	last := reopened.notifications[len(reopened.notifications)-1]
	var params published
	if err := json.Unmarshal(last.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.URI != opened || len(params.Diagnostics) == 0 {
		t.Errorf("opening %s erased the doctor's findings: %+v", opened, params)
	}
}

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

func mustRel(t *testing.T, root, path string) string {
	t.Helper()
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(rel)
}

// TestACatalogueEntryReadsItsFlagsFromTheUsageLine: the flags an entry lists
// are the ones its usage line names, in order and once each, whatever dash
// they take and however the value is attached -- and a bare -- names none.
// The usage lines are the command table's own.
func TestACatalogueEntryReadsItsFlagsFromTheUsageLine(t *testing.T) {
	for usage, want := range map[string][]string{
		"aru doctor [--strict] [--profile=performance] | --list":                     {"--strict", "--profile", "--list"},
		"aru native:run [-server addr] [-dark]":                                      {"-server", "-dark"},
		"aru serve [-- flags for the application]":                                   {},
		"aru font:search [query] [--category serif] [--variable] [--limit 25|--all]": {"--category", "--variable", "--limit", "--all"},
		"aru queue:retry --tenant=<id> [<id>...] [--queue=default]":                  {"--tenant", "--queue"},
	} {
		entry := lsp.CommandFromUsage("name", usage, "description")
		if entry.Name != "name" || entry.Usage != usage || entry.Description != "description" {
			t.Errorf("the entry for %q does not carry what it was given: %+v", usage, entry)
		}
		if !reflect.DeepEqual(entry.Flags, want) {
			t.Errorf("%q names %v, want %v", usage, entry.Flags, want)
		}
	}
}
