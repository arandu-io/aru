// Package security is the slice of the framework's security bridge this
// fixture imports, declared the way the framework declares it: aliases and
// calls through to hesape/auth, and one envelope of its own.
//
// This package is a bridge. It is removed in v1.0.0; import github.com/arandu-io/hesape/auth directly.
package security

import (
	"context"

	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/session"
)

type Grant = auth.Grant

type Subject = auth.Subject

type Action = auth.Action

type Policy[T any] = auth.Policy[T]

func Authorize[T any](ctx context.Context, p Policy[T], s Subject, a Action, resource T) (Grant, error) {
	return auth.Authorize(ctx, p, s, a, resource)
}

func SystemGrant(a Action, tenant string) Grant { return auth.SystemGrant(a, tenant) }

// SessionStore is an envelope: it translates hesape/session's record store,
// so it is the framework's own.
type SessionStore struct {
	inner *session.RecordStore[Subject]
}
