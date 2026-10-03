package models

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/arandu-io/hesape/database/model"
)

const redactedSecret = "[redacted]"

// TwoFactor is one account's application-owned authenticator enrolment.
//
// ConfirmedAt is nil until the first code proves the enrolment. It is a pointer
// because the column is NULL then, and the conditional writes ask for exactly
// that: a zero time would be stored as a date and match nothing.
type TwoFactor struct {
	model.Model

	UserID       string     `db:"user_id"`
	TenantID     string     `db:"tenant_id"`
	Secret       string     `db:"secret"`
	ConfirmedAt  *time.Time `db:"confirmed_at"`
	LastUsedStep uint64     `db:"last_used_step"`
	CreatedAt    time.Time  `db:"created_at"`
}

// twoFactorTable is the table of TwoFactor.
// Its query, TwoFactors, is generated beside it by aru model:build.
//
// TwoFactors returns the model for the user_two_factor table.
//
// The key is the account's id: one enrolment per account, written by the
// application and never generated.
var twoFactorTable = model.NewTable(model.TableSpec{
	Name:            "user_two_factor",
	New:             func() model.Entity { return new(TwoFactor) },
	PrimaryKey:      "user_id",
	ManualKey:       true,
	UpdatedAtColumn: model.NoColumn,
})

// Enabled reports whether the enrolment was proved with its first code.
func (t TwoFactor) Enabled() bool { return t.ConfirmedAt != nil && !t.ConfirmedAt.IsZero() }

// MarshalJSON keeps the encrypted secret out of responses and debug dumps.
func (t TwoFactor) MarshalJSON() ([]byte, error) {
	marker := ""
	if t.Secret != "" {
		marker = redactedSecret
	}
	return json.Marshal(struct {
		UserID   string `json:"user_id"`
		TenantID string `json:"tenant_id"`
		Enabled  bool   `json:"enabled"`
		Secret   string `json:"secret"`
	}{UserID: t.UserID, TenantID: t.TenantID, Enabled: t.Enabled(), Secret: marker})
}

// LogValue records only the account and whether its factor is active.
func (t TwoFactor) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("user", t.UserID),
		slog.String("tenant", t.TenantID),
		slog.Bool("enabled", t.Enabled()),
	)
}

// String prevents formatting a factor from exposing its encrypted secret.
func (t TwoFactor) String() string {
	if t.UserID == "" {
		return "two factor: none"
	}
	if t.Enabled() {
		return "two factor: enabled for " + t.UserID
	}
	return "two factor: awaiting confirmation for " + t.UserID
}

// RecoveryCode is one single-use recovery code of an enrolment, stored as a
// password hash. UsedAt is nil until the code is spent.
type RecoveryCode struct {
	model.Model

	ID        string     `db:"id"`
	TenantID  string     `db:"tenant_id"`
	UserID    string     `db:"user_id"`
	CodeHash  string     `db:"code_hash"`
	UsedAt    *time.Time `db:"used_at"`
	CreatedAt time.Time  `db:"created_at"`
}

// recoveryCodeTable is the table of RecoveryCode.
// Its query, RecoveryCodes, is generated beside it by aru model:build.
//
// RecoveryCodes returns the model for the user_recovery_codes table. The id is
// text the model generates on insert.
var recoveryCodeTable = model.NewTable(model.TableSpec{
	Name:            "user_recovery_codes",
	New:             func() model.Entity { return new(RecoveryCode) },
	UniqueIDs:       true,
	UpdatedAtColumn: model.NoColumn,
})

// MarshalJSON keeps the hash out of responses and debug dumps.
func (c RecoveryCode) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ID       string `json:"id"`
		TenantID string `json:"tenant_id"`
		UserID   string `json:"user_id"`
		Used     bool   `json:"used"`
	}{ID: c.ID, TenantID: c.TenantID, UserID: c.UserID, Used: c.UsedAt != nil})
}

// LogValue records only the identifiers when a code reaches structured logs.
func (c RecoveryCode) LogValue() slog.Value {
	return slog.GroupValue(slog.String("id", c.ID), slog.String("user", c.UserID), slog.String("tenant", c.TenantID))
}
