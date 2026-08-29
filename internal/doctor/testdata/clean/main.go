// Command app is the whole application: in Go the binary is the server, so the
// entry point and the command line are this one file.
package main

import (
	"log"

	frameevents "github.com/arandu-io/framework/events"
	"github.com/arandu-io/framework/kernel"
	"github.com/arandu-io/hesape/view"

	// The migrations register themselves, and a package nobody imports is not
	// in the binary at all -- so its init never runs and the schema is never
	// applied. Nothing else here names the package, so the import is blank.
	_ "example.test/p/database/migrations"
)

func main() {
	// The same shape the gaps fixture has, so the control is a real control:
	// this is what builds the outbox writer.
	_ = frameevents.NewOutbox(nil)

	k := kernel.New()
	k.Register(
		view.NewModule(),
		// The outbox table, next to the module that writes to it. This is the
		// shape both shipped bootstraps have, and the control for
		// outbox-not-registered: a rule that fired here would fire on every
		// correct project.
		frameevents.NewModule(),
	)

	if err := k.Run(); err != nil {
		log.Fatal(err)
	}
}
