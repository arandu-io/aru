package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/arandu-io/aru/internal/gen"
)

// makePolicyUsage is the usage line of make:policy, in the dispatch table and in
// its refusal, naming every flag the command accepts.
const makePolicyUsage = `aru make:policy <module> [--force] [--dry-run]`

// makePolicy writes the policy of a module that does not have one.
//
// It exists for the module written before the generator, and for the one whose
// policy was deleted -- both of which `aru doctor` reports as an entity nobody
// decided who may reach. This is the fix it points at.
func makePolicy(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:policy", flag.ContinueOnError)
	fs.SetOutput(stderr)
	force := fs.Bool("force", false, "overwrite an existing policy, preserving the custom block")
	dryRun := fs.Bool("dry-run", false, "print what would be written, and write nothing")

	var name string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:policy: %w", err)
	}
	if name == "" {
		return fmt.Errorf("usage: %s", makePolicyUsage)
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}
	modulePath, err := readModulePath(root)
	if err != nil {
		return err
	}

	// The entity is what the policy is about, and app/Models is where it lives.
	// Asking for a policy about something that does not exist is almost always a
	// typo, and finding out here beats finding out from a file nobody references.
	// Normalized here rather than left as given, because this name is echoed
	// back in the suggestion below and make:module takes the lowercase form. The
	// entity check runs before the name is validated, so an argument in any
	// shape reaches that line.
	spec := gen.Module{
		Name:       gen.Normalize(name),
		Fields:     []gen.Field{{Name: "placeholder", Type: gen.TypeString}},
		ModulePath: modulePath,
	}
	entity := filepath.Join(root, "app", "Models", spec.Entity()+".go")
	if _, err := os.Stat(entity); err != nil {
		return errors.New(missingEntity(spec))
	}

	// The tenant is inferred from what the project already says about the
	// entity, rather than asked for again: two sources of truth about one
	// decision is how they drift.
	spec.Tenant, err = entityBelongsToATenant(entity, spec.Entity(),
		filepath.Join(root, "app", "Repositories", spec.RepositoryType()+".go"))
	if err != nil {
		return fmt.Errorf("make:policy: %w", err)
	}

	files, err := gen.Generate(spec)
	if err != nil {
		return fmt.Errorf("make:policy: %w", err)
	}

	for _, f := range files {
		if filepath.Dir(f.Path) != filepath.Join("app", "Policies") {
			continue
		}
		if *dryRun {
			return emit("make:policy", root, []gen.File{f}, *force, true, stdout)
		}
		written, skipped, err := gen.Write(root, []gen.File{f}, *force)
		if err != nil {
			return err
		}
		if len(skipped) > 0 {
			return fmt.Errorf("%s already exists; rerun with --force to regenerate it (the custom block is preserved)", f.Path)
		}
		fmt.Fprintln(stdout, "created", written[0])
		fmt.Fprintf(stdout, `
The policy denies every action. Open what this module needs inside the custom
block, and nothing else -- that is what makes the default safe.
`)
		return nil
	}
	return fmt.Errorf("make:policy: no policy was generated")
}

// entityBelongsToATenant reports whether the policy of entity has to compare
// the row's tenant with the subject's.
//
// The model answers first, because every module has one: a struct carrying
// TenantID is a row that belongs to one tenant, and TenantID is the field the
// policy template compares. A module the generator writes today has no
// repository, so reading only the repository answered no for every one of
// them, and a policy regenerated with --force lost the check it was written
// with.
//
// The repository answers second, for a module written before the model was
// the data entry point, whose queries filter by the tenant through the Grant.
//
// A model that cannot be read is an error rather than a no. Guessing no would
// write the policy without the check, which is the one outcome this function
// exists to prevent.
func entityBelongsToATenant(modelPath, entity, repositoryPath string) (bool, error) {
	_, tenant, err := gen.FieldsFromModel(modelPath, entity)
	if err != nil {
		return false, fmt.Errorf("reading whether %s belongs to a tenant: %w", entity, err)
	}
	return tenant || repositoryUsesTenant(repositoryPath), nil
}

// repositoryUsesTenant reports whether the repository already scopes by tenant,
// so the generated policy matches what the queries do.
//
// A policy that forgot the tenant check on a repository that filters by it is
// the one combination that looks safe and is not: every query is scoped, so
// nothing leaks until somebody adds a query that is not.
func repositoryUsesTenant(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(b), "data.Tenant(g)")
}

// missingEntity is what make:policy says when the model it is about is not
// there, and it is a function so it can be tested.
//
// The suggestion has to run as printed. It carries --fields because make:module
// refuses without it, and the module name rather than the entity because
// make:module takes the lowercase form. A fix printed in a shape the tool
// rejects is a second error for whoever copies it.
func missingEntity(m gen.Module) string {
	return fmt.Sprintf("app/Models/%s.go does not exist -- create the module with `aru make:module %s --fields %q`",
		m.Entity(), m.Name, "name:string!")
}
