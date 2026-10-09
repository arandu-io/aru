package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/arandu-io/aru/internal/gen"
)

// uniqueIDs matches the field of a model.TableSpec that makes the model fill
// the key on insert.
var uniqueIDs = regexp.MustCompile(`\bUniqueIDs:\s*true\b`)

// makeFactoryUsage is the usage line of make:factory, in the dispatch table and in
// its refusal, naming every flag the command accepts.
const makeFactoryUsage = `aru make:factory <Name> [--force] [--dry-run]`

// makeFactory writes the factory of an entity that already exists.
//
// There is no --model flag: the fields are read off app/Models, because in Go
// the model is the schema. Two sources of
// truth about one set of columns is how they drift.
func makeFactory(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:factory", flag.ContinueOnError)
	fs.SetOutput(stderr)
	force := fs.Bool("force", false, "overwrite an existing factory, preserving the custom block")
	dryRun := fs.Bool("dry-run", false, "print what would be written, and write nothing")

	name, args := takeName(args)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:factory: %w", err)
	}
	if name == "" {
		return fmt.Errorf("usage: %s", makeFactoryUsage)
	}
	if err := checkFlatTree("make:factory", name); err != nil {
		return err
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}
	if err := requireModelCore("make:factory", root); err != nil {
		return err
	}
	modulePath, err := readModulePath(root)
	if err != nil {
		return err
	}

	entity := unsuffixed(name, "Factory")
	model := filepath.Join(root, "app", "Models", entity+".go")
	if _, err := os.Stat(model); err != nil {
		return fmt.Errorf("app/Models/%s.go does not exist -- create it with `aru make:model %s --fields \"...\"`", entity, entity)
	}

	fields, tenant, err := gen.FieldsFromModel(model, entity)
	if err != nil {
		return fmt.Errorf("make:factory: %w", err)
	}
	source, err := os.ReadFile(model)
	if err != nil {
		return fmt.Errorf("make:factory: %w", err)
	}

	fields, parents := gen.SplitParents(root, fields)
	spec := gen.FactorySpec{
		Entity:       entity,
		Tenant:       tenant,
		Fields:       fields,
		ModelsImport: gen.Module{Name: "x", ModulePath: modulePath}.ModelsImport(),
		Parents:      parents,
	}

	file, err := gen.RenderFactory(spec)
	if err != nil {
		return fmt.Errorf("make:factory: %w", err)
	}
	if err := emit("make:factory", root, []gen.File{file}, *force, *dryRun, stdout); err != nil {
		return err
	}
	if *dryRun {
		return nil
	}

	fmt.Fprintf(stdout, `
The default state came from the fields of app/Models/%s.go: the model is the
schema here, so the factory is read off it rather than declared a second time.
It sits in the custom block, and from now on it is yours. aru model:build
rewrites everything outside that block on every build, so the typed methods
follow the entity without anybody running this again.

Make builds rows and stores nothing; Create stores them and takes a Grant, like
every other write -- a factory is no way around the policy that guards the table.
`, entity)
	if len(parents) > 0 {
		fmt.Fprint(stdout, parentStates(parents))
	}
	if !uniqueIDs.Match(source) {
		fmt.Fprintf(stdout, `
The factory leaves the key empty, and the table in app/Models/%s.go does not
set UniqueIDs, so nothing fills it on insert. Set UniqueIDs: true in its
model.TableSpec, in place of ManualKey and KeyType.
`, entity)
	}
	return nil
}

// parentStates is what make:factory says about the fields it left out of the
// default state: each holds the key of another model of the project, and the
// state that fills it is named, with the line a test or a seeder writes.
func parentStates(parents []gen.FactoryParent) string {
	var b strings.Builder
	b.WriteString("\nThe default state leaves ")
	for i, p := range parents {
		if i > 0 {
			b.WriteString(" and ")
		}
		b.WriteString(p.Field)
	}
	b.WriteString(` empty.
Each holds the key of a model this project has, and a drawn key would point at
a row nobody stored. The custom block has a state for each, which takes the id
of a row the caller stored:
`)
	for _, p := range parents {
		fmt.Fprintf(&b, "\n    For%s(%s)", p.Entity, p.Arg)
	}
	b.WriteString("\n")
	return b.String()
}
