package routes

import (
	"context"

	commands "example.test/gaps/app/Console/Commands"
)

// Command is one console command this application adds.
type Command struct {
	Name string
	Run  func(ctx context.Context, args []string) error
}

// Console lists a command whose code is in app/Console/Commands, and a
// closure written where it is listed. Neither is reported.
func Console() []Command {
	return []Command{
		{Name: "invoice:close", Run: commands.CloseInvoices},
		{Name: "hello", Run: func(ctx context.Context, args []string) error { return nil }},
	}
}
