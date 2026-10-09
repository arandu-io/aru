// Package foundation is a fixture of a package that is not a bridge.
package foundation

import hfoundation "github.com/arandu-io/hesape/foundation"

// Application is declared here.
type Application struct{}

type Bootable = hfoundation.Bootable

func New(name string) *Application { return &Application{} }
