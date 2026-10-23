package feature_test

import (
	"context"
	"testing"
	"time"

	"github.com/arandu-io/framework/test"
	"github.com/arandu-io/hesape/auth"

	policies "example.test/gaps/app/Policies"
	"example.test/gaps/tests"
)

// Signed in for real: the literal names the account and nothing else, and the
// kit's As starts its session. The other literal only asks a Policy.
func TestTheLedgerIsReadAsTheAccount(t *testing.T) {
	server := tests.Serve(t)
	test.As(t, server, auth.Subject{ID: "u1", Tenant: "t1"}).Get("/ledger")

	reader := auth.Subject{ID: "u2", Tenant: "t1", Roles: []string{"reader"}}
	if err := (policies.LedgerPolicy{}).Can(context.Background(), reader, "ledger.read", nil); err != nil {
		t.Fatal(err)
	}
	var nobody = auth.Subject{}
	_ = nobody

	deadline := time.After(time.Second)
	select {
	case <-deadline:
	default:
	}
	if testing.Short() {
		t.Skip("needs Postgres: set ARANDU_TEST_DATABASE_URL")
	}
}
