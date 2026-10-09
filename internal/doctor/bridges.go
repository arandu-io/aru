package doctor

import "strings"

// The import paths a rule below names when it asks whether a call reaches the
// framework. They are data rather than references: the strings an import
// spells, and the only way to tell this package's Transaction or Authorize from
// a function of the same name declared anywhere else.
const (
	frameworkData       = "github.com/arandu-io/framework/data"
	frameworkEvents     = "github.com/arandu-io/framework/events"
	frameworkKernel     = "github.com/arandu-io/framework/kernel"
	frameworkFoundation = "github.com/arandu-io/framework/foundation"
	hesapePrefix        = "github.com/arandu-io/hesape/"
)

// bridgeTargets maps each bridge package of the framework to the package it
// points at.
//
// A bridge is a package whose symbols are aliases and thin envelopes over
// another package's, kept so an application that imported the old path goes
// on compiling. A project reaches the same component through either path, and
// it does so file by file: an application half way through moving imports one
// in a controller and the other in a service. A rule or a detector that knew
// only one of the two went quiet on the other half of the project, and a quiet
// rule reads exactly like a clean one.
//
// Each entry is the one target the bridge's own package documentation names
// when it says what to import instead. framework/data also forwards Tenant to
// hesape/auth and Migration to hesape/database/migrations; those two are not
// entries here, and a rule that needs one of them names it where it asks.
//
// kernel and config point one directory across rather than into hesape. They
// are listed because a rule asking for the kernel has to accept what the
// kernel became, and they contribute no native capability for the same
// reason: what they point at is framework code.
//
// TestEveryBridgeIsMapped reads the framework's package documentation and
// fails when this list and the bridges it declares disagree.
var bridgeTargets = map[string]string{
	"github.com/arandu-io/framework/arandutest":              "github.com/arandu-io/hesape/arandutest",
	"github.com/arandu-io/framework/config":                  "github.com/arandu-io/framework/foundation/bootstrap",
	frameworkData:                                            "github.com/arandu-io/hesape/database",
	frameworkEvents:                                          "github.com/arandu-io/hesape/events",
	"github.com/arandu-io/framework/http":                    "github.com/arandu-io/hesape/http",
	"github.com/arandu-io/framework/jobs":                    "github.com/arandu-io/hesape/queue",
	frameworkKernel:                                          frameworkFoundation,
	"github.com/arandu-io/framework/mail":                    "github.com/arandu-io/hesape/mail",
	"github.com/arandu-io/framework/observability":           "github.com/arandu-io/hesape/log",
	"github.com/arandu-io/framework/observability/errorpage": "github.com/arandu-io/hesape/exception",
	"github.com/arandu-io/framework/scheduler":               "github.com/arandu-io/hesape/console/scheduling",
	frameworkSecurity:                                        hesapeAuth,
	"github.com/arandu-io/framework/storage":                 "github.com/arandu-io/hesape/filesystem",
	frameworkValidation:                                      hesapeValidation,
	"github.com/arandu-io/framework/view":                    "github.com/arandu-io/hesape/view",
}

// reaches reports whether an import path is the bridge, or the package the
// bridge points at. The empty path, which is what a lookup of an unknown alias
// answers, reaches nothing.
func reaches(path, bridge string) bool {
	if path == "" {
		return false
	}
	return path == bridge || path == bridgeTargets[bridge]
}

// nativeComponent answers the hesape component an import path reaches, and
// false when it reaches none: hesape/database/model is database, and so is
// framework/data, which is a bridge to it.
func nativeComponent(path string) (string, bool) {
	if target, bridged := bridgeTargets[path]; bridged {
		path = target
	}
	rest, native := strings.CutPrefix(path, hesapePrefix)
	if !native {
		return "", false
	}
	component, _, _ := strings.Cut(rest, "/")
	return component, component != ""
}

// callsInto reports whether called -- a call's name as callName renders it --
// is the function fn of the bridge, or of the package the bridge points at,
// through this file's imports.
//
// The import decides and the spelling does not: hesape/auth arrives as auth,
// an application may alias framework/security as fsecurity, and a local value
// that happens to be called security is not the package.
func (f *file) callsInto(called, bridge, fn string) bool {
	imported, ok := f.importedFunction(called)
	return ok && imported.name == fn && reaches(imported.pkg, bridge)
}
