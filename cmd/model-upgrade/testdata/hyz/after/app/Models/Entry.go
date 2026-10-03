package models

import (
	"errors"
	"time"

	"github.com/arandu-io/hesape/database/model"
)

// Entry is an editorial entry, soft deleted, paged fifty at a time.
type Entry struct {
	model.Model

	ID         string     `db:"id"`
	TenantID   string     `db:"tenant_id"`
	Collection string     `db:"collection"`
	Title      string     `db:"title"`
	DeletedAt  *time.Time `db:"deleted_at"`
}

// entriesPerPage is how many entries a page holds.
const entriesPerPage = 50

// entryTable is the table of Entry.
// Its query, Entries, is generated beside it by aru model:build.
//
// Entries returns the model for editorial_entries.
var entryTable = model.NewTable(model.TableSpec{
	Name:         "editorial_entries",
	New:          func() model.Entity { return new(Entry) },
	ManualKey:    true,
	NoTimestamps: true,
	SoftDeletes:  true,
	PerPage:      entriesPerPage,
	Events: map[model.Event][]func(model.Entity) error{
		model.Saving: {func(entity model.Entity) error {
			e := entity.(*Entry)
			if e.Title == "" {
				return errors.New("entry: a title is required")
			}
			return nil
		}},
	},
})

// Term is a taxonomy term, shared by every tenant.
type Term struct {
	model.Model

	ID   string `db:"id"`
	Name string `db:"name"`
}

// termTable is the table of Term.
// Its query, Terms, is generated beside it by aru model:build.
//
// Terms returns the model for os_terms, which no tenant owns.
var termTable = model.NewTable(model.TableSpec{
	Name:      "os_terms",
	New:       func() model.Entity { return new(Term) },
	UniqueIDs: true,
	Global:    true,
})

// Lead is scoped by the organisation rather than by tenant_id.
type Lead struct {
	model.Model

	ID    int64  `db:"id"`
	OrgID string `db:"org_id"`
}

// leadTable is the table of Lead.
// Its query, Leads, is generated beside it by aru model:build.
//
// Leads returns the model for leads, keyed by the database.
var leadTable = model.NewTable(model.TableSpec{
	Name:         "leads",
	New:          func() model.Entity { return new(Lead) },
	TenantColumn: "org_id",
})
