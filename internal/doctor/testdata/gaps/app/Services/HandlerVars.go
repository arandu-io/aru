package services

import (
	"context"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"

	policies "example.test/gaps/app/Policies"
)

// This file holds its work in package-level vars instead of declaring methods,
// which is the shape that walked past the rule reading the WHERE clause.
//
// Nothing about it is exotic: a handler assigned to a var is how a table of
// them gets built, and the body runs like any other. Reading only declared
// functions meant the same SELECT was an error in ExportService and silent
// here, four lines apart, over the same table.

// ExportAll is the leak, in a var. Same statement as ExportService.Export, same
// table, same missing predicate -- and it used to produce no finding at all.
var ExportAll = func(ctx context.Context, db *data.DB, g security.Grant) error {
	if err := g.Check(policies.ActionViewReport); err != nil {
		return err
	}

	_, err := db.QueryContext(ctx, `SELECT id, total FROM reports WHERE archived = 0`)
	return err
}

// ExportScoped is the control. The tenant comes off the Grant and reaches the
// WHERE, so a rule that reported this one would fire on its own fix.
var ExportScoped = func(ctx context.Context, db *data.DB, g security.Grant) error {
	if err := g.Check(policies.ActionViewReport); err != nil {
		return err
	}

	_, err := db.QueryContext(ctx,
		`SELECT id, total FROM reports WHERE tenant_id = ? ORDER BY id`,
		data.Tenant(g))
	return err
}

// exportHandlers is the same body one level in, held by a struct field inside a
// slice. A var is not always a bare literal, and the reader follows the value
// rather than matching a shape.
var exportHandlers = []struct {
	Name string
	Run  func(context.Context, *data.DB, security.Grant) error
}{
	{
		Name: "purge",
		Run: func(ctx context.Context, db *data.DB, g security.Grant) error {
			if err := g.Check(policies.ActionViewReport); err != nil {
				return err
			}

			_, err := db.ExecContext(ctx, `DELETE FROM reports WHERE archived = 1`)
			return err
		},
	},
}

// nestedExport nests a literal inside a literal. Both bodies name the table, and
// the report has to carry one finding per statement rather than one per level
// the reader walked through.
var nestedExport = func(ctx context.Context, db *data.DB, g security.Grant) func() error {
	return func() error {
		if err := g.Check(policies.ActionViewReport); err != nil {
			return err
		}

		_, err := db.QueryContext(ctx, `SELECT id FROM reports WHERE archived = 0`)
		return err
	}
}

var _ = exportHandlers
var _ = nestedExport
