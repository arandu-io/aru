package main

import (
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
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
	if err := requireListing("make:module", root); err != nil {
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
	// wiring is not. A generator that edited routes/web.go and the provider behind
	// your back would be a generator you cannot read the output of -- the whole
	// point of explicit wiring is that the file says what the application is.
	fmt.Fprint(stdout, wiring(root, spec, len(written)))
	return nil
}

// wiring is what to do with the files that were just written.
//
// It is a function so it can be tested: an instruction that does not compile is
// worse than no instruction, because it is followed. root is the project the
// lines are pasted into; the imports they need are read against its files, and
// an empty root prints every one of them.
func wiring(root string, m gen.Module, count int) string {
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

  app/Providers/AppServiceProvider.go -- in the routes.Deps literal the
  provider builds, the one bootstrap/app.go hands to WithRouting

      %s: controllers.New%s(%s),

  database/seeders/seeders.go -- in the registry, and in DatabaseSeeder's
  list if it should run by default

      %sSeeder{},
%s%s%s
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
		importsNote(wiringImports(root, m)),
		nestedNote(m),
		tenantClaim(m),
		m.MigrationType())
}

// wiringImport is one package a printed line names: the file the line goes
// in, the name the line calls it by, and the import that binds that name.
type wiringImport struct {
	file, name, path string
}

// line is the import as it is pasted: with the name when the path's last
// element does not already say it, which is the case for every directory of
// app/, whose package clauses are lowercase.
func (w wiringImport) line() string {
	if path.Base(w.path) == w.name {
		return strconv.Quote(w.path)
	}
	return w.name + " " + strconv.Quote(w.path)
}

// wiringImports are the packages the printed lines name that the files they
// go in do not bind yet, read from those files.
//
// A name a file already binds is left out, whatever path binds it. The
// skeleton's routes/web.go imports hesape's middleware, and a project may
// import another package under that name; printing a second import of the same
// name is the line that answers "middleware redeclared in this block" when it
// is pasted. A file that cannot be read gets every import printed: nothing
// says what it holds.
func wiringImports(root string, m gen.Module) []wiringImport {
	wanted := []wiringImport{
		{"routes/web.go", "middleware", "github.com/arandu-io/hesape/http/middleware"},
		{"routes/web.go", "controllers", m.ModulePath + "/app/Http/Controllers"},
		{"app/Providers/AppServiceProvider.go", "controllers", m.ModulePath + "/app/Http/Controllers"},
		{"app/Providers/AppServiceProvider.go", "services", m.ModulePath + "/app/Services"},
	}
	bound := map[string]map[string]bool{}
	var out []wiringImport
	for _, w := range wanted {
		names, ok := bound[w.file]
		if !ok {
			names = importedNames(root, w.file)
			bound[w.file] = names
		}
		if !names[w.name] {
			out = append(out, w)
		}
	}
	return out
}

// importedNames is every name the imports of root/rel bind, or none when the
// file cannot be read or parsed. An import without a name binds its package's
// name, taken as the lowercase last element of the path, which is how every
// package of a project and of the framework is declared.
func importedNames(root, rel string) map[string]bool {
	names := map[string]bool{}
	if root == "" {
		return names
	}
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, filepath.FromSlash(rel)), nil, parser.ImportsOnly)
	if err != nil {
		return names
	}
	for _, spec := range f.Imports {
		p, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := strings.ToLower(path.Base(p))
		if spec.Name != nil {
			name = spec.Name.Name
		}
		names[name] = true
	}
	return names
}

// importsNote says which imports the printed lines need, by file, and nothing
// when every name they use is already bound where they go.
func importsNote(missing []wiringImport) string {
	if len(missing) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nThey name packages a file does not import yet:\n")
	file := ""
	for _, w := range missing {
		if w.file != file {
			file = w.file
			fmt.Fprintf(&b, "\n  %s\n\n", file)
		}
		fmt.Fprintf(&b, "      %s\n", w.line())
	}
	return b.String()
}

// serviceConstruction is how the provider builds the module's service:
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
