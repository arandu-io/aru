package controllers

import (
	"net/http"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/httpx"
	"github.com/arandu-io/framework/security"

	repositories "example.test/p/app/Repositories"
)

// BillingController skips the service: it holds the repository itself, so the
// Grant would have to be issued here.
type BillingController struct {
	charges *repositories.BillingRepository
}

// Index is a violation twice over: the data of the view is a map, and the
// controller reaches the data package beyond data.Query.
func (c BillingController) Index(ctx *httpx.Context) error {
	db := data.Wrap(nil, data.DialectSQLite)
	_ = db

	return ctx.View("billing.index", map[string]any{"title": "Billing"})
}

// Show is a violation twice over: the view does not exist, and its data is a map
// built on the line above.
func (c BillingController) Show(ctx *httpx.Context) error {
	payload := map[string]string{"id": ctx.Param("id")}
	return ctx.View("billing.missing", payload)
}

// ByTenant is a violation: the tenant comes from the request, so the client
// chooses whose data to read.
func (c BillingController) ByTenant(ctx *httpx.Context) error {
	tenant := ctx.Param("tenant_id")
	_ = tenant
	return nil
}

// ByOrg is the shape the name-based rule never caught: the parameter is called
// org, so nothing in it contains "tenant", and the value goes straight into the
// tenant of a Grant.
func (c BillingController) ByOrg(ctx *httpx.Context) error {
	org := ctx.Query("org")
	g := security.SystemGrant("billing.view", org)
	_ = g
	return nil
}

// ByHeader is the form the header rule was written for.
func ByHeader(w http.ResponseWriter, r *http.Request) {
	_ = r.Header.Get("X-Tenant-Id")
	_ = w
}

// ByPathValue is the same hole one layer down: a plain net/http handler, a path
// segment the client controls, and the value straight into the tenant of a
// Grant.
func ByPathValue(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("account")
	g := security.SystemGrant("billing.view", tenant)
	_ = g
	_ = w
}
