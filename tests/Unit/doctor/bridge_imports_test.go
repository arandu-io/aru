package doctor_test

import (
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/doctor"
)

// importSpelling is one way a project reaches the framework: the import path
// of each package the twin project below uses, and the name the code calls it
// by.
type importSpelling struct {
	name                    string
	data, security, events  string
	dataPkg, securityPkg    string
	eventsPkg               string
	kernel, kernelPkg, kind string
}

// bridged is a project written against the framework's bridge packages, and
// direct the same project written against the packages they point at, each
// called by its own name -- the spelling an application has once it moved.
var (
	bridged = importSpelling{
		name:     "bridge",
		data:     "github.com/arandu-io/framework/data",
		security: "github.com/arandu-io/framework/security",
		events:   "github.com/arandu-io/framework/events",
		dataPkg:  "data", securityPkg: "security", eventsPkg: "events",
		kernel: "github.com/arandu-io/framework/kernel", kernelPkg: "kernel", kind: "Kernel",
	}
	direct = importSpelling{
		name:     "hesape",
		data:     "github.com/arandu-io/hesape/database",
		security: "github.com/arandu-io/hesape/auth",
		events:   "github.com/arandu-io/hesape/events",
		dataPkg:  "database", securityPkg: "auth", eventsPkg: "events",
		kernel: "github.com/arandu-io/framework/foundation", kernelPkg: "foundation", kind: "Application",
	}
)

// twinProject writes one project in the spelling given.
//
// Every import sits on a line of its own and is not aliased, and nothing after
// a package name shares a line with what a finding or a node points at, so the
// two spellings put every finding and every node on the same line and column.
// What differs between them is the import path and the name it is called by,
// which is exactly what the doctor must not care about.
func twinProject(t *testing.T, s importSpelling) string {
	t.Helper()
	root := t.TempDir()
	replace := strings.NewReplacer(
		"{{data}}", s.data, "{{security}}", s.security, "{{events}}", s.events,
		"{{dataPkg}}", s.dataPkg, "{{securityPkg}}", s.securityPkg, "{{eventsPkg}}", s.eventsPkg,
		"{{kernel}}", s.kernel, "{{kernelPkg}}", s.kernelPkg, "{{kind}}", s.kind,
	)
	for name, body := range map[string]string{
		"go.mod":  "module example.test/project\n\ngo 1.26\n",
		"main.go": "package main\n\nfunc main() {}\n",
		"bootstrap/app.go": `package bootstrap

import (
	"{{kernel}}"

	audit "example.org/community/audit"
	fleet "example.org/community/fleet"
)

// Boot registers one module on the application it is handed and one on the
// application it builds, which are the two shapes the wiring takes.
func Boot(app *{{kernelPkg}}.{{kind}}, cfg any) {
	app.Register(
		audit.NewModule(),
	)
	built := {{kernelPkg}}.New(cfg)
	built.Register(
		fleet.NewModule(),
	)
}
`,
		"app/Http/Controllers/ReportController.go": `package controllers

import (
	"context"

	"{{data}}"
	"{{security}}"
)

type ReportController struct {
	db *{{dataPkg}}.DB
}

// Export opens a transaction from a handler, which skipped the service.
func (c *ReportController) Export(ctx context.Context) error {
	return {{dataPkg}}.Transaction(ctx, c.db, func(context.Context) error {
		return nil
	})
}

// Audit lets the request choose the tenant of a Grant.
func (c *ReportController) Audit(ctx *Context) error {
	org := ctx.Query("org")
	g := {{securityPkg}}.SystemGrant("report.audit", org)
	_ = g
	return nil
}
`,
		"app/Services/InvoiceService.go": `package services

import (
	"context"

	"{{data}}"
	"{{events}}"
	"{{security}}"

	models "example.test/project/app/Models"
	policies "example.test/project/app/Policies"
	repositories "example.test/project/app/Repositories"
)

type InvoiceService struct {
	db       *{{dataPkg}}.DB
	invoices *repositories.InvoiceRepository
	payments *repositories.PaymentRepository
}

// Show reads one invoice after authorizing the action, and never authorizes
// the invoice it read.
func (s *InvoiceService) Show(ctx context.Context, actor {{securityPkg}}.Subject, id string) (models.Invoice, error) {
	g, err := {{securityPkg}}.Authorize(ctx, policies.InvoicePolicy{},
		actor, policies.InvoiceView, models.Invoice{})
	if err != nil {
		return models.Invoice{}, err
	}
	return s.invoices.Find(ctx, g, id)
}

// Settle writes two aggregates in one transaction.
func (s *InvoiceService) Settle(ctx context.Context, g {{securityPkg}}.Grant, id string) error {
	return {{dataPkg}}.Transaction(ctx, s.db, func(ctx context.Context) error {
		if err := s.invoices.Settle(ctx, g, id); err != nil {
			return err
		}
		return s.payments.Record(ctx, g, id)
	})
}

// Outbox stores events, and nothing in this project brings the table.
func (s *InvoiceService) Outbox() any {
	return {{eventsPkg}}.NewOutbox(s.db)
}
`,
	} {
		writeFile(t, filepath.Join(root, filepath.FromSlash(name)), replace.Replace(body))
	}
	return root
}

// TestABridgeImportAndTheImportItPointsAtReadTheSame runs one project twice:
// once importing the framework's bridge packages, and once importing what
// they point at, under the names those packages have.
//
// Several detectors matched one of the two. The rule against a handler
// reaching the data package knew framework/data and the identifier data, the
// re-authorization rule knew the spelling security.Authorize, the transaction
// rule knew data.Transaction, the graph found modules registered only on a
// kernel.Kernel, and it counted a native capability only for an import that
// already spelled hesape. A project half way through moving its imports was
// reported for one half and silently passed on the other, and a silent rule
// reads exactly like a clean project.
//
// So the assertion is equality, on both profiles, of the findings and of the
// graph. The list of what has to be there is checked as well, because two
// empty reports are also equal.
func TestABridgeImportAndTheImportItPointsAtReadTheSame(t *testing.T) {
	// An empty module cache, so the graph answers about what bootstrap
	// registers and not about what this machine happens to have downloaded.
	t.Setenv("GOMODCACHE", t.TempDir())

	for _, profile := range []doctor.Profile{doctor.Conventional, doctor.Performance} {
		t.Run(string(profile), func(t *testing.T) {
			analyses := map[string]doctor.Analysis{}
			for _, s := range []importSpelling{bridged, direct} {
				analysis, err := doctor.Analyze(twinProject(t, s), profile)
				if err != nil {
					t.Fatalf("%s: Analyze: %v", s.name, err)
				}
				analyses[s.name] = analysis

				rules := map[string]bool{}
				for _, f := range analysis.Findings {
					rules[f.Rule] = true
				}
				want := []string{
					"handler-reaches-data",
					"tenant-from-request",
					"resource-not-reauthorized",
					"outbox-not-registered",
				}
				if profile == doctor.Performance {
					want = append(want, "transaction-across-aggregates")
				}
				for _, rule := range want {
					if !rules[rule] {
						t.Errorf("%s spelling: %s did not fire, so this project no longer tests it:\n%s",
							s.name, rule, describe(analysis.Findings))
					}
				}

				if got, want := labelsOf(analysis.Graph, "native-capability"), []string{"auth", "database", "events"}; !reflect.DeepEqual(got, want) {
					t.Errorf("%s spelling: native capabilities = %v, want %v", s.name, got, want)
				}
				if got, want := labelsOf(analysis.Graph, "community-module"), []string{"example.org/community/audit", "example.org/community/fleet"}; !reflect.DeepEqual(got, want) {
					t.Errorf("%s spelling: community modules = %v, want %v", s.name, got, want)
				}
			}

			if a, b := analyses[bridged.name].Findings, analyses[direct.name].Findings; !reflect.DeepEqual(a, b) {
				t.Errorf("the two spellings report different findings\nbridge:\n%s\nhesape:\n%s", describe(a), describe(b))
			}
			if a, b := analyses[bridged.name].Graph, analyses[direct.name].Graph; !reflect.DeepEqual(a, b) {
				t.Errorf("the two spellings draw different graphs\nbridge: %#v\nhesape: %#v", a, b)
			}
		})
	}
}

// TestTheHesapeEventsModuleBringsNoOutboxTable pins the one place the two
// spellings must not agree.
//
// framework/events is a bridge everywhere except its Module, which it declares
// because hesape's carries no Migrations. Registering hesape's module runs a
// relay over a table nothing created, so the project that registers it is the
// project whose sign-up fails with `no such table: outbox`, and the doctor has
// to keep saying so.
func TestTheHesapeEventsModuleBringsNoOutboxTable(t *testing.T) {
	for _, tc := range []struct {
		module   string
		reported bool
	}{
		{"github.com/arandu-io/framework/events", false},
		{"github.com/arandu-io/hesape/events", true},
	} {
		t.Run(tc.module, func(t *testing.T) {
			root := twinProject(t, direct)
			writeFile(t, filepath.Join(root, "bootstrap", "events.go"), `package bootstrap

import (
	registered "`+tc.module+`"
	"github.com/arandu-io/framework/foundation"
)

func Events(app *foundation.Application) {
	app.Register(registered.NewModule())
}
`)
			findings, err := doctor.Run(root, doctor.Conventional)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := findRule(findings, "outbox-not-registered") != nil; got != tc.reported {
				t.Errorf("registering %s.NewModule: outbox-not-registered reported = %v, want %v\n%s",
					tc.module, got, tc.reported, describe(findings))
			}
		})
	}
}

// labelsOf answers the sorted labels of the graph's nodes of one kind.
func labelsOf(graph doctor.ProjectGraph, kind string) []string {
	var out []string
	for _, node := range graph.Nodes {
		if node.Kind == kind {
			out = append(out, node.Label)
		}
	}
	sort.Strings(out)
	return out
}

// describe renders findings one per line, for a failure message.
func describe(findings []doctor.Finding) string {
	var lines []string
	for _, f := range findings {
		lines = append(lines, f.File+":"+itoa(f.Line)+": ["+f.Rule+"] "+f.Message)
	}
	return strings.Join(lines, "\n")
}
