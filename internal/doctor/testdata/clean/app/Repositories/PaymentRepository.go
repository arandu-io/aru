package repositories

import (
	"context"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"

	models "example.test/p/app/Models"
	policies "example.test/p/app/Policies"
)

// PaymentRepository reads and writes the payments table.
type PaymentRepository struct {
	db *data.DB
}

// NewPaymentRepository returns a repository over an instrumented handle.
func NewPaymentRepository(db *data.DB) *PaymentRepository {
	return &PaymentRepository{db: db}
}

// Find returns one payment by id, scoped to the grant's tenant.
func (r *PaymentRepository) Find(ctx context.Context, g security.Grant, id string) (models.Payment, error) {
	if err := g.Check(policies.ActionViewPayment); err != nil {
		return models.Payment{}, err
	}
	row := r.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, invoice_id, amount, created_at FROM payments WHERE id = ? AND tenant_id = ?`,
		id, data.Tenant(g))

	var p models.Payment
	err := row.Scan(&p.ID, &p.TenantID, &p.InvoiceID, &p.Amount, &p.CreatedAt)
	return p, err
}

// Create inserts the payment and returns it as stored.
func (r *PaymentRepository) Create(ctx context.Context, g security.Grant, p models.Payment) (models.Payment, error) {
	if err := g.Check(policies.ActionCreatePayment); err != nil {
		return models.Payment{}, err
	}

	p.TenantID = data.Tenant(g)
	p.CreatedAt = time.Now().UTC()
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO payments (id, tenant_id, invoice_id, amount, created_at) VALUES (?, ?, ?, ?, ?)`,
		p.ID, p.TenantID, p.InvoiceID, p.Amount, p.CreatedAt)
	return p, err
}
