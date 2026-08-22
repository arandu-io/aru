// Package seeders fills a database for development.
package seeders

import (
	"context"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"

	models "example.test/p/app/Models"
	policies "example.test/p/app/Policies"
	repositories "example.test/p/app/Repositories"
)

// Seed writes the demo rows.
//
// This is one of the few places security.SystemGrant belongs: there is no
// request and no signed-in subject, and the call site is the audit.
func Seed(ctx context.Context, invoices *repositories.InvoiceRepository) error {
	g := security.SystemGrant(policies.ActionCreateInvoice, "demo")

	_, err := invoices.Create(ctx, g, models.Invoice{Reference: "DEMO-1", Total: 1000})
	return err
}

// Reset clears the demo rows of every tenant, so that seeding twice does not
// double them.
//
// It crosses tenants on purpose, and says so on the line where it happens. That
// is the only form of escape that survives review: the reason is in the diff,
// next to the statement it excuses, rather than inferred from the name of the
// function or the directory it sits in.
func Reset(ctx context.Context, db *data.DB) error {
	//arandu:system-grant the development seeder owns the database and reseeds every tenant
	_, err := db.ExecContext(ctx, `DELETE FROM invoices WHERE reference LIKE 'DEMO-%'`)
	return err
}
