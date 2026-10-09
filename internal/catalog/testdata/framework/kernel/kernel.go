// Package kernel is a fixture of a bridge into another package of the same
// module.
//
// This package is a bridge. It is removed in v1.0.0; import github.com/arandu-io/framework/foundation directly.
package kernel

import "github.com/arandu-io/framework/foundation"

type Kernel = foundation.Application

type Bootable = foundation.Bootable

func New(name string) *Kernel { return foundation.New(name) }
