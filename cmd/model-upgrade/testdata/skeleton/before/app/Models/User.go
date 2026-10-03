// Package models holds this application's domain types.
package models

import (
	"database/sql/driver"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/hesape/database/model"
)

// User is an account owned by this application.
type User struct {
	ID       string `db:"id"`
	TenantID string `db:"tenant_id"`
	Name     string `db:"name"`
	Email    string `db:"email"`
	Password string `db:"password"`

	// Roles is stored as a JSON array of text in one portable column.
	Roles Roles `db:"roles"`

	VerifiedAt *time.Time `db:"verified_at"`
	CreatedAt  time.Time  `db:"created_at"`
}

// RoleMember is an ordinary account.
const RoleMember = "member"

// Users returns the model for the application-owned users table.
//
// The primary key is text the model generates on insert, so nothing writes an
// id by hand. The table has no updated_at column.
func Users(db *data.DB) *model.Model[User] {
	m := model.NewModel[User]("users", db, db.GetQueryGrammar(), db.GetPostProcessor()).UseUniqueIDs()
	m.UpdatedAtColumn = ""
	return m
}

// Roles is the set of roles an account carries, stored as a JSON array of text.
type Roles []string

// Value writes the roles as a JSON array.
func (r Roles) Value() (driver.Value, error) {
	b, err := json.Marshal([]string(r))
	return string(b), err
}

// LogValue records only identifiers when a whole User reaches structured logs.
func (u User) LogValue() slog.Value {
	return slog.GroupValue(slog.String("id", u.ID), slog.String("tenant", u.TenantID))
}
