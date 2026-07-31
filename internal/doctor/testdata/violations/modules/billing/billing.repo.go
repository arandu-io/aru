package billing

import (
	"context"
	"fmt"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
)

type Repo struct{ db *data.DB }

type Invoice struct {
	ID       string
	Password string
}

// Violation: receives a Grant and never checks it.
func (r *Repo) Find(ctx context.Context, g security.Grant, id string) (Invoice, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id FROM invoices WHERE id = ?`, id)
	var i Invoice
	return i, row.Scan(&i.ID)
}

// Violation: SQL assembled with Sprintf.
func (r *Repo) Search(ctx context.Context, g security.Grant, term string) error {
	if err := g.Check("billing.view"); err != nil {
		return err
	}
	query := fmt.Sprintf("SELECT id FROM invoices WHERE name = '%s'", term)
	_, err := r.db.QueryContext(ctx, query)
	return err
}

// Violation: SystemGrant with an empty tenant, in a request path.
func (r *Repo) Everything(ctx context.Context) error {
	g := security.SystemGrant("billing.view", "")
	return g.Check("billing.view")
}
