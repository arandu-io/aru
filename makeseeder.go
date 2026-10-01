package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/arandu-io/aru/internal/gen"
)

// makeSeeder writes one seeder into database/seeders.
//
// DatabaseSeeder is the entry point, the others are called by it, and a name on
// the command line runs one of them.
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

	// `aru make:seeder InvoiceSeeder` is what people type, and the suffix is not
	// duplicated.
	entity := unsuffixed(name, "Seeder")
	spec := gen.SeederSpec{Entity: entity, Factory: seedsThroughFactory(root, entity)}
	if spec.Factory {
		if spec.ModulePath, err = readModulePath(root); err != nil {
			return err
		}
	}

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

// seedsThroughFactory reports whether the entity has what a seeder needs to
// create rows through its factory: the model, the policy whose action it names,
// and the factory. Missing any of them, the seeder is the empty one, because a
// file naming a package member that is not there does not compile.
func seedsThroughFactory(root, entity string) bool {
	for _, path := range []string{
		filepath.Join(root, "app", "Models", entity+".go"),
		filepath.Join(root, "app", "Policies", entity+"Policy.go"),
		filepath.Join(root, "database", "factories", entity+"Factory.go"),
	} {
		if _, err := os.Stat(path); err != nil {
			return false
		}
	}
	return true
}

// wiringSeeder prints the registration, and does not perform it: seeders.go has
// no custom block, so a patch would edit code somebody wrote by hand.
//
// The seeder is named positionally, because that is the only spelling db:seed
// accepts: it refuses `--class=` with the word to type instead. A message that
// printed the flag would end by sending the reader to a command that answers an
// error rather than running what was just written.
func wiringSeeder(s gen.SeederSpec) string {
	return fmt.Sprintf(`
It runs nothing yet, and it is not addressable: `+"`aru db:seed %s`"+`
answers "unknown seeder" until it is listed. Two lines, by hand:

  database/seeders/seeders.go -- in the registry

      %s{},

  database/seeders/DatabaseSeeder.go -- in the list Run walks, if it should run
  by default and in which order

      %s{},

A seeder writes through the factory, and the factory through the database. The
connection arrives through Deps, which is explicit for the same reason the rest
of the wiring is -- once, for every seeder, and not again:

  database/seeders/seeders.go -- the field

      DB *data.DB

  bootstrap/migrate.go -- where seeders.Run is called, with the connection
  bootstrap/app.go opened, returned on App as DB

      seeders.Deps{Users: app.Users, Tenant: cfg.Auth.Tenant, DB: app.DB}

Then:

    aru db:seed %s
`, s.Type(), s.Type(), s.Type(), s.Type())
}
