package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/arandu-io/aru/internal/gen"
)

// makeTest writes the unit test of an entity that already exists.
//
// It is the granular form of the test `aru make:module` emits, rendered from the
// same template, and it exists for the module written before the generator and
// for the test somebody deleted -- both of which leave an entity whose
// authorization nothing checks.
//
// There is no --feature. tests/Feature boots the application and makes a
// request, and a request needs a path: the routes of a module are wiring the
// generator prints for somebody to paste, so the URL a module answers on is not
// knowable here. A feature stub would have to assert that the application boots,
// which the skeleton's own suite already does in every project -- a second copy
// of it per entity is a file that makes the suite look bigger and prove less.
func makeTest(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:test", flag.ContinueOnError)
	fs.SetOutput(stderr)
	force := fs.Bool("force", false, "overwrite an existing test, preserving the custom block")
	dryRun := fs.Bool("dry-run", false, "print what would be written, and write nothing")

	name, args := takeName(args)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:test: %w", err)
	}
	if name == "" {
		return errors.New(`usage: aru make:test <Name> [--force]`)
	}
	if err := checkFlatTree("make:test", name); err != nil {
		return err
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}
	modulePath, err := readModulePath(root)
	if err != nil {
		return err
	}

	// unsuffixed, so `aru make:test Invoice` and `aru make:test InvoiceTest`
	// name the same entity, and Normalize so what reaches the template is the
	// module name the entity is derived from -- which is what the policy action
	// in the generated assertion is spelled with.
	spec := gen.Module{Name: gen.Normalize(unsuffixed(name, "Test")), ModulePath: modulePath}
	if err := subjectOfTheTest(root, spec); err != nil {
		return err
	}

	file, err := gen.RenderTest(spec)
	if err != nil {
		return fmt.Errorf("make:test: %w", err)
	}
	if err := emit("make:test", root, []gen.File{file}, *force, *dryRun, stdout); err != nil {
		return err
	}
	if *dryRun {
		return nil
	}

	fmt.Fprintf(stdout, `
It runs without a database. Every repository method checks the Grant before it
touches the handle, which is the property under test: the zero Grant never gets
through, and a grant for one action does not open another. Two more come with
it -- that %sPolicy refuses an action nobody wrote a rule for, and that List
refuses a sort column outside the allowlist.

Tests for the rules you write go between the arandu:begin custom markers, and
survive --force.
`, spec.Entity())
	return nil
}

// subjectOfTheTest refuses to write a test whose subject is not there.
//
// The generated file names three packages and one identifier in each, and Go
// offers no way to skip an assertion that does not compile: a test written
// against a repository nobody wrote is a package that fails to build, and
// `go test ./...` then reports nothing about any other test in the project
// either. Finding that out here costs one read per file.
//
// The identifier is checked and not only the file, because the three come from
// the generator together: a model without Err<Entity>Sort is one written before
// the sort allowlist existed, and the test would name a symbol that is not
// there.
func subjectOfTheTest(root string, m gen.Module) error {
	for _, want := range []struct{ path, declares, fix string }{
		{
			filepath.Join("app", "Models", m.Entity()+".go"),
			"Err" + m.Entity() + "Sort",
			fmt.Sprintf("Write the entity with `aru make:model %s --fields \"name:string!\"`", m.Entity()),
		},
		{
			filepath.Join("app", "Repositories", m.RepositoryType()+".go"),
			"New" + m.RepositoryType(),
			fmt.Sprintf("A repository comes with the module: `aru make:module %s --fields \"name:string!\"`", m.Name),
		},
		{
			filepath.Join("app", "Policies", m.PolicyType()+".go"),
			m.Entity() + "List",
			fmt.Sprintf("Write the policy with `aru make:policy %s`", m.Name),
		},
	} {
		body, err := os.ReadFile(filepath.Join(root, want.path))
		if err != nil {
			return fmt.Errorf("make:test: the test is written against %s, and it does not exist. %s", want.path, want.fix)
		}
		if !strings.Contains(string(body), want.declares) {
			return fmt.Errorf("make:test: the test asks for %s by name, and %s does not declare it. %s",
				want.declares, want.path, want.fix)
		}
	}
	return nil
}
