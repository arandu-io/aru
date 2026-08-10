// Package bootstrap wires the application, as bootstrap/app.php does.
package bootstrap

import (
	"github.com/arandu-io/framework/kernel"
	"github.com/arandu-io/framework/modules/auth"
	"github.com/arandu-io/framework/view"
)

// Boot builds the auth service and registers the modules of this application.
//
// The events module was here and somebody removed it while tidying: nothing else
// in the project mentions it, the build is green, the test suite is green, and
// the first person to sign up gets a 500. auth.Register publishes
// auth.user.registered in the same transaction that creates the account, and
// that transaction cannot commit without the outbox table.
//
// The service is built here rather than handed in, because that is where the
// outbox is built -- auth.NewService constructs it from the repository's handle,
// and the registration below constructs nothing.
func Boot() *kernel.Kernel {
	svc := auth.NewService(auth.NewUserRepo(nil), nil, nil)

	k := kernel.New()
	k.Register(
		view.NewModule(),
		auth.New(svc, auth.FixedTenant("acme")),
	)
	return k
}
