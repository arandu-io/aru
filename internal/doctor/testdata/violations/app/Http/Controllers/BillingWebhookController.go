package controllers

import (
	"net/http"

	fhttp "github.com/arandu-io/hesape/http"
)

// BillingWebhookController receives what the billing provider posts.
type BillingWebhookController struct{}

// Receive believes whatever arrives. Its path is exempt from the CSRF check,
// and nothing here asks who sent the request: any page a signed-in person
// opens can post to it, and so can anybody else.
func (c *BillingWebhookController) Receive(ctx *fhttp.Context) error {
	return ctx.Status(http.StatusNoContent)
}
