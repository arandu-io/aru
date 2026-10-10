package doctor_test

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/doctor"
	"github.com/arandu-io/aru/tests"
)

// mapOf builds the second schema of a project, with an empty module cache so
// the answer is about the project and not about the machine.
func mapOf(t *testing.T, root string) doctor.ProjectMap {
	t.Helper()
	t.Setenv("GOMODCACHE", t.TempDir())
	analysis, err := doctor.AnalyzeWithMap(root, doctor.Conventional)
	if err != nil {
		t.Fatalf("AnalyzeWithMap: %v", err)
	}
	if analysis.Map == nil {
		t.Fatal("AnalyzeWithMap answered no map")
	}
	return *analysis.Map
}

// nodeAt answers the node of a file, failing when there is none.
func nodeAt(t *testing.T, m doctor.ProjectMap, kind, file string) doctor.MapNode {
	t.Helper()
	for _, node := range m.Nodes {
		if node.Kind == kind && node.File == file {
			return node
		}
	}
	var seen []string
	for _, node := range m.Nodes {
		if node.File == file {
			seen = append(seen, node.Kind)
		}
	}
	t.Fatalf("no %s node for %s; the file is %v", kind, file, seen)
	return doctor.MapNode{}
}

func nodeLabelled(t *testing.T, m doctor.ProjectMap, kind, label string) doctor.MapNode {
	t.Helper()
	for _, node := range m.Nodes {
		if node.Kind == kind && node.Label == label {
			return node
		}
	}
	t.Fatalf("no %s labelled %s", kind, label)
	return doctor.MapNode{}
}

// edgeSet renders the typed edges as "kind from -> to", by label.
func edgeSet(m doctor.ProjectMap) map[string]doctor.MapEdge {
	labels := map[string]string{}
	for _, node := range m.Nodes {
		labels[node.ID] = node.Kind + ":" + node.Label
	}
	out := map[string]doctor.MapEdge{}
	for _, edge := range m.Edges {
		out[edge.Kind+" "+labels[edge.From]+" -> "+labels[edge.To]] = edge
	}
	return out
}

// TestTheMapClassifiesByWhatAFileDeclares: the kind of an artifact is read
// from the contract it asserts and the shape it declares, so each of the
// generators' artifacts arrives as what it is -- a JSON resource and a
// notification are not "application" because their folders are not on a list.
func TestTheMapClassifiesByWhatAFileDeclares(t *testing.T) {
	m := mapOf(t, tests.MapProject(t))
	for file, want := range map[string]struct{ kind, variant string }{
		"app/Http/Resources/InvoiceResource.go":                          {"resource", ""},
		"app/Notifications/InvoicePaid.go":                               {"notification", ""},
		"app/Clients/StripeClient.go":                                    {"client", ""},
		"app/Clients/StripeFake.go":                                      {"client", "fake"},
		"app/Http/Controllers/StripeWebhookController.go":                {"webhook", ""},
		"app/Mcp/ShowInvoice.go":                                         {"mcp-tool", ""},
		"app/Mcp/Invoices.go":                                            {"mcp-resource", ""},
		"app/Mcp/ReviewInvoice.go":                                       {"mcp-prompt", ""},
		"database/factories/InvoiceFactory.go":                           {"factory", ""},
		"database/seeders/InvoiceSeeder.go":                              {"seeder", ""},
		"tests/Unit/Invoice_test.go":                                     {"test", ""},
		"tests/Feature/InvoiceTenantScope_test.go":                       {"test", ""},
		"app/Http/Controllers/InvoiceController.go":                      {"controller", "resource"},
		"app/Http/Controllers/SettingsController.go":                     {"controller", "singleton"},
		"app/Http/Controllers/ExportController.go":                       {"controller", "invokable"},
		"app/Http/Controllers/TaskController.go":                         {"controller", "resource"},
		"app/Http/Controllers/InvoiceAPIController.go":                   {"controller", "plain"},
		"app/Http/Requests/InvoiceRequest.go":                            {"request", ""},
		"app/Policies/InvoicePolicy.go":                                  {"policy", ""},
		"app/Models/Invoice.go":                                          {"model", ""},
		"app/Jobs/SendInvoice.go":                                        {"job", ""},
		"app/Events/InvoicePaid.go":                                      {"event", ""},
		"app/Listeners/NotifyAccounting.go":                              {"listener", ""},
		"app/Mail/WelcomeEmail.go":                                       {"mail", ""},
		"routes/web.go":                                                  {"route-file", ""},
		"database/migrations/2026_07_31_000001_create_invoices_table.go": {"migration", ""},
	} {
		var found *doctor.MapNode
		for i := range m.Nodes {
			switch m.Nodes[i].Kind {
			case "action", "feature", "route", "native-capability", "diagnostic":
				continue
			}
			if m.Nodes[i].File == file {
				found = &m.Nodes[i]
			}
		}
		if found == nil {
			t.Errorf("%s is not in the map", file)
			continue
		}
		if found.Kind != want.kind || found.Variant != want.variant {
			t.Errorf("%s is %s/%s, want %s/%s", file, found.Kind, found.Variant, want.kind, want.variant)
		}
	}
	if nested := nodeAt(t, m, "controller", "app/Http/Controllers/TaskController.go"); nested.NestedUnder != "projects" {
		t.Errorf("the nested controller reads nestedUnder %q, want projects", nested.NestedUnder)
	}
}

// TestAFileIsWhatItAssertsWhereverItSits: a notification written in a folder
// nobody listed is still a notification, which is the difference between
// reading the anatomy and reading the path.
func TestAFileIsWhatItAssertsWhereverItSits(t *testing.T) {
	root := tests.MapProject(t)
	writeFile(t, filepath.Join(root, "app", "Billing", "Overdue.go"), `package billing

import hnotifications "github.com/arandu-io/hesape/notifications"

type Overdue struct{}

var _ hnotifications.Notification = Overdue{}
`)
	m := mapOf(t, root)
	if got := nodeAt(t, m, "notification", "app/Billing/Overdue.go"); got.Label != "Overdue" {
		t.Errorf("the notification is labelled %q", got.Label)
	}
}

// TestGeneratedCodeIsNeverAFeature: the query file model:build writes joins
// the feature of the entity it was written from, is marked as generated, and
// opens nothing.
func TestGeneratedCodeIsNeverAFeature(t *testing.T) {
	m := mapOf(t, tests.MapProject(t))
	query := nodeAt(t, m, "model", "app/Models/InvoiceQuery.go")
	if !query.Generated {
		t.Error("the query file model:build writes is not marked generated")
	}
	invoice := nodeLabelled(t, m, "feature", "Invoice")
	if query.Feature != invoice.ID {
		t.Errorf("the query file joins %q, want the Invoice feature", query.Feature)
	}
	for _, node := range m.Nodes {
		if node.Kind == "feature" && strings.HasSuffix(node.Label, "Query") {
			t.Errorf("a feature is named %s, after generated code", node.Label)
		}
	}
	// Only the kinds that name an entity open a feature: the job, the mail
	// and the client are part of a feature or of none.
	var features []string
	for _, node := range m.Nodes {
		if node.Kind == "feature" {
			features = append(features, node.Label)
		}
	}
	sort.Strings(features)
	if want := []string{"Export", "Invoice", "InvoiceAPI", "Settings", "Task"}; !reflect.DeepEqual(features, want) {
		t.Errorf("features = %v, want %v", features, want)
	}
	if job := nodeAt(t, m, "job", "app/Jobs/SendInvoice.go"); job.Feature != invoice.ID {
		t.Errorf("SendInvoice joins %q, want the Invoice feature", job.Feature)
	}
}

// TestEachRouteIsANodeWithItsMethodPatternAndName: what the router would
// register, one node per method and pattern, under the group's prefix, with
// the nested parameters the inflector names and the action each one reaches.
func TestEachRouteIsANodeWithItsMethodPatternAndName(t *testing.T) {
	m := mapOf(t, tests.MapProject(t))
	labels := map[string]string{}
	for _, node := range m.Nodes {
		labels[node.ID] = node.Label
	}
	reaches := map[string]string{}
	for _, edge := range m.Edges {
		if edge.Kind == doctor.EdgeRoutesTo {
			reaches[edge.From] = labels[edge.To]
			if edge.At == nil || edge.At.File != "routes/web.go" {
				t.Errorf("routes-to %s carries no place in routes/web.go: %+v", labels[edge.From], edge.At)
			}
		}
	}
	var got []string
	for _, node := range m.Nodes {
		if node.Kind != "route" {
			continue
		}
		if node.Method+" "+node.Pattern != node.Label {
			t.Errorf("route %s is labelled %q", node.ID, node.Label)
		}
		got = append(got, node.Method+" "+node.Pattern+" "+node.Name+" "+reaches[node.ID])
	}
	sort.Strings(got)
	want := []string{
		"DELETE /app/invoices/{id} invoices.destroy InvoiceController.Destroy",
		"DELETE /tasks/{task} projects.tasks.destroy TaskController.Destroy",
		"GET /app/invoices invoices.index InvoiceController.Index",
		"GET /app/invoices/create invoices.create InvoiceController.Create",
		"GET /app/invoices/{id} invoices.show InvoiceController.Show",
		"GET /app/invoices/{id}/edit invoices.edit InvoiceController.Edit",
		"GET /health health ",
		"GET /projects/{project}/tasks projects.tasks.index TaskController.Index",
		"GET /projects/{project}/tasks/create projects.tasks.create TaskController.Create",
		"GET /settings settings.show SettingsController.Show",
		"GET /settings/edit settings.edit SettingsController.Edit",
		"GET /tasks/{task} projects.tasks.show TaskController.Show",
		"GET /tasks/{task}/edit projects.tasks.edit TaskController.Edit",
		"PATCH /app/invoices/{id} invoices.update InvoiceController.Update",
		"PATCH /settings settings.update SettingsController.Update",
		"PATCH /tasks/{task} projects.tasks.update TaskController.Update",
		"POST /app/invoices invoices.store InvoiceController.Store",
		"POST /exports exports.store ExportController.Invoke",
		"POST /projects/{project}/tasks projects.tasks.store TaskController.Store",
		"POST /tasks/{task}/close projects.tasks.close TaskController.Close",
		"POST /webhooks/stripe webhooks.stripe StripeWebhookController.Store",
		"PUT /app/invoices/{id} invoices.update InvoiceController.Update",
		"PUT /settings settings.update SettingsController.Update",
		"PUT /tasks/{task} projects.tasks.update TaskController.Update",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestTheEdgesAreReadFromTheCode: one edge of each kind, between the two
// artifacts the code links, with the place it was read from.
func TestTheEdgesAreReadFromTheCode(t *testing.T) {
	m := mapOf(t, tests.MapProject(t))
	edges := edgeSet(m)
	for want, at := range map[string]string{
		"routes-to route:GET /app/invoices -> action:InvoiceController.Index":         "routes/web.go",
		"validates-with action:InvoiceController.Store -> request:InvoiceRequest":     "app/Http/Controllers/InvoiceController.go",
		"validates-with service:InvoiceService -> request:InvoiceRequest":             "app/Services/InvoiceService.go",
		"authorizes service:InvoiceService -> policy:InvoicePolicy":                   "app/Services/InvoiceService.go",
		"persists service:InvoiceService -> model:Invoice":                            "app/Services/InvoiceService.go",
		"persists model:Invoice -> migration:2026_07_31_000001_create_invoices_table": "database/migrations/2026_07_31_000001_create_invoices_table.go",
		"renders action:InvoiceController.Index -> view:invoices.index":               "app/Http/Controllers/InvoiceController.go",
		"renders action:InvoiceAPIController.Index -> resource:InvoiceResource":       "app/Http/Controllers/InvoiceAPIController.go",
		"renders mail:WelcomeEmail -> view:mail.welcome-email":                        "app/Mail/WelcomeEmail.go",
		"renders view:invoices.index -> view:layouts.app":                             "resources/views/invoices/index.kyse.go",
		"tested-by service:InvoiceService -> test:Invoice_test":                       "tests/Unit/Invoice_test.go",
		"tested-by model:Invoice -> test:InvoiceTenantScope_test":                     "tests/Feature/InvoiceTenantScope_test.go",
		"tested-by client:StripeClient -> test:StripeClient_test":                     "tests/Unit/StripeClient_test.go",
		"dispatches action:StripeWebhookController.Store -> job:SendInvoice":          "app/Http/Controllers/StripeWebhookController.go",
		"dispatches action:StripeWebhookController.Store -> event:InvoicePaid":        "app/Http/Controllers/StripeWebhookController.go",
		"listens-to listener:NotifyAccounting -> event:InvoicePaid":                   "app/Listeners/NotifyAccounting.go",
		"contains controller:InvoiceController -> action:InvoiceController.Index":     "",
	} {
		edge, found := edges[want]
		if !found {
			t.Errorf("missing edge %s", want)
			continue
		}
		switch {
		case at == "" && edge.At != nil:
			t.Errorf("%s carries a place, and containment is written nowhere", want)
		case at != "" && (edge.At == nil || edge.At.File != at || edge.At.Line < 1 || edge.At.Column < 1):
			t.Errorf("%s was read from %+v, want a place in %s", want, edge.At, at)
		}
	}

	// The near misses: a controller reads a model only to format it, and a
	// policy names the model it decides about. Neither writes a row.
	for _, unwanted := range []string{
		"persists controller:InvoiceController -> model:Invoice",
		"persists action:InvoiceController.Index -> model:Invoice",
		"persists policy:InvoicePolicy -> model:Invoice",
		"dispatches bootstrap:app -> job:SendInvoice",
	} {
		if _, found := edges[unwanted]; found {
			t.Errorf("the map draws %s, which the code does not do", unwanted)
		}
	}

	kinds := map[string]bool{}
	for _, kind := range m.EdgeKinds {
		if kind.Meaning == "" || kind.Follows == "" || kind.DoesNotFollow == "" {
			t.Errorf("edge kind %s does not state its reach: %+v", kind.Kind, kind)
		}
		kinds[kind.Kind] = true
	}
	for _, edge := range m.Edges {
		if !kinds[edge.Kind] {
			t.Errorf("edge %s -> %s has kind %s, which EdgeKinds does not list", edge.From, edge.To, edge.Kind)
		}
	}
	for _, kind := range []string{"contains", "routes-to", "validates-with", "authorizes", "persists", "renders", "tested-by", "dispatches", "listens-to"} {
		if !kinds[kind] {
			t.Errorf("EdgeKinds does not list %s", kind)
		}
	}
}

// TestADiagnosticCarriesItsRuleItsPlaceAndItsDocumentation: the finding keeps
// the rule's name, the level, the line and the column its text starts at,
// and where the rule is documented -- the project's own skill when it carries
// one, at the row of the rule, and the skeleton's otherwise.
func TestADiagnosticCarriesItsRuleItsPlaceAndItsDocumentation(t *testing.T) {
	root := tests.MapProject(t)
	m := mapOf(t, root)
	diagnostics := 0
	for _, node := range m.Nodes {
		if node.Kind != "diagnostic" {
			continue
		}
		diagnostics++
		if node.Rule == "" || node.Severity == "" || node.Line < 1 || node.Column < 1 || node.EndColumn <= node.Column {
			t.Errorf("diagnostic %s is incomplete: %+v", node.ID, node)
		}
		if !strings.HasPrefix(node.RuleDoc, "https://") {
			t.Errorf("without the skill, %s is documented at %q", node.Rule, node.RuleDoc)
		}
	}
	if diagnostics == 0 {
		t.Fatal("the fixture produced no diagnostic, so nothing here was checked")
	}

	var rule string
	for _, node := range m.Nodes {
		if node.Kind == "diagnostic" {
			rule = node.Rule
			break
		}
	}
	writeFile(t, filepath.Join(root, ".agents", "skills", "arandu-doctor", "SKILL.md"),
		"---\nname: arandu-doctor\n---\n\n| rule | severity |\n| --- | --- |\n| `"+rule+"` | warning |\n")
	m = mapOf(t, root)
	for _, node := range m.Nodes {
		if node.Kind == "diagnostic" && node.Rule == rule {
			if want := ".agents/skills/arandu-doctor/SKILL.md#L7"; node.RuleDoc != want {
				t.Errorf("%s is documented at %q, want %q", rule, node.RuleDoc, want)
			}
		}
	}
}

// TestTheDiagnosticColumnIsWhereTheLineSaysSomething: a finding names a line,
// and the node starts at the first character of that line that is not blank.
func TestTheDiagnosticColumnIsWhereTheLineSaysSomething(t *testing.T) {
	m := mapOf(t, fixture(t, "violations"))
	checked := 0
	for _, node := range m.Nodes {
		if node.Kind != "diagnostic" || node.Rule != "grant-not-checked" {
			continue
		}
		checked++
		if node.Column < 1 || node.EndColumn <= node.Column || node.EndLine != node.Line {
			t.Errorf("grant-not-checked spans %d:%d-%d:%d", node.Line, node.Column, node.EndLine, node.EndColumn)
		}
	}
	if checked == 0 {
		t.Fatal("violations has no grant-not-checked finding to check")
	}
}

// TestTheProfileComesFromTheManifest: a project that declares performance is
// checked against it, and one that declares nothing is conventional.
func TestTheProfileComesFromTheManifest(t *testing.T) {
	root := tests.MapProject(t)
	profile, err := doctor.DeclaredProfile(root)
	if err != nil {
		t.Fatal(err)
	}
	if profile != doctor.Performance {
		t.Errorf("a project declaring both profiles is checked against %s, want performance", profile)
	}
	writeFile(t, filepath.Join(root, "arandu.mod.toml"), "name = \"example/project\"\nprofiles = [\"conventional\"]\n")
	if profile, err = doctor.DeclaredProfile(root); err != nil || profile != doctor.Conventional {
		t.Errorf("a conventional project reads %s, %v", profile, err)
	}
	empty := t.TempDir()
	if profile, err = doctor.DeclaredProfile(empty); err != nil || profile != doctor.Conventional {
		t.Errorf("a project without a manifest reads %s, %v", profile, err)
	}

	t.Setenv("GOMODCACHE", t.TempDir())
	analysis, err := doctor.AnalyzeWithMap(tests.MapProject(t), doctor.Performance)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Map.Profile != "performance" || analysis.Map.SchemaVersion != 2 {
		t.Errorf("map profile %q, schema %d", analysis.Map.Profile, analysis.Map.SchemaVersion)
	}
}

// TestTheMapIsDeterministicAndConsistent: two builds of one tree are equal,
// every edge and every group names a node that exists, and the first schema
// built beside it is the one Analyze builds alone.
func TestTheMapIsDeterministicAndConsistent(t *testing.T) {
	root := tests.MapProject(t)
	first, second := mapOf(t, root), mapOf(t, root)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("two builds of the same tree differ")
	}
	ids := map[string]bool{}
	for i, node := range first.Nodes {
		if ids[node.ID] {
			t.Errorf("node %s repeats", node.ID)
		}
		ids[node.ID] = true
		if i > 0 && first.Nodes[i-1].ID > node.ID {
			t.Errorf("nodes are not sorted at %d", i)
		}
	}
	for _, edge := range first.Edges {
		if !ids[edge.From] || !ids[edge.To] {
			t.Errorf("edge %s -> %s names a node that is not there", edge.From, edge.To)
		}
	}
	grouped := 0
	for _, group := range first.Groups {
		for _, id := range group.NodeIDs {
			if !ids[id] {
				t.Errorf("group %s lists %s, which is not a node", group.ID, id)
			}
			grouped++
		}
	}
	if grouped != len(first.Nodes) {
		t.Errorf("%d nodes are grouped of %d", grouped, len(first.Nodes))
	}

	withMap, err := doctor.AnalyzeWithMap(root, doctor.Conventional)
	if err != nil {
		t.Fatal(err)
	}
	alone, err := doctor.Analyze(root, doctor.Conventional)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(withMap.Graph, alone.Graph) || !reflect.DeepEqual(withMap.Findings, alone.Findings) {
		t.Error("building the second schema changed the first, or the findings")
	}
	if alone.Map != nil {
		t.Error("Analyze built the second schema it was not asked for")
	}
}

// TestTheCleanFixtureRoutesThroughAControllerParameter: the routes of the
// generator's clean shape take the controller as a typed parameter, and both
// the action route and the resource reach it.
func TestTheCleanFixtureRoutesThroughAControllerParameter(t *testing.T) {
	m := mapOf(t, fixture(t, "clean"))
	edges := edgeSet(m)
	for _, want := range []string{
		"routes-to route:GET / -> action:InvoiceController.Index",
		"routes-to route:GET /invoices -> action:InvoiceController.Index",
		"routes-to route:GET /invoices/{id} -> action:InvoiceController.Show",
		"renders action:InvoiceController.Index -> view:invoices.index",
	} {
		if _, found := edges[want]; !found {
			var have []string
			for key := range edges {
				if strings.HasPrefix(key, "routes-to") || strings.HasPrefix(key, "renders") {
					have = append(have, key)
				}
			}
			sort.Strings(have)
			t.Errorf("missing %s; have %s", want, strconv.Quote(strings.Join(have, "; ")))
		}
	}
}

// scopedRoutes registers routes through variables that share a name across
// scopes, and through a controller held in a local variable. A router's prefix
// and a controller's type are read from the declaration the identifier
// denotes, not from the last variable spelled the same way.
const scopedRoutes = `package routes

import (
	"github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/security"

	controllers "example.test/project/app/Http/Controllers"
)

type Deps struct {
	Invoice  *controllers.InvoiceController
	Settings *controllers.SettingsController
	Sessions *security.SessionStore
}

func Web(r *http.Router, d Deps) {
	app := r.Group("/app")
	{
		app := app.Group("/admin")
		app.Get("/stats", d.Invoice.Index).Name("admin.stats")
	}
	app.Get("/home", d.Invoice.Index).Name("home")

	app = app.Group("/v2")
	app.Get("/later", d.Invoice.Index).Name("later")

	settings := d.Settings
	r.Singleton("settings", settings)
}
`

func TestARouteIsReadThroughTheVariableItsNameDenotes(t *testing.T) {
	root := tests.MapProject(t)
	if err := os.WriteFile(filepath.Join(root, "routes", "web.go"), []byte(scopedRoutes), 0o644); err != nil {
		t.Fatal(err)
	}
	m := mapOf(t, root)
	var got []string
	for _, node := range m.Nodes {
		if node.Kind == "route" {
			got = append(got, node.Method+" "+node.Pattern+" "+node.Name)
		}
	}
	sort.Strings(got)
	want := []string{
		"GET /app/admin/stats admin.stats",
		"GET /app/home home",
		"GET /app/v2/later later",
		"GET /settings settings.show",
		"GET /settings/edit settings.edit",
		"PATCH /settings settings.update",
		"PUT /settings settings.update",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
