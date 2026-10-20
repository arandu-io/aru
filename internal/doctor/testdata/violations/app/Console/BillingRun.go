// Package console holds a command outside app/Console/Commands.
package console

import "context"

// BillingRunName is the name routes/console.go registers.
const BillingRunName = "billing:run"

// RunBillingRun invoices what is due.
func RunBillingRun(ctx context.Context, args []string) error { return nil }
