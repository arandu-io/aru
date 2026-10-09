package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/arandu-io/aru/internal/gen"
)

// makeServiceUsage is the usage line of make:service, in the dispatch table and
// in its refusal, naming every flag the command accepts.
const makeServiceUsage = `aru make:service <Name> [--force] [--dry-run]`

// makeService writes the service of an entity that already has its model, its
// policy and its request, with one use case in the shape every service method
// has.
//
// The three are required rather than written here: a service is the place they
// meet, and a command that invented a policy to have one to call would ship
// the policy nobody decided. `aru make:model <Name> --policy --requests` writes
// the two that are missing next to the model.
func makeService(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:service", flag.ContinueOnError)
	fs.SetOutput(stderr)
	force := fs.Bool("force", false, "overwrite an existing service and its test, preserving the custom blocks")
	dryRun := fs.Bool("dry-run", false, "print what would be written, and write nothing")

	name, args := takeName(args)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:service: %w", err)
	}
	if name == "" {
		return fmt.Errorf("usage: %s", makeServiceUsage)
	}
	if err := checkFlatTree("make:service", name); err != nil {
		return err
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}
	if err := requireModelCore("make:service", root); err != nil {
		return err
	}
	modulePath, err := readModulePath(root)
	if err != nil {
		return err
	}

	entity := unsuffixed(name, "Service")
	m := gen.Module{Name: gen.Normalize(entity), ModulePath: modulePath}

	model := filepath.Join(root, "app", "Models", m.Entity()+".go")
	request := filepath.Join(root, "app", "Http", "Requests", m.Request()+".go")
	policy := filepath.Join(root, "app", "Policies", m.PolicyType()+".go")
	var missing []string
	for _, p := range []string{model, policy, request} {
		if _, err := os.Stat(p); err != nil {
			rel, _ := filepath.Rel(root, p)
			missing = append(missing, filepath.ToSlash(rel))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("make:service: a service is where the model, the policy and the request meet, and %v %s missing.\n"+
			"Write them first:\n\n    aru make:model %s --fields \"...\" --policy --requests", missing, isOrAre(len(missing)), m.Entity())
	}

	modelFields, tenant, err := gen.FieldsFromModel(model, m.Entity())
	if err != nil {
		return fmt.Errorf("make:service: %w", err)
	}
	requestFields, _, err := gen.FieldsFromModel(request, m.Request())
	if err != nil {
		return fmt.Errorf("make:service: %w", err)
	}
	m.Tenant = tenant
	m.Fields = gen.ServiceFields(modelFields, requestFields)

	files, err := gen.GenerateService(m)
	if err != nil {
		return fmt.Errorf("make:service: %w", err)
	}
	if err := emit("make:service", root, files, *force, *dryRun, stdout); err != nil {
		return err
	}
	if *dryRun {
		return nil
	}
	fmt.Fprint(stdout, wiringService(m))
	return nil
}

func isOrAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// wiringService is what to do with the service that was just written: build
// it in bootstrap/app.go and hand it to whatever calls it.
func wiringService(m gen.Module) string {
	return fmt.Sprintf(`
The file is written, the wiring is not. bootstrap/app.go builds the service
with the connection and hands it to the controller, the job or the tool that
calls it:

      services.New%s(db)

Create copies the fields the request and the model both declare, under one name
and one type; anything else is yours to write in fill. It validates, asks
%s, and only then reaches the Model with the Grant -- and the policy
denies everything until you open what this use case needs.
`, m.ServiceType(), m.PolicyType())
}
