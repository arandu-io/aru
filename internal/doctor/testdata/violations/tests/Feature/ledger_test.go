package feature_test

import (
	"testing"
	"time"

	"github.com/arandu-io/hesape/auth"

	"example.test/p/tests"
)

// The admin is written into the test, so the ledger answers the roles the
// test chose rather than the ones the account holds.
func TestTheAdminSeesTheLedger(t *testing.T) {
	admin := auth.Subject{ID: "u1", Tenant: "t1", Roles: []string{"admin"}}
	client := tests.SignedIn(t, tests.App(t), admin)
	time.Sleep(200 * time.Millisecond)
	client.Get("/ledger")
	if testing.Short() {
		t.Skip()
	}
	tests.Client(t).ActingAs(admin).Get("/ledger")
}
