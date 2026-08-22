// Package policies decides who may do what, and it is not optional: no
// repository is reachable without one.
package policies

import (
	"context"
	"fmt"

	"github.com/arandu-io/framework/security"

	models "example.test/p/app/Models"
)

// Actions of this entity. Constants rather than strings at the call site: a typo
// in an action name would silently authorize nothing, or worse, everything.
const (
	ActionViewInvoice   security.Action = "invoice.view"
	ActionCreateInvoice security.Action = "invoice.create"
	ActionUpdateInvoice security.Action = "invoice.update"
	ActionDeleteInvoice security.Action = "invoice.delete"
)

// InvoicePolicy is the only authority over who does what with an Invoice.
type InvoicePolicy struct{}

// Can decides whether the subject may perform the action.
func (InvoicePolicy) Can(ctx context.Context, s security.Subject, a security.Action, o models.Invoice) error {
	// Tenant isolation comes first and applies to every action. Without it every
	// check below would be pointless in a multi-tenant system.
	if o.ID != "" && o.TenantID != s.Tenant {
		return fmt.Errorf("invoice belongs to another tenant")
	}

	// arandu:begin custom
	switch a {
	case ActionViewInvoice:
		if s.HasRole("admin") || s.HasRole("staff") {
			return nil
		}
	case ActionCreateInvoice, ActionUpdateInvoice, ActionDeleteInvoice:
		if s.HasRole("admin") {
			return nil
		}
	}
	// arandu:end custom

	return fmt.Errorf("no rule allows %s on invoice", a)
}
