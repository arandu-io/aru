package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/gen"
)

// The wiring a command prints is tested for the same reason `wiring` is: an
// instruction that does not compile is worse than no instruction, because it is
// followed. The check is not that the text reads well -- it is that every
// identifier it names is one the generated file actually declares.

func TestTheControllerWiringNamesWhatTheFileDeclares(t *testing.T) {
	for _, c := range []struct {
		kind  gen.Kind
		route string
	}{
		{gen.KindResource, `r.Resource("invoices", d.Invoice)`},
		{gen.KindInvokable, `r.Action("GET", "/invoices", d.Invoice.Handle).Name("invoices")`},
		{gen.KindPlain, "(no route yet"},
	} {
		t.Run(string(c.kind), func(t *testing.T) {
			stub := gen.Stub{
				Type: "InvoiceController", ModulePath: "example.test/project",
				Resource: "invoices", Entity: "Invoice", Kind: c.kind,
			}
			files, err := gen.GenerateController(stub)
			if err != nil {
				t.Fatalf("GenerateController: %v", err)
			}
			source := string(files[0].Content)
			message := wiringController(stub, gen.Module{Name: "invoice", ModulePath: "example.test/project"})

			if !strings.Contains(message, c.route) {
				t.Errorf("the printed route is not %q:\n%s", c.route, message)
			}
			// The constructor the message tells you to call has to exist, with
			// the arguments the message passes it.
			if !strings.Contains(source, "func NewInvoiceController(sessions *security.SessionStore, csrf *security.CSRF)") {
				t.Error("the generated controller has no constructor of the shape the message calls")
			}
			if !strings.Contains(message, "Invoice: controllers.NewInvoiceController(sessions, csrf),") {
				t.Errorf("the printed bootstrap line does not call the generated constructor:\n%s", message)
			}
			if c.kind == gen.KindInvokable && !strings.Contains(source, "func (c *InvoiceController) Handle(") {
				t.Error("the message registers Handle and the file does not declare it")
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
	for _, want := range []string{"InvoiceSeeder{},", "Invoices *repositories.InvoiceRepository", "aru db:seed InvoiceSeeder"} {
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

// TestGuessTableIsArtisansTableGuesser: the table and whether the migration
// creates it are read out of the migration name exactly as it is typed.
func TestGuessTableIsArtisansTableGuesser(t *testing.T) {
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

// TestNextMigrationSequenceReadsTheDirectory. The order of migrations is the
// order of their ids, so two files written on one day need two numbers -- and
// the number comes from the files rather than the clock, so it is the same
// number on every machine.
func TestNextMigrationSequenceReadsTheDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "database", "migrations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if n, err := nextMigrationSequence(root, "2026_08_07"); err != nil || n != 1 {
		t.Fatalf("empty directory: %d, %v; want 1", n, err)
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
	if n, err := nextMigrationSequence(root, "2026_08_07"); err != nil || n != 5 {
		t.Fatalf("got %d, %v; want 5", n, err)
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
// The snippet the message prints calls services and repositories, and the file
// it says to paste into imports neither. Pasted as printed, the project stops
// compiling with "undefined: services" -- an instruction that does not compile,
// which is what the function's own comment says is worse than no instruction.
func TestTheModuleWiringNamesTheImportsItsSnippetNeeds(t *testing.T) {
	spec := gen.Module{
		Name:       "invoice",
		ModulePath: "example.test/project",
		Fields:     []gen.Field{{Name: "title", Type: gen.TypeString}},
	}
	message := wiring(spec, 12)

	// The snippet is what makes the imports necessary. If it stops calling the
	// two packages, this test is measuring the wrong thing and should be read
	// again rather than deleted.
	for _, call := range []string{"services.New", "repositories.New"} {
		if !strings.Contains(message, call) {
			t.Fatalf("the snippet no longer calls %q, so this test no longer describes it:\n%s", call, message)
		}
	}

	for _, want := range []string{
		`"example.test/project/app/Repositories"`,
		`"example.test/project/app/Services"`,
	} {
		if !strings.Contains(message, want) {
			t.Errorf("the message does not print the import %s, which its own snippet needs:\n%s", want, message)
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
