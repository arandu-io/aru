package services

import (
	"context"
	"net/http"

	"github.com/arandu-io/hesape/auth"
)

// CheckoutClosedError is a domain error: naming its status through net/http is
// how a domain error says what the router answers, and it is not reported.
type CheckoutClosedError struct{}

func (CheckoutClosedError) Error() string { return "checkout: closed" }

// HTTPStatus is the status the router maps this error to.
func (CheckoutClosedError) HTTPStatus() int { return http.StatusConflict }

// CheckoutService takes the request itself, so only a controller can call it.
type CheckoutService struct{}

// Start reads what it needs off the request instead of a request struct.
func (s *CheckoutService) Start(ctx context.Context, r *http.Request, actor auth.Subject) error {
	_ = r.URL
	return nil
}

// systemActor gives itself a role nobody stored and nobody can revoke.
func systemActor(tenant string) auth.Subject {
	return auth.Subject{ID: "system:checkout", Tenant: tenant, Roles: []string{"admin"}}
}
