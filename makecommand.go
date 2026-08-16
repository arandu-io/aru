package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/arandu-io/aru/internal/gen"
)

// makeCommand writes one console command.
//
// What differs from the usual shape is how the command becomes reachable.
// Discovery scans app/Console/Commands, instantiates what it finds and reads a
// signature string at run time. Here the command is a value returned by
// Console() in routes/console.go, because nothing is discovered by reflection.
//
// The cost is one line pasted by hand, which this command prints. What it buys
// is that the console listing and the compiler read the same slice: a command
// that is not in it does not exist, and one that is in it with a broken
// signature does not build.
func makeCommand(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:command", flag.ContinueOnError)
	fs.SetOutput(stderr)
	signature := fs.String("signature", "", "what the person types: invoice:close. Derived from the name when left out")
	desc := fs.String("description", "", "the one line the console listing prints next to it")
	force := fs.Bool("force", false, "overwrite an existing command, preserving the custom block")
	dryRun := fs.Bool("dry-run", false, "print what would be written, and write nothing")

	name, args := takeName(args)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:command: %w", err)
	}
	if name == "" {
		return fmt.Errorf(`usage: aru make:command <Name> [--signature=invoice:close] [--description="..."] [--force]`)
	}
	if err := checkFlatTree("make:command", name); err != nil {
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

	spec := gen.Command{
		Name:        name,
		Signature:   *signature,
		Description: *desc,
		ModulePath:  modulePath,
	}
	files, err := gen.GenerateCommand(spec)
	if err != nil {
		return fmt.Errorf("make:command: %w", err)
	}
	if err := emit("make:command", root, files, *force, *dryRun, stdout); err != nil {
		return err
	}
	if *dryRun {
		return nil
	}

	// The wiring, printed rather than written. It is one line, in a file the
	// person reads, and a generator that edits routes/console.go behind their
	// back is a generator whose output nobody can account for.
	fmt.Fprintf(stdout, `
Add it to Console() in routes/console.go, inside the custom block:

    {
        Name:        %q,
        Description: %q,
        Run:         commands.New%s().Run,
    },

and the import:

    commands "%s/app/Console/Commands"

Then `+"`aru %s`"+` runs it.
`, spec.SignatureOrDefault(), spec.DescriptionOrDefault(), spec.Type(), modulePath, spec.SignatureOrDefault())
	return nil
}
