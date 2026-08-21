// Package bootstrap wires the application, as bootstrap/app.php does.
package bootstrap

import (
	"database/sql"

	"github.com/arandu-io/queue"
	"github.com/arandu-io/queue/kv"
)

// Queue builds the queue the worker drains.
//
// It is wired against a repository that was deleted, and the wiring is what
// makes the fixture worth having: the file parses, the build resolves through
// the proxy, and nothing anywhere says the address is gone. The subpackage is
// here for the same reason -- it is as retired as the root, and a check that
// only matched the root would let this line through.
func Queue(db *sql.DB, resp string) (queue.Queue, error) {
	if resp != "" {
		return kv.Open(resp)
	}
	return queue.NewTable(db), nil
}
