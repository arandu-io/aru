// Package services holds the business rules. It is the layer Laravel leaves to
// an organized team and this framework requires: it is where security.Authorize
// runs, which is the only place a Grant comes from.
package services

import (
	"context"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"

	models "example.test/p/app/Models"
	policies "example.test/p/app/Policies"
	repositories "example.test/p/app/Repositories"
)

// InvoiceService is the only caller of the invoice repository.
type InvoiceService struct {
	db       *data.DB
	repo     *repositories.InvoiceRepository
	payments *repositories.PaymentRepository
	policy   policies.InvoicePolicy
}

// NewInvoiceService wires the service. Explicit constructor, no container.
func NewInvoiceService(db *data.DB, repo *repositories.InvoiceRepository, payments *repositories.PaymentRepository) *InvoiceService {
	return &InvoiceService{db: db, repo: repo, payments: payments}
}

// List returns a page of invoices.
func (s *InvoiceService) List(ctx context.Context, actor security.Subject, q data.Query) ([]models.Invoice, error) {
	g, err := security.Authorize(ctx, s.policy, actor, policies.ActionViewInvoice, models.Invoice{})
	if err != nil {
		return nil, err
	}
	return s.repo.List(ctx, g, q)
}

// Get returns one invoice.
func (s *InvoiceService) Get(ctx context.Context, actor security.Subject, id string) (models.Invoice, error) {
	g, err := security.Authorize(ctx, s.policy, actor, policies.ActionViewInvoice, models.Invoice{})
	if err != nil {
		return models.Invoice{}, err
	}
	return s.repo.Find(ctx, g, id)
}

// Create validates, authorizes and stores.
func (s *InvoiceService) Create(ctx context.Context, actor security.Subject, in models.Invoice) (models.Invoice, error) {
	candidate := models.Invoice{
		TenantID:  actor.Tenant,
		Reference: in.Reference,
		Total:     in.Total,
	}

	g, err := security.Authorize(ctx, s.policy, actor, policies.ActionCreateInvoice, candidate)
	if err != nil {
		return models.Invoice{}, err
	}
	return s.repo.Create(ctx, g, candidate)
}

// Settle records the payment and stores the invoice, in one transaction.
//
// Two aggregates in one commit: correct on a relational database, and a finding
// on the performance profile, where the two live in different partitions and
// nothing commits them together. The SQL is one level down, in the repositories,
// which is the shape a service has -- and the reason the check reads the
// repository fields when the transaction holds no statement of its own.
func (s *InvoiceService) Settle(ctx context.Context, actor security.Subject, invoice models.Invoice, p models.Payment) error {
	g, err := security.Authorize(ctx, s.policy, actor, policies.ActionUpdateInvoice, invoice)
	if err != nil {
		return err
	}

	return data.Transaction(ctx, s.db, func(ctx context.Context) error {
		if _, err := s.repo.Create(ctx, g, invoice); err != nil {
			return err
		}
		_, err := s.payments.Create(ctx, g, p)
		return err
	})
}
