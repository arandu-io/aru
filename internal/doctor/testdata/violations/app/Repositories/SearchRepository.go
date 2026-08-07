package repositories

import (
	"context"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
)

// SearchRepository builds its statement by concatenation, with a value the
// caller supplied.
//
// It is the half sql-built-with-sprintf never read: the rule only ever looked at
// fmt.Sprintf, while ADR 0024 stated as fact that it had been widened -- in the
// paragraph arguing the project does not need sqlc. The one barrier against
// hand-built SQL had a hole exactly where the decision leaned on it.
type SearchRepository struct {
	db *data.DB
}

// Search reaches the table with the term pasted into the statement.
func (s *SearchRepository) Search(ctx context.Context, g security.Grant, term string) error {
	if err := g.Check("invoice.view"); err != nil {
		return err
	}
	_, err := s.db.QueryContext(ctx, "SELECT id FROM invoices WHERE reference LIKE '%"+term+"%' AND tenant_id = ?", data.Tenant(g))
	return err
}
