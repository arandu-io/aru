// Package migrations is the fixture for the migration rules.
//
// Nothing in this project imports it, which is the whole of
// migrations-not-linked: the init below never runs, so `aru migrate` reads an
// empty registry and reports that there is nothing to apply.
package migrations

import (
	"context"

	"github.com/arandu-io/hesape/database/migrations"
	"github.com/arandu-io/hesape/database/schema"
)

// CreateChargesTable creates a table and cannot be rolled back: it declares no
// Down, so migrate:rollback deletes the record and leaves the table standing.
type CreateChargesTable struct{ migrations.BaseMigration }

func init() { migrations.Register(CreateChargesTable{}) }

// GetName is the migration's identity, and its order.
func (CreateChargesTable) GetName() string { return "0001_01_01_000000_create_charges_table" }

// Up creates the table.
func (CreateChargesTable) Up(ctx context.Context, conn migrations.Connection) error {
	return conn.Schema().Create(ctx, "charges", func(table *schema.Blueprint) {
		table.String("id").Primary()
		table.String("tenant_id")
		table.String("reference")
		table.Timestamps()
	})
}
