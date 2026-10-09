package models

import "time"

// Report is the entity of the reports page. It carries the tenant column, which
// is what makes every query against it a query that has to be scoped.
type Report struct {
	ID       string
	TenantID string
	Total    int64
	Archived bool
	Due      time.Time
	Name     string
}

// ReportQuery is the query of the entity; its scopes are the query by design.
type ReportQuery struct {
	since time.Time
}

// arandu:begin custom

// Overdue is a rule of the entity, with the time passed in.
func (r Report) Overdue(now time.Time) bool { return now.After(r.Due) }

// Slug answers the entity's own field; it reimplements nothing.
func (r Report) Slug() string { return r.Name }

// Recent is a scope: it is the query, and reads the clock as one.
func (q *ReportQuery) Recent() *ReportQuery {
	q.since = time.Now().Add(-24 * time.Hour)
	return q
}

// arandu:end custom
