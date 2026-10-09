// Package http is the slice of the framework's request bridge this fixture
// imports.
//
// This package is a bridge. It is removed in v1.0.0; import github.com/arandu-io/hesape/http directly.
package http

import (
	hhttp "github.com/arandu-io/hesape/http"
	"github.com/arandu-io/hesape/routing"
)

type Context = hhttp.Context

// Router is an envelope over hesape/routing.Router, with the renderer and the
// flash the hesape router deliberately does not hold.
type Router struct {
	inner *routing.Router
}
