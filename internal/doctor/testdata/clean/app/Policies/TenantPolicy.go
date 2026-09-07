package policies

import (
	"context"

	"github.com/arandu-io/framework/security"

	models "example.test/p/app/Models"
)

// TenantPolicy decides who may reach a tenant row.
type TenantPolicy struct{}

// Can answers about one tenant.
func (TenantPolicy) Can(_ context.Context, s security.Subject, a security.Action, record models.Tenant) error {
	if record.ID != "" && record.ID != s.Tenant {
		return security.ErrForbidden
	}
	return nil
}
