package models

import "github.com/arandu-io/hesape/database/model"

// User is read without trouble: the problems are elsewhere.
type User struct {
	model.Model[User]

	ID string `db:"id"`
}

// Users returns the model for users.
func Users(db model.DB) *model.Model[User] {
	return model.NewModel[User]("users", db, nil, nil).UseUniqueIDs()
}
