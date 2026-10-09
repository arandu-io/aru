package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/arandu-io/aru/internal/gen"
)

// makeModuleUsage is the usage line of make:module, in the dispatch table and in
// its refusal, naming every flag the command accepts.
const makeModuleUsage = `aru make:module <name> --fields "title:string!,amount:money" [--tenant] [--parent=<resource>] [--force] [--dry-run]`

// makeModule generates a module: Model-backed entity, policy, service, request,
// routes, handlers and tests.
//
// It calls no model. The same flags produce the same bytes, which is what makes
// the golden files in internal/gen a real test -- and what makes regeneration
// safe enough that people actually do it.
func makeModule(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:module", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fields := fs.String("fields", "", `the fields, as "name:type" separated by commas. Suffix ! for required, u for unique`)
	tenant := fs.Bool("tenant", false, "scope every query by the tenant in the Grant")
	parent := fs.String("parent", "", "nest the module under this resource, as the route table names it: projects")
	force := fs.Bool("force", false, "overwrite existing files, preserving the custom blocks")
	dryRun := fs.Bool("dry-run", false, "print what would be written, and write nothing")

	// The name comes first and the flags after it -- which is how everyone types
	// it, and what the flag package does not support: it stops parsing at the
	// first positional argument. So the name is taken off the front by hand.
	var name string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:module: %w", err)
	}
	if name == "" {
		if fs.NArg() == 0 {
			return fmt.Errorf("usage: %s\n%s", makeModuleUsage, gen.TypeList())
		}
		name = fs.Arg(0)
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}
	if err := requireModelCore("make:module", root); err != nil {
		return err
	}
	modulePath, err := readModulePath(root)
	if err != nil {
		return err
	}

	parsed, err := gen.ParseFields(*fields)
	if err != nil {
		return fmt.Errorf("make:module: %w", err)
	}

	module := gen.Module{
		Name:       name,
		Fields:     parsed,
		Tenant:     *tenant,
		ModulePath: modulePath,
		Parent:     strings.ToLower(strings.ReplaceAll(*parent, "_", "-")),
		Generator:  version,
		Gates:      gen.ProjectGates(root),
	}
	// A nested table stores the parent's id in a column of the type the
	// parent's key has, read off the parent's model and migration rather than
	// assumed: a text key in a UUID column is a write Postgres refuses.
	if module.Parent != "" {
		if module.ParentKey, err = gen.ParentKeyOf(root, module); err != nil {
			return fmt.Errorf("make:module: %w", err)
		}
	}

	// The migration id comes from resolveMigrationID, which is where every
	// command that writes a module's migration gets it: the next free sequence
	// of today, or the id this module's migration already has.
	spec, err := resolveMigrationID(root, module)
	if err != nil {
		return fmt.Errorf("make:module: %w", err)
	}

	files, err := gen.Generate(spec)
	if err != nil {
		return fmt.Errorf("make:module: %w", err)
	}

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

	// Say what happened and what is required next, without congratulating
	// anyone.
	//
	// Three lines, and none of them is optional: the code is written, the
	// wiring is not. A generator that edited routes/web.go and bootstrap/app.go behind
	// your back would be a generator you cannot read the output of -- the whole
	// point of explicit wiring is that the file says what the application is.
	fmt.Fprint(stdout, wiring(spec, len(written)))
	return nil
}

// wiring is what to do with the files that were just written.
//
// It is a function so it can be tested: an instruction that does not compile is
// worse than no instruction, because it is followed.
func wiring(m gen.Module, count int) string {
	return fmt.Sprintf(`
%s created, %d files.

The policy denies every action. Open what this module needs in
app/Policies/%s.go, inside the custom block, and nothing else -- that is what
makes the default safe.

Then, by hand, because the wiring is meant to be readable -- four lines:

  routes/web.go -- the field in Deps

      %s *controllers.%s

  routes/web.go -- the routes, in the custom block, behind the sign-in guard:
  the controller reads who is asking from what the guard puts on the request

      r.Group("", middleware.RequireAuth(d.Sessions)).Resource(%q, d.%s)

  bootstrap/app.go -- in the routes.Deps literal

      %s: controllers.New%s(%s),

  database/seeders/seeders.go -- in the registry, and in DatabaseSeeder's
  list if it should run by default

      %sSeeder{},

They name two packages a file may not import yet:
"github.com/arandu-io/framework/http/middleware" in routes/web.go, and
"%s/app/Services" in bootstrap/app.go.
%s%s
The migration is not one of them: %s registers itself in its own init, and
nothing lists it. What it needs is to be linked -- something has to import
database/migrations, or Go leaves the package, and its init, out of the binary.

Then:

    aru view:build
    aru migrate
`,
		m.Name, count,
		m.PolicyType(),
		m.Entity(), m.Controller(),
		m.RouteResource(), m.Entity(),
		m.Entity(), m.Controller(), serviceConstruction(m),
		m.Entity(),
		m.ModulePath,
		nestedNote(m),
		tenantClaim(m),
		m.MigrationType())
}

// serviceConstruction is how bootstrap/app.go builds the module's service:
// with the connection, and for a nested module with the service its parent is
// loaded through.
func serviceConstruction(m gen.Module) string {
	if m.Parent != "" {
		return fmt.Sprintf("services.New%s(db, services.New%s(db))", m.ServiceType(), m.ParentServiceType())
	}
	return fmt.Sprintf("services.New%s(db)", m.ServiceType())
}

// nestedNote says what a nested module leans on, and is empty for one that
// does not nest.
func nestedNote(m gen.Module) string {
	if m.Parent == "" {
		return ""
	}
	return fmt.Sprintf(`
The module nests under %[1]s. The listing, the form and the store answer at
/%[1]s/{%[2]s}/%[3]s and the record at /%[3]s/{%[4]s}. The service loads the
%[2]s through %[5]s.Get, under its own policy, and lists and creates by the
one it loaded -- so %[1]s has to be a module of its own, written first.
`, m.Parent, m.ParentParam(), m.Resource(), m.MemberParam(), m.ParentServiceType())
}

// tenantClaim is the line to paste for a module generated with --tenant, and it
// is empty for one generated without.
//
// The table carries a tenant column, and the suite that reads the catalogue
// fails until somebody states that every read of it is scoped. That failure is
// the check working: the generator writes the table and a person writes the
// claim, because the claim is the step where somebody reads the queries.
//
// So it is printed and never written. Filling the map in from here would answer
// the question the map exists to ask, and it is the same reason nothing else in
// this message edits a file for you.
func tenantClaim(m gen.Module) string {
	if !m.Tenant {
		return ""
	}
	return fmt.Sprintf(`
  tests/Feature/TenantScope_test.go -- the claim, once every read takes the tenant

      %q: "why every read of it is scoped",

The table has a tenant column, so that suite is red until the line is there. Add
it after the reads are scoped, not before.
`, m.Table())
}

// readModulePath reads the module path from the project's go.mod, because the
// generated test imports the module by path.
func readModulePath(root string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("reading go.mod: %w", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if after, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(after), nil
		}
	}
	return "", fmt.Errorf("go.mod has no module line")
}
