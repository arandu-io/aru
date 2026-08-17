package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/arandu-io/aru/internal/gen"
)

// makeListener writes one event listener.
//
// The shape differs from the usual one where the delivery does. In process, an
// event object is dispatched and a listener subscribes to its class; here the
// event went to the outbox in the same transaction as the row it is about, and
// the relay hands it over after the commit.
//
// So a listener answers a NAME rather than a type, which is what lets the
// producer and the consumer end up in different binaries without either
// changing.
func makeListener(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:listener", flag.ContinueOnError)
	fs.SetOutput(stderr)
	event := fs.String("event", "", "the event name it answers: invoice.paid. Left out, it sees every event")
	force := fs.Bool("force", false, "overwrite an existing listener, preserving the custom block")
	dryRun := fs.Bool("dry-run", false, "print what would be written, and write nothing")

	name, args := takeName(args)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:listener: %w", err)
	}
	if name == "" {
		return fmt.Errorf(`usage: aru make:listener <Name> [--event=invoice.paid] [--force]`)
	}
	if err := checkFlatTree("make:listener", name); err != nil {
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

	spec := gen.Listener{Name: name, Event: *event, ModulePath: modulePath}
	files, err := gen.GenerateListener(spec)
	if err != nil {
		return fmt.Errorf("make:listener: %w", err)
	}
	if err := emit("make:listener", root, files, *force, *dryRun, stdout); err != nil {
		return err
	}
	if *dryRun {
		return nil
	}

	fmt.Fprintf(stdout, `
Wire it in bootstrap/app.go, where the events module is registered. The listener
is the Publisher a Relay hands events to, and the Relay is what the module runs:

    listeners "%s/app/Listeners"

    relay := events.NewRelay(events.NewOutbox(db), listeners.New%s(), events.RelayOptions{})

and, in the k.Register(...) list, in place of events.NewModule():

    events.WithRelay(relay),

The relay calls it after the write commits, never inside the transaction that
wrote it. Delivery is at-least-once, so what it does has to be safe to do twice --
the doc comment on Publish says where that goes.
`, modulePath, spec.Type())
	return nil
}
