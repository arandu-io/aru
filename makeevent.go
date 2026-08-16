package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/arandu-io/aru/internal/gen"
)

// makeEvent writes one domain event.
//
// It writes a domain event, and there is no broadcast hook: there is no broadcasting
// layer, and websockets are a later decision with a written trigger
// (00-meta/DOC-scope.md). What is left is the shape that matters -- a constructor
// of events.Event, and the constant that names it.
func makeEvent(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:event", flag.ContinueOnError)
	fs.SetOutput(stderr)
	aggregate := fs.String("aggregate", "", "what it happened to, in lowercase: invoice")
	eventName := fs.String("event-name", "", "the published key (default: <aggregate>.<verb>)")
	fields := fs.String("fields", "", `the payload, as "name:type" separated by commas`)
	force := fs.Bool("force", false, "overwrite an existing event, preserving the custom block")
	dryRun := fs.Bool("dry-run", false, "print what would be written, and write nothing")

	name, args := takeName(args)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:event: %w", err)
	}
	if name == "" {
		return fmt.Errorf(`usage: aru make:event <Name> --aggregate=invoice [--event-name=invoice.paid] [--fields "..."]`)
	}
	if err := checkFlatTree("make:event", name); err != nil {
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

	spec := gen.EventSpec{
		Type:       gen.Exported(name),
		Aggregate:  gen.Normalize(*aggregate),
		ModulePath: modulePath,
	}
	spec.EventName = *eventName
	if spec.EventName == "" && spec.Aggregate != "" {
		spec.EventName = gen.DefaultEventKey(spec.Type, spec.Aggregate)
	}
	if *fields != "" {
		parsed, err := gen.ParseFields(*fields)
		if err != nil {
			return fmt.Errorf("make:event: %w", err)
		}
		spec.Fields = parsed
	}

	file, err := gen.RenderEvent(spec)
	if err != nil {
		return fmt.Errorf("make:event: %w", err)
	}
	if err := emit("make:event", root, []gen.File{file}, *force, *dryRun, stdout); err != nil {
		return err
	}
	if *dryRun {
		return nil
	}

	fmt.Fprintf(stdout, `
An event is not wired: it is recorded and it is stored.

  app/Models -- the entity embeds events.Recorder and records it next to the
  rule that produced it

  app/Services -- inside data.Transaction, in the SAME transaction as the write

      if err := outbox.Store(ctx, g, %s.PullEvents()); err != nil {
          return err
      }

Store outside data.Transaction returns ErrNoTransaction on purpose: an event
stored next to a row that then rolled back is worse than no event.
`, spec.Aggregate)
	return nil
}
