package models

// Tenant is a customer of this installation.
//
// It has no TenantID, and cannot: the row is the tenant. Everything else in
// this application carries the column and is scoped by it; this table is what
// the column points at.
type Tenant struct {
	ID   string `db:"id"`
	Name string `db:"name"`
}
