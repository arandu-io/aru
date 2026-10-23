package doctor_test

import (
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/doctor"
)

// policyHead is a policy over one entity with no Writes, as a policy written
// before the declaration existed reads.
const policyHead = `package policies

import (
	"context"
	"errors"

	"github.com/arandu-io/hesape/auth"

	models "example.test/shape/app/Models"
)

const (
	InvoiceView   auth.Action = "invoice.view"
	InvoiceUpdate auth.Action = "invoice.update"
)

type InvoicePolicy struct{}

func (InvoicePolicy) Can(ctx context.Context, s auth.Subject, a auth.Action, x models.Invoice) error {
	return errors.New("no")
}
`

const invoiceModel = "package models\n\ntype Invoice struct{ ID string }\n"

// TestAPolicyThatDeclaresNoWritesIsReported: a policy over an entity without
// Writes issues Grants that write whatever the action was, and the warning
// says so on the line of Can. The near misses stay quiet: the method in
// another file of the package, and a Can that is not a policy's decision.
func TestAPolicyThatDeclaresNoWritesIsReported(t *testing.T) {
	writes := "\nfunc (InvoicePolicy) Writes(a auth.Action) bool { return a == InvoiceUpdate }\n"
	for _, c := range []struct {
		name  string
		files map[string]string
		want  int
	}{
		{"a policy with Can and no Writes", map[string]string{
			"app/Models/Invoice.go":         invoiceModel,
			"app/Policies/InvoicePolicy.go": policyHead,
		}, 1},
		{"Writes beside Can", map[string]string{
			"app/Models/Invoice.go":         invoiceModel,
			"app/Policies/InvoicePolicy.go": policyHead + writes,
		}, 0},
		{"Writes in another file of the package", map[string]string{
			"app/Models/Invoice.go":         invoiceModel,
			"app/Policies/InvoicePolicy.go": policyHead,
			"app/Policies/InvoiceWrites.go": "package policies\n\nimport \"github.com/arandu-io/hesape/auth\"\n" + writes,
		}, 0},
		{"Writes declared on another type", map[string]string{
			"app/Models/Invoice.go":         invoiceModel,
			"app/Policies/InvoicePolicy.go": policyHead + "\ntype ReportPolicy struct{}\n\nfunc (ReportPolicy) Writes(a auth.Action) bool { return false }\n",
		}, 1},
		{"a Can that is not a policy's decision", map[string]string{
			"app/Policies/Gate.go": "package policies\n\nimport \"context\"\n\ntype Gate struct{}\n\nfunc (Gate) Can(ctx context.Context, name string) error { return nil }\n",
		}, 0},
		{"a policy declared in a test", map[string]string{
			"app/Models/Invoice.go":                invoiceModel,
			"tests/Unit/InvoicePolicy_test.go":     strings.Replace(policyHead, "package policies", "package unit_test", 1),
			"app/Policies/InvoicePolicyActions.go": "package policies\n",
		}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := findingsOf(t, structureProject(t, c.files), "policy-declares-no-writes")
			if len(got) != c.want {
				var lines []string
				for _, f := range got {
					lines = append(lines, f.String())
				}
				t.Fatalf("%d finding(s), want %d:\n%s", len(got), c.want, strings.Join(lines, "\n"))
			}
			for _, f := range got {
				if f.Severity != doctor.Warning || f.File != "app/Policies/InvoicePolicy.go" || f.Line != 19 {
					t.Errorf("reported as %s at %s:%d, want a warning on the line of Can, app/Policies/InvoicePolicy.go:19", f.Severity, f.File, f.Line)
				}
				if !strings.Contains(f.Why, "Writes(a auth.Action) bool") {
					t.Errorf("the correction does not name the method to add: %s", f.Why)
				}
			}
		})
	}
}
