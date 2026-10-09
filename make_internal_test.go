package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/gen"
	"github.com/arandu-io/aru/internal/testlayout"
)

// The wiring a command prints is tested for the same reason `wiring` is: an
// instruction that does not compile is worse than no instruction, because it is
// followed. The check is not that the text reads well -- it is that every
// identifier it names is one the generated file actually declares.

func TestTheControllerWiringNamesWhatTheFileDeclares(t *testing.T) {
	for _, c := range []struct {
		name     string
		stub     gen.Stub
		routes   []string
		declared []string
	}{
		{"resource", gen.Stub{Kind: gen.KindResource, Resource: "invoices"},
			[]string{`r.Resource("invoices", d.Invoice)`}, []string{"func (c *InvoiceController) Index("}},
		{"singleton", gen.Stub{Kind: gen.KindSingleton, Resource: "invoice"},
			[]string{`r.Singleton("invoice", d.Invoice)`}, []string{"func (c *InvoiceController) Show(", "_ fhttp.Updater"}},
		{"invokable", gen.Stub{Kind: gen.KindInvokable, Resource: "invoices"},
			[]string{`r.Invokable("POST", "/invoices", d.Invoice).Name("invoices")`},
			[]string{"func (c *InvoiceController) Invoke(", "_ fhttp.Invoker"}},
		{"nested with an action", gen.Stub{Kind: gen.KindResource, Resource: "invoices", Parent: "accounts", Action: "send"},
			[]string{`r.Resource("accounts.invoices", d.Invoice)`, `r.ResourceAction("POST", "accounts.invoices", "send", d.Invoice.Send)`},
			[]string{"func (c *InvoiceController) Send(", `ctx.Param("account")`, `ctx.Param("invoice")`}},
		{"plain", gen.Stub{Kind: gen.KindPlain, Resource: "invoices"}, []string{"(no route yet"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			stub := c.stub
			stub.Type, stub.ModulePath, stub.Entity = "InvoiceController", "example.test/project", "Invoice"
			files, err := gen.GenerateController(stub)
			if err != nil {
				t.Fatalf("GenerateController: %v", err)
			}
			source := string(files[0].Content)
			message := wiringController(stub, gen.Module{Name: "invoice", ModulePath: "example.test/project"})

			for _, route := range c.routes {
				if !strings.Contains(message, route) {
					t.Errorf("the printed routes do not hold %q:\n%s", route, message)
				}
			}
			// The constructor the message tells you to call has to exist, with
			// the arguments the message passes it.
			if !strings.Contains(source, "func NewInvoiceController() *InvoiceController") {
				t.Error("the generated controller has no constructor of the shape the message calls")
			}
			if !strings.Contains(message, "Invoice: controllers.NewInvoiceController(),") {
				t.Errorf("the printed bootstrap line does not call the generated constructor:\n%s", message)
			}
			for _, d := range c.declared {
				if !strings.Contains(source, d) {
					t.Errorf("the message registers what the file does not declare: %q is missing", d)
				}
			}
		})
	}
}

func TestTheMiddlewareWiringNamesTheConstructor(t *testing.T) {
	stub := gen.Stub{Type: "EnsureAccountIsActive", ModulePath: "example.test/project"}
	files, err := gen.GenerateMiddleware(stub)
	if err != nil {
		t.Fatalf("GenerateMiddleware: %v", err)
	}
	source := string(files[0].Content)
	message := wiringMiddleware(stub)

	if !strings.Contains(source, "func EnsureAccountIsActive() fhttp.Middleware") {
		t.Error("the generated middleware is not a constructor returning fhttp.Middleware")
	}
	if !strings.Contains(message, "appmiddleware.EnsureAccountIsActive()") {
		t.Errorf("the message does not call the generated constructor:\n%s", message)
	}
	// The alias is not decoration: bootstrap/app.go already imports the
	// framework package called middleware, so an unaliased import would not
	// compile in the one file the message sends you to.
	if !strings.Contains(message, `appmiddleware "example.test/project/app/Http/Middleware"`) {
		t.Errorf("the message does not alias the import:\n%s", message)
	}
}

func TestTheMigrationWiringNamesTheDeclaredType(t *testing.T) {
	spec := gen.MigrationSpec{
		ID: "2026_08_07_000002_add_status_to_invoices", Type: "AddStatusToInvoices", Table: "invoices",
		Fields: []gen.Field{{Name: "status", Type: gen.TypeString}},
	}
	file, err := gen.RenderMigration(spec)
	if err != nil {
		t.Fatalf("RenderMigration: %v", err)
	}
	source := string(file.Content)
	for _, want := range []string{
		"type AddStatusToInvoices struct{ migrations.BaseMigration }",
		"func init() { migrations.Register(AddStatusToInvoices{}) }",
		`func (AddStatusToInvoices) GetName() string { return "2026_08_07_000002_add_status_to_invoices" }`,
		"func (AddStatusToInvoices) Up(ctx context.Context, conn migrations.Connection) error {",
		"func (AddStatusToInvoices) Down(ctx context.Context, conn migrations.Connection) error {",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("the generated migration does not declare %q:\n%s", want, source)
		}
	}

	message := wiringMigration(spec, "example.test/project", false)
	if !strings.Contains(message, "AddStatusToInvoices is written") {
		t.Errorf("the message does not name the declared type:\n%s", message)
	}
	// The message sends you to a blank import, so the path it prints has to be
	// the one the package actually has.
	if !strings.Contains(message, `_ "example.test/project/database/migrations"`) {
		t.Errorf("the message does not print the import that links the package:\n%s", message)
	}
	// It also has to name a file this project has. It named main.go, which the
	// skeleton does not put the import in -- and does not have at that path.
	if strings.Contains(message, "main.go") {
		t.Errorf("the message sends you to main.go, which is not where the import goes:\n%s", message)
	}

	// The other half: a project that already links the package is told so, and
	// told to add nothing. Being sent to add an import you already have is how
	// a project ends up with two.
	linked := wiringMigration(spec, "example.test/project", true)
	if !strings.Contains(linked, "already") {
		t.Errorf("the message does not say the import is already there:\n%s", linked)
	}
	if strings.Contains(linked, "bootstrap/app.go") {
		t.Errorf("the message still sends you to a file to edit:\n%s", linked)
	}
	// Nothing lists a migration any more, so a message that told you to add one
	// to a list would send you to write a line that does not compile.
	if strings.Contains(message, "All()") {
		t.Errorf("the message still registers the migration in a list:\n%s", message)
	}
}

func TestTheSeederWiringNamesTheDeclaredType(t *testing.T) {
	spec := gen.SeederSpec{Entity: "Invoice"}
	file, err := gen.RenderSeeder(spec)
	if err != nil {
		t.Fatalf("RenderSeeder: %v", err)
	}
	if !strings.Contains(string(file.Content), "type InvoiceSeeder struct{}") {
		t.Error("the generated seeder does not declare the type the message registers")
	}
	message := wiringSeeder(spec)
	for _, want := range []string{"InvoiceSeeder{},", "DB *data.DB", "aru db:seed InvoiceSeeder"} {
		if !strings.Contains(message, want) {
			t.Errorf("the message does not say %q:\n%s", want, message)
		}
	}
	// db:seed refuses --class= with the word to type instead, so a message that
	// printed the flag would send the reader to a command that answers an error.
	if strings.Contains(message, "--class") {
		t.Errorf("the message still names the seeder with the flag db:seed refuses:\n%s", message)
	}
}

func TestTheModelMessageNamesTheTwoCommandsThatReachTheTable(t *testing.T) {
	m := gen.Module{Name: "invoice", Fields: []gen.Field{{Name: "reference", Type: gen.TypeString}},
		ModulePath: "example.test/project", Date: "2026_08_07"}
	message := modelWiring(m, true)

	for _, want := range []string{"aru make:module invoice", "aru make:policy invoice", "CreateInvoicesTable registers"} {
		if !strings.Contains(message, want) {
			t.Errorf("the message does not say %q:\n%s", want, message)
		}
	}
	// Both commands take the module name, which is lowercase. This line said
	// "aru make:policy Invoice" and pinned it there: the entity name is what the
	// message had, and make:policy refuses it with "module name must be
	// lowercase letters, digits and underscore". A suggestion the tool rejects
	// is a second error for whoever copies it.
	if strings.Contains(message, "aru make:policy "+m.Entity()) {
		t.Errorf("the message suggests make:policy with the entity name, which the command refuses:\n%s", message)
	}
	// Without --migration there is no migration, and a message that talked about
	// one would send you looking for a file that is not there.
	if strings.Contains(modelWiring(m, false), "CreateInvoicesTable") {
		t.Error("the message names a migration the command did not write")
	}
}

func TestTheRequestMessageDoesNotWireAnything(t *testing.T) {
	message := usageRequest(gen.Stub{Type: "StoreInvoice", ModulePath: "example.test/project"})
	if strings.Contains(message, "bootstrap/app.go") || strings.Contains(message, "routes.Deps") {
		t.Error("a request is a type, not a dependency: the message wires it")
	}
	if !strings.Contains(message, "there is no authorize() here") {
		t.Error("the message does not say where authorization lives, which is the one thing it is for")
	}
}

// TestTheNameIsReadTheWayItIsTyped: the developer this is for types the class
// name, and sometimes types the suffix as well.
func TestTheNameIsReadTheWayItIsTyped(t *testing.T) {
	for in, want := range map[string]string{
		"Invoice":           "InvoiceController",
		"invoice":           "InvoiceController",
		"invoice_line":      "InvoiceLineController",
		"InvoiceController": "InvoiceController",
	} {
		if got := suffixed(in, "Controller"); got != want {
			t.Errorf("suffixed(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"Invoice":       "Invoice",
		"InvoiceSeeder": "Invoice",
		"invoice":       "Invoice",
	} {
		if got := unsuffixed(in, "Seeder"); got != want {
			t.Errorf("unsuffixed(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestANestedNameIsRefused. A nested name such as Admin/UserController would
// mean a second package, a second import alias and a second shape of wiring.
func TestANestedNameIsRefused(t *testing.T) {
	for _, name := range []string{"Admin/UserController", `Admin\UserController`} {
		if err := checkFlatTree("make:controller", name); err == nil {
			t.Errorf("%q was accepted", name)
		}
	}
}

// TestGuessTableReadsTheMigrationName: the table and whether the migration
// creates it are read out of the migration name exactly as it is typed.
func TestGuessTableReadsTheMigrationName(t *testing.T) {
	for _, c := range []struct {
		name   string
		table  string
		create bool
		ok     bool
	}{
		{"create_invoices_table", "invoices", true, true},
		{"create_invoices", "invoices", true, true},
		{"add_status_to_invoices", "invoices", false, true},
		{"add_status_to_invoices_table", "invoices", false, true},
		{"drop_column_in_invoices", "invoices", false, true},
		{"backfill_everything", "", false, false},
	} {
		table, create, ok := guessTable(c.name)
		if table != c.table || create != c.create || ok != c.ok {
			t.Errorf("guessTable(%q) = %q,%v,%v; want %q,%v,%v", c.name, table, create, ok, c.table, c.create, c.ok)
		}
	}
}

// TestTheNextSequenceIsReadOffTheDirectory. The order of migrations is the
// order of their ids, so two files written on one day need two numbers -- and
// the number comes from the files rather than the clock, so it is the same
// number on every machine.
func TestTheNextSequenceIsReadOffTheDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "database", "migrations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	inv, err := readMigrationInventory(root)
	if err != nil {
		t.Fatalf("empty directory: %v", err)
	}
	if n := inv.nextSequence("2026_08_07"); n != 1 {
		t.Fatalf("empty directory: %d; want 1", n)
	}

	for _, name := range []string{
		"2026_08_07_000001_create_invoices_table.go",
		"2026_08_07_000004_add_status_to_invoices.go",
		"2026_08_06_000009_create_users_table.go", // another day: not counted
		"migrations.go", // not a migration: not counted
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package migrations\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if inv, err = readMigrationInventory(root); err != nil {
		t.Fatal(err)
	}
	if n := inv.nextSequence("2026_08_07"); n != 5 {
		t.Fatalf("got %d; want 5", n)
	}
}

// TestARequestThatCollidesIsRefusedByTypeAndNotByPath: make:module packs
// StoreInvoice and UpdateInvoice into InvoiceRequest.go, so a check on the file
// name would miss it and the real error would be a "redeclared in this block"
// three directories away.
func TestARequestThatCollidesIsRefusedByTypeAndNotByPath(t *testing.T) {
	dir := t.TempDir()
	source := "package requests\n\ntype StoreInvoice struct{}\ntype UpdateInvoice struct{}\n"
	if err := os.WriteFile(filepath.Join(dir, "InvoiceRequest.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	where, taken := requestTypeAlreadyDeclared(dir, "StoreInvoice")
	if !taken || where != "InvoiceRequest.go" {
		t.Errorf("StoreInvoice: %q, %v; want InvoiceRequest.go, true", where, taken)
	}
	if _, taken := requestTypeAlreadyDeclared(dir, "StoreReport"); taken {
		t.Error("a type nobody declared was reported as taken")
	}
}

// TestTheModuleWiringNamesTheImportsItsSnippetNeeds.
//
// The snippets the message prints call the service constructor and the sign-in
// guard, and the files they go in may not import those packages yet. Pasted as
// printed, the project must compile on the same Model-first path the generated
// service uses.
func TestTheModuleWiringNamesTheImportsItsSnippetNeeds(t *testing.T) {
	spec := gen.Module{
		Name:       "invoice",
		ModulePath: "example.test/project",
		Fields:     []gen.Field{{Name: "title", Type: gen.TypeString}},
	}
	message := wiring(spec, 12)

	if !strings.Contains(message, "services.New") {
		t.Fatalf("the snippet no longer calls the service constructor:\n%s", message)
	}
	// The page carries the token the CSRF middleware issued, so the controller
	// takes the service and nothing else.
	if !strings.Contains(message, "Invoice: controllers.NewInvoiceController(services.NewInvoiceService(db)),") {
		t.Errorf("the printed constructor does not take the service alone:\n%s", message)
	}

	for _, want := range []string{`"example.test/project/app/Services"`, `"github.com/arandu-io/framework/http/middleware"`} {
		if !strings.Contains(message, want) {
			t.Errorf("the message does not print the import %s, which its own snippet needs:\n%s", want, message)
		}
	}

	// The controller reads who is asking from the request, and only the guard
	// puts it there: a route printed without it is a route whose every action
	// the policy refuses.
	if !strings.Contains(message, `r.Group("", middleware.RequireAuth(d.Sessions)).Resource("invoices", d.Invoice)`) {
		t.Errorf("the route is not printed behind the sign-in guard:\n%s", message)
	}
	if strings.Contains(message, "/app/Repositories") || strings.Contains(message, "repositories.New") {
		t.Errorf("the Model-first wiring still introduces a CRUD repository:\n%s", message)
	}
}

// TestTheTenantClaimNamesTheTableTheMigrationCreates.
//
// A module generated with --tenant lands in the skeleton's tenant suite the
// first time it is generated: the table carries the column and nothing yet says
// every read of it is scoped. That is the suite working rather than a defect, so
// what the generator owes is the line to paste -- and the line is worth printing
// only if it names the table that was actually created. A claim about a table
// the catalogue does not have fails the same suite from the other side.
func TestTheTenantClaimNamesTheTableTheMigrationCreates(t *testing.T) {
	spec := gen.Module{
		Name:       "purchase_order",
		ModulePath: "example.test/project",
		Tenant:     true,
		Fields:     []gen.Field{{Name: "title", Type: gen.TypeString}},
		Date:       "2026_07_31",
	}

	files, err := gen.Generate(spec)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var migration string
	for _, f := range files {
		if strings.Contains(filepath.ToSlash(f.Path), "database/migrations/") {
			migration = string(f.Content)
		}
	}
	if migration == "" {
		t.Fatal("the module generated no migration, so there is no table for the claim to be about")
	}

	message := wiring(spec, len(files))

	// The file the line goes in, and the line itself, in the shape the suite's
	// own failure asks for.
	if !strings.Contains(message, "tests/Feature/TenantScope_test.go") {
		t.Errorf("the message does not name the file the claim goes in:\n%s", message)
	}
	claim := `"` + spec.Table() + `": "why every read of it is scoped",`
	if !strings.Contains(message, claim) {
		t.Errorf("the message does not print %s to paste:\n%s", claim, message)
	}

	// And the half that makes the line true rather than merely present.
	if !strings.Contains(migration, `"`+spec.Table()+`"`) {
		t.Errorf("the claim names %q and the migration creates no such table:\n%s", spec.Table(), migration)
	}
}

// TestTheTenantClaimIsAbsentWithoutATenantColumn is the other direction.
//
// A table with no tenant column that gets claimed anyway fails the same suite,
// so a module generated without --tenant must not be told to claim one.
func TestTheTenantClaimIsAbsentWithoutATenantColumn(t *testing.T) {
	message := wiring(gen.Module{
		Name:       "report",
		ModulePath: "example.test/project",
		Fields:     []gen.Field{{Name: "title", Type: gen.TypeString}},
	}, 12)

	for _, absent := range []string{"TenantScope_test.go", "why every read of it is scoped"} {
		if strings.Contains(message, absent) {
			t.Errorf("a module with no tenant column is told to claim one (%q):\n%s", absent, message)
		}
	}
}

// TestTheMissingEntityMessageSuggestsACommandThatRuns.
//
// make:policy refuses when the model is not there and prints how to create it.
// The command it printed had no --fields, which make:module requires, and
// echoed the argument as given, which make:module refuses when it is not
// lowercase -- so the fix for one error produced two.
func TestTheMissingEntityMessageSuggestsACommandThatRuns(t *testing.T) {
	for _, given := range []string{"invoice", "Invoice", "PurchaseOrder"} {
		message := missingEntity(gen.Module{Name: gen.Normalize(given)})

		if !strings.Contains(message, "--fields") {
			t.Errorf("%q: the suggested command omits --fields, which make:module requires:\n%s", given, message)
		}
		if !strings.Contains(message, "aru make:module "+gen.Normalize(given)+" ") {
			t.Errorf("%q: the suggested command does not carry the normalized name:\n%s", given, message)
		}
	}
}

// projectWithModule writes a project holding one generated module, and answers
// its root.
//
// The module is generated rather than written by hand, because what make:test is
// asked about has to be the tree make:module produces: a fixture typed out here
// would be a second opinion on what an entity looks like, and the copy nobody
// regenerates is the one that drifts.
// bareProject writes the three files a command needs to recognize a project, and
// nothing else.
func bareProject(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	for file, body := range map[string]string{
		"go.mod":      "module example.test/project\n",
		"main.go":     "package main\n\nfunc main() {}\n",
		"arandu.toml": "name = \"project\"\n",
	} {
		if err := os.WriteFile(filepath.Join(root, file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func projectWithModule(t *testing.T, name string) string {
	t.Helper()

	root := bareProject(t)

	files, err := gen.Generate(moduleUnderTest(name))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, _, err := gen.Write(root, files, false); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return root
}

// moduleUnderTest is the specification the project above is generated from. The
// date is fixed, or the migration id would move with the calendar.
func moduleUnderTest(name string) gen.Module {
	return gen.Module{
		Name:       name,
		Fields:     []gen.Field{{Name: "reference", Type: gen.TypeString, Required: true, Unique: true}},
		Tenant:     true,
		ModulePath: "example.test/project",
		Date:       "2026_07_31",
	}
}

// TestMakeTestWritesTheTestMakeModuleWrites runs the command and reads what
// landed.
//
// Three things are asserted about the file and they are the three the layout
// depends on: where it is, what it is called, and the package clause it carries.
// A test written one directory over, or named PurchaseOrderTest.go, compiles
// into a package and runs nowhere -- and the fourth assertion, that the bytes
// are the ones make:module writes, is the rule against two shapes of one file.
func TestMakeTestWritesTheTestMakeModuleWrites(t *testing.T) {
	root := projectWithModule(t, "purchase_order")
	t.Chdir(root)

	// The command is the way back after somebody deletes the test, so it is
	// deleted first: writing over the file make:module left would prove nothing
	// about a project that lost it.
	path := filepath.Join("tests", "Unit", "PurchaseOrder_test.go")
	if err := os.Remove(filepath.Join(root, path)); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := exercise(t, "make:test", "PurchaseOrder")
	if code != 0 {
		t.Fatalf("make:test exited %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "created "+path) {
		t.Errorf("the command does not say what it wrote: %q", stdout)
	}

	body, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatalf("make:test reported success and wrote no file: %v", err)
	}
	if !strings.HasPrefix(string(body), "package unit_test\n") {
		t.Errorf("the package clause is not unit_test: %.40q", body)
	}
	// What it asserts, and not only that it exists. A stub that declared a Test
	// function and checked nothing would satisfy every line above.
	for _, want := range []string{
		"func TestEveryPurchaseOrderReadRequiresAuthorization(t *testing.T)",
		"auth.ErrForbidden",
		"services.NewPurchaseOrderService(nil)",
		"model.Entity",
		"(*models.PurchaseOrderQuery).Get",
		"func TestThePurchaseOrderPolicyDeniesWhatItDoesNotKnow(t *testing.T)",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the generated test does not carry %q, so it proves less than it claims", want)
		}
	}

	// The same bytes make:module writes, because it is the same template
	// rendered through the same function.
	files, err := gen.Generate(moduleUnderTest("purchase_order"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, f := range files {
		if !strings.HasSuffix(f.Path, "PurchaseOrder_test.go") {
			continue
		}
		if string(f.Content) != string(body) {
			t.Error("make:test and make:module write different tests for one entity")
		}
		return
	}
	t.Fatal("make:module generated no test, so there was nothing to compare against")
}

// TestTheGeneratedTestSatisfiesTheLayoutChecks runs the four checks over the
// project the command wrote into.
//
// It is the same four `aru doctor` reports in a project and the same four this
// repository's own suite runs over itself, so a generated test that broke one of
// them would be the generator fighting the guard it ships.
func TestTheGeneratedTestSatisfiesTheLayoutChecks(t *testing.T) {
	root := projectWithModule(t, "purchase_order")
	t.Chdir(root)

	if code, _, stderr := exercise(t, "make:test", "PurchaseOrder", "--force"); code != 0 {
		t.Fatalf("make:test exited %d: %s", code, stderr)
	}

	for _, c := range testlayout.Checks() {
		result, err := c.Run(root)
		if err != nil {
			t.Errorf("%s: %v", c.Name, err)
			continue
		}
		// Every one of these is of the form "every X is Y", and every one is
		// true of no X at all: a check handed nothing reports nothing wrong.
		if result.Examined == 0 {
			t.Errorf("%s examined no file in the generated project, so it had nothing to be true of", c.Name)
		}
		for _, rel := range result.Unreadable {
			t.Errorf("%s could not parse %s of the generated project", c.Name, rel)
		}
		for _, p := range result.Problems {
			t.Errorf("%s: %s\n    %s", c.Name, p, p.Why)
		}
	}
}

// TestMakeTestRefusesATestThatWouldNotCompile covers the failure that costs the
// most.
//
// The generated file names the Model, Service and Policy packages, and Go has
// no way to skip an assertion that does not build. Each subject is checked
// before anything is written, and the refusal names the missing file and the
// command that creates it.
func TestMakeTestRefusesATestThatWouldNotCompile(t *testing.T) {
	for _, c := range []struct {
		what    string
		remove  string
		expects []string
	}{
		{"no model", filepath.Join("app", "Models", "PurchaseOrder.go"),
			[]string{"app/Models/PurchaseOrder.go", "aru make:model"}},
		{"no service", filepath.Join("app", "Services", "PurchaseOrderService.go"),
			[]string{"app/Services/PurchaseOrderService.go", "aru make:module"}},
		{"no policy", filepath.Join("app", "Policies", "PurchaseOrderPolicy.go"),
			[]string{"app/Policies/PurchaseOrderPolicy.go", "aru make:policy"}},
	} {
		t.Run(c.what, func(t *testing.T) {
			root := projectWithModule(t, "purchase_order")
			t.Chdir(root)
			if err := os.Remove(filepath.Join(root, c.remove)); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "tests", "Unit", "PurchaseOrder_test.go")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}

			code, _, stderr := exercise(t, "make:test", "PurchaseOrder")
			if code == 0 {
				t.Fatal("make:test wrote a test against a file that is not there")
			}
			for _, want := range c.expects {
				if !strings.Contains(stderr, want) {
					t.Errorf("the refusal does not mention %q: %q", want, stderr)
				}
			}
			if _, err := os.Stat(path); err == nil {
				t.Error("the command refused and wrote the file anyway")
			}
		})
	}
}

// TestMakeTestRefusesAnEntityThatDoesNotDeclareWhatTheTestNames is the half a
// stat cannot catch.
//
// A plain model written before the Model-first path is a file that is there but
// declares no table whose rows are the entity, which the emitted proof needs.
// The check reads the file rather than only finding it, so the answer is the
// missing boundary rather than a compiler error two commands later.
func TestMakeTestRefusesAnEntityThatDoesNotDeclareWhatTheTestNames(t *testing.T) {
	root := projectWithModule(t, "purchase_order")
	t.Chdir(root)

	model := filepath.Join(root, "app", "Models", "PurchaseOrder.go")
	body, err := os.ReadFile(model)
	if err != nil {
		t.Fatal(err)
	}
	stripped := strings.ReplaceAll(string(body), "return new(PurchaseOrder)", "return legacyModel()")
	if stripped == string(body) {
		t.Fatal("the generated model no longer allocates its rows with new(PurchaseOrder), so this test measures nothing")
	}
	if err := os.WriteFile(model, []byte(stripped), 0o644); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := exercise(t, "make:test", "PurchaseOrder", "--force")
	if code == 0 {
		t.Fatal("make:test wrote a test naming a symbol the model does not declare")
	}
	if !strings.Contains(stderr, "return new(PurchaseOrder)") {
		t.Errorf("the refusal does not name the missing Model boundary: %q", stderr)
	}
}

// migrationsIn is the migration file names of a project, sorted, for an
// assertion that reads the same on every run.
func migrationsIn(t *testing.T, root string) []string {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(root, "database", "migrations"))
	if err != nil {
		t.Fatalf("reading database/migrations: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// declaredTwice reports a package-level name that two files of the directory
// declare, which is the shape that does not compile.
func declaredTwice(t *testing.T, root string) (string, bool) {
	t.Helper()

	inv, err := readMigrationInventory(root)
	if err != nil {
		t.Fatalf("reading database/migrations: %v", err)
	}
	for _, name := range migrationsIn(t, root) {
		if where, taken := inv.declaredBy("CreateInvoicesTable", strings.TrimSuffix(name, ".go")); taken {
			return where, true
		}
	}
	return "", false
}

// TestMakeModuleKeepsTheMigrationItAlreadyWrote.
//
// A migration id is immutable: it is the key the applied-migrations table
// records. Writing a second one for a module that has one leaves
// database/migrations with two files declaring one type -- which does not
// compile, naming a file the developer never touched -- and a migration recorded
// as applied under an id no file carries any more.
//
// Both halves of the same failure are here. Twice in one day it is the sequence
// that moves; a day later it is the date, and that half was reachable from the
// moment the command existed.
func TestMakeModuleKeepsTheMigrationItAlreadyWrote(t *testing.T) {
	t.Run("twice in one day", func(t *testing.T) {
		root := bareProject(t)
		t.Chdir(root)

		var out, errOut strings.Builder
		for _, pass := range []string{"first", "second"} {
			out.Reset()
			errOut.Reset()
			args := []string{"invoice", "--fields", "reference:string!", "--force"}
			if err := makeModule(args, &out, &errOut); err != nil {
				t.Fatalf("the %s make:module: %v\n%s", pass, err, errOut.String())
			}
		}

		if names := migrationsIn(t, root); len(names) != 1 {
			t.Fatalf("database/migrations holds %v, want the one migration the module has", names)
		}
	})

	t.Run("a day later", func(t *testing.T) {
		// The module on disk carries a date in the past, which is what a project
		// generated on any earlier day looks like.
		root := projectWithModule(t, "invoice")
		t.Chdir(root)

		var out, errOut strings.Builder
		args := []string{"invoice", "--fields", "reference:string!", "--force"}
		if err := makeModule(args, &out, &errOut); err != nil {
			t.Fatalf("make:module: %v\n%s", err, errOut.String())
		}

		names := migrationsIn(t, root)
		if len(names) != 1 {
			t.Fatalf("regenerating a day later left %v: two files declaring one type do not compile", names)
		}
		if names[0] != "2026_07_31_000001_create_invoices_table.go" {
			t.Errorf("the migration was renamed to %s, and a migration id is immutable", names[0])
		}
		if where, twice := declaredTwice(t, root); twice {
			t.Errorf("CreateInvoicesTable is declared twice, in %s as well", where)
		}
	})
}

// TestMakeModelKeepsTheMigrationItAlreadyWrote is the same property for the
// other command that writes a migration for an entity.
func TestMakeModelKeepsTheMigrationItAlreadyWrote(t *testing.T) {
	root := projectWithModule(t, "invoice")
	t.Chdir(root)

	var out, errOut strings.Builder
	args := []string{"Invoice", "--fields", "reference:string!", "--migration", "--force"}
	if err := makeModel(args, &out, &errOut); err != nil {
		t.Fatalf("make:model --migration: %v\n%s", err, errOut.String())
	}

	names := migrationsIn(t, root)
	if len(names) != 1 {
		t.Fatalf("make:model --migration left %v: two files declaring one type do not compile", names)
	}
	if names[0] != "2026_07_31_000001_create_invoices_table.go" {
		t.Errorf("the migration was renamed to %s, and a migration id is immutable", names[0])
	}
}

// TestTheMakeModelUsageNamesEveryFlag reads the flags make:model accepts from
// the flag set itself -- what -h prints inside the command -- and requires the
// usage line in the dispatch table to name each one.
//
// The flags are declared inside the function, so nothing outside it can list
// them, and the usage line had stopped at --force while the command went on
// to take --seed, --policy, --requests, --all, --dry-run and three short forms.
func TestTheMakeModelUsageNamesEveryFlag(t *testing.T) {
	var stderr strings.Builder
	_ = makeModel([]string{"-h"}, &strings.Builder{}, &stderr)

	c, found := lookup("make:model")
	if !found {
		t.Fatal("make:model is not in the dispatch table")
	}
	declared := regexp.MustCompile(`(?m)^  -(\S+)`).FindAllStringSubmatch(stderr.String(), -1)
	if len(declared) == 0 {
		t.Fatalf("the flag set printed no flag, so nothing was compared:\n%s", stderr.String())
	}
	for _, m := range declared {
		spelled := "--" + m[1]
		if len(m[1]) == 1 {
			spelled = "-" + m[1] + "|"
		}
		if !strings.Contains(c.usage, spelled) {
			t.Errorf("make:model accepts -%s and its usage line does not name it as %s: %s", m[1], spelled, c.usage)
		}
	}
}

// flagsWithoutAFlagSet are the commands this binary does not parse with a
// flag.FlagSet it can show: the ones that forward to the project's own binary,
// whose flags belong to the application; serve and dev, which hand everything
// after -- to it; the two that take no flag at all; and the font family, which
// reads its arguments with its own loop because a family name is several words
// on either side of the flags.
//
// It is written out rather than inferred, and the test below holds it to what
// it says: a command named here that starts showing a flag set fails, so this
// list cannot become the place a new flag hides from the check.
var flagsWithoutAFlagSet = map[string]string{
	"key:generate": "takes no argument",
	"lsp":          "takes no argument",
	"serve":        "forwards what follows -- to the application",
	"dev":          "forwards what follows -- to the application",
	"font:add":     "reads its own arguments",
	"font:search":  "reads its own arguments",
	"font:info":    "reads its own arguments",
	"font:list":    "reads its own arguments",
	"font:remove":  "reads its own arguments",

	"migrate": "forwards", "migrate:rollback": "forwards", "migrate:status": "forwards",
	"migrate:fresh": "forwards", "schedule:list": "forwards", "schedule:run": "forwards",
	"queue:work": "forwards", "queue:listen": "forwards", "queue:restart": "forwards",
	"queue:pause": "forwards", "queue:resume": "forwards", "queue:clear": "forwards",
	"queue:monitor": "forwards", "queue:failed": "forwards", "queue:retry": "forwards",
	"queue:forget": "forwards", "queue:flush": "forwards", "queue:prune-failed": "forwards",
	"queue:retry-batch": "forwards", "queue:prune-batches": "forwards", "route:list": "forwards",
	"db:seed": "forwards", "vendor:publish": "forwards",
}

// TestEveryUsageLineNamesEveryFlag reads each command's flag set and requires
// its usage line to name every flag in it.
//
// The flag set is read the way a person meets it: the command is run with a
// flag it does not define, and the flag package answers by listing every flag
// it does. That is the set the command parses, whatever its source spells, so a
// flag added to a FlagSet and forgotten in commands.go fails here -- which is
// how fourteen usage lines came to leave out --dry-run, --force, --tenant,
// --secret and the rest.
func TestEveryUsageLineNamesEveryFlag(t *testing.T) {
	t.Chdir(t.TempDir())
	listed := regexp.MustCompile(`(?m)^\s+-([A-Za-z0-9-]+)`)

	for _, c := range commands {
		_, _, stderr := exercise(t, c.name, "--not-a-flag-of-any-command")
		shown := strings.Contains(stderr, "flag provided but not defined")

		if reason, exempt := flagsWithoutAFlagSet[c.name]; exempt {
			if shown {
				t.Errorf("%s is listed as having no flag set (%s), and it parses one: take it off the list", c.name, reason)
			}
			continue
		}
		if !shown {
			t.Errorf("%s showed no flag set for an undefined flag, and it is not listed as having none:\n%s", c.name, stderr)
			continue
		}
		for _, m := range listed.FindAllStringSubmatch(stderr, -1) {
			name := m[1]
			if name == "not-a-flag-of-any-command" {
				continue
			}
			named := regexp.MustCompile(`(^|[\s\[|(])--?` + regexp.QuoteMeta(name) + `($|[\s\]|=)])`)
			if !named.MatchString(c.usage) {
				t.Errorf("%s accepts -%s and its usage line does not name it:\n  %s", c.name, name, c.usage)
			}
		}
	}
}
