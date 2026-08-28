package gen_test

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/gen"
)

// stubFields is the fixture the granular commands generate from, and it is the
// same one `spec` uses: a request written by make:request and the Store half of
// a request written by make:module have to be recognizably the same file.
func stubFields() []gen.Field {
	return []gen.Field{
		{Name: "reference", Type: gen.TypeString, Required: true},
		{Name: "supplier_email", Type: gen.TypeEmail, Required: true},
		{Name: "total", Type: gen.TypeMoney, Required: true},
		{Name: "delivery_date", Type: gen.TypeDate},
	}
}

// TestGoldenStubs is what makes the granular commands trustworthy, for the same
// reason TestGolden makes make:module trustworthy: the same specification has to
// produce the same bytes, forever, and a template change shows up as a diff in
// review instead of surprising somebody whose project regenerated differently.
func TestGoldenStubs(t *testing.T) {
	for _, c := range []struct {
		golden string
		build  func() (gen.File, error)
	}{
		{"InvoiceController.plain.go", fileAt(0, func() ([]gen.File, error) {
			return gen.GenerateController(controllerStub(gen.KindPlain))
		})},
		{"InvoiceController.resource.go", fileAt(0, func() ([]gen.File, error) {
			return gen.GenerateController(controllerStub(gen.KindResource))
		})},
		{"InvoiceController.invokable.go", fileAt(0, func() ([]gen.File, error) {
			return gen.GenerateController(controllerStub(gen.KindInvokable))
		})},
		{"EnsureAccountIsActive.go", fileAt(0, func() ([]gen.File, error) {
			return gen.GenerateMiddleware(gen.Stub{Type: "EnsureAccountIsActive", ModulePath: "example.test/project"})
		})},
		{"StoreInvoice.go", fileAt(0, func() ([]gen.File, error) {
			return gen.GenerateRequest(gen.Stub{Type: "StoreInvoice", ModulePath: "example.test/project", Fields: stubFields()})
		})},
		{"StoreReport.go", fileAt(0, func() ([]gen.File, error) {
			return gen.GenerateRequest(gen.Stub{Type: "StoreReport", ModulePath: "example.test/project"})
		})},
		{"add_status_to_invoices.go", func() (gen.File, error) {
			return gen.RenderMigration(gen.MigrationSpec{
				ID:    "2026_08_07_000002_add_status_to_invoices",
				Type:  "AddStatusToInvoices",
				Table: "invoices",
				Fields: []gen.Field{
					{Name: "status", Type: gen.TypeString},
					{Name: "paid_at", Type: gen.TypeTimestamp},
				},
			})
		}},
		{"InvoiceFactory.tenant.go", func() (gen.File, error) { return gen.RenderFactory(factorySpec(true)) }},
		{"InvoiceFactory.global.go", func() (gen.File, error) { return gen.RenderFactory(factorySpec(false)) }},
		{"InvoiceSeeder.go", func() (gen.File, error) {
			return gen.RenderSeeder(gen.SeederSpec{Entity: "Invoice"})
		}},
		{"SendInvoice.go", func() (gen.File, error) {
			return gen.RenderJob(gen.JobSpec{
				Type: "SendInvoice", EventName: "invoice.send", ModulePath: "example.test/project",
				Fields: []gen.Field{{Name: "invoice_id", Type: gen.TypeUUID}, {Name: "amount", Type: gen.TypeMoney}},
			})
		}},
		{"InvoicePaid.go", func() (gen.File, error) {
			return gen.RenderEvent(gen.EventSpec{
				Type: "InvoicePaid", Aggregate: "invoice", EventName: "invoice.paid", ModulePath: "example.test/project",
				Fields: []gen.Field{{Name: "invoice_id", Type: gen.TypeUUID}, {Name: "amount", Type: gen.TypeMoney}},
			})
		}},
		{"InvoiceStatus.go", func() (gen.File, error) { return gen.RenderEnum(enumSpec(false)) }},
		{"PaymentKind.go", func() (gen.File, error) { return gen.RenderEnum(enumSpec(true)) }},
		// Both branches of the listener, because the difference between them is
		// a conditional inside a doc comment: the one place a template can be
		// broken by a reflow that touches nothing else.
		{"NotifyAccounting.go", fileAt(0, func() ([]gen.File, error) {
			return gen.GenerateListener(gen.Listener{
				Name: "NotifyAccounting", Event: "invoice.paid", ModulePath: "example.test/project",
			})
		})},
		{"AuditTrail.go", fileAt(0, func() ([]gen.File, error) {
			return gen.GenerateListener(gen.Listener{Name: "AuditTrail", ModulePath: "example.test/project"})
		})},
		{"CloseInvoices.go", fileAt(0, func() ([]gen.File, error) {
			return gen.GenerateCommand(gen.Command{
				Name: "CloseInvoices", Signature: "invoice:close",
				Description: "Close the invoices past their due date", ModulePath: "example.test/project",
			})
		})},
		{"WelcomeEmail.go", fileAt(0, func() ([]gen.File, error) { return gen.RenderMail(mailSpec()) })},
		{"welcome-email.kyse.go", fileAt(1, func() ([]gen.File, error) { return gen.RenderMail(mailSpec()) })},
		{"welcome-email-text.kyse.go", fileAt(2, func() ([]gen.File, error) { return gen.RenderMail(mailSpec()) })},
	} {
		t.Run(c.golden, func(t *testing.T) {
			file, err := c.build()
			if err != nil {
				t.Fatalf("generate: %v", err)
			}

			// Every generated Go file has to parse. A generator that emits Go
			// that does not compile is worse than no generator at all, and this
			// is the cheapest half of that guarantee. A .kyse.go is a template
			// rather than Go, and the parser that reads it is aru view:build.
			if !strings.HasSuffix(file.Path, ".kyse.go") {
				if _, err := parser.ParseFile(token.NewFileSet(), file.Path, file.Content, parser.AllErrors); err != nil {
					t.Fatalf("%s does not parse: %v", file.Path, err)
				}
			}

			path := filepath.Join(goldens(t, "stubs"), c.golden+".golden")
			if *update {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, file.Content, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%s: %v -- run: go test ./tests/Unit/gen -update", path, err)
			}
			if !bytes.Equal(want, file.Content) {
				t.Errorf("%s differs from the golden file.\nRun `go test ./tests/Unit/gen -update` and review the diff.", file.Path)
			}
		})
	}
}

func controllerStub(k gen.Kind) gen.Stub {
	return gen.Stub{
		Type:       "InvoiceController",
		ModulePath: "example.test/project",
		Resource:   "invoices",
		Entity:     "Invoice",
		Kind:       k,
	}
}

func factorySpec(tenant bool) gen.FactorySpec {
	fields := make([]gen.FactoryField, 0, len(stubFields()))
	for _, f := range stubFields() {
		fields = append(fields, f.Factory())
	}
	return gen.FactorySpec{
		Entity:       "Invoice",
		Tenant:       tenant,
		Fields:       fields,
		ModelsImport: "example.test/project/app/Models",
	}
}

func enumSpec(asInt bool) gen.EnumSpec {
	name := "InvoiceStatus"
	if asInt {
		name = "PaymentKind"
	}
	values, err := gen.ParseEnumValues("draft,sent,paid,void", name, asInt)
	if err != nil {
		panic(err)
	}
	return gen.EnumSpec{Type: name, Values: values, Int: asInt}
}

func mailSpec() gen.MailSpec {
	return gen.MailSpec{
		Type:       "WelcomeEmail",
		ModulePath: "example.test/project",
		Subject:    "Welcome aboard",
		Fields:     []gen.Field{{Name: "name", Type: gen.TypeString}, {Name: "link", Type: gen.TypeString}},
	}
}

// fileAt adapts the generators that return a slice, which they do because a
// command may write more than one file and the caller should not have to change
// shape when it does, and picks the one the case is about.
func fileAt(n int, f func() ([]gen.File, error)) func() (gen.File, error) {
	return func() (gen.File, error) {
		files, err := f()
		if err != nil {
			return gen.File{}, err
		}
		return files[n], nil
	}
}

// TestTheGranularCommandsAndMakeModuleAgree is the rule this generator is not
// allowed to break inside itself: two ways to write one thing do not exist, so
// the pieces that both paths emit are the same bytes rather than two copies that
// happen to match today.
func TestTheGranularCommandsAndMakeModuleAgree(t *testing.T) {
	module, err := gen.Generate(spec(true))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	find := func(suffix string) string {
		for _, f := range module {
			if strings.HasSuffix(f.Path, suffix) {
				return string(f.Content)
			}
		}
		t.Fatalf("make:module generated no %s", suffix)
		return ""
	}

	t.Run("the session helpers", func(t *testing.T) {
		files, err := gen.GenerateController(controllerStub(gen.KindResource))
		if err != nil {
			t.Fatalf("GenerateController: %v", err)
		}
		// The three methods come from one template, so the only difference
		// between the two files is the receiver type.
		stub := strings.ReplaceAll(string(files[0].Content), "InvoiceController", "PurchaseOrderController")
		for _, method := range []string{"actor(ctx *fhttp.Context)", "signIn(ctx *fhttp.Context)", "token(ctx *fhttp.Context)"} {
			if !strings.Contains(stub, method) || !strings.Contains(find("Controller.go"), method) {
				t.Errorf("%s is not emitted by both make:controller and make:module", method)
			}
		}
	})

	t.Run("the validation rules", func(t *testing.T) {
		files, err := gen.GenerateRequest(gen.Stub{
			Type: "StorePurchaseOrder", ModulePath: "example.test/project", Fields: spec(true).Fields,
		})
		if err != nil {
			t.Fatalf("GenerateRequest: %v", err)
		}
		// Every rule make:module writes for the same fields has to appear,
		// spelled identically, in what make:request writes.
		//
		// Only the Store half is compared: make:module also emits an Update
		// request, which carries the id and its own rule for it, and that is the
		// pair being a pair rather than a second shape of the rules.
		granular := string(files[0].Content)
		store := find("PurchaseOrderRequest.go")
		if end := strings.Index(store, "type UpdatePurchaseOrder"); end > 0 {
			store = store[:end]
		}
		for _, line := range strings.Split(store, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "validation.") {
				continue
			}
			if !strings.Contains(granular, line) {
				t.Errorf("make:module writes %q and make:request does not", line)
			}
		}
	})

	t.Run("the migration", func(t *testing.T) {
		// make:module's migration is rendered through MigrationSpec, which is
		// what make:migration --create renders through too.
		file, err := gen.RenderMigration(spec(true).MigrationSpec())
		if err != nil {
			t.Fatalf("RenderMigration: %v", err)
		}
		if string(file.Content) != find("_create_purchase_orders_table.go") {
			t.Error("make:module and make:migration emit different migrations for one specification")
		}
	})

	t.Run("the model", func(t *testing.T) {
		files, err := gen.GenerateModel(spec(true), gen.ModelParts{})
		if err != nil {
			t.Fatalf("GenerateModel: %v", err)
		}
		if string(files[0].Content) != find("app/Models/PurchaseOrder.go") {
			t.Error("make:model and make:module emit different models for one specification")
		}
	})

	t.Run("the unit test", func(t *testing.T) {
		// make:module's test is rendered through RenderTest, which is what
		// make:test renders through: a project that lost the file gets the same
		// bytes back rather than a second opinion about what it asserts.
		file, err := gen.RenderTest(spec(true))
		if err != nil {
			t.Fatalf("RenderTest: %v", err)
		}
		if string(file.Content) != find("PurchaseOrder_test.go") {
			t.Error("make:module and make:test emit different tests for one specification")
		}
	})
}

// TestTheGeneratedActionsDoNotAnswerSuccess: an action generated with an empty
// body that answered 200 would look like it worked in the browser, in the logs
// and on every dashboard, which is the failure nobody debugs.
func TestTheGeneratedActionsDoNotAnswerSuccess(t *testing.T) {
	for _, kind := range []gen.Kind{gen.KindResource, gen.KindInvokable} {
		files, err := gen.GenerateController(controllerStub(kind))
		if err != nil {
			t.Fatalf("GenerateController: %v", err)
		}
		body := string(files[0].Content)
		if !strings.Contains(body, "http.StatusNotImplemented") {
			t.Errorf("%s: a generated action does not answer 501", kind)
		}
		if strings.Contains(body, "http.StatusOK") {
			t.Errorf("%s: a generated action answers success with no body", kind)
		}
	}
}

// TestTheGeneratedRequestHasNoAuthorize is the thesis of the product in one
// assertion: there is one path to a yes, and a FormRequest is not on it.
func TestTheGeneratedRequestHasNoAuthorize(t *testing.T) {
	files, err := gen.GenerateRequest(gen.Stub{Type: "StoreInvoice", ModulePath: "example.test/project", Fields: stubFields()})
	if err != nil {
		t.Fatalf("GenerateRequest: %v", err)
	}
	for _, line := range strings.Split(string(files[0].Content), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		if strings.Contains(line, "Authorize") || strings.Contains(line, "func (r StoreInvoice) authorize") {
			t.Errorf("the generated request decides authorization: %q", line)
		}
	}
}

// TestAnAlteringMigrationAddsNothingNotNull: a NOT NULL column added to a table
// that has rows fails on every row already there, and during a rollout the
// previous binary does not fill it in.
func TestAnAlteringMigrationAddsNothingNotNull(t *testing.T) {
	file, err := gen.RenderMigration(gen.MigrationSpec{
		ID: "2026_08_07_000002_add_status_to_invoices", Type: "AddStatusToInvoices", Table: "invoices",
		Fields: []gen.Field{{Name: "status", Type: gen.TypeString, Required: true}},
	})
	if err != nil {
		t.Fatalf("RenderMigration: %v", err)
	}
	// The comments are stripped first: the file explains at length why it never
	// writes NOT NULL, and the explanation contains the words.
	if strings.Contains(withoutComments(string(file.Content)), "NOT NULL") {
		t.Error("an altering migration adds a NOT NULL column")
	}
}

// TestAnAlteringMigrationDropsTheIndexBeforeTheColumn: SQLite refuses to drop a
// column an index still names, so a Down that dropped the column alone fails on
// the engine a project runs by default.
//
// The template used to say the opposite in a comment -- that dropping the column
// drops the index on all three engines -- and emitted no drop at all, which made
// every unique field in an altering migration a rollback that could not run.
func TestAnAlteringMigrationDropsTheIndexBeforeTheColumn(t *testing.T) {
	file, err := gen.RenderMigration(gen.MigrationSpec{
		ID: "2026_08_07_000003_add_reference_to_invoices", Type: "AddReferenceToInvoices",
		Table: "invoices", Tenant: true,
		Fields: []gen.Field{{Name: "reference", Type: gen.TypeString, Unique: true}},
	})
	if err != nil {
		t.Fatalf("RenderMigration: %v", err)
	}
	source := string(file.Content)

	_, down, ok := strings.Cut(source, ") Down(")
	if !ok {
		t.Fatalf("the migration has no Down:\n%s", source)
	}
	dropIndex := strings.Index(down, "DropUnique(")
	dropColumn := strings.Index(down, "DropColumn(")
	if dropIndex < 0 {
		t.Fatalf("Down does not drop the unique index, so SQLite refuses the column drop:\n%s", down)
	}
	if dropColumn < 0 {
		t.Fatalf("Down does not drop the column:\n%s", down)
	}
	if dropIndex > dropColumn {
		t.Fatalf("Down drops the column before the index, which SQLite refuses:\n%s", down)
	}

	// The index Up creates and the one Down drops have to be the same name, or
	// the rollback drops nothing and reports success.
	if !strings.Contains(source, `"invoices_reference_uidx"`) {
		t.Fatalf("the index is not named the same on both sides:\n%s", source)
	}
	if strings.Count(source, `"invoices_reference_uidx"`) != 2 {
		t.Fatalf("the index name appears %d times, want one in Up and one in Down:\n%s",
			strings.Count(source, `"invoices_reference_uidx"`), source)
	}
}

func withoutComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "//") {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

// TestAMigrationWithNoColumnsIsRefused. data.Migrate records a migration whose
// Up applied zero statements, and the id is immutable -- so an empty skeleton is
// a migration that is marked as applied and can never run.
func TestAMigrationWithNoColumnsIsRefused(t *testing.T) {
	_, err := gen.RenderMigration(gen.MigrationSpec{ID: "x", Type: "X", Table: "invoices", Create: true})
	if err == nil {
		t.Fatal("a migration with no columns was generated")
	}
}

// TestAnEventWithoutAnAggregateIsRefused. Aggregate and AggregateID are what
// say what the event happened to; empty, they produce an event no consumer can
// correlate, and nothing anywhere reports it.
func TestAnEventWithoutAnAggregateIsRefused(t *testing.T) {
	_, err := gen.RenderEvent(gen.EventSpec{Type: "InvoicePaid", EventName: "invoice.paid"})
	if err == nil {
		t.Fatal("an event with no aggregate was generated")
	}
}

// TestTheDefaultKeysReadLikeSomebodyWroteThem. The derived name is what most
// people keep, so it has to be the name they would have typed.
func TestTheDefaultKeysReadLikeSomebodyWroteThem(t *testing.T) {
	if got := gen.DefaultEventName("SendInvoice"); got != "send-invoice" {
		t.Errorf("DefaultEventName = %q, want send-invoice", got)
	}
	for _, c := range []struct{ typeName, aggregate, want string }{
		{"InvoicePaid", "invoice", "invoice.paid"},
		{"PurchaseOrderApproved", "purchase_order", "purchase_order.approved"},
		{"PaymentSettled", "invoice", "invoice.payment-settled"},
	} {
		if got := gen.DefaultEventKey(c.typeName, c.aggregate); got != c.want {
			t.Errorf("DefaultEventKey(%q, %q) = %q, want %q", c.typeName, c.aggregate, got, c.want)
		}
	}
}

// TestNormalizeAcceptsBothSpellings: the developer this is for types the class
// name, and `aru make:module` takes the module name.
func TestNormalizeAcceptsBothSpellings(t *testing.T) {
	for in, want := range map[string]string{
		"PurchaseOrder":  "purchase_order",
		"purchase_order": "purchase_order",
		"purchase-order": "purchase_order",
		"Invoice":        "invoice",
		"HTTPServer":     "http_server",
		"invoiceLine":    "invoice_line",
	} {
		if got := gen.Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}
