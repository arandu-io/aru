package contract_test

import (
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/contract"
	"github.com/arandu-io/aru/internal/doctor"
	"github.com/arandu-io/aru/tests"
)

// TestEveryRuleACardNamesIsADoctorRule: a card that says a rule verifies it
// names a rule `aru doctor --list` prints. A renamed rule would otherwise
// leave the card promising a check nothing runs.
func TestEveryRuleACardNamesIsADoctorRule(t *testing.T) {
	known := map[string]bool{}
	for _, r := range doctor.List() {
		known[r.Name] = true
	}
	for _, rule := range contract.Rules() {
		if !known[rule] {
			t.Errorf("a card names %q, which the doctor does not check", rule)
		}
	}
}

// TestTheRecipesTheContractDecidedAreThere: the six recipes `aru mcp`
// promises by name, each touching at least one card and saying how in steps.
func TestTheRecipesTheContractDecidedAreThere(t *testing.T) {
	for _, name := range []string{"crud", "action", "nested", "job", "webhook", "integration"} {
		r, ok := contract.RecipeNamed(name)
		if !ok {
			t.Errorf("no recipe %q", name)
			continue
		}
		if len(r.Steps) == 0 || len(r.Cards) == 0 {
			t.Errorf("recipe %q has no steps or touches no card: %+v", name, r)
		}
	}
}

// TestEveryCardSaysWhereAndHow: a card without a path or a signature is a
// card that cannot answer where_does_it_go, and an import that is not
// canonical teaches the path the doctor reports.
func TestEveryCardSaysWhereAndHow(t *testing.T) {
	for _, c := range contract.Cards() {
		if c.Path == "" || c.Signature == "" || c.Title == "" {
			t.Errorf("card %q lacks a path, a signature or a title", c.Kind)
		}
		for _, imp := range c.Imports {
			if strings.HasPrefix(imp.Path, "github.com/arandu-io/framework/security") ||
				strings.HasPrefix(imp.Path, "github.com/arandu-io/framework/data") ||
				strings.HasPrefix(imp.Path, "github.com/arandu-io/framework/validation") {
				t.Errorf("card %q imports %s, a bridge whose names live in hesape", c.Kind, imp.Path)
			}
		}
	}
}

// TestAFindingCarriesTheCardItsRuleVerifies: the doctor reads the contract,
// so a finding names the kind of code whose shape answers it.
func TestAFindingCarriesTheCardItsRuleVerifies(t *testing.T) {
	findings, err := doctor.Run(tests.Fixture(t, "doctor", "violations"), doctor.Conventional)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, f := range findings {
		seen[f.Rule] = f.Contract
	}
	for rule, kind := range map[string]string{
		"handler-reaches-data":          "controller",
		"sensitive-field-not-redacted":  "model",
		"client-outside-clients":        "client",
		"csrf-exempt-without-signature": "webhook",
	} {
		got, fired := seen[rule]
		if !fired {
			t.Errorf("%s did not fire on the violations fixture", rule)
			continue
		}
		if got != kind {
			t.Errorf("%s carries the card %q, want %q", rule, got, kind)
		}
	}
}

// TestTheWebhookRecipeStoresAnEventAndDispatchesNoJob: a received webhook is
// verified, stored as an event in the outbox and handled by a listener after
// the commit. The recipe used to have the service dispatch a job, and a job
// whose handler takes a service imports app/Services: the service importing
// app/Jobs back is an import cycle, so the recipe taught code that does not
// compile once any job takes a service.
func TestTheWebhookRecipeStoresAnEventAndDispatchesNoJob(t *testing.T) {
	r, ok := contract.RecipeNamed("webhook")
	if !ok {
		t.Fatal("no webhook recipe")
	}
	steps := strings.Join(r.Steps, "\n")
	for _, want := range []string{"webhook.Verify", "CSRFExcept", "aru make:event", "outbox.Store", "database.Transaction", "aru make:listener", "listeners.Each"} {
		if !strings.Contains(steps, want) {
			t.Errorf("the webhook recipe does not say %q:\n%s", want, steps)
		}
	}
	for _, step := range r.Steps {
		if strings.HasPrefix(step, "aru make:job") || strings.Contains(step, "dispatches a job") {
			t.Errorf("the webhook recipe has the service dispatch a job: %q", step)
		}
	}
	if strings.Join(r.Cards, ",") != "webhook,event,listener,service" {
		t.Errorf("the webhook recipe touches %v, want webhook, event, listener and service", r.Cards)
	}

	card, _ := contract.Lookup("webhook")
	if strings.Join(card.Generators, ",") != "make:controller,make:request,make:event,make:listener" {
		t.Errorf("the webhook card names the generators %v", card.Generators)
	}
	for _, step := range card.May {
		if strings.Contains(step, "job") {
			t.Errorf("the webhook card may %q", step)
		}
	}
	if verified, _ := contract.ForRule("csrf-exempt-without-signature"); verified.Kind != "webhook" {
		t.Errorf("csrf-exempt-without-signature verifies the card %q, want webhook", verified.Kind)
	}
}
