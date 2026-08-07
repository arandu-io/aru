// Package seeders fills a database for development, as database/seeders does in
// Laravel.
package seeders

import (
	"context"

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
