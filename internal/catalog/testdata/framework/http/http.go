// Package http is a fixture of a bridge that is mostly an envelope.
//
// This package is a bridge. It is removed in v1.0.0; import github.com/arandu-io/hesape/http directly.
package http

import (
	hhttp "github.com/arandu-io/hesape/http"
	"github.com/arandu-io/hesape/routing"
)

type Context = hhttp.Context

// Router is an envelope over the component's router.
type Router struct {
	inner *routing.Router
}

func NewRouter() *Router { return &Router{inner: routing.NewRouter()} }
