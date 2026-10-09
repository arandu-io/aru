package controllers

import (
	"io"
	"net/http"

	hhttp "github.com/arandu-io/hesape/http"
	"github.com/arandu-io/hesape/webhook"
)

// WebhookController receives what other systems post.
type WebhookController struct {
	secrets webhook.SecretSet
}

// Stripe checks the signature over the body before it believes a byte of it,
// which is what makes its exempt path safe.
func (c *WebhookController) Stripe(ctx *hhttp.Context) error {
	r := ctx.Request()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	if !webhook.Verify(c.secrets, r.Header.Get("Webhook-Timestamp"), r.Header.Get("Webhook-Id"), body, r.Header.Get("Webhook-Signature")) {
		return ctx.Status(http.StatusUnauthorized)
	}
	return ctx.Status(http.StatusNoContent)
}

// Status answers a read, which the CSRF check never guards.
func (c *WebhookController) Status(ctx *hhttp.Context) error {
	return ctx.Status(http.StatusNoContent)
}

// Legacy verifies nothing, and the CSRF check guards every path it answers.
func (c *WebhookController) Legacy(ctx *hhttp.Context) error {
	return ctx.Status(http.StatusNoContent)
}
