package services

import (
	"github.com/arandu-io/hesape/database/model"

	models "example.test/p/app/Models"
)

// archiveTable declares a table outside the package that declares entities,
// where nothing generates a typed query around it.
var archiveTable = model.NewTable(model.TableSpec{Name: "archive"})

// openTickets reaches past the typed query to the core builder.
func openTickets(db model.DB) {
	models.Tickets(db).Base().Where("status", "open")
}

// closedTickets holds the core builder and calls it.
func closedTickets(b *model.Builder) {
	b.Where("status", "closed")
}
