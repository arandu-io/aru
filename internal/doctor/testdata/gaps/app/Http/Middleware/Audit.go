package middleware

import (
	"github.com/arandu-io/framework/httpx"

	repositories "example.test/gaps/app/Repositories"
)

// Audit holds the repository itself, which means the Grant it needs has to be
// issued here -- the middleware authorizing itself, one layer before the
// controller the rule was written for.
type Audit struct {
	reports *repositories.ReportRepository
}

// Handle records the request.
func (m Audit) Handle(ctx *httpx.Context, next httpx.Handler) error {
	return next(ctx)
}
