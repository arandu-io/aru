package models

import (
	"log/slog"
	"time"

	"github.com/arandu-io/hesape/database/model"
)

// Media is one image of the library.
//
// It embeds the model, so a row returned by a query carries the connection and
// can be saved again. Build new rows through MediaLibrary: a struct literal has
// no connection and its write methods return model.ErrUnwired.
type Media struct {
	model.Model

	ID        string    `db:"id"`
	TenantID  string    `db:"tenant_id"`
	FileKey   string    `db:"file_key"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// mediaTable is the table of Media.
// Its query, MediaRecords, is generated beside it by aru model:build.
//
// MediaLibrary returns the configured model for the media table.
//
// The primary key is application-generated text, so it does not increment.
var mediaTable = model.NewTable(model.TableSpec{
	Name:      "media",
	New:       func() model.Entity { return new(Media) },
	ManualKey: true,
})

// LogValue records the identifiers and nothing else.
func (m Media) LogValue() slog.Value {
	return slog.GroupValue(slog.String("id", m.ID))
}
