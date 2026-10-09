package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/arandu-io/aru/internal/gen"
)

// makeResourceUsage is the usage line of make:resource, in the dispatch table
// and in its refusal, naming every flag the command accepts.
const makeResourceUsage = `aru make:resource <Name> [--force] [--dry-run]`

// makeResource writes the JSON Resource of an entity that already exists: the
// list of fields it answers with, its collection, and the test that proves
// ctx.JSON writes those fields and no other.
//
// The fields are read off app/Models, for the reason make:factory reads them
// there: the model is the schema, and a second declaration of its columns is
// how the two drift.
func makeResource(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:resource", flag.ContinueOnError)
	fs.SetOutput(stderr)
	force := fs.Bool("force", false, "overwrite an existing resource and its test, preserving the custom blocks")
	dryRun := fs.Bool("dry-run", false, "print what would be written, and write nothing")

	name, args := takeName(args)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:resource: %w", err)
	}
	if name == "" {
		return fmt.Errorf("usage: %s", makeResourceUsage)
	}
	if err := checkFlatTree("make:resource", name); err != nil {
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

	m := gen.Module{Name: gen.Normalize(unsuffixed(name, "Resource")), ModulePath: modulePath}
	model := filepath.Join(root, "app", "Models", m.Entity()+".go")
	if _, err := os.Stat(model); err != nil {
		return fmt.Errorf("app/Models/%s.go does not exist -- a resource answers with the fields of a model, "+
			"so create it first with `aru make:model %s --fields \"...\"`", m.Entity(), m.Entity())
	}
	fields, tenant, err := gen.ColumnsFromModel(model, m.Entity())
	if err != nil {
		return fmt.Errorf("make:resource: %w", err)
	}

	spec := gen.ResourceSpec{
		Entity: m.Entity(), Table: m.Table(), Tenant: tenant, Fields: fields, ModulePath: modulePath,
	}
	files, err := gen.RenderResource(spec)
	if err != nil {
		return fmt.Errorf("make:resource: %w", err)
	}
	if err := emit("make:resource", root, files, *force, *dryRun, stdout); err != nil {
		return err
	}
	if *dryRun {
		return nil
	}

	fmt.Fprintf(stdout, `
The resource answers with every column of app/Models/%[1]s.go except the
tenant; delete the lines for what this answer should not carry, in ToArray and
in the list its test compares against. A field added to the model later does
not leave until somebody adds it here.

A controller answers with it, and a tool of the MCP server with the same one:

      return ctx.JSON(http.StatusOK, resources.New%[2]s(found))
      return mcp.JSON(resources.New%[2]s(found)), nil
`, m.Entity(), spec.Type())
	return nil
}
