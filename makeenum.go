package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/arandu-io/aru/internal/gen"
)

// makeEnum writes one enum.
//
// It is `php artisan make:enum`, with the one inversion of behaviour in the
// whole granular family: --values is required. artisan emits an empty enum
// because in PHP the enum is a construct of the language and the empty body
// already behaves correctly -- from(), cases(), type safety. Go has no such
// construct, so the value of an enum is exactly the boilerplate, and emitting
// `type InvoiceStatus string` alone would emit zero useful lines.
func makeEnum(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:enum", flag.ContinueOnError)
	fs.SetOutput(stderr)
	values := fs.String("values", "", "the closed set, separated by commas: draft,sent,paid,void")
	asInt := fs.Bool("int", false, "back the enum with an integer instead of a string")
	force := fs.Bool("force", false, "overwrite an existing enum, preserving the custom block")
	dryRun := fs.Bool("dry-run", false, "print what would be written, and write nothing")

	name, args := takeName(args)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:enum: %w", err)
	}
	if name == "" {
		return fmt.Errorf(`usage: aru make:enum <Name> --values draft,sent,paid,void [--int] [--force]`)
	}
	if err := checkFlatTree("make:enum", name); err != nil {
		return err
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}

	typeName := gen.Exported(name)
	parsed, err := gen.ParseEnumValues(*values, typeName, *asInt)
	if err != nil {
		return fmt.Errorf("make:enum: %w", err)
	}

	spec := gen.EnumSpec{Type: typeName, Values: parsed, Int: *asInt}
	file, err := gen.RenderEnum(spec)
	if err != nil {
		return fmt.Errorf("make:enum: %w", err)
	}
	if err := emit("make:enum", root, []gen.File{file}, *force, *dryRun, stdout); err != nil {
		return err
	}
	if *dryRun {
		return nil
	}

	fmt.Fprintf(stdout, `
An enum is not wired: it is a type. Where it goes:

  app/Models -- the column, typed

      Status enums.%s

  app/Http/Requests -- the input, parsed rather than assigned

      status, err := enums.Parse%s(ctx.Input("status"))

The stored value and the label are separate on purpose. Renaming a constant must
never rewrite a column, and changing what a form shows must never change a row.
`, spec.Type, spec.Type)
	return nil
}
