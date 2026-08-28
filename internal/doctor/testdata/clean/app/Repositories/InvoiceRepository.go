// Package repositories is the only door to the database: every method takes a
// Grant, and every method checks it.
package repositories

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"

	models "example.test/p/app/Models"
	policies "example.test/p/app/Policies"
)

// ErrNotFound is returned instead of sql.ErrNoRows, so callers never import
// database/sql.
var ErrNotFound = errors.New("invoice: not found")

// InvoiceRepository reads and writes the invoices table.
//
// Every method starts with g.Check. The Grant is required by the signature, so
// forgetting it is a compile error, and the check proves the grant was issued for
// this exact action -- which catches copy-paste between methods.
type InvoiceRepository struct {
	db *data.DB
}

// NewInvoiceRepository returns a repository over an instrumented handle.
func NewInvoiceRepository(db *data.DB) *InvoiceRepository {
	return &InvoiceRepository{db: db}
}

const columns = `id, tenant_id, reference, total, created_at`

// Find returns one invoice by id, scoped to the grant's tenant.
func (r *InvoiceRepository) Find(ctx context.Context, g security.Grant, id string) (models.Invoice, error) {
	if err := g.Check(policies.ActionViewInvoice); err != nil {
		return models.Invoice{}, err
	}
	// The tenant comes from the Grant, never from the request.
	row := r.db.QueryRowContext(ctx,
		`SELECT `+columns+` FROM invoices WHERE id = ? AND tenant_id = ?`,
		id, data.Tenant(g))

	var i models.Invoice
	err := row.Scan(&i.ID, &i.TenantID, &i.Reference, &i.Total, &i.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Invoice{}, ErrNotFound
	}
	return i, err
}

// List returns a page of invoices in the grant's tenant.
//
// The read path is authorized exactly like the write path: a query with no
// policy is a tenant leak with a technical name.
func (r *InvoiceRepository) List(ctx context.Context, g security.Grant, q data.Query) ([]models.Invoice, error) {
	if err := g.Check(policies.ActionViewInvoice); err != nil {
		return nil, err
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT `+columns+` FROM invoices WHERE tenant_id = ? ORDER BY created_at, id LIMIT ?`,
		data.Tenant(g), q.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Invoice
	for rows.Next() {
		var i models.Invoice
		if err := rows.Scan(&i.ID, &i.TenantID, &i.Reference, &i.Total, &i.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Create inserts the invoice and returns it as stored.
func (r *InvoiceRepository) Create(ctx context.Context, g security.Grant, i models.Invoice) (models.Invoice, error) {
	if err := g.Check(policies.ActionCreateInvoice); err != nil {
		return models.Invoice{}, err
	}

	i.TenantID = data.Tenant(g)
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO invoices (`+columns+`) VALUES (?, ?, ?, ?, ?)`,
		i.ID, i.TenantID, i.Reference, i.Total, i.CreatedAt)
	return i, err
}

// arandu:begin custom
// Queries beyond the ones above go here, and survive regeneration. Keep the
// g.Check as the first line of each one.

// Overdue lists the invoices whose customer is past the grace period.
//
// The join is correct here and is a finding on the performance profile, which is
// what this method is in the fixture for: a wide-column store keeps the two
// entities in different partitions and cannot read them in one statement.
func (r *InvoiceRepository) Overdue(ctx context.Context, g security.Grant) ([]models.Invoice, error) {
	if err := g.Check(policies.ActionViewInvoice); err != nil {
		return nil, err
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT i.id, i.tenant_id, i.reference, i.total, i.created_at
		 FROM invoices i JOIN customers c ON c.id = i.customer_id
		 WHERE i.tenant_id = ? AND c.grace_days < ?`,
		data.Tenant(g), 30)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Invoice
	for rows.Next() {
		var i models.Invoice
		if err := rows.Scan(&i.ID, &i.TenantID, &i.Reference, &i.Total, &i.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Void marks the invoice void and records why, in one transaction.
//
// Also correct here and a finding on the performance profile: the two rows live
// in different partitions there, so nothing can commit them together.
func (r *InvoiceRepository) Void(ctx context.Context, g security.Grant, id, reason string) error {
	if err := g.Check(policies.ActionUpdateInvoice); err != nil {
		return err
	}

	return data.Transaction(ctx, r.db, func(ctx context.Context) error {
		if _, err := r.db.ExecContext(ctx,
			`UPDATE invoices SET voided_at = ? WHERE id = ? AND tenant_id = ?`,
			time.Now().UTC(), id, data.Tenant(g)); err != nil {
			return err
		}
		_, err := r.db.ExecContext(ctx,
			`INSERT INTO invoice_events (invoice_id, tenant_id, reason) VALUES (?, ?, ?)`,
			id, data.Tenant(g), reason)
		return err
	})
}

// Note records a note about one invoice.
//
// The read happens before the transaction opens and the transaction touches one
// table, which is what the performance profile allows. It is in the fixture so
// that a rule counting the whole method rather than the region inside the
// transaction is caught: that reading is off by one table here.
func (r *InvoiceRepository) Note(ctx context.Context, g security.Grant, id, text string) error {
	if err := g.Check(policies.ActionUpdateInvoice); err != nil {
		return err
	}

	var reference string
	row := r.db.QueryRowContext(ctx,
		`SELECT reference FROM invoices WHERE id = ? AND tenant_id = ?`,
		id, data.Tenant(g))
	if err := row.Scan(&reference); err != nil {
		return err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO invoice_notes (invoice_id, tenant_id, note) VALUES (?, ?, ?)`,
		id, data.Tenant(g), reference+": "+text); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// arandu:end custom
