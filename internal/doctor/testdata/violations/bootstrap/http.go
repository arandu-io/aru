// Package bootstrap wires the application.
package bootstrap

import "github.com/arandu-io/framework/http/middleware"

// Exempt is the CSRF exemption the pipeline is built with: the webhook paths,
// on the promise that every route under them checks who sent the request.
func Exempt() middleware.CSRFOption {
	return middleware.CSRFExcept("/webhooks/")
}
