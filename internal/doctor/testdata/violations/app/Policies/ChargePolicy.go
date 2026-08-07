package policies

import (
	"context"
	"fmt"

	"github.com/arandu-io/framework/security"

	models "example.test/p/app/Models"
)

// ChargePolicy is what `aru make:policy` writes and nobody opened: it denies
// every action, so the entity is unreachable and the module looks finished.
type ChargePolicy struct{}

// Can decides whether the subject may perform the action.
func (ChargePolicy) Can(ctx context.Context, s security.Subject, a security.Action, o models.Charge) error {
	// arandu:begin custom
	// Open the actions this application needs.
	// arandu:end custom

	return fmt.Errorf("no rule allows %s on charge", a)
}
