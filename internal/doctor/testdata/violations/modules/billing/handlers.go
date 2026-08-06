package billing

import (
	"context"
	"net/http"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
)

// Violation: the handler reaches the data package, and reads the tenant from the
// request.
func (m *Module) show(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant_id")
	_ = tenant
	db := data.Wrap(nil, data.DialectSQLite)
	_ = db
}

// Violation: authenticates without rotating the session.
func (m *Module) doLogin(w http.ResponseWriter, r *http.Request) {
	if _, err := m.svc.Authenticate(r.Context(), "t", "e", "p"); err != nil {
		return
	}
	w.WriteHeader(http.StatusOK)
}

// The module declares network = false and calls out anyway. Whoever installed it
// agreed to a module that stays inside the process.
func notifyBillingProvider(ctx context.Context, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://provider.example/charge", nil)
	if err != nil {
		return err
	}
	_, err = http.DefaultClient.Do(req)
	return err
}

// ShowByOrg is the shape the name-based rule never caught: the header is called
// X-Org, so nothing in it contains "tenant", and the value goes straight into
// the tenant of a Grant. Whoever writes the client picks the header name, so the
// name proves nothing -- what makes a value a tenant is that it scopes SQL.
func ShowByOrg(w http.ResponseWriter, r *http.Request) {
	org := r.Header.Get("X-Org")
	g := security.SystemGrant("billing.view", org)
	_ = g
	_ = w
}

// ShowByHeader is the form the rule was written for and never caught.
func ShowByHeader(w http.ResponseWriter, r *http.Request) {
	_ = r.Header.Get("X-Tenant-Id")
	_ = w
}
