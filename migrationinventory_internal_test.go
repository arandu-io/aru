package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/aru/internal/gen"
)

// The migration id is immutable once the migration has been applied, so every
// question the generator asks about database/migrations is asked before a file
// exists and answered once. What these tests pin is the answer for a directory
// that could not be read: it is a refusal, and never an empty inventory.

// brokenMigration is a file the Go parser refuses: it opens a type and never
// closes it, which is what a half-written migration looks like. What it declares
// cannot be read, and a declaration nobody read is a name nothing collides with.
const brokenMigration = "2026_08_20_000001_create_invoices_table.go"

// brokenMigrationSource declares CreateInvoicesTable, which is the type
// create_invoices_table would be written under. The collision is real; only the
// reading of it can fail.
const brokenMigrationSource = "package migrations\n\ntype CreateInvoicesTable struct {\n"

// projectWithABrokenMigration writes a project whose database/migrations holds
// one file that does not parse, and answers its root.
func projectWithABrokenMigration(t *testing.T) string {
	t.Helper()

	root := bareProject(t)
	dir := filepath.Join(root, "database", "migrations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, brokenMigration), []byte(brokenMigrationSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// migrationsSnapshot is every file of database/migrations and its bytes.
func migrationsSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()

	dir := filepath.Join(root, "database", "migrations")
	snapshot := map[string]string{}
	for _, name := range migrationsIn(t, root) {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		snapshot[name] = string(body)
	}
	return snapshot
}

// migrationsUnchanged fails when database/migrations no longer holds what the
// snapshot recorded.
//
// The error coming back proves only that the command answered. What has to be
// true is that it wrote nothing: a refusal that left a second file declaring one
// type behind is the same defect, reported.
func migrationsUnchanged(t *testing.T, root string, before map[string]string) {
	t.Helper()

	after := migrationsSnapshot(t, root)
	for name, body := range after {
		was, existed := before[name]
		switch {
		case !existed:
			t.Errorf("the command refused and wrote %s anyway", name)
		case was != body:
			t.Errorf("the command refused and rewrote %s", name)
		}
	}
	for name := range before {
		if _, still := after[name]; !still {
			t.Errorf("the command refused and removed %s", name)
		}
	}
}

// TestResolveMigrationIDRefusesAnInventoryThatDoesNotParse.
//
// The id is decided before any file exists, so the reading behind it is the last
// point at which a declaration nobody saw can still be noticed. A file the parser
// refused is not an absence: what it declares is unknown, and minting a fresh id
// over it writes a second file declaring the same type -- which does not compile,
// and whose error names a file the developer never touched.
func TestResolveMigrationIDRefusesAnInventoryThatDoesNotParse(t *testing.T) {
	root := projectWithABrokenMigration(t)

	m, err := resolveMigrationID(root, gen.Module{Name: "invoice", ModulePath: "example.test/project"})
	if err == nil {
		t.Fatalf("minted %s_%06d over an inventory it could not read", m.Date, m.Sequence)
	}
	if !strings.Contains(err.Error(), brokenMigration) {
		t.Errorf("the refusal does not name the file the developer has to fix: %v", err)
	}
}

// TestEveryCommandThatWritesAMigrationRefusesAnInventoryItCannotRead.
//
// Three commands write a migration and all three take the id from one reading of
// database/migrations, so a refusal in one of them and not in the others is the
// same defect left in two thirds of the places it can happen. Each is checked for
// what it left on disk as well as for what it returned.
func TestEveryCommandThatWritesAMigrationRefusesAnInventoryItCannotRead(t *testing.T) {
	for _, c := range []struct {
		command string
		run     func(args []string, stdout, stderr io.Writer) error
		args    []string
	}{
		{"make:migration", makeMigration, []string{"create_invoices_table", "--fields", "reference:string"}},
		{"make:module", makeModule, []string{"invoice", "--fields", "reference:string"}},
		{"make:model", makeModel, []string{"Invoice", "--fields", "reference:string", "--migration"}},
	} {
		t.Run(c.command, func(t *testing.T) {
			root := projectWithABrokenMigration(t)
			before := migrationsSnapshot(t, root)
			t.Chdir(root)

			var out, errOut strings.Builder
			err := c.run(c.args, &out, &errOut)
			if err == nil {
				t.Fatalf("%s wrote over an inventory it could not read:\n%s", c.command, out.String())
			}
			if !strings.Contains(err.Error(), brokenMigration) {
				t.Errorf("the refusal does not name the file the developer has to fix: %v", err)
			}
			migrationsUnchanged(t, root, before)
		})
	}
}

// TestForceDoesNotOverwriteTheBrokenFileItWritesASecondOneBesideIt.
//
// --force means "overwrite an existing migration file", and refusing it reads
// backwards until you see which file it would have written. The id about to be
// minted carries today's date; the file that does not parse carries the date it
// was written on. So --force never reaches that file -- what it produces is a
// second one beside it, declaring the same type, which is the shape the reading
// exists to stop.
func TestForceDoesNotOverwriteTheBrokenFileItWritesASecondOneBesideIt(t *testing.T) {
	root := projectWithABrokenMigration(t)
	before := migrationsSnapshot(t, root)
	t.Chdir(root)

	var out, errOut strings.Builder
	err := makeMigration([]string{"create_invoices_table", "--fields", "reference:string", "--force"}, &out, &errOut)
	if err == nil {
		t.Fatalf("--force wrote over an inventory it could not read:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), brokenMigration) {
		t.Errorf("the refusal does not name the file the developer has to fix: %v", err)
	}
	migrationsUnchanged(t, root, before)
}

// TestMakeMigrationStillMintsAnIDOverAnInventoryThatParses.
//
// A guard that refuses everything is not a guard. The same directory, holding a
// file the parser reads, has to answer the sequence and let the command write --
// and the sequence has to come off that file rather than off the clock, which is
// the reason the directory is read at all.
func TestMakeMigrationStillMintsAnIDOverAnInventoryThatParses(t *testing.T) {
	root := bareProject(t)
	dir := filepath.Join(root, "database", "migrations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Today's date, because the sequence is per day: a file written yesterday
	// occupies no number today, so a fixed date would prove nothing about the
	// number this command picks.
	today := time.Now().UTC().Format("2006_01_02")
	occupied := today + "_000004_create_users_table.go"
	if err := os.WriteFile(filepath.Join(dir, occupied),
		[]byte("package migrations\n\ntype CreateUsersTable struct{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	var out, errOut strings.Builder
	if err := makeMigration([]string{"create_invoices_table", "--fields", "reference:string"}, &out, &errOut); err != nil {
		t.Fatalf("make:migration: %v\n%s", err, errOut.String())
	}

	want := today + "_000005_create_invoices_table.go"
	names := migrationsIn(t, root)
	if len(names) != 2 || names[0] != occupied || names[1] != want {
		t.Fatalf("database/migrations holds %v, want %s beside %s", names, want, occupied)
	}
}

// TestAMigrationsDirectoryThatIsNotThereIsTheOneRealAbsence.
//
// Everything else that cannot be read is refused, and this one is not: a project
// with no migrations yet has nothing to count and nothing to collide with, so the
// empty answer is the true one. It is written down because it is the single
// exception, and an exception nobody pinned is the one a later reading widens.
func TestAMigrationsDirectoryThatIsNotThereIsTheOneRealAbsence(t *testing.T) {
	root := bareProject(t)

	inv, err := readMigrationInventory(filepath.Join(root, "database", "migrations"))
	if err != nil {
		t.Fatalf("a project with no migrations was refused: %v", err)
	}
	if n := inv.nextSequence("2026_08_20"); n != 1 {
		t.Errorf("the first migration of a day takes sequence %d, want 1", n)
	}
	if where, taken := inv.declaredBy("CreateInvoicesTable", ""); taken {
		t.Errorf("a name nobody declares was reported as taken, in %s", where)
	}
}

// TestADirectoryThatCannotBeReadIsNotAnEmptyOne.
//
// os.ReadDir failing and the directory not being there are two answers, and only
// one of them is an absence. Reading the first as the second hands the command an
// inventory with nothing in it, so every name is free and every sequence is one,
// over a directory that may hold both.
func TestADirectoryThatCannotBeReadIsNotAnEmptyOne(t *testing.T) {
	root := projectWithABrokenMigration(t)
	dir := filepath.Join(root, "database", "migrations")

	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	// Restored whatever happens, or the temporary directory cannot be removed.
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if _, err := os.ReadDir(dir); err == nil {
		t.Skip("this filesystem reads a directory with no permission bits")
	}

	if _, err := readMigrationInventory(dir); err == nil {
		t.Fatal("a directory that could not be read answered as an empty one")
	}
}
