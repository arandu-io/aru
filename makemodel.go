package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/arandu-io/aru/internal/gen"
)

// makeModel writes the entity, and the parts the flags ask for.
//
// It is for somebody porting forty models who wants the struct and the migration
// in one gesture. --all is the data side of an entity -- migration, factory,
// seeder, policy and request -- and `aru make:module` is the whole feature, with
// the controller, the service, the views and the route wiring besides. The two
// are not two ways to write one thing: one writes an entity and the other writes
// a feature.
//
// Neither command writes a Repository for CRUD. The Model is the data entry
// point, and the output of this command is the cheapest place there is to teach
// where the Service and Policy join that path.
func makeModel(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:model", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fields := fs.String("fields", "", `the fields, as "name:type" separated by commas. Suffix ! for required, u for unique`)
	tenant := fs.Bool("tenant", false, "the entity belongs to a tenant: add TenantID")
	withMigration := fs.Bool("migration", false, "also write the migration that creates the table")
	withFactory := fs.Bool("factory", false, "also write the factory that builds the entity")
	withSeeder := fs.Bool("seed", false, "also write the seeder that fills the table")
	withPolicy := fs.Bool("policy", false, "also write the policy that decides who may reach the entity")
	withRequests := fs.Bool("requests", false, "also write the request that validates the input")
	all := fs.Bool("all", false, "write the migration, factory, seeder, policy and request as well")
	force := fs.Bool("force", false, "overwrite existing files, preserving the custom blocks")
	dryRun := fs.Bool("dry-run", false, "print what would be written, and write nothing")

	// The short forms are the ones a person types forty times, and they are
	// aliases of the same variable rather than flags of their own: two flags
	// spelling one thing can disagree, and -m -migration=false would have to
	// mean something.
	fs.BoolVar(withMigration, "m", false, "short for --migration")
	fs.BoolVar(withFactory, "f", false, "short for --factory")
	fs.BoolVar(withSeeder, "s", false, "short for --seed")
	fs.BoolVar(all, "a", false, "short for --all")

	name, args := takeName(args)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:model: %w", err)
	}
	if name == "" {
		return fmt.Errorf("usage: aru make:model <Name> --fields %q [--tenant] [--migration] [--factory] [--force]\n%s",
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
		return fmt.Errorf("make:model: %w\nusage: aru make:model <Name> --fields %q [--tenant] [--migration] [--factory] [--force]\n%s",
			err, "reference:string!u,total:money", gen.TypeList())
	}

	parts := gen.ModelParts{
		Migration: *withMigration,
		Factory:   *withFactory,
		Seeder:    *withSeeder,
		Policy:    *withPolicy,
		Request:   *withRequests,
	}
	if *all {
		parts = gen.Everything()
	}

	// Both spellings of the name are accepted: the developer this is for types
	// the class name, PurchaseOrder, and the generator needs purchase_order.
	spec := gen.Module{
		Name:       gen.Normalize(name),
		Fields:     parsed,
		Tenant:     *tenant,
		ModulePath: modulePath,
	}

	// Only when there is a migration to name. The date and the sequence feed
	// MigrationID and nothing else, so asking for them without --migration would
	// read the directory to fill in two fields no template renders.
	if parts.Migration {
		if spec, err = resolveMigrationID(root, spec); err != nil {
			return fmt.Errorf("make:model: %w", err)
		}
	}

	files, err := gen.GenerateModel(spec, parts)
	if err != nil {
		return fmt.Errorf("make:model: %w", err)
	}
	if err := emit("make:model", root, files, *force, *dryRun, stdout); err != nil {
		return err
	}
	if *dryRun {
		return nil
	}

	fmt.Fprint(stdout, modelWiring(spec, parts.Migration))
	return nil
}

// modelWiring is the part of this command that matters most: it names, in one
// screen, the difference between the data side of an entity and a whole
// feature.
func modelWiring(m gen.Module, migration bool) string {
	out := fmt.Sprintf(`
A model here is data plus its Hesape Model entry point. Rows returned by a query
carry their connection and can be saved again. Application use cases reach that
persistence through a Service, after a Policy issues a security.Grant.

What writes the whole feature:

    aru make:module %s --fields "..."
        a Model-backed entity, policy, service, request, controller, migration,
        four screens and test

    aru make:policy %s
        the policy alone, for a use-case path written by hand

A Repository remains available for a complex query, read model or report; the
generated CRUD path uses the Model directly.

--all writes the data side of the entity -- migration, factory, seeder, policy
and request. It is not a smaller make:module: the controller, the service, the
screens and the route wiring are the feature, and this is the entity.
`, m.Name, m.Name)

	if migration {
		out += fmt.Sprintf(`
The migration is written and not applied. Nothing has to list it: %s registers
itself in its own init, in database/migrations. What it needs is to be linked --
something has to import that package, or Go leaves it, and its init, out of the
binary.

Then:

    aru migrate
`, m.MigrationType())
	}
	return out
}
