package controllers

import (
	"net/http"
)

// ordered stands for a query: the doctor reads names, not types.
type ordered struct{}

// OrderBy orders by a column.
func (o *ordered) OrderBy(column any, direction ...string) *ordered { return o }

// Sorted orders by the column the request names, which lets the reader order
// by any column of the table, hidden ones included.
func Sorted(r *http.Request, o *ordered) *ordered {
	sort := r.URL.Query().Get("sort")
	return o.OrderBy(sort)
}
