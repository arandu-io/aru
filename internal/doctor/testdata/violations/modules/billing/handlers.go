package billing

import (
	"net/http"

	"github.com/arandu-io/framework/data"
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
