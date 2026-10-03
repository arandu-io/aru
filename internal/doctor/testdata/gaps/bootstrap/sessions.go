package bootstrap

import (
	"encoding/json"

	"github.com/arandu-io/hesape/cache"
	"github.com/arandu-io/hesape/session"

	// Imported by name, for the type below. That links the connector exactly
	// as a blank import would: the package's init runs either way, so
	// SESSION_DRIVER=redis is served.
	hredis "github.com/arandu-io/hesape/redis"
)

// Sessions keeps the sessions in the shared store when it is the one the
// connector opened.
func Sessions(store cache.SharedStore) session.Handler[json.RawMessage] {
	if shared, ok := store.(*hredis.Shared); ok {
		return shared.Sessions()
	}
	return nil
}
