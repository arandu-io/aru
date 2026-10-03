package models

import "github.com/arandu-io/hesape/database/model"

// Ticket is an entity on the model core. Its query file beside it was edited
// by hand after model:build wrote it, so it is not what the entity generates.
type Ticket struct {
	model.Model

	ID       string `db:"id"`
	TenantID string `db:"tenant_id"`
	Subject  string `db:"subject"`
}

var ticketTable = model.NewTable(model.TableSpec{
	Name:      "tickets",
	New:       func() model.Entity { return new(Ticket) },
	UniqueIDs: true,
})
