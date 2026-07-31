package order

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
)

// Errors this repository returns, so callers never import database/sql.
var (
	ErrNotFound = errors.New("order: not found")
	ErrConflict = errors.New("order: already exists")
	ErrSort     = errors.New("order: sort field not allowed")
)

// Pagination bounds. A request that asks for everything gets the maximum, never
// everything: an unbounded query is how one page load takes a database down.
const (
	defaultLimit = 50
	maxLimit     = 200
)

// Repo is the only door to the orders table.
//
// Every method starts with g.Check. The Grant is required by the signature, so
// forgetting it is a compile error, and the check proves the grant was issued for
// this exact action -- which catches copy-paste between methods.
//
// The SQL uses "?" placeholders and types every supported database shares, so
// these statements run unchanged on SQLite and PostgreSQL.
type Repo struct {
	db *data.DB
}

// NewRepo returns a repository over an instrumented handle.
func NewRepo(db *data.DB) *Repo { return &Repo{db: db} }

// Compile-time proof of the contract.
var _ data.Repository[Order, string] = (*Repo)(nil)

const columns = `id, tenant_id, reference, total, created_at`

// Find returns one order by id, scoped to the grant's tenant.
func (r *Repo) Find(ctx context.Context, g security.Grant, id string) (Order, error) {
	if err := g.Check(ActionView); err != nil {
		return Order{}, err
	}
	// The tenant comes from the Grant, never from the request.
	row := r.db.QueryRowContext(ctx,
		`SELECT `+columns+` FROM orders WHERE id = ? AND tenant_id = ?`,
		id, data.Tenant(g))
	return scan(row)
}

// sortable is the ordering allowlist. A sort field is a column name, and a column
// name taken from the request is injection through a door nobody watches.
var sortable = map[string]string{
	"":           "created_at",
	"created_at": "created_at",
	"reference":  "reference",
}

// List returns a page of orders in the grant's tenant.
//
// Pagination is keyset based: OFFSET grows more expensive with every page and
// skips rows when data changes underneath it.
func (r *Repo) List(ctx context.Context, g security.Grant, q data.Query) ([]Order, error) {
	if err := g.Check(ActionView); err != nil {
		return nil, err
	}
	column, ok := sortable[q.Sort]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrSort, q.Sort)
	}

	limit := q.Limit
	switch {
	case limit <= 0:
		limit = defaultLimit
	case limit > maxLimit:
		limit = maxLimit
	}

	query := `SELECT ` + columns + ` FROM orders WHERE tenant_id = ?`
	args := []any{data.Tenant(g)}
	if q.Cursor != "" {
		query += ` AND (created_at > (SELECT created_at FROM orders WHERE id = ? AND tenant_id = ?)
		            OR (created_at = (SELECT created_at FROM orders WHERE id = ? AND tenant_id = ?) AND id > ?))`
		args = append(args, q.Cursor, args[0], q.Cursor, args[0], q.Cursor)
	}
	query += ` ORDER BY ` + column + `, id LIMIT ?`
	args = append(args, limit)
	_ = strconv.Itoa // kept for templates that build placeholder lists

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Order
	for rows.Next() {
		o, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Create inserts the order and returns it as stored.
func (r *Repo) Create(ctx context.Context, g security.Grant, o Order) (Order, error) {
	if err := g.Check(ActionCreate); err != nil {
		return Order{}, err
	}

	var err error
	if o.ID == "" {
		if o.ID, err = NewID(); err != nil {
			return Order{}, err
		}
	}
	o.TenantID = data.Tenant(g)
	o.CreatedAt = time.Now().UTC()

	_, err = r.db.ExecContext(ctx,
		`INSERT INTO orders (`+columns+`) VALUES (?, ?, ?, ?, ?)`,
		o.ID, o.TenantID, o.Reference, o.Total, o.CreatedAt)
	if err != nil {
		if isConflict(err) {
			return Order{}, ErrConflict
		}
		return Order{}, err
	}
	return o, nil
}

// Update writes the mutable fields. The tenant is not one of them:
// moving a row between tenants is not an update, it is a migration.
func (r *Repo) Update(ctx context.Context, g security.Grant, o Order) (Order, error) {
	if err := g.Check(ActionUpdate); err != nil {
		return Order{}, err
	}

	res, err := r.db.ExecContext(ctx,
		`UPDATE orders SET reference = ?, total = ? WHERE id = ? AND tenant_id = ?`,
		o.Reference, o.Total, o.ID, data.Tenant(g))
	if err != nil {
		if isConflict(err) {
			return Order{}, ErrConflict
		}
		return Order{}, err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return Order{}, ErrNotFound
	}
	return o, nil
}

// Delete removes one order within the grant's tenant.
func (r *Repo) Delete(ctx context.Context, g security.Grant, id string) error {
	if err := g.Check(ActionDelete); err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM orders WHERE id = ? AND tenant_id = ?`, id, data.Tenant(g))
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

type rowScanner interface{ Scan(dest ...any) error }

func scan(row rowScanner) (Order, error) {
	var o Order
	err := row.Scan(&o.ID, &o.TenantID, &o.Reference, &o.Total, &o.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Order{}, ErrNotFound
	}
	if err != nil {
		return Order{}, err
	}
	return o, nil
}

// normalize lowercases and trims, which is what keeps a plain UNIQUE index
// case-insensitive on every engine.
func normalize(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// NewID returns a version 4 UUID as text. Ids come from the application, not from
// a database default, so one schema works on every engine.
func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("order: reading random bytes: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// isConflict recognizes a duplicate key across engines by message, which is the
// price of not importing a driver into a module.
func isConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "duplicate entry")
}

// arandu:begin custom
// Queries beyond the five above go here, and survive regeneration. Keep the
// g.Check as the first line of each one.
// arandu:end custom
