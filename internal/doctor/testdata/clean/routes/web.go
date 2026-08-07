// Package routes declares the routes of the application, as routes/web.php does.
package routes

import (
	"github.com/arandu-io/framework/httpx"

	controllers "example.test/p/app/Http/Controllers"
)

// Web registers the web routes.
func Web(r *httpx.Router, invoices controllers.InvoiceController) {
	r.Action("GET", "/", invoices.Index).Name("home")
	r.Resource("invoices", invoices)
}
