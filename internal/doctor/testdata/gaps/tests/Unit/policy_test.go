package unit_test

import (
	"context"
	"testing"

	"github.com/arandu-io/hesape/auth"

	policies "example.test/gaps/app/Policies"
)

// A Policy's unit test builds the Subject it asks about: the subject is the
// rule's input here, and reaches no handler.
func TestTheLedgerPolicyLetsAnAdminRead(t *testing.T) {
	admin := auth.Subject{ID: "a1", Tenant: "t1", Roles: []string{"admin"}}
	if err := (policies.LedgerPolicy{}).Can(context.Background(), admin, "ledger.read", nil); err != nil {
		t.Fatal(err)
	}
}
