// Package policies decides who may do what.
//
// It is here, and app/Repositories is not, and that pairing is what this fixture
// is for: the policy requirement used to be discovered through the repository,
// so a tree shaped like this one had none of it checked.
package policies

import (
	"context"
	"fmt"

	"github.com/arandu-io/framework/security"

	models "example.test/orm/app/Models"
)

// Actions of this entity.
const (
	ActionViewInvoice   security.Action = "invoice.view"
	ActionCreateInvoice security.Action = "invoice.create"
)

// InvoicePolicy is the only authority over who does what with an Invoice.
type InvoicePolicy struct{}

// Can decides whether the subject may perform the action.
func (InvoicePolicy) Can(ctx context.Context, s security.Subject, a security.Action, o models.Invoice) error {
	if o.ID != "" && o.TenantID != s.Tenant {
		return fmt.Errorf("invoice belongs to another tenant")
	}

	// arandu:begin custom
	switch a {
	case ActionViewInvoice, ActionCreateInvoice:
		if s.Tenant != "" {
			return nil
		}
	}
	// arandu:end custom

	return fmt.Errorf("not permitted")
}
