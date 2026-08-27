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

// Navigation is the listing with no second Authorize in it, and it is correct.
//
// Get is the name a single-row read also answers to, and that collision is what
// made this method a finding once: authorize the action, then call something
// ending in Get, and the rule read a listing as an object fetched without an
// object-level decision. There is no object here to decide about. The terminal
// was handed the Grant, the scopes on the Grant are what built the statement,
// and every row that came back is a row this tenant owns.
//
// The loop in List above is a different thing and stays: it filters rows by a
// per-row rule, which is a decision this application makes rather than one the
// tenant scope makes for it. A listing that has no such rule ends here.
func (s *InvoiceService) Navigation(ctx context.Context, subject security.Subject) (int, error) {
	g, err := security.Authorize(ctx, policies.InvoicePolicy{}, subject, policies.ActionViewInvoice, nil)
	if err != nil {
		return 0, err
	}

	found, err := models.Invoices(s.conn, s.grammar, s.processor).
		NewQuery().
		OrderBy("number").
		Get(ctx, g)
	if err != nil {
		return 0, err
	}
	return len(found), nil
}

// Numbers is the same listing with the columns named.
//
// The terminal takes the columns to select after the Grant, so this call has
// three arguments where Navigation has two -- and the third is what a read of
// one row would put there to say which row. Telling the two apart by counting
// arguments alone would report this one. String constants are the difference:
// naming the columns narrows what comes back from each row, never the answer to
// one row.
func (s *InvoiceService) Numbers(ctx context.Context, subject security.Subject) (int, error) {
	g, err := security.Authorize(ctx, policies.InvoicePolicy{}, subject, policies.ActionViewInvoice, nil)
	if err != nil {
		return 0, err
	}

	found, err := models.Invoices(s.conn, s.grammar, s.processor).
		NewQuery().
		Get(ctx, g, "id", "number")
	if err != nil {
		return 0, err
	}
	return len(found), nil
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
