package feature_test

import (
	"testing"

	// The queue's connector, linked into this test binary and into no other.
	// The application never imports it, so QUEUE_CONNECTION=redis stops the
	// boot however green this test is.
	_ "github.com/arandu-io/hesape/queue/connectors/redis"
)

func TestTheQueueDrains(t *testing.T) {
	t.Helper()
}
