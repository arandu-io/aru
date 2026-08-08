package main

import (
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/arandu-io/aru/internal/gen"
)

// makeModel writes the entity, and the parts the flags ask for.
//
// It is for somebody porting forty models who wants the struct and the
// migration in one gesture. What it
// never writes is a repository -- see gen.GenerateModel, which says why -- and
// the output of the command is the cheapest place there is to teach the one
// thing that has to be unlearned: a model here reaches nothing.
func makeModel(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:model", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fields := fs.String("fields", "", `the fields, as "name:type" separated by commas. Suffix ! for required, u for unique`)
	tenant := fs.Bool("tenant", false, "the entity belongs to a tenant: add TenantID")
	withMigration := fs.Bool("migration", false, "also write the migration that creates the table")
	withFactory := fs.Bool("factory", false, "also write the factory that builds the entity")
	force := fs.Bool("force", false, "overwrite existing files, preserving the custom blocks")
	dryRun := fs.Bool("dry-run", false, "print what would be written, and write nothing")

	name, args := takeName(args)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:model: %w", err)
	}
	if name == "" {
		return fmt.Errorf("usage: aru make:model <Name> --fields %q\n%s",
			"reference:string!u,total:money", gen.TypeList())
	}
	if err := checkFlatTree("make:model", name); err != nil {
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

	// --fields is required here, and it is the difference that matters. Where an
	// ORM is involved the
	// model is empty because the columns are discovered at runtime; in Go the
	// struct is the schema, so a model with no fields describes nothing.
	parsed, err := gen.ParseFields(*fields)
	if err != nil {
		return fmt.Errorf("make:model: %w\nusage: aru make:model <Name> --fields %q\n%s",
			err, "reference:string!u,total:money", gen.TypeList())
	}

	date := time.Now().UTC().Format("2006_01_02")
	seq := 1
	if *withMigration {
		if seq, err = nextMigrationSequence(root, date); err != nil {
			return err
		}
	}

	// Both spellings of the name are accepted: the developer this is for types
	// the class name, PurchaseOrder, and the generator needs purchase_order.
	spec := gen.Module{
		Name:       gen.Normalize(name),
		Fields:     parsed,
		Tenant:     *tenant,
		ModulePath: modulePath,
		Date:       date,
		Sequence:   seq,
	}

	files, err := gen.GenerateModel(spec, gen.ModelParts{Migration: *withMigration, Factory: *withFactory})
	if err != nil {
		return fmt.Errorf("make:model: %w", err)
	}
	if err := emit("make:model", root, files, *force, *dryRun, stdout); err != nil {
		return err
	}
	if *dryRun {
		return nil
	}

	fmt.Fprint(stdout, modelWiring(spec, *withMigration))
	return nil
}

// modelWiring is the part of this command that matters most: it names, in one
// screen, the difference between an ORM model and this one, and the two
// commands that make the entity reachable.
func modelWiring(m gen.Module, migration bool) string {
	out := fmt.Sprintf(`
A model here is data. It has no save, no find and no query builder: where an ORM
the model reaches the database, and here nothing does except a Repository, whose
every method takes a security.Grant that a Policy issued -- the reads included.

So this entity reaches no table yet, and nothing will fail to compile because of
it. What makes it reachable:

    aru make:module %s --fields "..."
        the whole path in one command: policy, repository, service, request,
        controller, views and test

    aru make:policy %s
        the policy alone, for an entity that already has a repository

`+"`aru doctor`"+` reports a repository with no policy as an error, and a policy that
denies everything as a warning. Both are on purpose.
`, m.Name, m.Entity())

	if migration {
		out += fmt.Sprintf(`
The migration is written and not applied: it has to be listed in All(), in
database/migrations/migrations.go, inside the custom block:

    %s,

Then:

    aru migrate
`, m.MigrationVar())
	}
	return out
}
