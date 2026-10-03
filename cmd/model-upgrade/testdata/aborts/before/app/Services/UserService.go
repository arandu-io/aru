package services

import (
	"context"

	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/database/model"

	models "example.test/aborts/app/Models"
)

// credentialUser embeds the generic model to hand it to a provider.
type credentialUser struct{ *model.Model[models.User] }

// held keeps the model in a variable before asking it for a query.
func held(ctx context.Context, db model.DB, g auth.Grant) error {
	m := models.Users(db)
	_, err := m.NewQuery().Count(ctx, g)
	return err
}

// filled asks for an instance with attributes.
func filled(db model.DB) error {
	_, err := models.Users(db).NewInstance(map[string]any{"id": "u-1"}, true)
	return err
}

var _ credentialUser
