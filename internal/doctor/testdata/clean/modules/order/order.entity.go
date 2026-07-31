package order

import (
	"log/slog"
	"time"
)

// Order is the entity. It has no persistence methods: this is not Active
// Record, and a type that can save itself can save itself from anywhere.
type Order struct {
	ID        string
	TenantID  string
	Reference string
	Total     int64
	CreatedAt time.Time
}

// LogValue implements slog.LogValuer, so passing the whole entity to a log call
// records the identifiers and nothing else. Add any sensitive field to the
// custom block below and it stays out of logs, dumps and the debug page.
func (o Order) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("id", o.ID),
		slog.String("tenant", o.TenantID),
	)
}

// arandu:begin custom
// MarshalJSON, computed fields and anything else about this entity go here.
// arandu:end custom
