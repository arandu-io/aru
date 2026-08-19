package policies

import (
	"context"
	"fmt"

	"github.com/arandu-io/framework/security"

	models "example.test/p/app/Models"
)

// Actions of this entity.
const (
	ActionViewPayment   security.Action = "payment.view"
	ActionCreatePayment security.Action = "payment.create"
)

// PaymentPolicy is the only authority over who does what with a Payment.
type PaymentPolicy struct{}

// Can decides whether the subject may perform the action.
func (PaymentPolicy) Can(ctx context.Context, s security.Subject, a security.Action, o models.Payment) error {
	if o.ID != "" && o.TenantID != s.Tenant {
		return fmt.Errorf("payment belongs to another tenant")
	}

	// arandu:begin custom
	switch a {
	case ActionViewPayment, ActionCreatePayment:
		if s.HasRole("admin") {
			return nil
		}
	}
	// arandu:end custom

	return fmt.Errorf("no rule allows %s on payment", a)
}
