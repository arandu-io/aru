package services

import (
	"context"

	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/database/model"

	models "example.test/hyz/app/Models"
)

// MediaService is the library.
type MediaService struct{ db model.DB }

// Recent lists the newest images.
func (s *MediaService) Recent(ctx context.Context, g auth.Grant) (model.Collection[models.Media], error) {
	return models.MediaLibrary(s.db).NewQuery().OrderByDesc("created_at").OrderBy("id").Limit(20).Get(ctx, g)
}

// Store keeps a new image.
func (s *MediaService) Store(ctx context.Context, g auth.Grant, key string) (*models.Media, error) {
	instance, err := models.MediaLibrary(s.db).NewInstance(nil, false)
	if err != nil {
		return nil, err
	}
	m := instance.Entity
	m.FileKey = key
	if _, err := m.Save(ctx, g); err != nil {
		return nil, err
	}
	return m, nil
}

// Search pages the entries of a collection whose title matches.
func (s *MediaService) Search(ctx context.Context, g auth.Grant, key, text string) (int64, error) {
	q := models.Entries(s.db).NewQuery().Where("collection", "=", key)
	q = q.Where(func(w *model.Builder[models.Entry]) {
		w.Where("title", "like", "%"+text+"%")
	})
	return q.Clone().Count(ctx, g)
}

// Latest reads the newest entry of a collection through a model held in a
// variable and read once, which is one query before the upgrade and after it.
func (s *MediaService) Latest(ctx context.Context, g auth.Grant, key string) (*models.Entry, error) {
	entries := models.Entries(s.db)
	return entries.Where("collection", "=", key).OrderByDesc("created_at").First(ctx, g)
}

// Shared lists the terms every tenant sees.
func (s *MediaService) Shared(ctx context.Context, g auth.Grant) ([]*models.Term, error) {
	return models.Terms(s.db).Query().Get(ctx, g)
}
