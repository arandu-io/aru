package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/arandu-io/aru/internal/gen"
)

// makeFactory writes the factory of an entity that already exists.
//
// It is `php artisan make:factory --model=Invoice`, without the flag: the fields
// are read off app/Models, because in Go the model is the schema. Two sources of
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
		return fmt.Errorf("usage: aru make:factory <Name> [--force]")
	}
	if err := checkFlatTree("make:factory", name); err != nil {
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

	entity := unsuffixed(name, "Factory")
	model := filepath.Join(root, "app", "Models", entity+".go")
	if _, err := os.Stat(model); err != nil {
		return fmt.Errorf("app/Models/%s.go does not exist -- create it with `aru make:model %s --fields \"...\"`", entity, entity)
	}

	fields, tenant, err := gen.FieldsFromModel(model, entity)
	if err != nil {
		return fmt.Errorf("make:factory: %w", err)
	}

	spec := gen.FactorySpec{
		Entity:       entity,
		Tenant:       tenant,
		Fields:       fields,
		ModelsImport: gen.Module{Name: "x", ModulePath: modulePath}.ModelsImport(),
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
The fields came from app/Models/%s.go: the model is the schema here, so the
factory is read off it rather than declared a second time. Regenerate it with
--force after changing the entity; whatever sits between the arandu:begin custom
markers is preserved.

A factory builds a value and stops there. Storing it is a repository call, and a
repository call takes a Grant -- there is no ->create() to reach for.
`, entity)
	return nil
}
