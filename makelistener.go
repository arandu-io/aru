package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/arandu-io/aru/internal/gen"
)

// makeListenerUsage is the usage line of make:listener, in the dispatch table and in
// its refusal, naming every flag the command accepts.
const makeListenerUsage = `aru make:listener <Name> [--event=invoice.paid] [--force] [--dry-run]`

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
		return fmt.Errorf("usage: %s", makeListenerUsage)
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

	list, found := gen.FindListenerList(root, modulePath)
	fmt.Fprint(stdout, wiringListener(spec, list, found))
	return nil
}

// wiringListener is where the listener that was just written goes.
//
// The relay takes one Publisher. When bootstrap/app.go already builds it with
// a listeners.Each list, the listener is one more element of that list, and
// the answer names the line the list opens on. Only a bootstrap without one is
// told to build a relay around this listener -- printed for a list, that line
// replaces the Publisher, and pasting it drops every listener the list held.
func wiringListener(spec gen.Listener, list gen.ListenerList, found bool) string {
	var b strings.Builder
	if found {
		fmt.Fprintf(&b, `
Wire it in bootstrap/app.go. The relay hands every committed event to each
listener in the %s.Each list it is built with, at bootstrap/app.go:%d.
Add it to that list:

    %s

A collaborator the listener needs is a parameter of New%s and a field of
the listener, passed there from what Build has already made.
`, list.Package, list.Line, list.Entry(spec), spec.Type())
	} else {
		fmt.Fprintf(&b, `
Wire it in bootstrap/app.go, where the events module is registered. The listener
is the Publisher a Relay hands events to, and the Relay is what the module runs:

    listeners "%s/app/Listeners"

    relay := events.NewRelay(events.NewOutbox(db), listeners.New%s(), events.RelayOptions{})

and, in the k.Register(...) list, in place of events.NewModule():

    events.WithRelay(relay),
`, spec.ModulePath, spec.Type())
	}
	b.WriteString(`
The relay calls it after the write commits, never inside the transaction that
wrote it. Delivery is at-least-once, so what it does has to be safe to do twice --
the doc comment on Publish says where that goes.
`)
	return b.String()
}
