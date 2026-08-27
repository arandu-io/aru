package repositories

import (
	"context"
	"fmt"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"

	models "example.test/p/app/Models"
)

// BillingRepository has no policy next to it: nobody decided who may reach the
// charges of a tenant.
type BillingRepository struct{ db *data.DB }

// Find is a violation: it receives a Grant and never checks it.
func (r *BillingRepository) Find(ctx context.Context, g security.Grant, id string) (models.Charge, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id FROM charges WHERE id = ?`, id)
	var c models.Charge
	return c, row.Scan(&c.ID)
}

// Get reads one charge by key, and is written correctly: it checks the grant it
// was given and scopes the statement to the grant's tenant.
//
// It is here so that the caller above it can be a violation. Get is the name a
// listing terminal also answers to, and this one is not a listing -- the id says
// which row, and a service that fetches this row after authorizing the action is
// the hole the re-authorization rule exists for.
func (r *BillingRepository) Get(ctx context.Context, g security.Grant, id string) (models.Charge, error) {
	if err := g.Check("billing.view"); err != nil {
		return models.Charge{}, err
	}
	row := r.db.QueryRowContext(ctx,
		`SELECT id FROM charges WHERE id = ? AND tenant_id = ?`,
		id, data.Tenant(g))
	var c models.Charge
	return c, row.Scan(&c.ID)
}

// Search is a violation: SQL assembled with Sprintf.
func (r *BillingRepository) Search(ctx context.Context, g security.Grant, term string) error {
	if err := g.Check("billing.view"); err != nil {
		return err
	}
	query := fmt.Sprintf("SELECT id FROM charges WHERE name = '%s'", term)
	_, err := r.db.QueryContext(ctx, query)
	return err
}

// Everything is a violation: SystemGrant with an empty tenant, which returns the
// zero Grant and makes this call site dead code.
func (r *BillingRepository) Everything(ctx context.Context) error {
	g := security.SystemGrant("billing.view", "")
	return g.Check("billing.view")
}
