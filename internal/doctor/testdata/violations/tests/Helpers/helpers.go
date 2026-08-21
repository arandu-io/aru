package helpers

import "testing"

// Fixture is scaffolding: it takes a *testing.T and is built to be linked into
// a test binary.
func Fixture(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}
