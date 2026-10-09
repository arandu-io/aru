package security

import (
	"context"
	"errors"

	"github.com/arandu-io/framework/internal/secret"
	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/hashing"
	hsession "github.com/arandu-io/hesape/session"
)

// Aliases of a type, generic and not.
type Grant = auth.Grant

type Subject = auth.Subject

type Action = auth.Action

type Policy[T any] = auth.Policy[T]

// An alias of an instantiation names no single symbol.
type StringPolicy = auth.Policy[string]

// An alias of a pointer names no single symbol either.
type GrantPointer = *auth.Grant

// A defined type over the component's is the framework's own.
type Role auth.Action

// A value and a constant that only point.
var ErrForbidden = auth.ErrForbidden

const MaxSignInFailures = auth.MaxSignInFailures

// A value computed here, a value with a declared type, and an iota.
var ErrLocal = errors.New("local")

var ErrTyped error = auth.ErrForbidden

const (
	First = iota
	Second
)

// A forward, generic, with the type arguments spelled.
func Authorize[T any](ctx context.Context, p Policy[T], s Subject, a Action, resource T) (Grant, error) {
	return auth.Authorize[T](ctx, p, s, a, resource)
}

// A forward under another name.
func HashPassword(password string) (string, error) { return hashing.Make(password) }

// A forward with no result.
func Forget(ctx context.Context, s Subject) { auth.Forget(ctx, s) }

// A variadic forward, and one that drops the ellipsis.
func Any(actions ...Action) Action { return auth.Any(actions...) }

func First1(actions ...Action) Action { return auth.First(actions) }

// Arguments reordered, an argument added, and a body of two statements.
func Swap(a, b string) string { return auth.Swap(b, a) }

func Background(s Subject) Grant { return auth.For(context.Background(), s) }

func Twice(s Subject) Grant {
	_ = s
	return auth.For(nil, s)
}

// A forward whose signature names a type the framework declares: an
// envelope, whatever its body.
func Wrap(store *SessionStore) error { return hsession.Check(store) }

// A forward into a package a project cannot import.
func Reveal() string { return secret.Reveal() }

// A forward to an unexported function of this package.
func Hidden() string { return hidden() }

func hidden() string { return "" }

// A forward to an exported function of this package that is itself an alias.
func Allowed(ctx context.Context, s Subject) { Forget(ctx, s) }

// SessionStore is an envelope.
type SessionStore struct {
	inner *hsession.RecordStore[Subject]
}

// Methods are part of their type and are never listed on their own.
func (s *SessionStore) Load() error { return nil }

type unexported = auth.Grant

// An alias of an unexported name of this package is the framework's own.
type Exposed = unexported
