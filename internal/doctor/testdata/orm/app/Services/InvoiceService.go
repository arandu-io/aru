// Package services is where the work happens, and where the Grant is obtained.
package services

import (
	"context"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/database/query"

	models "example.test/orm/app/Models"
	policies "example.test/orm/app/Policies"
)

// InvoiceService reads and writes invoices through the model.
type InvoiceService struct {
	conn      query.Connection
	grammar   query.Grammar
	processor query.Processor
}

// List is the clean shape: authorize, then query under the Grant that came
// back. It has to be reported by nothing.
func (s *InvoiceService) List(ctx context.Context, subject security.Subject) ([]string, error) {
	g, err := security.Authorize(ctx, policies.InvoicePolicy{}, subject, policies.ActionViewInvoice, nil)
	if err != nil {
		return nil, err
	}

	found, err := models.Invoices(s.conn, s.grammar, s.processor).
		NewQuery().
		OrderBy("created_at").
		Get(ctx, g)
	if err != nil {
		return nil, err
	}

	// The second decision, with the row in hand: the first Authorize said the
	// caller may look at invoices, this one says which invoices.
	out := make([]string, 0, len(found))
	for _, invoice := range found {
		if _, err := security.Authorize(ctx, policies.InvoicePolicy{}, subject, policies.ActionViewInvoice, *invoice.Entity); err != nil {
			continue
		}
		out = append(out, invoice.Entity.Number)
	}
	return out, nil
}

// Unauthorized receives a Grant and queries under it without ever asking a
// policy or checking the action. It is the planted grant-not-checked, and it is
// the finding that matters most in a model-first project: the model reads the
// tenant off the Grant and checks nothing else, so this rule is all that is
// left holding the read path to a policy.
func (s *InvoiceService) Unauthorized(ctx context.Context, g security.Grant) (int64, error) {
	return models.Invoices(s.conn, s.grammar, s.processor).
		NewQuery().
		Count(ctx, g)
}
