package controllers

import (
	"context"
	"encoding/json"
	"net/http"

	fhttp "github.com/arandu-io/hesape/http"

	services "example.test/p/app/Services"
)

// sessionStore is the store the route guard already loaded the session from.
type sessionStore interface {
	Load(ctx context.Context, r *http.Request) (string, error)
}

// ChargeController compiles, answers every route, and puts each piece of work
// somewhere that already has an owner. Each action below is one of those, and
// there are thirteen of them: more than one resource in one type.
type ChargeController struct {
	svc      *services.ChargeService
	sessions sessionStore
}

// NewChargeController is what make:module wrote, and the wiring it printed was
// never pasted: nothing constructs the controller, so no route reaches it.
func NewChargeController(svc *services.ChargeService) *ChargeController {
	return &ChargeController{svc: svc}
}

// Store reads the form field by field and validates it itself, so a job that
// creates a charge through the same service skips the validation.
func (c *ChargeController) Store(ctx *fhttp.Context) error {
	in := services.ChargeInput{Amount: ctx.Input("amount"), Reference: ctx.Request.FormValue("reference")}
	if errs := in.Validate(); len(errs) > 0 {
		return errs
	}
	return c.svc.Create(ctx.Ctx(), in)
}

// Reject answers the rejected form with 422 itself, which htmx discards.
func (c *ChargeController) Reject(ctx *fhttp.Context) error {
	return ctx.Status(http.StatusUnprocessableEntity)
}

// Export encodes its own JSON onto the response.
func (c *ChargeController) Export(ctx *fhttp.Context) error {
	ctx.Response.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(ctx.Response).Encode(map[string]string{"status": "ok"})
}

// Show loads the session the guard already loaded.
func (c *ChargeController) Show(ctx *fhttp.Context) error {
	if _, err := c.sessions.Load(ctx.Ctx(), ctx.Request); err != nil {
		return err
	}
	return nil
}

// Row answers a page view as a fragment.
func (c *ChargeController) Row(ctx *fhttp.Context) error {
	return ctx.Fragment(http.StatusOK, "billing.index", services.ChargeInput{})
}

// Done redirects to a path written by hand.
func (c *ChargeController) Done(ctx *fhttp.Context) error {
	return ctx.Redirect("/charges/" + ctx.Param("id"))
}

// Act lets a hidden input choose which operation runs.
func (c *ChargeController) Act(ctx *fhttp.Context) error {
	switch ctx.Input("op") {
	case "refund":
		return c.svc.Refund(ctx.Ctx(), ctx.Param("id"))
	case "void":
		return c.svc.Void(ctx.Ctx(), ctx.Param("id"))
	}
	return nil
}

// Index, Create, Edit, Update, Destroy and Cancel are the rest of the thirteen.
func (c *ChargeController) Index(ctx *fhttp.Context) error { return nil }

// Create renders the form.
func (c *ChargeController) Create(ctx *fhttp.Context) error { return nil }

// Edit renders the stored record.
func (c *ChargeController) Edit(ctx *fhttp.Context) error { return nil }

// Update writes the stored record.
func (c *ChargeController) Update(ctx *fhttp.Context) error { return nil }

// Destroy removes the record.
func (c *ChargeController) Destroy(ctx *fhttp.Context) error { return nil }

// Cancel stops a pending charge.
func (c *ChargeController) Cancel(ctx *fhttp.Context) error { return nil }
