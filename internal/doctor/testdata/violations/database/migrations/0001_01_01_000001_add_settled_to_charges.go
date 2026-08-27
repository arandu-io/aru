package migrations

import (
	"context"

	"github.com/arandu-io/hesape/database/migrations"
	"github.com/arandu-io/hesape/database/schema"
)

// AddSettledToCharges adds a column to a table that already has rows, and adds
// it NOT NULL. It has a Down, so the only thing wrong with it is the column.
type AddSettledToCharges struct{ migrations.BaseMigration }

func init() { migrations.Register(AddSettledToCharges{}) }

// GetName is the migration's identity, and its order.
func (AddSettledToCharges) GetName() string { return "0001_01_01_000001_add_settled_to_charges" }

// Up adds the columns. settled_at is nullable and correct; settled_by is not,
// and fails on every row already in the table.
func (AddSettledToCharges) Up(ctx context.Context, conn migrations.Connection) error {
	return conn.Schema().Table(ctx, "charges", func(table *schema.Blueprint) {
		table.Timestamp("settled_at").Nullable()
		table.String("settled_by")
	})
}

// Down drops the columns.
func (AddSettledToCharges) Down(ctx context.Context, conn migrations.Connection) error {
	return conn.Schema().Table(ctx, "charges", func(table *schema.Blueprint) {
		table.DropColumn("settled_at")
		table.DropColumn("settled_by")
	})
}
