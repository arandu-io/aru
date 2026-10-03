package seeders

import (
	"context"

	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/database/model"
	factory "github.com/arandu-io/hesape/database/model/factories"

	models "example.test/skeleton/app/Models"
	factories "example.test/skeleton/database/factories"
)

// Deps is what a seeder receives.
type Deps struct {
	Tenant string
	DB     model.DB
}

// NoteSeeder seeds the notes.
type NoteSeeder struct{}

// Run creates the rows through the factory, in d.Tenant.
func (NoteSeeder) Run(ctx context.Context, d Deps) error {
	seeded, err := models.Notes(d.DB).NewQuery().Exists(ctx, auth.SystemGrant("note.list", d.Tenant))
	if err != nil || seeded {
		return err
	}
	authors, err := factories.UserFactory(d.DB).Count(2).Create(ctx, auth.SystemGrant("user.create", d.Tenant))
	if err != nil {
		return err
	}
	byAuthor := factories.NoteFactory(d.DB).Count(6).
		Sequence(factories.WrittenBy(authors[0].ID), factories.WrittenBy(authors[1].ID))
	_, err = byAuthor.Create(ctx, auth.SystemGrant("note.create", d.Tenant))
	return err
}

// keep is a factory held in a variable, typed with the generic factory.
var keep func(model.DB) *factory.Factory[models.Note]
