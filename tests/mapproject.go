package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arandu-io/aru/internal/gen"
)

// MapProjectModule is the module path of the project MapProject writes.
const MapProjectModule = "example.test/project"

// mapProjectRoutes is the routes file of MapProject, written the way the
// skeleton's routes/web.go is: a Deps struct of controllers, a group with a
// guard held in a variable, and one registration of each shape the router has.
const mapProjectRoutes = `package routes

import (
	"github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/security"

	controllers "example.test/project/app/Http/Controllers"
)

type Deps struct {
	Invoice  *controllers.InvoiceController
	Settings *controllers.SettingsController
	Export   *controllers.ExportController
	Task     *controllers.TaskController
	Stripe   *controllers.StripeWebhookController
	Sessions *security.SessionStore
}

func Web(r *http.Router, d Deps) {
	app := r.Group("/app", middleware.RequireAuth(d.Sessions))
	app.Resource("invoices", d.Invoice)
	r.Group("", middleware.RequireAuth(d.Sessions)).Singleton("settings", d.Settings)
	r.Invokable("POST", "/exports", d.Export).Name("exports.store")
	r.Resource("projects.tasks", d.Task)
	r.ResourceAction("POST", "projects.tasks", "close", d.Task.Close)
	r.Action("POST", "/webhooks/stripe", d.Stripe.Store).Name("webhooks.stripe")
	r.Get("/health", func(w http.ResponseWriter, req *http.Request) {}).Name("health")
}
`

// mapProjectWebhook is a webhook receiver in the shape the implementation
// contract gives one: a controller that verifies the signature with the
// webhook package, records the event and hands the work to a job.
const mapProjectWebhook = `package controllers

import (
	hhttp "github.com/arandu-io/hesape/http"
	"github.com/arandu-io/hesape/webhook"

	events "example.test/project/app/Events"
	jobs "example.test/project/app/Jobs"
)

type StripeWebhookController struct{}

func (c *StripeWebhookController) Store(ctx *hhttp.Context) error {
	if err := webhook.Verify(ctx.Request); err != nil {
		return err
	}
	_ = events.InvoicePaid{}.Event("id")
	return jobs.DispatchSendInvoice(ctx.Ctx(), nil, nil, jobs.SendInvoice{})
}
`

// mapProjectAPI answers the invoices as JSON through the generated resource.
const mapProjectAPI = `package controllers

import (
	hhttp "github.com/arandu-io/hesape/http"

	resources "example.test/project/app/Http/Resources"
)

type InvoiceAPIController struct{}

func (c *InvoiceAPIController) Index(ctx *hhttp.Context) error {
	return ctx.JSON(200, resources.NewInvoiceCollection(nil))
}
`

// mapProjectLayout is the layout every generated screen extends.
const mapProjectLayout = "//go:build kyse\n\npackage layouts\n\n<html><body>@yield('content')</body></html>\n"

// mapProjectGoldens are the generator goldens MapProject copies, by the path
// the generator writes each to.
var mapProjectGoldens = map[string]string{
	"InvoiceResource.go":         "app/Http/Resources/InvoiceResource.go",
	"InvoicePaidNotification.go": "app/Notifications/InvoicePaid.go",
	"StripeClient.go":            "app/Clients/StripeClient.go",
	"StripeFake.go":              "app/Clients/StripeFake.go",
	"StripeClient_test.go":       "tests/Unit/StripeClient_test.go",
	"ShowInvoice.go":             "app/Mcp/ShowInvoice.go",
	"Invoices.go":                "app/Mcp/Invoices.go",
	"ReviewInvoice.go":           "app/Mcp/ReviewInvoice.go",
	"SendInvoice.go":             "app/Jobs/SendInvoice.go",
	"InvoicePaid.go":             "app/Events/InvoicePaid.go",
	"NotifyAccounting.go":        "app/Listeners/NotifyAccounting.go",
	"WelcomeEmail.go":            "app/Mail/WelcomeEmail.go",
	"welcome-email.kyse.go":      "resources/views/mail/welcome-email.kyse.go",
}

// MapProject writes a project the way the generators write one and answers
// its root: a whole module from make:module, a singleton, an invokable and a
// nested controller from make:controller, one artifact of every other kind a
// generator has a command for -- each copied from that generator's golden --
// a webhook receiver, a JSON action, a layout, and a routes file with one
// registration of each shape.
//
// It is what the project map is checked against in two suites: the doctor's,
// which reads the map, and the language server's, which walks it.
func MapProject(tb testing.TB) string {
	tb.Helper()
	root := tb.TempDir()
	write := func(rel, body string) {
		tb.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	write("go.mod", "module "+MapProjectModule+"\n\ngo 1.26\n")
	write("main.go", "package main\n\nfunc main() {}\n")
	write("arandu.toml", "name = \"project\"\n")
	write("arandu.mod.toml", "name = \"example/project\"\nframework = \">= 0.3\"\nprofiles = [\"conventional\", \"performance\"]\n\n"+
		"[permissions]\nnetwork = false\nfilesystem = false\nexec = false\nmigrations = true\n")

	files, err := gen.Generate(gen.Module{
		Name:       "invoice",
		Fields:     []gen.Field{{Name: "reference", Type: gen.TypeString, Required: true}},
		Tenant:     true,
		ModulePath: MapProjectModule,
		Date:       "2026_07_31",
	})
	if err != nil {
		tb.Fatal(err)
	}
	for _, stub := range []gen.Stub{
		{Type: "SettingsController", ModulePath: MapProjectModule, Resource: "settings", Entity: "Settings", Kind: gen.KindSingleton},
		{Type: "ExportController", ModulePath: MapProjectModule, Resource: "exports", Entity: "Export", Kind: gen.KindInvokable},
		{Type: "TaskController", ModulePath: MapProjectModule, Resource: "tasks", Entity: "Task", Kind: gen.KindResource, Parent: "projects", Action: "close"},
	} {
		controller, err := gen.GenerateController(stub)
		if err != nil {
			tb.Fatal(err)
		}
		files = append(files, controller...)
	}
	if _, _, err := gen.Write(root, files, false); err != nil {
		tb.Fatal(err)
	}

	for golden, at := range mapProjectGoldens {
		body, err := os.ReadFile(Fixture(tb, "gen", "stubs", golden+".golden"))
		if err != nil {
			tb.Fatal(err)
		}
		write(at, string(body))
	}
	write("app/Http/Controllers/StripeWebhookController.go", mapProjectWebhook)
	write("app/Http/Controllers/InvoiceAPIController.go", mapProjectAPI)
	write("resources/views/layouts/app.kyse.go", mapProjectLayout)
	write("routes/web.go", mapProjectRoutes)
	return root
}
