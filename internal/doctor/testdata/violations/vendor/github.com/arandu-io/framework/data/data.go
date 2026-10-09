// Package data is the slice of the framework's database bridge this fixture
// imports.
//
// This package is a bridge. It is removed in v1.0.0; import github.com/arandu-io/hesape/database directly.
package data

import (
	"database/sql"

	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/database"
)

type DB = database.DB

type Query = database.Query

const DialectSQLite = database.DialectSQLite

func Tenant(g auth.Grant) string { return auth.Tenant(g) }

func Wrap(db *sql.DB, d database.Dialect) *DB { return database.Wrap(db, d) }
