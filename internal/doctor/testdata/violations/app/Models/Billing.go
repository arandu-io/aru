package models

import "time"

// Charge is the entity, and it holds a secret with no redaction: one
// observability.Dump publishes the token on the debug page.
type Charge struct {
	ID       string
	TenantID string
	Password string
	APIToken string
}

// arandu:begin custom

// Overdue is a rule of the entity that reads the clock itself, so no test can
// say on which date a charge becomes overdue.
func (c Charge) Overdue(due time.Time) bool { return time.Now().After(due) }

// arandu:end custom
