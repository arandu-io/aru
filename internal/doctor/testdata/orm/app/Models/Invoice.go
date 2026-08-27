// Package models holds the application's entities.
//
// This tree is the model-first shape: there is no app/Repositories at all, and
// that absence is the point of the fixture. Every authorization rule used to be
// gated on a directory named Repositories or a type named InvoiceRepository, so
// a project shaped like this one reported nothing -- and a rule that finds
// nothing does not fail, it passes.
package models

import (
	"time"

	"github.com/arandu-io/hesape/database/model"
	"github.com/arandu-io/hesape/database/query"
)

// Invoice is an entity with a tenant column, which is what the tenant rules
// read to know the table is partitioned.
type Invoice struct {
	ID        string    `db:"id"`
	TenantID  string    `db:"tenant_id"`
	Number    string    `db:"number"`
	Total     int64     `db:"total"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// Invoices is the façade a query starts from.
func Invoices(conn query.Connection, grammar query.Grammar, processor query.Processor) *model.Model[Invoice] {
	return model.NewModel[Invoice]("invoices", conn, grammar, processor)
}

// Ledger is the entity with no tenant column, and no policy either. It is what
// entity-without-policy has to see in a tree with no repositories in it.
type Ledger struct {
	ID    string `db:"id"`
	Entry string `db:"entry"`
}

// Ledgers is its façade.
func Ledgers(conn query.Connection, grammar query.Grammar, processor query.Processor) *model.Model[Ledger] {
	return model.NewModel[Ledger]("ledgers", conn, grammar, processor)
}
