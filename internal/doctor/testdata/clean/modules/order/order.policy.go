package order

import (
	"context"
	"fmt"

	"github.com/arandu-io/framework/security"
)

// Actions of this module. Constants rather than strings at the call site: a typo
// in an action name would silently authorize nothing, or worse, everything.
const (
	ActionView   security.Action = "order.view"
	ActionCreate security.Action = "order.create"
	ActionUpdate security.Action = "order.update"
	ActionDelete security.Action = "order.delete"
)

// Policy is the only authority over who does what with Order.
//
// IT DENIES EVERYTHING. That is deliberate: a generated policy that allowed
// anything would be a hole shipped by default, in every project that ran the
// generator. Open what this module actually needs, and nothing else.
type Policy struct{}

// Can decides whether the subject may perform the action.
func (Policy) Can(ctx context.Context, s security.Subject, a security.Action, o Order) error {
	// Tenant isolation comes first and applies to every action. Without it every
	// check below would be pointless in a multi-tenant system.
	if o.ID != "" && o.TenantID != s.Tenant {
		return fmt.Errorf("order belongs to another tenant")
	}

	// arandu:begin custom
	switch a {
	case ActionView, ActionCreate, ActionUpdate, ActionDelete:
		if s.HasRole("admin") {
			return nil
		}
	}
	// Open the actions this module needs. For example:
	//
	//	switch a {
	//	case ActionView:
	//		if s.HasRole("admin") || s.HasRole("staff") {
	//			return nil
	//		}
	//	case ActionCreate, ActionUpdate, ActionDelete:
	//		if s.HasRole("admin") {
	//			return nil
	//		}
	//	}
	// arandu:end custom

	return fmt.Errorf("no rule allows %s on order", a)
}
