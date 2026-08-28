package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/arandu-io/aru/internal/gen"
	"github.com/arandu-io/aru/internal/spec"
)

// generate turns a specification into a module.
//
// The same engine as make:module, fed from a file instead of from flags. That
// is not a detail: two generators would drift, and what a model produces has to
// go through the code path that already has golden files.
//
// The model writes the YAML. It never writes Go.
func generate(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("generate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	force := flags.Bool("force", false, "overwrite existing files, preserving the custom blocks")
	dryRun := flags.Bool("dry-run", false, "validate and print what would be written, and write nothing")
	check := flags.Bool("check", false, "validate the specification and stop")

	var path string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		path, args = args[0], args[1:]
	}
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("generate: %w", err)
	}
	if path == "" && flags.NArg() > 0 {
		path = flags.Arg(0)
	}
	if path == "" {
		return fmt.Errorf("usage: aru generate <spec.yaml> [--check] [--dry-run] [--force]\n" +
			"`aru schema` prints the JSON Schema a specification is written against")
	}

	// Validation first, always. An error in the spec dies here rather than
	// becoming code that does not compile -- which is the difference between
	// this pipeline and asking a model for Go.
	module, err := spec.Read(path)
	if err != nil {
		return err
	}
	if *check {
		fmt.Fprintf(stdout, "%s is valid: module %s, %d field(s)\n", path, module.Name, len(module.Fields))
		return nil
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}
	modulePath, err := readModulePath(root)
	if err != nil {
		return err
	}

	// The module is built once and reused, rather than converted again where it
	// is needed. Converting twice read the clock twice, which is how the wiring
	// message ends up naming a migration the run did not write.
	target, err := resolveMigrationID(root, fromSpec(module, modulePath))
	if err != nil {
		return fmt.Errorf("generate: %w", err)
	}

	files, err := gen.Generate(target)
	if err != nil {
		return fmt.Errorf("generate: %w", err)
	}

	// The specification is committed with the code it produced. That is what
	// makes the round trip real: regenerating reads this file and produces the
	// bytes already there.
	//
	// It lives in database/specs/ because a specification is mostly about the
	// entity and its columns, and database/ is where the tree already keeps
	// factories, migrations and seeders. There is no modules/<name>/ to put it
	// beside.
	saved, err := spec.Marshal(module)
	if err != nil {
		return err
	}
	files = append(files, gen.File{
		Path:    filepath.Join(spec.Dir, module.Name+".yaml"),
		Content: saved,
	})

	if *dryRun {
		for _, f := range files {
			fmt.Fprintf(stdout, "%s (%d bytes)\n", f.Path, len(f.Content))
		}
		return nil
	}

	written, skipped, err := gen.Write(root, files, *force)
	if err != nil {
		return err
	}
	for _, p := range written {
		fmt.Fprintln(stdout, "created", p)
	}
	if len(skipped) > 0 {
		fmt.Fprintf(stderr, "\n%d file(s) already existed and were left alone:\n", len(skipped))
		for _, p := range skipped {
			fmt.Fprintln(stderr, "  ", p)
		}
		fmt.Fprintln(stderr, "\nrerun with --force to regenerate them; whatever sits between the")
		fmt.Fprintln(stderr, "arandu:begin custom markers is preserved.")
		if len(written) == 0 {
			return fmt.Errorf("nothing was written")
		}
	}

	fmt.Fprintf(stdout, "\n%s generated from %s, %d files.\n", module.Name, path, len(written))
	fmt.Fprintf(stdout, "\nThe policy opens what %s listed and denies the rest.\n",
		filepath.Base(path))
	fmt.Fprint(stdout, wiring(target, len(written)))
	return nil
}

// fromSpec converts a specification into what the generator takes.
//
// One direction, and it is the narrow one: everything the DSL expresses maps
// onto something make:module already generates. A spec field with no equivalent
// would mean the DSL grew past the generator, and the DSL is a closed set.
//
// The date and the sequence are left zero for resolveMigrationID to fill in. A
// conversion that read the clock would name the migration file, and it would
// name it differently on every call.
func fromSpec(m spec.Module, modulePath string) gen.Module {
	fields := make([]gen.Field, 0, len(m.Fields))
	for _, f := range m.Fields {
		fields = append(fields, gen.Field{
			Name:     f.Name,
			Type:     gen.Type(f.Type),
			Required: f.Required,
			Unique:   f.Unique,
		})
	}

	return gen.Module{
		Name:        m.Name,
		Fields:      fields,
		Tenant:      m.Tenant,
		ModulePath:  modulePath,
		Permissions: m.Permissions,
	}
}

// schemaCommand prints the JSON Schema.
//
// It is what a model is given, and it is generated from the same constants the
// validator uses -- a schema written by hand drifts from the code on the second
// change.
func schemaCommand(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("schema", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "", "write to a file instead of stdout")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("schema: %w", err)
	}

	body, err := spec.Schema()
	if err != nil {
		return err
	}
	if *output == "" {
		_, err = stdout.Write(body)
		return err
	}
	if err := os.WriteFile(*output, body, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", *output, err)
	}
	fmt.Fprintln(stdout, "wrote", *output)
	return nil
}
