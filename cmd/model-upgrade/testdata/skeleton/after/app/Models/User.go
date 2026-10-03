// Package models holds this application's domain types.
package models

import (
	"database/sql/driver"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/arandu-io/hesape/database/model"
)

// User is an account owned by this application.
type User struct {
	model.Model

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

// userTable is the table of User.
// Its query, Users, is generated beside it by aru model:build.
//
// Users returns the model for the application-owned users table.
//
// The primary key is text the model generates on insert, so nothing writes an
// id by hand. The table has no updated_at column.
var userTable = model.NewTable(model.TableSpec{
	Name:            "users",
	New:             func() model.Entity { return new(User) },
	UniqueIDs:       true,
	UpdatedAtColumn: model.NoColumn,
})

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
