// Package jobs is the slice of the framework's queue bridge this fixture
// imports. Both names are the framework's own.
//
// This package is a bridge. It is removed in v1.0.0; import github.com/arandu-io/hesape/queue directly.
package jobs

import (
	"github.com/arandu-io/hesape/auth"
	hjobs "github.com/arandu-io/hesape/queue/jobs"
)

// Job envelops the hesape job with the payload decoding the framework adds.
type Job struct {
	inner *hjobs.Job
}

// GrantFor rebuilds the Grant a job was pushed under, and refuses one whose
// tenant is empty before it calls through.
func GrantFor(j *Job) (auth.Grant, error) {
	if j == nil {
		return auth.Grant{}, nil
	}
	return hjobs.GrantFor(j.inner)
}
