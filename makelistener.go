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
// The relay takes one Publisher. When the project already builds it with an
// events.Listeners list, the listener is one more element of that list, and
// the answer names the file and line the list opens on. A list written as
// app/Listeners' Each takes Publishers only, so it is turned into
// events.Listeners first. Only a project with no list is told to build a relay
// -- printed for a list, that line replaces the Publisher, and pasting it drops
// every listener the list held.
func wiringListener(spec gen.Listener, list gen.ListenerList, found bool) string {
	var b strings.Builder
	imports := func() {
		var lines []string
		if list.Package == "" {
			lines = append(lines, fmt.Sprintf("    listeners %q", spec.ModulePath+"/app/Listeners"))
		}
		if list.Events == "" {
			lines = append(lines, fmt.Sprintf("    %q", "github.com/arandu-io/hesape/events"))
		}
		if len(lines) > 0 {
			fmt.Fprintf(&b, "\nand the imports %s does not have yet:\n\n%s\n", list.File, strings.Join(lines, "\n"))
		}
	}
	switch {
	case found && !list.Each:
		fmt.Fprintf(&b, `
The relay hands every committed event to the listeners of the events.Listeners
list it is built with, at %s:%d. Add it to that list:

    %s
`, list.File, list.Line, list.Entry(spec))
		imports()
	case found:
		fmt.Fprintf(&b, `
The relay at %s:%d is built with %s.Each, a list of Publishers, and
events.Listeners is the list the relay takes now: it matches the name and decodes
the payload, so a listener of one event is handed a value. Make the list
%[4]s.Listeners -- each element it holds becomes %[4]s.Every(<element>) -- and
add:

    %[5]s

%[3]s.Each is then unused, and app/Listeners/Each.go can go.
`, list.File, list.Line, list.Package, list.EventsName(), list.Entry(spec))
		imports()
	default:
		list = gen.ListenerList{File: "app/Providers/AppServiceProvider.go", Package: "listeners", Events: "hevents"}
		fmt.Fprintf(&b, `
Wire it where the provider builds the services, app/Providers/AppServiceProvider.go.
The relay is what hands committed events to the listeners, and the events
module runs it:

    relay := events.NewRelay(hevents.NewOutbox(db), hevents.Listeners{
        %s
    }, events.RelayOptions{Locker: d.Locker})

kept on the provider as its Relay field, and in bootstrap/app.go, in
WithProviders(...), in place of events.NewModule():

    events.WithRelay(app.Relay),

where events is "github.com/arandu-io/framework/events", hevents is
"github.com/arandu-io/hesape/events" and listeners is
%q.
`, list.Entry(spec), spec.ModulePath+"/app/Listeners")
	}
	b.WriteString(`
The relay calls it after the write commits, never inside the transaction that
wrote it. Delivery is at-least-once, so what it does has to be safe to do twice --
the doc comment on the listener says where that goes.
`)
	if spec.EventOrAny() != "" {
		fmt.Fprintf(&b, `
The fields it reads are declared on %s, in the custom block, under the JSON
names the producer stored.
`, spec.Payload())
	}
	return b.String()
}
