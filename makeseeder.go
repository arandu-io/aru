package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/arandu-io/aru/internal/gen"
)

// makeSeeder writes one seeder.
//
// It is `php artisan make:seeder`. The role is Laravel's -- DatabaseSeeder is
// the entry point, the others are called by it, and --class runs one -- and the
// vocabulary is identical on purpose.
func makeSeeder(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:seeder", flag.ContinueOnError)
	fs.SetOutput(stderr)
	force := fs.Bool("force", false, "overwrite an existing seeder, preserving the custom block")
	dryRun := fs.Bool("dry-run", false, "print what would be written, and write nothing")

	name, args := takeName(args)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:seeder: %w", err)
	}
	if name == "" {
		return fmt.Errorf("usage: aru make:seeder <Name> [--force]")
	}
	if err := checkFlatTree("make:seeder", name); err != nil {
		return err
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}

	// `aru make:seeder InvoiceSeeder` is what the Laravel developer types, and
	// artisan does not duplicate the suffix either.
	spec := gen.SeederSpec{Entity: unsuffixed(name, "Seeder")}

	file, err := gen.RenderSeeder(spec)
	if err != nil {
		return fmt.Errorf("make:seeder: %w", err)
	}
	if err := emit("make:seeder", root, []gen.File{file}, *force, *dryRun, stdout); err != nil {
		return err
	}
	if *dryRun {
		return nil
	}

	fmt.Fprint(stdout, wiringSeeder(spec))
	return nil
}

// wiringSeeder prints the registration, and does not perform it: seeders.go has
// no custom block, so a patch would edit code somebody wrote by hand.
func wiringSeeder(s gen.SeederSpec) string {
	return fmt.Sprintf(`
It runs nothing yet, and it is not addressable: `+"`aru db:seed --class=%s`"+`
answers "unknown seeder" until it is listed. Two lines, by hand:

  database/seeders/seeders.go -- in the registry

      %s{},

  database/seeders/DatabaseSeeder.go -- in the list Run walks, if it should run
  by default and in which order

      %s{},

A seeder writes, so it needs a repository and a Grant. The repository arrives
through Deps, which is explicit for the same reason the rest of the wiring is:

  database/seeders/seeders.go -- the field

      %s *repositories.%sRepository

  main.go -- where seeders.Run is called

      seeders.Run(ctx, seeders.Deps{Auth: app.Auth, Tenant: cfg.Auth.Tenant,
          %s: repositories.New%sRepository(db)}, args)

Then:

    aru db:seed --class=%s
`, s.Type(), s.Type(), s.Type(), s.Plural(), s.Entity, s.Plural(), s.Entity, s.Type())
}
