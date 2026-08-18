package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/arandu-io/aru/internal/gen"
)

// makeMigration writes one migration.
//
// It writes one migration, with one difference from the usual shape that is not
// cosmetic:
// --fields is required. See the flag's own error message -- an empty Up applies
// nothing and is still recorded as applied, and a migration id is immutable.
func makeMigration(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:migration", flag.ContinueOnError)
	fs.SetOutput(stderr)
	create := fs.String("create", "", "the table this migration creates")
	table := fs.String("table", "", "the existing table this migration adds columns to")
	fields := fs.String("fields", "", `the columns, as "name:type" separated by commas`)
	tenant := fs.Bool("tenant", false, "the table belongs to a tenant: compose the keys with tenant_id")
	force := fs.Bool("force", false, "overwrite an existing migration file")
	dryRun := fs.Bool("dry-run", false, "print what would be written, and write nothing")

	name, args := takeName(args)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:migration: %w", err)
	}
	if name == "" {
		return fmt.Errorf("usage: aru make:migration <name> [--create=<table> | --table=<table>] --fields \"status:string,paid_at:timestamp\"")
	}
	// The argument names the file here, not a class, so it is not normalized: it
	// is written in snake_case.
	if !isMigrationName(name) {
		return fmt.Errorf("make:migration: %q must be lowercase letters, digits and underscore, starting with a letter -- for example create_invoices_table or add_status_to_invoices", name)
	}
	if *create != "" && *table != "" {
		return fmt.Errorf("make:migration: --create and --table ask for two different migrations; pick one")
	}

	target, creating := *create, *create != ""
	if *table != "" {
		target, creating = *table, false
	}
	if target == "" {
		// The rule is: the flag always wins, and the
		// name is only read when there is no flag.
		guessed, guessedCreate, ok := guessTable(name)
		if !ok {
			return fmt.Errorf("make:migration: %q does not say which table it is about. "+
				"Add --create=<table> or --table=<table>", name)
		}
		target, creating = guessed, guessedCreate
	}

	parsed, err := gen.ParseFields(*fields)
	if err != nil {
		return fmt.Errorf("make:migration: a migration with an empty Up applies nothing and is still recorded as applied, "+
			"and a migration id is immutable -- it would never run again: %w", err)
	}
	if !creating {
		for _, f := range parsed {
			if f.Required {
				return fmt.Errorf("make:migration: column %q is declared required: a NOT NULL column added to a table that has rows "+
					"fails on every row already there, and during a rollout the previous binary does not fill it in. "+
					"Add it nullable, backfill, and tighten it in a later migration", f.Name)
			}
		}
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}

	date := time.Now().UTC().Format("2006_01_02")
	seq, err := nextMigrationSequence(root, date)
	if err != nil {
		return err
	}

	modulePath, err := readModulePath(root)
	if err != nil {
		return fmt.Errorf("make:migration: %w", err)
	}

	spec := gen.MigrationSpec{
		ID:     fmt.Sprintf("%s_%06d_%s", date, seq, name),
		Type:   migrationType(name),
		Table:  target,
		Tenant: *tenant,
		Fields: parsed,
		Create: creating,
	}

	// A specification error must never become broken code, so both collisions are
	// checked before anything is written. The second one matters more than it
	// looks: two types of one name in database/migrations simply do not compile,
	// and the error would name a file the developer did not touch.
	if !*force {
		if _, err := os.Stat(filepath.Join(root, spec.Path())); err == nil {
			return fmt.Errorf("make:migration: %s already exists", spec.Path())
		}
	}
	if where, taken := migrationTypeAlreadyDeclared(filepath.Join(root, "database", "migrations"), spec.Type, spec.ID); taken {
		return fmt.Errorf("make:migration: database/migrations already declares %s, in %s -- pick another name", spec.Type, where)
	}

	file, err := gen.RenderMigration(spec)
	if err != nil {
		return fmt.Errorf("make:migration: %w", err)
	}
	if err := emit("make:migration", root, []gen.File{file}, *force, *dryRun, stdout); err != nil {
		return err
	}
	if *dryRun {
		return nil
	}

	fmt.Fprint(stdout, wiringMigration(spec, modulePath))
	return nil
}

// wiringMigration says the one thing that fails silently.
func wiringMigration(s gen.MigrationSpec, modulePath string) string {
	return fmt.Sprintf(`
%s is written and not applied. Nothing has to list it: the init in the file
registers it, under the name GetName returns, and that name is also its order.

What still fails silently is linking. Go leaves a package nobody imports out of
the binary, and an init that is not in the binary never runs -- so something has
to import database/migrations. A blank import is enough where nothing else does:

  main.go

      _ %q

Then:

    aru migrate
`, s.Type, modulePath+"/database/migrations")
}

// migrationName is the conventional shape: snake_case, describing the change.
var migrationName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func isMigrationName(s string) bool { return migrationName.MatchString(s) }

// migrationType turns the file name into the type name: add_status_to_invoices
// becomes AddStatusToInvoices.
func migrationType(name string) string { return gen.Exported(name) }

// createPattern and changePattern guess the table from the name, which is kept
// because it is real parity of gesture: it is the way the name is typed.
var (
	createPattern = regexp.MustCompile(`^create_(\w+?)(_table)?$`)
	changePattern = regexp.MustCompile(`_(?:to|from|in)_(\w+?)(_table)?$`)
)

// guessTable reads the table out of the migration name.
// It only guesses: when the name does not match, the caller asks for the flag
// rather than picking a table.
func guessTable(name string) (table string, create bool, ok bool) {
	if m := createPattern.FindStringSubmatch(name); m != nil {
		return m[1], true, true
	}
	if m := changePattern.FindStringSubmatch(name); m != nil {
		return m[1], false, true
	}
	return "", false, false
}

// migrationID is the shape of every migration file name: a date and a sequence.
var migrationID = regexp.MustCompile(`^(\d{4}_\d{2}_\d{2})_(\d{6})_`)

// nextMigrationSequence returns the six-digit half of today's migration id.
//
// The order of migrations is the order of their ids, so two files written on one
// day need two numbers. It comes from the directory rather than from the clock:
// a timestamp to the second collides when two commands run in the same second,
// and a number read off the files is the same number on every machine, which is
// what makes the golden files mean something.
func nextMigrationSequence(root, date string) (int, error) {
	dir := filepath.Join(root, "database", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 1, nil
		}
		return 0, err
	}

	highest := 0
	for _, e := range entries {
		m := migrationID.FindStringSubmatch(e.Name())
		if m == nil || m[1] != date {
			continue
		}
		n, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		if n > highest {
			highest = n
		}
	}
	return highest + 1, nil
}

// migrationTypeAlreadyDeclared reports which file of database/migrations already
// declares a package-level name of that spelling. The file being written is
// skipped, so regenerating with --force is not a collision with itself.
//
// Both type and value declarations are read. The migration is a type now, and a
// type is what collides with it -- but a project that still holds a value of the
// name from the shape before it would otherwise be told to write a file that
// does not compile, and the error would name neither file.
func migrationTypeAlreadyDeclared(dir, name, self string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".go" || e.Name() == self+".go" {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, e.Name()), nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.Name == name {
						return e.Name(), true
					}
				case *ast.ValueSpec:
					for _, ident := range s.Names {
						if ident.Name == name {
							return e.Name(), true
						}
					}
				}
			}
		}
	}
	return "", false
}
