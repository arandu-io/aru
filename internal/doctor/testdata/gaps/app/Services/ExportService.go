package services

import (
	"context"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"

	policies "example.test/gaps/app/Policies"
)

// ExportService reaches the reports table from a receiver that is not a
// repository, which is where the tenant predicate stopped being read.
//
// The check that reads the WHERE clause began by asking whether the receiver was
// a repository, and a type ending in Service answered no -- so the statement
// below was never looked at by that rule, and by no other: the rules that guard
// the request path stop at controllers, middleware and routes/.
//
// The blind spot sat where the report sends people. The rule that keeps a handler
// away from the database ends its own sentence with "move the call into
// app/Services, where the Grant is issued", and app/Services was the directory
// nothing read.
type ExportService struct {
	db *data.DB
}

// Export is the leak. Everything about it reviews clean -- the Grant is taken,
// the Grant is checked, the statement is parameterized -- and it returns every
// customer's rows, because nothing in the predicate says whose they are.
func (s *ExportService) Export(ctx context.Context, g security.Grant) error {
	if err := g.Check(policies.ActionViewReport); err != nil {
		return err
	}

	_, err := s.db.QueryContext(ctx, `SELECT id, total FROM reports WHERE archived = 0`)
	return err
}

// Monthly is the control, and it is what tells the method above apart: the same
// table, read from the same type, with the tenant taken off the Grant. A rule
// that reported this one would be firing on the fix it asks for.
func (s *ExportService) Monthly(ctx context.Context, g security.Grant) error {
	if err := g.Check(policies.ActionViewReport); err != nil {
		return err
	}

	_, err := s.db.QueryContext(ctx,
		`SELECT id, total FROM reports WHERE tenant_id = ? ORDER BY id`,
		data.Tenant(g))
	return err
}
