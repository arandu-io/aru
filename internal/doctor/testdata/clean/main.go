// Command app is the whole application: in Go the binary is the server, so this
// file is what public/index.php and artisan are in Laravel.
package main

import (
	"log"

	"github.com/arandu-io/framework/events"
	"github.com/arandu-io/framework/kernel"
	"github.com/arandu-io/framework/modules/auth"
	"github.com/arandu-io/framework/view"

	// The migrations register themselves, and a package nobody imports is not
	// in the binary at all -- so its init never runs and the schema is never
	// applied. Nothing else here names the package, so the import is blank.
	_ "example.test/p/database/migrations"
)

func main() {
	// The same shape the gaps fixture has, so the control is a real control:
	// this is what builds the outbox writer.
	svc := auth.NewService(auth.NewUserRepo(nil), nil, nil)

	k := kernel.New()
	k.Register(
		view.NewModule(),
		auth.New(svc, auth.FixedTenant("acme")),
		// The outbox table, next to the module that writes to it. This is the
		// shape both shipped bootstraps have, and the control for
		// outbox-not-registered: a rule that fired here would fire on every
		// correct project.
		events.NewModule(),
	)

	if err := k.Run(); err != nil {
		log.Fatal(err)
	}
}
