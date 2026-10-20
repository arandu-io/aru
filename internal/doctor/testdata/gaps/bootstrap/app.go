// Package bootstrap wires the application.
package bootstrap

import (
	"encoding/json"

	frameevents "github.com/arandu-io/framework/events"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/kernel"
	"github.com/arandu-io/hesape/cache"
	"github.com/arandu-io/hesape/session"
	"github.com/arandu-io/hesape/view"

	controllers "example.test/gaps/app/Http/Controllers"
	services "example.test/gaps/app/Services"

	// The engine .env.example names. A connector registers itself from init(),
	// so this line is what puts MySQL in the binary.
	_ "github.com/arandu-io/hesape/database/connectors/mysql"

	// Imported by name, for the type below. That links the connector exactly
	// as a blank import would: the package's init runs either way, so
	// SESSION_DRIVER=redis is served.
	hredis "github.com/arandu-io/hesape/redis"
)

// Boot builds an application-owned outbox writer and registers the modules of
// this application.
//
// The events module was here and somebody removed it while tidying: nothing else
// in the project mentions it, the build is green, the test suite is green, and
// the first domain write gets a 500. The write stores its event in the same
// transaction, and that transaction cannot commit without the outbox table.
func Boot() *kernel.Kernel {
	_ = frameevents.NewOutbox(nil)

	k := kernel.New()
	k.Register(
		view.NewModule(),
	)
	return k
}

// Reports builds the reports controller, which is what makes its constructor
// wired: a constructor nothing calls is code no route reaches.
func Reports(exports *services.ExportService) *controllers.ReportController {
	return controllers.NewReportController(exports)
}

// Exempt is the CSRF exemption the pipeline is built with. "/hooks" has no
// trailing slash, so it exempts that one path and nothing below it.
func Exempt() middleware.CSRFOption {
	return middleware.CSRFExcept("/webhooks/", "/hooks")
}

// Sessions keeps the sessions in the shared store when it is the one the
// connector opened.
func Sessions(store cache.SharedStore) session.Handler[json.RawMessage] {
	if shared, ok := store.(*hredis.Shared); ok {
		return shared.Sessions()
	}
	return nil
}
