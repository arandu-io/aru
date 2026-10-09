package doctor

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// RuleInfo is one name a finding can carry, and how it is reported.
type RuleInfo struct {
	// Name is what a finding prints between brackets.
	Name string
	// Severities are the levels the rule reports at, error first. Most rules
	// have one; a rule with two decides by the case it found.
	Severities []Severity
	// Profile is the only profile the rule runs on, or empty when it runs on
	// every one.
	Profile Profile
}

// Severity says what the rule reports at, in the words a report uses: "error",
// "warning", or "error or warning".
func (r RuleInfo) Severity() string {
	words := make([]string, 0, len(r.Severities))
	for _, s := range r.Severities {
		words = append(words, s.String())
	}
	return strings.Join(words, " or ")
}

// List answers every rule the doctor checks, in the order the rules run.
//
// It is written out rather than collected from the findings, because a rule
// that fires on nothing in a given project still exists, and a list read off a
// report would leave it out. The package's tests read every finding the source
// can construct and fail when a name or a severity here is not the one the
// code reports.
func List() []RuleInfo {
	out := make([]RuleInfo, len(catalogue))
	for i, r := range catalogue {
		r.Severities = append([]Severity(nil), r.Severities...)
		out[i] = r
	}
	return out
}

// WriteList writes List as a table: the name, the severity, and the profile a
// rule is limited to.
func WriteList(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, r := range catalogue {
		only := ""
		if r.Profile != "" {
			only = "--profile=" + string(r.Profile) + " only"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Name, r.Severity(), only)
	}
	return tw.Flush()
}

var (
	errorOnly   = []Severity{Error}
	warningOnly = []Severity{Warning}
	either      = []Severity{Error, Warning}
)

// catalogue is List's answer, in the order of the rules slice and, within one
// rule, in the order its function reports them.
var catalogue = []RuleInfo{
	{Name: "file-does-not-parse", Severities: errorOnly},
	{Name: "repository-without-policy", Severities: errorOnly},
	{Name: "grant-not-received", Severities: errorOnly},
	{Name: "grant-not-checked", Severities: errorOnly},
	{Name: "grant-check-discarded", Severities: errorOnly},
	{Name: "policy-never-opened", Severities: warningOnly},
	{Name: "action-not-a-constant", Severities: errorOnly},
	{Name: "enum-rule-not-derived", Severities: either},
	{Name: "handler-reaches-data", Severities: errorOnly},
	{Name: "handler-reaches-the-model", Severities: errorOnly},
	{Name: "controller-reaches-repository", Severities: errorOnly},
	{Name: "tenant-from-request", Severities: errorOnly},
	{Name: "tenant-from-header", Severities: errorOnly},
	{Name: "system-grant-without-tenant", Severities: errorOnly},
	{Name: "system-grant-outside-scope", Severities: warningOnly},
	{Name: "sql-built-with-sprintf", Severities: errorOnly},
	{Name: "sql-built-by-concatenation", Severities: errorOnly},
	{Name: "sensitive-field-not-redacted", Severities: warningOnly},
	{Name: "session-not-rotated", Severities: errorOnly},
	{Name: "csrf-exempt-without-signature", Severities: warningOnly},
	{Name: "view-data-is-a-map", Severities: errorOnly},
	{Name: "view-does-not-exist", Severities: errorOnly},
	{Name: "permission-not-declared", Severities: errorOnly},
	{Name: "permission-not-used", Severities: warningOnly},
	{Name: "view-keeps-state-in-the-browser", Severities: errorOnly},
	{Name: "sql-without-tenant-scope", Severities: errorOnly},
	{Name: "outbox-not-registered", Severities: errorOnly},
	{Name: "resource-not-reauthorized", Severities: warningOnly},
	{Name: "raw-output-is-not-a-component", Severities: warningOnly},
	{Name: "retired-module", Severities: warningOnly},
	{Name: "import-not-canonical", Severities: warningOnly},
	{Name: "test-is-not-run", Severities: warningOnly},
	{Name: "test-outside-the-tests-tree", Severities: warningOnly},
	{Name: "package-clause-is-capitalised", Severities: warningOnly},
	{Name: "scaffolding-ships", Severities: warningOnly},
	{Name: "skills-out-of-date", Severities: warningOnly},
	{Name: "skills-missing", Severities: warningOnly},
	{Name: "migrations-not-linked", Severities: warningOnly},
	{Name: "added-column-not-nullable", Severities: warningOnly},
	{Name: "rollback-does-nothing", Severities: warningOnly},
	{Name: "driver-not-linked", Severities: warningOnly},
	{Name: "profile-not-declared", Severities: warningOnly, Profile: Performance},
	{Name: "join-across-aggregates", Severities: errorOnly, Profile: Performance},
	{Name: "transaction-across-aggregates", Severities: errorOnly, Profile: Performance},
	{Name: "model-query-stale", Severities: errorOnly},
	{Name: "model-core-outside-models", Severities: errorOnly},
	{Name: "input-read-by-hand", Severities: warningOnly},
	{Name: "validate-called-by-controller", Severities: warningOnly},
	{Name: "json-written-by-hand", Severities: warningOnly},
	{Name: "invalid-form-answered-by-hand", Severities: warningOnly},
	{Name: "session-loaded-in-controller", Severities: warningOnly},
	{Name: "redirect-to-literal-path", Severities: warningOnly},
	{Name: "html-template-in-app", Severities: warningOnly},
	{Name: "service-takes-http", Severities: warningOnly},
	{Name: "service-subpackage", Severities: warningOnly},
	{Name: "service-file-too-large", Severities: warningOnly},
	{Name: "controller-too-many-actions", Severities: warningOnly},
	{Name: "operation-chosen-by-form-field", Severities: warningOnly},
	{Name: "client-outside-clients", Severities: warningOnly},
	{Name: "model-rule-touches-io", Severities: warningOnly},
	{Name: "fragment-without-partial", Severities: warningOnly},
	{Name: "helper-reimplemented", Severities: warningOnly},
	{Name: "raw-sql-outside-repository", Severities: warningOnly},
	{Name: "generated-not-wired", Severities: warningOnly},
	{Name: "subject-built-by-hand", Severities: warningOnly},
}
