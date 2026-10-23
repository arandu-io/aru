package conformance_test

import (
	"testing"

	"github.com/arandu-io/framework/test/conformance"
)

// The kit, called: a test of the same name as a copied one, and no copy.
func TestApplicationCodeDoesNotNameSessionGuard(t *testing.T) {
	conformance.Boundaries(t, "../..")
}
