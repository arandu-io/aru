// Package controllers is the request path.
package controllers

import (
	models "example.test/orm/app/Models"
)

// InvoiceFilterController reads a filter off the request.
type InvoiceFilterController struct{}

// Status converts the query parameter to the model's typed status: a
// conversion, not a query, and nothing to report.
func (c *InvoiceFilterController) Status(raw string) models.InvoiceStatus {
	return models.InvoiceStatus(raw)
}
