package services

import (
	"github.com/arandu-io/framework/security"

	policies "example.test/gaps/app/Policies"
)

// issueGrant is a system grant on a request path, under a name the allowlist
// did not recognise.
func issueGrant() security.Grant {
	return security.SystemGrant(policies.ActionViewReport, "acme")
}

// ensureGrant is the same call, character for character, under a name that used
// to switch the check off: the allowlist matched the substring "ensure". A
// rename is not a security review.
func ensureGrant() security.Grant {
	return security.SystemGrant(policies.ActionViewReport, "acme")
}

// backfillTotals is a deliberate escape, and it says so on the line where it
// happens -- which is the only form of escape that survives code review.
func backfillTotals() security.Grant {
	//arandu:system-grant one-off backfill of reports.total, run from the console
	return security.SystemGrant(policies.ActionViewReport, "acme")
}
