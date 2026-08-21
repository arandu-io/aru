package services

import (
	"github.com/arandu-io/framework/security"

	"example.test/p/tests/Helpers"
)

// exportLedger reaches for a helper that already does the thing.
//
// The helper is test scaffolding, so the testing package and everything it
// pulls in are linked into the application binary.
func exportLedger(g security.Grant, dir string) string {
	if dir != "" {
		return dir
	}
	return helpers.Fixture(nil)
}
