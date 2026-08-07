package reporting

import (
	"context"

	"github.com/arandu-io/framework/data"
)

// InvoiceRepo is a repository in a directory the developer named themselves, with
// the shorter of the two spellings.
//
// It exists because isRepository matched the suffix "Repository" only, and this
// predicate gates all four authorization rules: a type it does not see is a type
// where none of them apply. The method below is exported, takes no Grant, and
// runs a SELECT with no tenant -- and doctor said nothing about it.
type InvoiceRepo struct {
	db *data.DB
}

// All reads every invoice, for everyone, with no policy having answered.
func (r *InvoiceRepo) All(ctx context.Context) error {
	_, err := r.db.QueryContext(ctx, `SELECT reference FROM invoices`)
	return err
}
