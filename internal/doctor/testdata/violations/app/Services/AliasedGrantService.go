package services

import (
	"github.com/arandu-io/framework/security"
	hjobs "github.com/arandu-io/hesape/queue/jobs"
)

// escalateBehindAnAlias is the same escalation as JobGrantService, written the
// way the rule could not see.
//
// Three things differ from that file and each one on its own was enough: the
// package is imported under a name the caller chose, so a rule matching the
// text `jobs.GrantFor` matches nothing; the function is a different module's,
// reached through a path the list did not name; and the Job goes in behind an
// ampersand, because this signature takes a pointer.
//
// What comes back is a Grant for an action and a tenant nobody authorized, and
// every Policy downstream says yes because the Grant looks legitimate.
func escalateBehindAnAlias(org string) security.Grant {
	return hjobs.GrantFor(&hjobs.Job{Action: "customer.delete", TenantID: org})
}

// emptyTenantBehindAnAlias is the same blind spot on the other rule.
func emptyTenantBehindAnAlias() security.Grant {
	return hjobs.GrantFor(&hjobs.Job{Action: "customer.view", TenantID: ""})
}
