package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/spec"
)

const invoiceSpec = `version: "1"
name: invoice
description: An invoice issued to a customer.
tenant: true
fields:
  - name: reference
    type: string
    required: true
    unique: true
  - name: customer_email
    type: email
    required: true
  - name: total
    type: money
    required: true
  - name: due_date
    type: date
  - name: paid
    type: bool
  - name: notes
    type: text
permissions:
  view: [admin, member]
  list: [admin, member]
  create: [admin]
  update: [admin]
  delete: [admin]
`

// project writes a throwaway project with a specification in it.
func project(t *testing.T, document string) (root, specPath string) {
	t.Helper()

	root = t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.test/project\n\ngo 1.25\n")
	writeFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(root, "arandu.toml"), "name = \"test\"\n")

	specPath = filepath.Join(root, "invoice.yaml")
	writeFile(t, specPath, document)
	return root, specPath
}

func TestGenerateProducesTheModule(t *testing.T) {
	root, specPath := project(t, invoiceSpec)
	chdir(t, root)

	var out, errOut strings.Builder
	if err := generate([]string{specPath}, &out, &errOut); err != nil {
		t.Fatalf("generate: %v\n%s", err, errOut.String())
	}

	for _, want := range []string{
		"app/Http/Controllers/InvoiceController.go",
		"app/Models/Invoice.go",
		"app/Policies/InvoicePolicy.go",
		"app/Repositories/InvoiceRepository.go",
	} {
		if _, err := os.Stat(filepath.Join(root, want)); err != nil {
			t.Errorf("%s was not generated", want)
		}
	}

	// The fields reached the entity with the right Go types. Whitespace is
	// collapsed first: gofmt aligns a struct's columns, so the gap between a
	// name and its type depends on the longest field in the block.
	entity := collapse(read(t, filepath.Join(root, "app/Models/Invoice.go")))
	for _, want := range []string{"Reference string", "CustomerEmail string", "Total int64", "Paid bool", "Notes string"} {
		if !strings.Contains(entity, want) {
			t.Errorf("the entity has no %q", want)
		}
	}

	// money is int64 and never a float: a decimal amount loses cents to binary
	// rounding, and the loss shows up in an invoice total.
	if strings.Contains(entity, "Total float") {
		t.Error("money became a float, and the cents will not survive")
	}
}

// TestTheRoundTripIsByteForByte covers the property that decides whether anyone
// ever regenerates.
//
// Generated code -> specification -> generated code, identical. Without it there
// is no way to promise that regenerating does not destroy work, and without that
// promise the generator is a one-time scaffold.
func TestTheRoundTripIsByteForByte(t *testing.T) {
	root, specPath := project(t, invoiceSpec)
	chdir(t, root)

	var out, errOut strings.Builder
	if err := generate([]string{specPath}, &out, &errOut); err != nil {
		t.Fatalf("the first generate: %v\n%s", err, errOut.String())
	}

	first := filesIn(t, filepath.Join(root, "app"))

	// Regenerate from the specification the first run saved, not from the one
	// the person wrote. That is the round trip: the file the generator emitted
	// has to be able to reproduce the generator's own output.
	saved := filepath.Join(root, spec.Dir, "invoice.yaml")
	out.Reset()
	errOut.Reset()
	if err := generate([]string{saved, "--force"}, &out, &errOut); err != nil {
		t.Fatalf("the second generate: %v\n%s", err, errOut.String())
	}

	second := filesIn(t, filepath.Join(root, "app"))

	if len(first) != len(second) {
		t.Fatalf("the second run produced %d files, the first %d", len(second), len(first))
	}
	for name, before := range first {
		after, present := second[name]
		if !present {
			t.Errorf("%s disappeared on regeneration", name)
			continue
		}
		if !bytes.Equal(before, after) {
			t.Errorf("%s differs after a round trip through the specification", name)
		}
	}
}

// TestRegeneratingKeepsTheCustomBlock covers the escape hatch. Without it,
// regenerating eats whatever the project added, and a generator people are
// afraid to rerun is a generator nobody reruns.
func TestRegeneratingKeepsTheCustomBlock(t *testing.T) {
	root, specPath := project(t, invoiceSpec)
	chdir(t, root)

	var out, errOut strings.Builder
	if err := generate([]string{specPath}, &out, &errOut); err != nil {
		t.Fatalf("generate: %v\n%s", err, errOut.String())
	}

	// The business rule the DSL deliberately cannot express.
	policyPath := filepath.Join(root, "app", "Policies", "InvoicePolicy.go")
	policy := read(t, policyPath)
	marked := strings.Replace(policy,
		"// arandu:begin custom\n",
		"// arandu:begin custom\n\t// A paid invoice cannot be deleted by anyone.\n", 1)
	if marked == policy {
		t.Fatal("the generated policy has no custom block to preserve")
	}
	writeFile(t, policyPath, marked)

	out.Reset()
	errOut.Reset()
	if err := generate([]string{specPath, "--force"}, &out, &errOut); err != nil {
		t.Fatalf("regenerating: %v\n%s", err, errOut.String())
	}

	if !strings.Contains(read(t, policyPath), "A paid invoice cannot be deleted") {
		t.Fatal("regenerating discarded what was inside the custom block")
	}
}

// TestABadSpecNeverBecomesCode is the pipeline's whole claim. An error in the
// specification dies at validation -- it does not become Go that fails to
// compile, which is what asking a model for code produces.
func TestABadSpecNeverBecomesCode(t *testing.T) {
	for _, c := range []struct {
		name     string
		document string
		says     string
	}{
		{"no version", "name: invoice\nfields:\n  - {name: x, type: string}\n", "version"},
		{"unknown type", "version: \"1\"\nname: invoice\nfields:\n  - {name: x, type: currency}\n", "currency"},
		{"typo in a key", "version: \"1\"\nname: invoice\nfields:\n  - {name: x, type: string, requried: true}\n", "requried"},
		{"reserved field", "version: \"1\"\nname: invoice\nfields:\n  - {name: id, type: uuid}\n", "generated for every module"},
		{"unknown action", "version: \"1\"\nname: invoice\nfields:\n  - {name: x, type: string}\npermissions:\n  approve: [admin]\n", "approve"},
		{"name in camel case", "version: \"1\"\nname: PurchaseOrder\nfields:\n  - {name: x, type: string}\n", "lowercase"},
		{"no fields", "version: \"1\"\nname: invoice\nfields: []\n", "nothing to store"},
	} {
		t.Run(c.name, func(t *testing.T) {
			root, specPath := project(t, c.document)
			chdir(t, root)

			var out, errOut strings.Builder
			err := generate([]string{specPath}, &out, &errOut)
			if err == nil {
				t.Fatal("the specification was accepted")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("the error does not mention %q:\n%v", c.says, err)
			}

			// And nothing was written. A partial module is worse than none:
			// it compiles halfway and looks finished.
			if _, statErr := os.Stat(filepath.Join(root, "modules")); statErr == nil {
				t.Error("a rejected specification still produced files")
			}
		})
	}
}

// TestEveryProblemIsReportedAtOnce: a model that got three fields wrong should
// learn all three from one response, and a person should not discover them one
// run at a time.
func TestEveryProblemIsReportedAtOnce(t *testing.T) {
	root, specPath := project(t, `version: "1"
name: Invoice
fields:
  - {name: x, type: currency}
  - {name: id, type: uuid}
`)
	chdir(t, root)

	var out, errOut strings.Builder
	err := generate([]string{specPath}, &out, &errOut)
	if err == nil {
		t.Fatal("the specification was accepted")
	}

	message := err.Error()
	for _, want := range []string{"lowercase", "currency", "generated for every module"} {
		if !strings.Contains(message, want) {
			t.Errorf("the error does not mention %q:\n%s", want, message)
		}
	}
}

// TestCheckValidatesWithoutWriting: what a pipeline runs on a pull request, and
// what a model's output goes through before a human ever sees a diff.
func TestCheckValidatesWithoutWriting(t *testing.T) {
	root, specPath := project(t, invoiceSpec)
	chdir(t, root)

	var out, errOut strings.Builder
	if err := generate([]string{specPath, "--check"}, &out, &errOut); err != nil {
		t.Fatalf("check: %v", err)
	}
	if !strings.Contains(out.String(), "is valid") {
		t.Errorf("check said %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(root, "modules")); err == nil {
		t.Error("--check wrote files")
	}
}

// TestTheSchemaIsValidJSONSchema: it is the artifact a model is given, and a
// schema that does not parse teaches a model nothing.
func TestTheSchemaIsValidJSONSchema(t *testing.T) {
	body, err := spec.Schema()
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("the schema is not valid JSON: %v", err)
	}

	for _, key := range []string{"$schema", "$id", "title", "properties", "required", "examples"} {
		if _, present := parsed[key]; !present {
			t.Errorf("the schema has no %q", key)
		}
	}
	// additionalProperties false is what turns a typo into an error for the
	// model too, and not only for the parser.
	if parsed["additionalProperties"] != false {
		t.Error("the schema accepts unknown properties, and a typo would validate")
	}
}

// TestTheExampleInTheSchemaValidates: a schema whose own example is rejected
// teaches a model to write something the generator refuses.
func TestTheExampleInTheSchemaValidates(t *testing.T) {
	body, err := spec.Schema()
	if err != nil {
		t.Fatal(err)
	}

	var parsed struct {
		Examples []spec.Module `json:"examples"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decoding the schema: %v", err)
	}
	if len(parsed.Examples) == 0 {
		t.Fatal("the schema carries no example")
	}
	for i, example := range parsed.Examples {
		if err := example.Validate(); err != nil {
			t.Errorf("example %d is rejected by the validator:\n%v", i, err)
		}
	}
}

// TestTheCommittedSchemaIsCurrent: the file in schema/ is what a model reads
// from the repository, and a stale one is worse than none.
func TestTheCommittedSchemaIsCurrent(t *testing.T) {
	current, err := spec.Schema()
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile("schema/module.schema.json")
	if err != nil {
		t.Fatalf("schema/module.schema.json is missing: %v", err)
	}

	if !bytes.Equal(current, committed) {
		t.Fatal("the committed schema is stale; run: aru schema --output schema/module.schema.json")
	}
}

func filesIn(t *testing.T, dir string) map[string][]byte {
	t.Helper()

	out := map[string][]byte{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = body
	}
	return out
}

// collapse squeezes runs of whitespace, so an assertion about generated Go does
// not depend on how gofmt aligned the columns.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

func read(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(body)
}

// TestThePermissionsOpenThePolicy: the specification says who may do what, and
// the generated policy has to enforce exactly that.
//
// Without this, `permissions` is a field that validates and generates nothing --
// a promise in a schema, which is worse than no field at all because a model
// writes it and believes it worked.
func TestThePermissionsOpenThePolicy(t *testing.T) {
	root, specPath := project(t, invoiceSpec)
	chdir(t, root)

	var out, errOut strings.Builder
	if err := generate([]string{specPath}, &out, &errOut); err != nil {
		t.Fatalf("generate: %v\n%s", err, errOut.String())
	}

	policy := collapse(read(t, filepath.Join(root, "app/Policies/InvoicePolicy.go")))

	// view and list went to admin and member; create, update and delete to
	// admin alone.
	//
	// The action constant carries the entity — InvoiceView, not ActionView.
	// app/Policies/ is one package for every entity, and ActionView would
	// collide the moment a second module is generated.
	for _, want := range []string{
		`case InvoiceView: if s.HasRole("admin") || s.HasRole("member")`,
		`case InvoiceCreate: if s.HasRole("admin") { return nil }`,
	} {
		if !strings.Contains(policy, want) {
			t.Errorf("the policy does not enforce %q", want)
		}
	}
	// member must not reach create. If the generator emitted one branch for
	// every action with every role, this is what would catch it.
	if strings.Contains(policy, `case ActionCreate: if s.HasRole("admin") || s.HasRole("member")`) {
		t.Error("create was opened to a role the specification did not list")
	}
	// And what nobody listed stays denied.
	if !strings.Contains(policy, "no rule allows") {
		t.Error("the policy has no final denial")
	}
}

// TestAPolicyWithNoPermissionsDeniesEverything: make:module has no
// specification, and a generator that guessed would ship a hole by default in
// every project that ran it.
func TestAPolicyWithNoPermissionsDeniesEverything(t *testing.T) {
	root, specPath := project(t, `version: "1"
name: invoice
fields:
  - {name: reference, type: string}
`)
	chdir(t, root)

	var out, errOut strings.Builder
	if err := generate([]string{specPath}, &out, &errOut); err != nil {
		t.Fatalf("generate: %v\n%s", err, errOut.String())
	}

	policy := collapse(read(t, filepath.Join(root, "app/Policies/InvoicePolicy.go")))
	if strings.Contains(policy, "HasRole") {
		t.Error("a specification with no permissions produced a policy that allows something")
	}
	if !strings.Contains(policy, "IT DENIES EVERYTHING") {
		t.Error("the policy does not say that it denies everything")
	}
}

// TestTwoModulesGeneratedTheSameDayGetDistinctSequences.
//
// The order of migrations is the order of their ids, so two files written on one
// day need two numbers. Every other command that writes a migration takes the
// sequence from the directory; a generate that hardcoded it would emit two
// distinct file names under the same date and the same number, and nothing
// would decide which of the two runs first.
func TestTwoModulesGeneratedTheSameDayGetDistinctSequences(t *testing.T) {
	root, first := project(t, invoiceSpec)
	chdir(t, root)

	second := filepath.Join(root, "shipment.yaml")
	writeFile(t, second, strings.Replace(invoiceSpec, "name: invoice", "name: shipment", 1))

	var out, errOut strings.Builder
	for _, path := range []string{first, second} {
		out.Reset()
		errOut.Reset()
		if err := generate([]string{path}, &out, &errOut); err != nil {
			t.Fatalf("generate %s: %v\n%s", path, err, errOut.String())
		}
	}

	entries, err := os.ReadDir(filepath.Join(root, "database", "migrations"))
	if err != nil {
		t.Fatalf("reading database/migrations: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 2 {
		t.Fatalf("database/migrations holds %v, want one migration per module", names)
	}

	seen := map[string]string{}
	for _, name := range names {
		m := migrationID.FindStringSubmatch(name)
		if m == nil {
			t.Fatalf("%s is not named like a migration", name)
		}
		if other, taken := seen[m[2]]; taken {
			t.Fatalf("%s and %s share sequence %s: nothing decides which runs first", other, name, m[2])
		}
		seen[m[2]] = name
	}
	if _, taken := seen["000002"]; !taken {
		t.Errorf("the second module did not take the next sequence: %v", names)
	}
}

// TestRegeneratingKeepsTheMigrationItAlreadyWrote is the other half of taking
// the next sequence, and without it the first half is a regression.
//
// A migration id is immutable: it is the key the applied-migrations table
// records. A run that minted a second id for a module that already has one would
// leave database/migrations with two files declaring one type -- which does not
// compile -- and a migration recorded as applied under an id no file carries.
func TestRegeneratingKeepsTheMigrationItAlreadyWrote(t *testing.T) {
	root, specPath := project(t, invoiceSpec)
	chdir(t, root)

	var out, errOut strings.Builder
	if err := generate([]string{specPath}, &out, &errOut); err != nil {
		t.Fatalf("the first generate: %v\n%s", err, errOut.String())
	}
	before := filesIn(t, filepath.Join(root, "database", "migrations"))
	if len(before) != 1 {
		t.Fatalf("the first run wrote %d migrations, want one", len(before))
	}

	// From the specification the first run saved, which is the round trip.
	out.Reset()
	errOut.Reset()
	saved := filepath.Join(root, spec.Dir, "invoice.yaml")
	if err := generate([]string{saved, "--force"}, &out, &errOut); err != nil {
		t.Fatalf("regenerating: %v\n%s", err, errOut.String())
	}

	after := filesIn(t, filepath.Join(root, "database", "migrations"))
	if len(after) != 1 {
		t.Fatalf("regenerating left %d migrations declaring one type, and they do not compile: %v",
			len(after), sortedNames(after))
	}
	for name, first := range before {
		second, present := after[name]
		if !present {
			t.Fatalf("%s was replaced by %v: a migration id is immutable", name, sortedNames(after))
		}
		if !bytes.Equal(first, second) {
			t.Errorf("%s differs after a round trip through the specification", name)
		}
	}
}

// sortedNames is the keys of a file map, for an error message that reads the
// same on every run.
func sortedNames(files map[string][]byte) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// TestASecondPathIsRefusedRatherThanIgnored.
//
// The command took args[0] and let the flag package keep the rest, and the flag
// package stops at the first positional argument -- so a second path landed in
// Args() and was read by nobody. A command that accepts N arguments and uses one
// lies about its own signature, and the flag that followed the extra path was
// dropped with it.
func TestASecondPathIsRefusedRatherThanIgnored(t *testing.T) {
	for name, order := range map[string]func(first, second string) []string{
		"the paths first": func(first, second string) []string {
			return []string{first, second}
		},
		"a flag first": func(first, second string) []string {
			return []string{"--force", first, second}
		},
	} {
		t.Run(name, func(t *testing.T) {
			root, first := project(t, invoiceSpec)
			chdir(t, root)

			second := filepath.Join(root, "shipment.yaml")
			writeFile(t, second, strings.Replace(invoiceSpec, "name: invoice", "name: shipment", 1))

			var out, errOut strings.Builder
			err := generate(order(first, second), &out, &errOut)
			if err == nil {
				t.Fatal("the second path was ignored without a word")
			}
			for _, want := range []string{"one specification per run", "invoice.yaml", "shipment.yaml"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the error does not mention %q:\n%v", want, err)
				}
			}
			// And nothing was written. Generating the first of two paths is the
			// half-done state the refusal exists to prevent.
			if _, statErr := os.Stat(filepath.Join(root, "app")); statErr == nil {
				t.Error("a refused run still generated files")
			}
		})
	}
}

// TestOnePathAndItsFlagsAreStillAccepted, in both orders, so the refusal above
// cannot be satisfied by refusing everything.
func TestOnePathAndItsFlagsAreStillAccepted(t *testing.T) {
	for name, order := range map[string]func(path string) []string{
		"the path first": func(path string) []string { return []string{path, "--check"} },
		"the flag first": func(path string) []string { return []string{"--check", path} },
	} {
		t.Run(name, func(t *testing.T) {
			root, specPath := project(t, invoiceSpec)
			chdir(t, root)

			var out, errOut strings.Builder
			if err := generate(order(specPath), &out, &errOut); err != nil {
				t.Fatalf("a run with one path was refused: %v\n%s", err, errOut.String())
			}
			if !strings.Contains(out.String(), "is valid") {
				t.Errorf("check said %q", out.String())
			}
		})
	}
}
