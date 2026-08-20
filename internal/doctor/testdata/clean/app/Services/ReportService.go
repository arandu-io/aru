package services

import (
	"context"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"

	models "example.test/p/app/Models"
	repositories "example.test/p/app/Repositories"
)

// Reporter builds the read model of the invoices dashboard.
//
// It is correct code, and its name begins with the three letters "Rep": it took
// a Grant from the caller that issued it and hands it down to the repository,
// which is where the check belongs. Classifying it as a repository -- which a
// substring match on "Repo" did, along with ReportPolicy and Reposition --
// reported it for not calling Check, and a rule that fires on correct code is
// how a tool teaches people to ignore it.
type Reporter struct {
	invoices *repositories.InvoiceRepository
}

// NewReporter wires the read model.
func NewReporter(invoices *repositories.InvoiceRepository) *Reporter {
	return &Reporter{invoices: invoices}
}

// Monthly returns the invoices of the current period, under the Grant the
// caller already obtained from security.Authorize.
func (r *Reporter) Monthly(ctx context.Context, g security.Grant) ([]models.Invoice, error) {
	return r.invoices.List(ctx, g, data.Query{Limit: 100})
}

// Overdue is the same work held by a var rather than declared, which is a
// function the rule reading the WHERE clause has to read and has to read
// correctly.
//
// The tenant comes off the Grant and reaches the predicate, so this one is the
// control: it is the fix the rule asks for, written in the shape that used to be
// invisible to it. Reporting it would mean the widening fires on correct code.
var Overdue = func(ctx context.Context, db *data.DB, g security.Grant) error {
	_, err := db.QueryContext(ctx,
		`SELECT id, total FROM invoices WHERE tenant_id = ? AND due_at < ?`,
		data.Tenant(g), "2026-01-01")
	return err
}
