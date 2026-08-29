package doctor_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/arandu-io/aru/internal/doctor"
)

var updateFindingsGolden = flag.Bool("update-findings-golden", false, "rewrite the Doctor findings compatibility golden")

func TestDoctorRunFindingsMatchTheIndependentCompatibilityGolden(t *testing.T) {
	root := fixture(t, "violations")
	findings, err := doctor.Run(root, doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	type goldenFinding struct {
		Rule     string `json:"rule"`
		Severity string `json:"severity"`
		File     string `json:"file"`
		Line     int    `json:"line"`
		Message  string `json:"message"`
		Why      string `json:"why"`
	}
	stable := make([]goldenFinding, len(findings))
	for i, finding := range findings {
		stable[i] = goldenFinding{
			Rule: finding.Rule, Severity: finding.Severity.String(),
			File: finding.File, Line: finding.Line,
			Message: finding.Message, Why: finding.Why,
		}
	}
	got, err := json.MarshalIndent(stable, "", "  ")
	if err != nil {
		t.Fatalf("encode findings: %v", err)
	}
	got = append(got, '\n')
	path := filepath.Join(root, "findings.golden.json")
	if *updateFindingsGolden {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("update findings golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read findings compatibility golden: %v; run go test ./tests/Unit/doctor -run TestDoctorRunFindingsMatchTheIndependentCompatibilityGolden -update-findings-golden", err)
	}
	if !bytes.Equal(got, want) {
		t.Error("Doctor Run findings changed from the compatibility golden; review the finding diff before updating it")
	}
}

func TestAnalyzePreservesRunFindingsAndBuildsADeterministicV1Graph(t *testing.T) {
	root := fixture(t, "violations")
	wantFindings, err := doctor.Run(root, doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	first, err := doctor.Analyze(root, doctor.Conventional)
	if err != nil {
		t.Fatalf("Analyze first run: %v", err)
	}
	second, err := doctor.Analyze(root, doctor.Conventional)
	if err != nil {
		t.Fatalf("Analyze second run: %v", err)
	}

	if !reflect.DeepEqual(first.Findings, wantFindings) {
		t.Fatalf("Analyze findings differ from Run\nAnalyze: %#v\nRun: %#v", first.Findings, wantFindings)
	}
	if !reflect.DeepEqual(first.Graph, second.Graph) {
		t.Fatalf("two graph builds differ\nfirst: %#v\nsecond: %#v", first.Graph, second.Graph)
	}
	if first.Graph.SchemaVersion != 1 {
		t.Fatalf("schema version = %d, want 1", first.Graph.SchemaVersion)
	}

	wantGroups := []struct {
		id    string
		label string
	}{
		{"application-features", "Application Features"},
		{"http", "HTTP"},
		{"database", "Database"},
		{"views", "Views"},
		{"async", "Async"},
		{"console", "Console"},
		{"native-capabilities", "Native Capabilities"},
		{"community-modules", "Community Modules"},
		{"diagnostics", "Diagnostics"},
	}
	if len(first.Graph.Groups) != len(wantGroups) {
		t.Fatalf("group count = %d, want exactly %d", len(first.Graph.Groups), len(wantGroups))
	}
	for i, want := range wantGroups {
		got := first.Graph.Groups[i]
		if got.ID != want.id || got.Label != want.label {
			t.Errorf("group %d = %q/%q, want %q/%q", i, got.ID, got.Label, want.id, want.label)
		}
		if !sortedStrings(got.NodeIDs) {
			t.Errorf("group %q node IDs are not sorted: %v", got.ID, got.NodeIDs)
		}
	}
	if !sortedNodes(first.Graph.Nodes) {
		t.Errorf("graph nodes are not sorted by ID: %#v", first.Graph.Nodes)
	}
	if !sortedEdges(first.Graph.Edges) {
		t.Errorf("graph edges are not sorted: %#v", first.Graph.Edges)
	}

	diagnostics := first.Graph.Groups[len(first.Graph.Groups)-1]
	if len(diagnostics.NodeIDs) != len(first.Findings) {
		t.Fatalf("diagnostic nodes = %d, findings = %d", len(diagnostics.NodeIDs), len(first.Findings))
	}
	nodes := make(map[string]doctor.Node, len(first.Graph.Nodes))
	for _, node := range first.Graph.Nodes {
		nodes[node.ID] = node
	}
	for _, id := range diagnostics.NodeIDs {
		node, ok := nodes[id]
		if !ok {
			t.Errorf("diagnostics references missing node %q", id)
			continue
		}
		if node.Kind != "diagnostic" || node.Level == "" || node.File == "" || node.Line < 1 || node.Column < 1 {
			t.Errorf("diagnostic node %q is incomplete: %#v", id, node)
		}
	}
}

func TestProjectGraphClassifiesArtifactsNativeCapabilitiesAndRegisteredCommunityModules(t *testing.T) {
	root := writeGraphFixture(t)
	analysis, err := doctor.Analyze(root, doctor.Conventional)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	nodes := make(map[string]doctor.Node, len(analysis.Graph.Nodes))
	for _, node := range analysis.Graph.Nodes {
		nodes[node.ID] = node
	}
	groups := make(map[string]doctor.Group, len(analysis.Graph.Groups))
	for _, group := range analysis.Graph.Groups {
		groups[group.ID] = group
	}
	kinds := func(groupID string) map[string]bool {
		out := map[string]bool{}
		for _, id := range groups[groupID].NodeIDs {
			out[nodes[id].Kind] = true
		}
		return out
	}
	for groupID, wantKinds := range map[string][]string{
		"application-features": {"feature", "policy", "service"},
		"http":                 {"controller", "request", "route"},
		"database":             {"migration", "model", "seeder"},
		"views":                {"view"},
		"async":                {"event", "job"},
		"console":              {"command", "console-route"},
	} {
		got := kinds(groupID)
		for _, want := range wantKinds {
			if !got[want] {
				t.Errorf("group %q kinds = %v, missing %q", groupID, got, want)
			}
		}
	}

	var native []string
	for _, id := range groups["native-capabilities"].NodeIDs {
		native = append(native, nodes[id].Label)
	}
	sort.Strings(native)
	if want := []string{"database", "queue", "view"}; !reflect.DeepEqual(native, want) {
		t.Errorf("native capabilities = %v, want %v", native, want)
	}

	var community []string
	for _, id := range groups["community-modules"].NodeIDs {
		community = append(community, nodes[id].Label)
	}
	if want := []string{"example.org/community/audit"}; !reflect.DeepEqual(community, want) {
		t.Errorf("community modules = %v, want only directly registered external module %v", community, want)
	}
	for _, module := range community {
		if module == "github.com/arandu-io/framework/modules/auth" {
			t.Fatal("the legacy first-party auth package was classified as a community module")
		}
	}

	contains := 0
	for _, edge := range analysis.Graph.Edges {
		if edge.Kind != "contains" {
			t.Errorf("edge %#v has a v1 kind other than contains", edge)
		}
		from, fromOK := nodes[edge.From]
		if !fromOK || from.Kind != "feature" {
			t.Errorf("edge %#v does not start at a feature", edge)
		}
		if _, toOK := nodes[edge.To]; !toOK {
			t.Errorf("edge %#v ends at a missing node", edge)
		}
		contains++
	}
	if contains == 0 {
		t.Fatal("the Invoice feature contains no artifacts")
	}

	for _, node := range analysis.Graph.Nodes {
		if node.Kind == "native-capability" || node.Kind == "community-module" || node.Kind == "diagnostic" {
			continue
		}
		if node.File == "" || node.Line < 1 || node.Column < 1 {
			t.Errorf("artifact %q has no source location: %#v", node.ID, node)
		}
	}
}

func TestCommunityModulesComeOnlyFromProvenKernelRegisterCalls(t *testing.T) {
	root := writeGraphFixture(t)
	path := filepath.Join(root, "bootstrap", "metrics.go")
	contents := `package bootstrap

import (
	collectors "example.org/metrics/collectors"
	prometheus "example.org/metrics/registry"
	search "example.org/search/module"
	"github.com/arandu-io/framework/kernel"
)

func RegisterMetrics() {
	prometheus.Register(collectors.NewModule())

	k := kernel.New()
	k.Register(search.NewModule())
}
`
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write non-kernel registry fixture: %v", err)
	}

	analysis, err := doctor.Analyze(root, doctor.Conventional)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	var community []string
	communityIDs := map[string]bool{}
	for _, group := range analysis.Graph.Groups {
		if group.ID == "community-modules" {
			for _, id := range group.NodeIDs {
				communityIDs[id] = true
			}
		}
	}
	for _, node := range analysis.Graph.Nodes {
		if communityIDs[node.ID] {
			community = append(community, node.Label)
		}
	}
	sort.Strings(community)
	if want := []string{"example.org/community/audit", "example.org/search/module"}; !reflect.DeepEqual(community, want) {
		t.Errorf("community modules = %v, want only modules registered on proven Kernel receivers: %v", community, want)
	}
}

func TestCommunityModulesFailClosedWhenTheProjectModuleIdentityIsUnknown(t *testing.T) {
	root := writeGraphFixture(t)
	if err := os.Remove(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("remove module identity: %v", err)
	}

	analysis, err := doctor.Analyze(root, doctor.Conventional)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	for _, group := range analysis.Graph.Groups {
		if group.ID == "community-modules" && len(group.NodeIDs) != 0 {
			t.Errorf("unknown module identity produced community module IDs %v", group.NodeIDs)
		}
	}
}

func TestDuplicateImportAliasesResolveInSourceOrderDuringIntermediateEdits(t *testing.T) {
	root := writeGraphFixture(t)
	contents := `package bootstrap

import (
	"github.com/arandu-io/framework/kernel"
	module "example.org/first"
	module "example.org/second"
	module "example.org/third"
	module "example.org/fourth"
	module "example.org/fifth"
	module "example.org/sixth"
	module "example.org/seventh"
	module "example.org/eighth"
)

func Boot(k *kernel.Kernel) {
	k.Register(module.NewModule())
}
`
	if err := os.WriteFile(filepath.Join(root, "bootstrap", "app.go"), []byte(contents), 0o644); err != nil {
		t.Fatalf("write duplicate-alias fixture: %v", err)
	}

	for attempt := 0; attempt < 32; attempt++ {
		analysis, err := doctor.Analyze(root, doctor.Conventional)
		if err != nil {
			t.Fatalf("Analyze attempt %d: %v", attempt, err)
		}
		var labels []string
		communityIDs := map[string]bool{}
		for _, group := range analysis.Graph.Groups {
			if group.ID == "community-modules" {
				for _, id := range group.NodeIDs {
					communityIDs[id] = true
				}
			}
		}
		for _, node := range analysis.Graph.Nodes {
			if communityIDs[node.ID] {
				labels = append(labels, node.Label)
			}
		}
		if want := []string{"example.org/first"}; !reflect.DeepEqual(labels, want) {
			t.Fatalf("attempt %d resolved duplicate alias to %v, want first AST import %v", attempt, labels, want)
		}
	}
}

func TestProjectGraphIDsDoNotCollapseDistinctPathsWithTheSameSlug(t *testing.T) {
	root := writeGraphFixture(t)
	files := map[string]string{
		"app/Services/A-B.go": `package services

type HyphenatedService struct{}
`,
		"app/Services/A/B.go": `package a

type NestedService struct{}
`,
	}
	for name, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create collision fixture directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("write collision fixture %s: %v", name, err)
		}
	}

	analysis, err := doctor.Analyze(root, doctor.Conventional)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	wantFiles := map[string]bool{
		"app/Services/A-B.go": false,
		"app/Services/A/B.go": false,
	}
	ids := map[string]bool{}
	for _, node := range analysis.Graph.Nodes {
		if node.Kind != "service" {
			continue
		}
		if _, wanted := wantFiles[node.File]; !wanted {
			continue
		}
		wantFiles[node.File] = true
		if ids[node.ID] {
			t.Errorf("distinct service paths share node ID %q", node.ID)
		}
		ids[node.ID] = true
	}
	for file, found := range wantFiles {
		if !found {
			t.Errorf("graph silently dropped colliding path %q", file)
		}
	}
}

func writeGraphFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod": `module example.test/project

go 1.26
`,
		"arandu.mod.toml": `name = "example/project"
framework = ">= 0.3"
profiles = ["conventional"]

[permissions]
network = false
filesystem = false
exec = false
migrations = true
`,
		"app/Models/Invoice.go": `package models

import "github.com/arandu-io/hesape/database/model"

type Invoice struct { model.Base }
`,
		"app/Policies/InvoicePolicy.go": `package policies

type InvoicePolicy struct{}
`,
		"app/Services/InvoiceService.go": `package services

type InvoiceService struct{}
`,
		"app/Http/Controllers/InvoiceController.go": `package controllers

import "github.com/arandu-io/hesape/view/components"

type InvoiceController struct { Button components.ButtonProps }
`,
		"app/Http/Requests/InvoiceRequest.go": `package requests

type InvoiceRequest struct{}
`,
		"app/Jobs/SendInvoiceJob.go": `package jobs

import "github.com/arandu-io/hesape/queue/jobs"

type SendInvoiceJob struct { Payload jobs.Job }
`,
		"app/Events/InvoicePaidEvent.go": `package events

type InvoicePaidEvent struct{}
`,
		"app/Console/Commands/CloseInvoicesCommand.go": `package commands

type CloseInvoicesCommand struct{}
`,
		"database/migrations/create_invoices.go": `package migrations

type CreateInvoicesTable struct{}
`,
		"database/seeders/InvoiceSeeder.go": `package seeders

type InvoiceSeeder struct{}
`,
		"routes/web.go": `package routes

func Web() {}
`,
		"routes/console.go": `package routes

func Console() {}
`,
		"resources/views/invoices/index.kyse.go": `//go:build kyse

package invoices

<h1>Invoices</h1>
`,
		"bootstrap/app.go": `package bootstrap

import (
	"github.com/arandu-io/framework/kernel"
	legacy "github.com/arandu-io/framework/modules/auth"
	local "example.test/project/modules/local"
	audit "example.org/community/audit"
)

func Boot(k *kernel.Kernel) {
	k.Register(
		audit.NewModule(),
		legacy.NewModule(),
		local.NewModule(),
	)
}
`,
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

func sortedStrings(values []string) bool {
	for i := 1; i < len(values); i++ {
		if values[i-1] > values[i] {
			return false
		}
	}
	return true
}

func sortedNodes(nodes []doctor.Node) bool {
	for i := 1; i < len(nodes); i++ {
		if nodes[i-1].ID > nodes[i].ID {
			return false
		}
	}
	return true
}

func sortedEdges(edges []doctor.Edge) bool {
	for i := 1; i < len(edges); i++ {
		previous, current := edges[i-1], edges[i]
		if previous.From > current.From ||
			previous.From == current.From && previous.To > current.To ||
			previous.From == current.From && previous.To == current.To && previous.Kind > current.Kind {
			return false
		}
	}
	return true
}
