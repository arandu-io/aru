package auth

import (
	"github.com/arandu-io/framework/http"
)

// Authenticator is what the controller signs a user in with.
type Authenticator interface {
	Authenticate(ctx *http.Context, email, password string) (string, error)
}

// LoginController is a violation: it authenticates and keeps the pre-login
// session id, which is session fixation.
type LoginController struct {
	auth Authenticator
}

// Login signs the user in.
func (c LoginController) Login(ctx *http.Context) error {
	if _, err := c.auth.Authenticate(ctx, ctx.Input("email"), ctx.Input("password")); err != nil {
		return err
	}
	return ctx.Redirect("/")
}
