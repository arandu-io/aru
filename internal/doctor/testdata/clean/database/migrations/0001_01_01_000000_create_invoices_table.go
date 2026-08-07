// Package migrations owns the schema, as database/migrations does in Laravel.
//
// Its existence is what makes `migrations = true` in arandu.mod.toml true.
package migrations

import "github.com/arandu-io/framework/kernel"

// CreateInvoicesTable is the first migration.
//
// Every type here spells the same in SQLite, PostgreSQL and MySQL, so one schema
// serves all three.
var CreateInvoicesTable = kernel.Migration{
	ID: "0001_01_01_000000_create_invoices_table",
	Up: `CREATE TABLE invoices (
		id         TEXT PRIMARY KEY,
		tenant_id  TEXT NOT NULL,
		reference  TEXT NOT NULL,
		total      INTEGER,
		created_at TIMESTAMP NOT NULL,
		UNIQUE (tenant_id, reference)
	);
	CREATE INDEX invoices_tenant_created_idx ON invoices (tenant_id, created_at, id);`,
	Down: `DROP TABLE invoices;`,
}
