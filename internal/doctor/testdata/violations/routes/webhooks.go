// Package routes declares the routes of the application.
package routes

import (
	"github.com/arandu-io/framework/http"

	controllers "example.test/p/app/Http/Controllers"
)

// Webhooks registers the routes another system calls.
func Webhooks(r *http.Router, billing *controllers.BillingWebhookController) {
	r.Post("/webhooks/billing", billing.Receive)
}
