// Package controllers holds the HTTP entry points, as app/Http/Controllers does
// in Laravel.
package controllers

import (
	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/http"

	services "example.test/p/app/Services"
	views "example.test/p/resources/views"
)

// InvoiceController renders invoices.
//
// It is thin on purpose: read the input, delegate to the service, render. It
// does not reach the repository, so the Grant is issued where the policy is
// consulted and nowhere else.
type InvoiceController struct {
	invoices *services.InvoiceService
}

// NewInvoiceController wires the controller.
func NewInvoiceController(invoices *services.InvoiceService) InvoiceController {
	return InvoiceController{invoices: invoices}
}

// Index lists the invoices of the signed-in tenant.
func (c InvoiceController) Index(ctx *http.Context) error {
	actor, err := http.Actor(ctx)
	if err != nil {
		return err
	}

	found, err := c.invoices.List(ctx.Ctx(), actor, data.Query{
		Limit:  50,
		Cursor: ctx.Query("cursor"),
	})
	if err != nil {
		return err
	}

	return ctx.View("invoices.index", views.InvoicesIndexData{
		Title:    "Invoices",
		Invoices: found,
	})
}

// Show renders one invoice as a fragment, for HTMX.
func (c InvoiceController) Show(ctx *http.Context) error {
	actor, err := http.Actor(ctx)
	if err != nil {
		return err
	}

	found, err := c.invoices.Get(ctx.Ctx(), actor, ctx.Param("id"))
	if err != nil {
		return err
	}
	return ctx.Fragment(200, "invoices.row", views.InvoiceRowData{Invoice: found})
}
