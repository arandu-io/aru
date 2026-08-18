// Package migrations owns the schema, as database/migrations does in Laravel.
//
// Its existence is what makes `migrations = true` in arandu.mod.toml true.
package migrations

import (
	"context"

	"github.com/arandu-io/hesape/database/migrations"
)

// CreateInvoicesTable is the first migration.
//
// Every type here spells the same in SQLite, PostgreSQL and MySQL, so one schema
// serves all three.
type CreateInvoicesTable struct{ migrations.BaseMigration }

func init() { migrations.Register(CreateInvoicesTable{}) }

// GetName is the migration's identity, and its order.
func (CreateInvoicesTable) GetName() string { return "0001_01_01_000000_create_invoices_table" }

// Up creates the table and the index every listing reads through.
func (CreateInvoicesTable) Up(ctx context.Context, conn migrations.Connection) error {
	statements := []string{
		`CREATE TABLE invoices (
			id         TEXT PRIMARY KEY,
			tenant_id  TEXT NOT NULL,
			reference  TEXT NOT NULL,
			total      INTEGER,
			created_at TIMESTAMP NOT NULL,
			UNIQUE (tenant_id, reference)
		)`,
		`CREATE INDEX invoices_tenant_created_idx ON invoices (tenant_id, created_at, id)`,
	}
	for _, statement := range statements {
		if _, err := conn.Statement(ctx, statement, nil); err != nil {
			return err
		}
	}
	return nil
}

// Down drops the table, and the index with it.
func (CreateInvoicesTable) Down(ctx context.Context, conn migrations.Connection) error {
	_, err := conn.Statement(ctx, `DROP TABLE invoices`, nil)
	return err
}
