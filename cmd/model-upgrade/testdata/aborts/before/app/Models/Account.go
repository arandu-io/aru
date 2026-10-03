package models

import "github.com/arandu-io/hesape/database/model"

// Account reads from a replica, which a table spec does not say.
type Account struct {
	model.Model[Account]

	ID string `db:"id"`
}

// Accounts returns the model for accounts.
func Accounts(db model.DB) *model.Model[Account] {
	m := model.NewModel[Account]("accounts", db, nil, nil)
	m.ConnectionName = "replica"
	return m
}

// Profile decides its table at run time.
type Profile struct {
	model.Model[Profile]

	ID string `db:"id"`
}

// Profiles returns the model for profiles.
func Profiles(db model.DB, archived bool) *model.Model[Profile] {
	m := model.NewModel[Profile]("profiles", db, nil, nil)
	if archived {
		m.SoftDeletes = true
	}
	return m
}

// Orphan embeds the model of a type nothing constructs.
type Orphan struct {
	model.Model[Orphan]
}
