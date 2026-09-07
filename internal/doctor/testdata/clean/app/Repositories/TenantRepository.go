package repositories

import (
	"context"
	"database/sql"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"

	models "example.test/p/app/Models"
	policies "example.test/p/app/Policies"
)

// tenantColumns is the column list, held in a constant so the two statements
// below cannot drift.
const tenantColumns = `id, name`

// TenantRepository reads the table the tenant column points at.
//
// It is here because it is the shape a rule about tenant scoping has to get
// right and nearly got wrong. Every method scopes itself -- by comparing the
// row's own id against the acting tenant, because the row IS the tenant -- and
// no statement here can filter by a tenant_id column, since the table does not
// have one and could not.
//
// A rule that read data.Tenant(g) as evidence of the column reported all of
// these, and the only way to silence it was a directive claiming they cross
// tenants, which they do not.
type TenantRepository struct{ db *sql.DB }

// Find reads the acting tenant's own row.
func (r *TenantRepository) Find(ctx context.Context, g security.Grant, id string) (*models.Tenant, error) {
	if err := g.Check(security.Action("tenant.view"), policies.TenantPolicy{}, models.Tenant{ID: id}); err != nil {
		return nil, err
	}
	if id != data.Tenant(g) {
		return nil, ErrNotFound
	}
	row := r.db.QueryRowContext(ctx, `SELECT `+tenantColumns+` FROM tenants WHERE id = ?`, id)
	var out models.Tenant
	if err := row.Scan(&out.ID, &out.Name); err != nil {
		return nil, ErrNotFound
	}
	return &out, nil
}

// Rename writes the acting tenant's own name.
func (r *TenantRepository) Rename(ctx context.Context, g security.Grant, id, name string) error {
	if err := g.Check(security.Action("tenant.update"), policies.TenantPolicy{}, models.Tenant{ID: id}); err != nil {
		return err
	}
	if id != data.Tenant(g) {
		return ErrNotFound
	}
	_, err := r.db.ExecContext(ctx, `UPDATE tenants SET name = ? WHERE id = ?`, name, id)
	return err
}
