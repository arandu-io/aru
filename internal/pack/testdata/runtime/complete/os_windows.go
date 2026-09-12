//go:build windows

package app

// schemesURI sits behind a build constraint, as the real one does. The check
// reads it anyway: the flag that names it is only passed for Windows, and the
// packaging runs everywhere.
var schemesURI string
