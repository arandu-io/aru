package models

import (
	"errors"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/hesape/database/model"
)

// Entry is an editorial entry, soft deleted, paged fifty at a time.
type Entry struct {
	model.Model[Entry]

	ID         string     `db:"id"`
	TenantID   string     `db:"tenant_id"`
	Collection string     `db:"collection"`
	Title      string     `db:"title"`
	DeletedAt  *time.Time `db:"deleted_at"`
}

// entriesPerPage is how many entries a page holds.
const entriesPerPage = 50

// Entries returns the model for editorial_entries.
func Entries(db *data.DB) *model.Model[Entry] {
	m := model.NewModel[Entry]("editorial_entries", db, db.GetQueryGrammar(), db.GetPostProcessor())
	m.KeyType = "string"
	m.Incrementing = false
	m.Timestamps = false
	m.SoftDeletes = true
	m.PerPage = entriesPerPage
	m.RegisterModelEvent(model.Saving, func(e *model.Model[Entry]) error {
		if e.Entity.Title == "" {
			return errors.New("entry: a title is required")
		}
		return nil
	})
	return m
}

// Term is a taxonomy term, shared by every tenant.
type Term struct {
	model.Model[Term]

	ID   string `db:"id"`
	Name string `db:"name"`
}

// Terms returns the model for os_terms, which no tenant owns.
func Terms(db *data.DB) *model.Model[Term] {
	m := model.NewModel[Term]("os_terms", db, db.GetQueryGrammar(), db.GetPostProcessor()).UseUniqueIDs()
	m.TenantColumn = ""
	return m
}

// Lead is scoped by the organisation rather than by tenant_id.
type Lead struct {
	model.Model[Lead]

	ID    int64  `db:"id"`
	OrgID string `db:"org_id"`
}

// Leads returns the model for leads, keyed by the database.
func Leads(db *data.DB) *model.Model[Lead] {
	m := model.NewModel[Lead]("leads", db, db.GetQueryGrammar(), db.GetPostProcessor())
	m.TenantColumn = "org_id"
	m.PrimaryKey = "id"
	return m
}
