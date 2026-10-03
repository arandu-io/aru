package services

import (
	"context"

	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/database/model"
	"github.com/arandu-io/hesape/pagination"

	models "example.test/skeleton/app/Models"
)

// NoteService is the service the skeleton writes over its notes.
type NoteService struct{ db model.DB }

// Create stores a note.
func (s *NoteService) Create(ctx context.Context, g auth.Grant, title string) (*models.Note, error) {
	instance, err := models.Notes(s.db).New()
	if err != nil {
		return nil, err
	}
	record := instance
	record.Title = title
	if _, err := record.Save(ctx, g); err != nil {
		return nil, err
	}
	return record, nil
}

// Get reads one note.
func (s *NoteService) Get(ctx context.Context, g auth.Grant, id string) (*models.Note, error) {
	return models.Notes(s.db).FindOrFail(ctx, g, id)
}

// Pinned reads the pinned notes through the query the model hands out.
func (s *NoteService) Pinned(ctx context.Context, g auth.Grant) (models.NoteCollection, error) {
	q := models.Notes(s.db).Where("pinned", true)
	q = q.Where(func(w *models.NoteQuery) { w.Where("title", "!=", "").OrWhere("body", "!=", "") })
	return q.Latest().Get(ctx, g)
}

// Count counts the notes of the tenant.
func (s *NoteService) Count(ctx context.Context, g auth.Grant) (int64, error) {
	return models.Notes(s.db).Count(ctx, g)
}

// Forget clears a factor through the second entity of TwoFactor.go.
func (s *NoteService) Forget(ctx context.Context, g auth.Grant, userID string) error {
	if _, err := models.RecoveryCodes(s.db).Where("user_id", "=", userID).Delete(ctx, g); err != nil {
		return err
	}
	_, err := models.TwoFactors(s.db).WhereKey(userID).Delete(ctx, g)
	return err
}

var _ = pagination.Options{}
