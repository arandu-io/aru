// Package routes declares the routes of the application.
package routes

import (
	"github.com/arandu-io/framework/http"

	controllers "example.test/gaps/app/Http/Controllers"
)

// Webhooks registers the routes another system calls. The one write under an
// exempt path verifies its signature; the rest are reads, or are not exempt.
func Webhooks(r *http.Router, hooks *controllers.WebhookController) {
	r.Post("/webhooks/stripe", hooks.Stripe)
	r.Get("/webhooks/status", hooks.Status)
	r.Post("/webhooksx", hooks.Legacy)
	r.Post("/hooks/legacy", hooks.Legacy)
}
