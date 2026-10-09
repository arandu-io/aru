// Package bootstrap wires the application: every constructor the generator
// wrote is called here, by hand, where a reader can follow it.
package bootstrap

import (
	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/http"

	controllers "example.test/p/app/Http/Controllers"
	repositories "example.test/p/app/Repositories"
	services "example.test/p/app/Services"
	"example.test/p/routes"
)

// Routes builds the controllers over one handle, registers their routes, and
// hands back the read model the dashboard uses.
func Routes(r *http.Router, db *data.DB) *services.Reporter {
	invoices := repositories.NewInvoiceRepository(db)
	payments := repositories.NewPaymentRepository(db)
	routes.Web(r, controllers.NewInvoiceController(services.NewInvoiceService(db, invoices, payments)))
	return services.NewReporter(invoices)
}
