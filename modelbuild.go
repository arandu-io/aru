package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/arandu-io/aru/internal/modelbuild"
)

// modelBuild writes the generated half of every entity in the module: the
// query file beside each struct embedding model.Model, and the typed factory
// of each entity that has one.
//
// It works on a module root as well as on a project, because a module that
// ships entities -- a package other applications import -- generates the same
// files, and the command that writes them has to be the one that runs there.
//
// --check writes nothing and fails when anything would change, for a pipeline
// that wants to know the committed files are the ones the source produces.
func modelBuild(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("model:build", flag.ContinueOnError)
	flags.SetOutput(stderr)
	check := flags.Bool("check", false, "write nothing, and exit 1 when a generated file is missing, stale or orphaned")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("model:build: %w", err)
	}

	root, err := moduleRoot()
	if err != nil {
		return err
	}
	if *check {
		changes, err := modelbuild.Plan(root)
		if err != nil {
			return fmt.Errorf("model:build: %w", err)
		}
		if len(changes) == 0 {
			return nil
		}
		for _, c := range changes {
			fmt.Fprintf(stdout, "%s is %s\n", c.Path, c.Why)
		}
		return fmt.Errorf("model:build --check: %d generated file(s) out of date; run `aru model:build`", len(changes))
	}
	return buildModels(root, stdout, stderr)
}

// buildModels brings the generated files of the module rooted at root up to
// date, and names each file it wrote or removed.
//
// It is what build, serve and dev run before the views, because the query
// files are Go the application imports: a build that compiled before writing
// them would compile against yesterday's entities. It is silent when nothing
// changes, and does nothing at all in a module with no entity.
func buildModels(root string, stdout, _ io.Writer) error {
	changes, err := modelbuild.Plan(root)
	if err != nil {
		return fmt.Errorf("model:build: %w", err)
	}
	if err := modelbuild.Apply(root, changes); err != nil {
		return fmt.Errorf("model:build: %w", err)
	}
	for _, c := range changes {
		verb := "wrote"
		if c.Content == nil {
			verb = "removed"
		}
		fmt.Fprintf(stdout, "%s %s\n", verb, c.Path)
	}
	return nil
}
