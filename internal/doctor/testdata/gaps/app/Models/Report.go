package models

// Report is the entity of the reports page. It carries the tenant column, which
// is what makes every query against it a query that has to be scoped.
type Report struct {
	ID       string
	TenantID string
	Total    int64
	Archived bool
}
