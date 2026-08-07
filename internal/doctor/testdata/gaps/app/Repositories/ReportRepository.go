// Package repositories is the door to the reports table. Every method here is
// one of the ways a repository used to reach the database with nothing standing
// in front of it.
package repositories

import (
	"context"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"

	models "example.test/gaps/app/Models"
	policies "example.test/gaps/app/Policies"
)

// ReportRepository reads and writes the reports table.
type ReportRepository struct {
	db *data.DB
}

// Totals takes no Grant at all, so there is no signature to satisfy and no
// check to forget: the read path simply has no policy. This is the shape RULE
// 17 exists for -- a read model, a report, an export -- and the audit only
// looked at methods that already took a Grant.
func (r *ReportRepository) Totals(ctx context.Context) ([]models.Report, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, total FROM reports WHERE archived = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Report
	for rows.Next() {
		var report models.Report
		if err := rows.Scan(&report.ID, &report.Total); err != nil {
			return nil, err
		}
		out = append(out, report)
	}
	return out, rows.Err()
}

// Purge receives a Grant, calls Check, and throws the answer away. The audit
// asked only whether a call to Check appeared in the body, so this passed --
// with an unscoped DELETE under it.
func (r *ReportRepository) Purge(ctx context.Context, g security.Grant) error {
	_ = g.Check(policies.ActionDeleteReport)

	_, err := r.db.ExecContext(ctx, `DELETE FROM reports WHERE archived = 1`)
	return err
}

// List is the correct shape, and it is what tells the rest of this file apart:
// the Grant is checked, and the tenant of the query comes from it.
func (r *ReportRepository) List(ctx context.Context, g security.Grant) ([]models.Report, error) {
	if err := g.Check(policies.ActionViewReport); err != nil {
		return nil, err
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT id, total FROM reports WHERE tenant_id = ? ORDER BY id`,
		data.Tenant(g))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Report
	for rows.Next() {
		var report models.Report
		if err := rows.Scan(&report.ID, &report.Total); err != nil {
			return nil, err
		}
		out = append(out, report)
	}
	return out, rows.Err()
}

// Archive is the module generated with --tenant whose predicate somebody
// removed: the Grant is checked, the tenant is not in the WHERE, and every
// customer's rows are updated by one call.
func (r *ReportRepository) Archive(ctx context.Context, g security.Grant, id string) error {
	if err := g.Check(policies.ActionDeleteReport); err != nil {
		return err
	}

	_, err := r.db.ExecContext(ctx, `UPDATE reports SET archived = 1 WHERE id = ?`, id)
	return err
}
