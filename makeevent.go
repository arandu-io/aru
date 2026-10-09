package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/arandu-io/aru/internal/gen"
)

// makeEventUsage is the usage line of make:event, in the dispatch table and in
// its refusal, naming every flag the command accepts.
const makeEventUsage = `aru make:event <Name> --aggregate=invoice [--event-name=invoice.paid] [--fields "..."] [--force] [--dry-run]`

// makeEvent writes one domain event.
//
// It writes a domain event, and there is no broadcast hook: there is no
// broadcasting layer, and websockets are a later decision. What is left is the
// shape that matters -- a constructor of events.Event, and the constant that
// names it.
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
		return fmt.Errorf("usage: %s", makeEventUsage)
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

	fmt.Fprint(stdout, wiringEvent(spec))
	return nil
}

// wiringEvent is what make:event prints: the lines a service pastes to store
// the event in the transaction of the write that caused it.
//
// Every name in it is one Go accepts where it is pasted -- the row is
// spec.Row(), never the aggregate as written -- because a snippet naming
// newsletter_delivery, or a package that does not exist, is one a person has
// to translate before the compiler reads it. The code comes from gen, which
// the generated event's doc comment and the compile harness read too; the two
// names it cannot know, the row and the service's database field, are said in
// a sentence below it.
func wiringEvent(spec gen.EventSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, `
An event is not wired: the service that makes the change stores it, in the
transaction that writes the row. In that service, app/Services/<Entity>Service.go:

  the imports

%s
  a field, and its value where the constructor builds the service

      %s

      %s

  and, in the method that writes, the event stored beside the row

%s
%s is the row the event happened to, loaded and authorized with the Grant g;
s.db is the service's *database.DB. Store outside database.Transaction returns
ErrNoTransaction on purpose: an event stored next to a row that then rolled
back is worse than no event. The outbox table comes with the events module
bootstrap/app.go registers, and aru doctor reports outbox-not-registered when
nothing registers it.

A listener does what follows from it, after the commit:

      aru make:listener <Name> --event=%s
`, indentLines(strings.Join(spec.StoreImports(), "\n")), gen.EventOutboxField, gen.EventOutboxValue,
		indentLines(spec.StoreSnippet()), spec.Row(), spec.EventName)
	return b.String()
}

// indentLines sets every line of code six spaces in, with its tabs as four
// spaces, the way the other printed wiring reads in a terminal.
func indentLines(code string) string {
	lines := strings.Split(code, "\n")
	for i, line := range lines {
		lines[i] = "      " + strings.ReplaceAll(line, "\t", "    ")
	}
	return strings.Join(lines, "\n") + "\n"
}
