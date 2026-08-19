package migrations

import (
	"context"

	"github.com/arandu-io/hesape/database/migrations"
)

// BackfillInvoiceTotals gives the rows that predate the column its default.
//
// It writes every tenant's rows and is correct: a migration runs once per
// database, from the pipeline, with no request and no signed-in subject behind
// it. There is no Grant here to take a tenant off -- data.Tenant has nothing to
// read -- so a rule asking this statement to name one would be asking for
// something that cannot be written.
type BackfillInvoiceTotals struct{ migrations.BaseMigration }

func init() { migrations.Register(BackfillInvoiceTotals{}) }

// GetName is the migration's identity, and its order.
func (BackfillInvoiceTotals) GetName() string { return "0001_01_01_000001_backfill_invoice_totals" }

// Up sets the default on the rows written before the column existed.
func (BackfillInvoiceTotals) Up(ctx context.Context, conn migrations.Connection) error {
	_, err := conn.Statement(ctx, `UPDATE invoices SET total = 0 WHERE total IS NULL`, nil)
	return err
}
