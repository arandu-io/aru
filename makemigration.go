package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
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

	fmt.Fprint(stdout, wiringMigration(spec, modulePath, migrationsAreLinked(root, modulePath)))
	return nil
}

// wiringMigration says the one thing that fails silently.
//
// linked is whether the project already imports database/migrations somewhere.
// It changes the message rather than being appended to it: a project generated
// by `aru new` has the import already, and telling that developer to add one
// sends them to write a line whose only effect is to be redundant.
func wiringMigration(s gen.MigrationSpec, modulePath string, linked bool) string {
	blank := fmt.Sprintf("_ %q", modulePath+"/database/migrations")

	link := fmt.Sprintf(`What still fails silently is linking. Go leaves a package nobody imports out of
the binary, and an init that is not in the binary never runs -- so something has
to import database/migrations, and nothing in this project does:

  bootstrap/app.go

      %s
`, blank)
	if linked {
		link = fmt.Sprintf(`Linking is already done: this project imports database/migrations, so the init
runs and the migration is in the binary. Nothing to add.

      %s
`, blank)
	}

	return fmt.Sprintf(`
%s is written and not applied. Nothing has to list it: the init in the file
registers it, under the name GetName returns, and that name is also its order.

%s
Then:

    aru migrate
`, s.Type, link)
}

// migrationsAreLinked reports whether any Go file under root imports the
// project's database/migrations package.
//
// It reads rather than assumes, because the answer differs per project: the
// skeleton links it in bootstrap/app.go, and a project that grew from something
// else may not link it at all. An instruction that guesses is one that sends
// half its readers to add a line they already have.
//
// A file it cannot read is treated as not importing: the cost of asking for an
// import that exists is a redundant line, and the cost of staying quiet when
// none exists is `aru migrate` finding nothing and saying so only by creating
// no tables.
func migrationsAreLinked(root, modulePath string) bool {
	want := []byte(strconv.Quote(modulePath + "/database/migrations"))
	found := false
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found {
			return nil //nolint:nilerr // an unreadable entry is not an answer, and the default is to ask.
		}
		if d.IsDir() {
			// The package that declares the migrations imports itself by no
			// path, and vendored trees are not this project.
			if d.Name() == "vendor" || d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err == nil && bytes.Contains(b, want) {
			found = true
		}
		return nil
	})
	return found
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
//
// It refuses rather than answering from a partial reading of the directory, and
// it is the first thing the three commands that write a migration do -- so an
// inventory that cannot be read whole stops them before an id exists.
func nextMigrationSequence(root, date string) (int, error) {
	inv, err := readMigrationInventory(filepath.Join(root, "database", "migrations"))
	if err != nil {
		return 0, err
	}
	return inv.nextSequence(date), nil
}

// migrationInventory is what database/migrations already holds: the highest
// sequence in use on each date, and the files declaring each package-level name.
type migrationInventory struct {
	highest  map[string]int
	declared map[string][]string
}

// readMigrationInventory reads database/migrations whole, or answers with an
// error naming the file it could not read.
//
// Everything it reports decides an identifier that is immutable once the
// migration has been applied, so a file left out of the reading is not an
// absence. A declaration nobody saw becomes a second file declaring one type,
// which does not compile and names a file the developer never touched; a
// sequence nobody saw becomes an identifier a file already carries. A directory
// that is not there is the one real absence, and the only one that answers
// empty.
//
// It repairs nothing. A file that does not parse is the developer's to fix or to
// move out of the directory, and rewriting it here would be this command editing
// a migration it did not write.
//
// Both type and value declarations are collected. The migration is a type now,
// and a type is what collides with it -- but a project still holding a value of
// the name from the shape before it would otherwise be told to write a file that
// does not compile, and the error would name neither file.
func readMigrationInventory(dir string) (migrationInventory, error) {
	inv := migrationInventory{highest: map[string]int{}, declared: map[string][]string{}}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return inv, nil
		}
		return inv, fmt.Errorf("reading %s: %w", dir, err)
	}

	for _, e := range entries {
		// The sequence is read off the name, so it counts whatever carries a
		// migration id -- a file the developer has not renamed to .go yet still
		// occupies its number.
		if m := migrationID.FindStringSubmatch(e.Name()); m != nil {
			n, err := strconv.Atoi(m[2])
			if err != nil {
				return inv, fmt.Errorf("%s carries a sequence that is not a number: %w", e.Name(), err)
			}
			if n > inv.highest[m[1]] {
				inv.highest[m[1]] = n
			}
		}

		if e.IsDir() || filepath.Ext(e.Name()) != ".go" {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, e.Name()), nil, parser.SkipObjectResolution)
		if err != nil {
			return inv, fmt.Errorf("%s does not parse, so what it declares cannot be read -- fix that file, "+
				"or move it out of %s: %w", e.Name(), dir, err)
		}
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					inv.declared[s.Name.Name] = append(inv.declared[s.Name.Name], e.Name())
				case *ast.ValueSpec:
					for _, ident := range s.Names {
						inv.declared[ident.Name] = append(inv.declared[ident.Name], e.Name())
					}
				}
			}
		}
	}
	return inv, nil
}

// nextSequence is the six-digit half of an id written on that date.
func (inv migrationInventory) nextSequence(date string) int { return inv.highest[date] + 1 }

// declaredBy names the file that already declares a package-level name of that
// spelling. The file being written is skipped, so regenerating with --force is
// not a collision with itself.
func (inv migrationInventory) declaredBy(name, self string) (string, bool) {
	for _, file := range inv.declared[name] {
		if file == self+".go" {
			continue
		}
		return file, true
	}
	return "", false
}

// resolveMigrationID fills in the date and sequence the module's migration has
// to carry, and it is the one place the three commands that write a module's
// migration decide that -- `aru generate`, `aru make:module` and
// `aru make:model --migration`. Written out three times it would drift, and the
// half that drifted would be the half nobody ran.
//
// A module with no migration yet takes the next free sequence of today, so two
// modules generated on one day cannot land on one number with nothing to order
// them.
//
// A module that already has a migration takes that file's date and sequence
// back. A migration id is immutable -- it is the key the applied-migrations
// table records -- so minting a second one leaves database/migrations with two
// files declaring one type, which does not compile and names a file the
// developer never touched, and leaves a migration recorded as applied under an
// id no file carries any more. Regenerating has to land on the file already
// there rather than beside it, and across a change of date as much as within one
// day.
//
// A declaration under a name that is not a migration id is the case nothing can
// resolve: the module can take neither the name nor the file. It is refused here
// rather than written.
func resolveMigrationID(root string, m gen.Module) (gen.Module, error) {
	date := time.Now().UTC().Format("2006_01_02")
	seq, err := nextMigrationSequence(root, date)
	if err != nil {
		return m, err
	}
	m.Date, m.Sequence = date, seq

	where, taken := migrationTypeAlreadyDeclared(filepath.Join(root, "database", "migrations"), m.MigrationType(), "")
	if !taken {
		return m, nil
	}

	id := migrationID.FindStringSubmatch(where)
	if id == nil {
		return m, fmt.Errorf("database/migrations already declares %s, in %s -- rename that declaration, or %s cannot be regenerated",
			m.MigrationType(), where, m.Name)
	}
	if seq, err = strconv.Atoi(id[2]); err != nil {
		return m, fmt.Errorf("%s carries a sequence that is not a number: %w", where, err)
	}

	m.Date, m.Sequence = id[1], seq
	return m, nil
}

// migrationTypeAlreadyDeclared reports which file of database/migrations already
// declares a package-level name of that spelling. The file being written is
// skipped, so regenerating with --force is not a collision with itself.
//
// It answers from what it could read, and answers "nobody" for a directory it
// could not read at all. That is safe only in the order the callers use: the
// sequence is read first, off the same directory, and that reading refuses an
// inventory it cannot take whole -- so by the time this is asked, every file in
// there has already parsed.
func migrationTypeAlreadyDeclared(dir, name, self string) (string, bool) {
	inv, err := readMigrationInventory(dir)
	if err != nil {
		return "", false
	}
	return inv.declaredBy(name, self)
}
