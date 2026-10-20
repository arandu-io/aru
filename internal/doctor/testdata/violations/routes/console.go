package routes

import (
	"context"

	console "example.test/p/app/Console"
)

// Command is one console command this application adds.
type Command struct {
	Name        string
	Description string
	Run         func(ctx context.Context, args []string) error
}

// Console returns this application's own commands. The function behind the
// one below lives in app/Console, beside the folder that owns it.
func Console() []Command {
	return []Command{
		{Name: console.BillingRunName, Description: "Invoice what is due", Run: console.RunBillingRun},
	}
}
