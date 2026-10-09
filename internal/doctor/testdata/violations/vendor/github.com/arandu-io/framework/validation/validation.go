// Package validation is the slice of the framework's validation bridge this
// fixture imports.
//
// This package is a bridge. It is removed in v1.0.0; import github.com/arandu-io/hesape/validation directly.
package validation

import "github.com/arandu-io/hesape/validation"

type Rules = validation.Rules

var MustCompile = validation.MustCompile
