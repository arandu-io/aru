// Package controllers is the request path.
package controllers

import (
	"context"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/database/query"

	models "example.test/orm/app/Models"
)

// InvoiceController is the handler.
type InvoiceController struct {
	conn      query.Connection
	grammar   query.Grammar
	processor query.Processor
}

// Index queries the model straight from the request path, which is the planted
// handler-reaches-the-model: the arrow of this framework is handler to service
// to data, and a controller that reads a row itself is a controller with the
// policy nowhere in sight.
func (c *InvoiceController) Index(ctx context.Context, g security.Grant) (int64, error) {
	return models.Invoices(c.conn, c.grammar, c.processor).
		NewQuery().
		Count(ctx, g)
}
