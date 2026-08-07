package policies

import (
	"context"
	"fmt"

	"github.com/arandu-io/framework/security"

	models "example.test/gaps/app/Models"
)

// Actions of this entity.
const (
	ActionViewReport   security.Action = "report.view"
	ActionDeleteReport security.Action = "report.delete"
)

// ReportPolicy is the only authority over who does what with a Report. It is
// opened, so nothing here is about the policy rules: this fixture is about the
// paths that reach the database around them.
type ReportPolicy struct{}

// Can decides whether the subject may perform the action.
func (ReportPolicy) Can(ctx context.Context, s security.Subject, a security.Action, o models.Report) error {
	// arandu:begin custom
	if s.HasRole("admin") {
		return nil
	}
	// arandu:end custom

	return fmt.Errorf("no rule allows %s on report", a)
}
