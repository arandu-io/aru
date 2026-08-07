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
	repo   *repositories.InvoiceRepository
	policy policies.InvoicePolicy
}

// NewInvoiceService wires the service. Explicit constructor, no container.
func NewInvoiceService(repo *repositories.InvoiceRepository) *InvoiceService {
	return &InvoiceService{repo: repo}
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
