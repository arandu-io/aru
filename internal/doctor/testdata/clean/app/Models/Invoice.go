// Package models holds the entities, as app/Models does in Laravel. They have no
// persistence methods: this is not Active Record, and a type that can save
// itself can save itself from anywhere.
package models

import (
	"log/slog"
	"time"
)

// Invoice is the entity.
type Invoice struct {
	ID        string
	TenantID  string
	Reference string
	Total     int64
	CreatedAt time.Time
}

// User is the account that signs in.
type User struct {
	ID           string
	TenantID     string
	Email        string
	PasswordHash string
}

// LogValue implements slog.LogValuer, so passing the whole user to a log call
// records the identifiers and nothing else -- the hash never reaches a log line,
// a dump or the debug page.
func (u User) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("id", u.ID),
		slog.String("tenant", u.TenantID),
	)
}

// MarshalJSON keeps the same promise on the wire.
func (u User) MarshalJSON() ([]byte, error) {
	return []byte(`{"id":"` + u.ID + `"}`), nil
}
