// Package routes declares the routes of the application, as routes/web.php
// does.
package routes

import (
	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/http"
)

// Web registers the web routes.
func Web(r *http.Router) {
	// arandu:begin custom
	// A handler written inline, in the block the skeleton invites people to
	// write in: same request path as a controller, same database, and the two
	// rules that police the boundary only looked at app/Http/Controllers.
	r.Action("GET", "/totals", func(ctx *http.Context) error {
		db := data.Wrap(nil, data.DialectSQLite)
		_ = db
		return nil
	})
	// arandu:end custom
}
