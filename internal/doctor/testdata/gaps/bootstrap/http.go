// Package bootstrap wires the application.
package bootstrap

import "github.com/arandu-io/framework/http/middleware"

// Exempt is the CSRF exemption the pipeline is built with. "/hooks" has no
// trailing slash, so it exempts that one path and nothing below it.
func Exempt() middleware.CSRFOption {
	return middleware.CSRFExcept("/webhooks/", "/hooks")
}
