package doctor_test

import (
	"strings"
	"testing"
)

// csrfBootstrap exempts the prefixes given, the way an application writes it
// in bootstrap/app.go.
func csrfBootstrap(prefixes ...string) string {
	quoted := make([]string, len(prefixes))
	for i, p := range prefixes {
		quoted[i] = `"` + p + `"`
	}
	return `package bootstrap

import "github.com/arandu-io/framework/http/middleware"

func Pipeline() middleware.CSRFOption {
	return middleware.CSRFExcept(` + strings.Join(quoted, ", ") + `)
}
`
}

// csrfRoutes registers the lines given on a router the hooks controller is
// reached through.
func csrfRoutes(lines ...string) string {
	return `package routes

import (
	"github.com/arandu-io/framework/http"

	controllers "example.test/shape/app/Http/Controllers"
)

func Webhooks(r *http.Router, hooks *controllers.HookController) {
	` + strings.Join(lines, "\n\t") + `
}
`
}

// hookController declares Verified, which checks the signature through the
// import named alias; Trusting and Legacy, which check nothing; and
// Delegating, which hands the body to a helper that verifies.
func hookController(alias string) string {
	return `package controllers

import (
	"io"
	"net/http"

	hhttp "github.com/arandu-io/hesape/http"
	` + alias + ` "github.com/arandu-io/hesape/webhook"
)

type HookController struct{ secrets ` + alias + `.SecretSet }

func (c *HookController) Verified(ctx *hhttp.Context) error {
	r := ctx.Request()
	body, _ := io.ReadAll(r.Body)
	if !` + alias + `.Verify(c.secrets, r.Header.Get("T"), r.Header.Get("I"), body, r.Header.Get("S")) {
		return ctx.Status(http.StatusUnauthorized)
	}
	return ctx.Status(http.StatusNoContent)
}

func (c *HookController) Trusting(ctx *hhttp.Context) error {
	return ctx.Status(http.StatusNoContent)
}

func (c *HookController) Legacy(ctx *hhttp.Context) error {
	return ctx.Status(http.StatusNoContent)
}

func (c *HookController) Delegating(ctx *hhttp.Context) error {
	if !c.check(ctx) {
		return ctx.Status(http.StatusUnauthorized)
	}
	return ctx.Status(http.StatusNoContent)
}

func (c *HookController) check(ctx *hhttp.Context) bool {
	r := ctx.Request()
	body, _ := io.ReadAll(r.Body)
	return ` + alias + `.Verify(c.secrets, r.Header.Get("T"), r.Header.Get("I"), body, r.Header.Get("S"))
}
`
}

// TestAnExemptPathIsReachedOnlyByAVerifyingAction is the rule's positive and
// negative cases, with the near misses the framework's matching decides.
func TestAnExemptPathIsReachedOnlyByAVerifyingAction(t *testing.T) {
	for _, c := range []struct {
		name     string
		prefixes []string
		routes   []string
		alias    string
		want     []string
	}{
		{"a write under the prefix that verifies nothing", []string{"/webhooks/"},
			[]string{`r.Post("/webhooks/billing", hooks.Trusting)`}, "webhook",
			[]string{"HookController.Trusting answers POST /webhooks/billing"}},
		{"a write that verifies", []string{"/webhooks/"},
			[]string{`r.Post("/webhooks/billing", hooks.Verified)`}, "webhook", nil},
		{"a write that verifies through an aliased import", []string{"/webhooks/"},
			[]string{`r.Post("/webhooks/billing", hooks.Verified)`}, "hook", nil},
		{"a read under the prefix", []string{"/webhooks/"},
			[]string{`r.Get("/webhooks/status", hooks.Trusting)`}, "webhook", nil},
		{"a path the prefix does not cover", []string{"/webhooks/"},
			[]string{`r.Post("/webhooksx", hooks.Trusting)`, `r.Post("/notes", hooks.Legacy)`}, "webhook", nil},
		{"one action under two exempt routes is one finding", []string{"/webhooks/"},
			[]string{`r.Post("/webhooks/a", hooks.Trusting)`, `r.Put("/webhooks/b", hooks.Trusting)`}, "webhook",
			[]string{"HookController.Trusting answers POST /webhooks/a"}},
		{"a prefix with no slash covers itself and nothing below", []string{"/hooks"},
			[]string{`r.Post("/hooks/legacy", hooks.Legacy)`, `r.Post("/hooks", hooks.Trusting)`}, "webhook",
			[]string{"answers POST /hooks,"}},
		{"a route inside a group under the prefix", []string{"/webhooks/"},
			[]string{`hooksGroup := r.Group("/webhooks")`, `hooksGroup.Post("/billing", hooks.Trusting)`}, "webhook",
			[]string{"answers POST /webhooks/billing"}},
		{"a write registered for every method", []string{"/webhooks/"},
			[]string{`r.Action("", "/webhooks/any", hooks.Trusting)`}, "webhook",
			[]string{"answers ANY /webhooks/any"}},
		{"the declared false positive: a helper verifies", []string{"/webhooks/"},
			[]string{`r.Post("/webhooks/billing", hooks.Delegating)`}, "webhook",
			[]string{"HookController.Delegating answers POST /webhooks/billing"}},
		{"no exemption at all", nil,
			[]string{`r.Post("/webhooks/billing", hooks.Trusting)`}, "webhook", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{
				"routes/webhooks.go":                     csrfRoutes(c.routes...),
				"app/Http/Controllers/HookController.go": hookController(c.alias),
			}
			if c.prefixes != nil {
				files["bootstrap/app.go"] = csrfBootstrap(c.prefixes...)
			}
			got := findingsOf(t, structureProject(t, files), "csrf-exempt-without-signature")
			if len(got) != len(c.want) {
				t.Fatalf("got %d finding(s), want %d: %v", len(got), len(c.want), got)
			}
			for i, f := range got {
				if !strings.Contains(f.Message, c.want[i]) {
					t.Errorf("finding says %q, want it to say %q", f.Message, c.want[i])
				}
				if f.Severity.String() != "warning" || f.File != "app/Http/Controllers/HookController.go" {
					t.Errorf("finding is a %s at %s, want a warning at the action", f.Severity, f.File)
				}
			}
		})
	}
}
