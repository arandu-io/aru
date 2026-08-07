package services

import (
	"github.com/arandu-io/framework/jobs"
	"github.com/arandu-io/framework/security"
)

// escalateThroughTheWrapper reaches SystemGrant without naming it.
//
// jobs.GrantFor exists so a worker can reissue exactly what the push
// authorized, and it does that by reading the action and the tenant off a Job.
// Job is an ordinary exported struct, so anywhere can build one -- and then the
// wrapper hands back a Grant for an action and a tenant nobody authorized.
//
// The doctor matched the literal name `security.SystemGrant`, so this produced
// no finding at all: not out-of-scope, not tenant-from-request, nothing. Found
// by adversarial review on 07/08/2026, reproduced in a generated project before
// this fixture existed.
func escalateThroughTheWrapper(org string) security.Grant {
	return jobs.GrantFor(jobs.Job{Action: "customer.delete", TenantID: org})
}

// emptyTenantThroughTheWrapper is the same blind spot on the other rule: an
// empty tenant is refused at run time, and saying so at build time is the whole
// point of checking it here.
func emptyTenantThroughTheWrapper() security.Grant {
	return jobs.GrantFor(jobs.Job{Action: "customer.view", TenantID: ""})
}
