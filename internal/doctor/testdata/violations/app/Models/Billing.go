package models

// Charge is the entity, and it holds a secret with no redaction: one
// observability.Dump publishes the token on the debug page.
type Charge struct {
	ID       string
	TenantID string
	Password string
	APIToken string
}
