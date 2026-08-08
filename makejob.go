package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/arandu-io/aru/internal/gen"
)

// makeJob writes one background job.
//
// It writes a background job. There is no --sync: one queue (RULE 9), and a
// synchronous job is a function call. There is no --queue either -- the queue is
// a constant in the generated file, and whoever chooses one at push time already
// has fwjobs.New(g, queue, ...).
func makeJob(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:job", flag.ContinueOnError)
	fs.SetOutput(stderr)
	eventName := fs.String("event-name", "", "the routing key stored in Job.Name (default: derived from the type)")
	fields := fs.String("fields", "", `the payload, as "name:type" separated by commas`)
	force := fs.Bool("force", false, "overwrite an existing job, preserving the custom blocks")
	dryRun := fs.Bool("dry-run", false, "print what would be written, and write nothing")

	name, args := takeName(args)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:job: %w", err)
	}
	if name == "" {
		return fmt.Errorf(`usage: aru make:job <Name> [--event-name=invoice.send] [--fields "invoice_id:uuid"] [--force]`)
	}
	if err := checkFlatTree("make:job", name); err != nil {
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

	spec := gen.JobSpec{Type: gen.Exported(name), ModulePath: modulePath}
	spec.EventName = *eventName
	if spec.EventName == "" {
		spec.EventName = gen.DefaultEventName(spec.Type)
	}
	if *fields != "" {
		parsed, err := gen.ParseFields(*fields)
		if err != nil {
			return fmt.Errorf("make:job: %w", err)
		}
		spec.Fields = parsed
	}

	file, err := gen.RenderJob(spec)
	if err != nil {
		return fmt.Errorf("make:job: %w", err)
	}
	if err := emit("make:job", root, []gen.File{file}, *force, *dryRun, stdout); err != nil {
		return err
	}
	if *dryRun {
		return nil
	}

	fmt.Fprintf(stdout, `
The handler is written, the registration is not.

  background.go -- in registerHandlers, inside the custom block

      w.Handle(appjobs.%s, appjobs.New%s())

  and the import, aliased because background.go already imports the framework
  package of the same name

      appjobs %q

Then:

    aru queue:work
`, spec.Const(), spec.Handler(), modulePath+"/app/Jobs")
	return nil
}
