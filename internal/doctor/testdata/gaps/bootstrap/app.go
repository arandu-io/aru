// Package bootstrap wires the application.
package bootstrap

import (
	frameevents "github.com/arandu-io/framework/events"
	"github.com/arandu-io/framework/kernel"
	"github.com/arandu-io/hesape/view"

	controllers "example.test/gaps/app/Http/Controllers"
	services "example.test/gaps/app/Services"

	// The engine .env.example names. A connector registers itself from init(),
	// so this line is what puts MySQL in the binary.
	_ "github.com/arandu-io/hesape/database/connectors/mysql"
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
