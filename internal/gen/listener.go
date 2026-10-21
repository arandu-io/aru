package gen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Listener is one event listener to write.
type Listener struct {
	// Name is the type name: NotifyAccounting.
	Name string
	// Event is the event name it answers: "invoice.paid". It is the string the
	// producer stored, not a Go type -- the outbox carries names and payloads,
	// so a listener and its event never have to be compiled together.
	Event string
	// ModulePath is the project's module path.
	ModulePath string
}

// Type is the struct the listener is declared as.
func (l Listener) Type() string { return exported(l.Name) }

// Receiver is the short receiver for the methods.
func (l Listener) Receiver() string { return Module{Name: Normalize(l.Name)}.Receiver() }

// EventOrAny is the event it answers, or the marker for "every event".
func (l Listener) EventOrAny() string {
	if strings.TrimSpace(l.Event) == "" {
		return ""
	}
	return l.Event
}

// EventConst is the constant a listener of one event declares its name in:
// NotifyAccountingEvent. The list the relay is built with names it, so the
// string is written once, beside the code that answers it.
func (l Listener) EventConst() string { return l.Type() + "Event" }

// Payload is the type a listener of one event decodes the stored payload
// into: NotifyAccountingPayload. It is the listener's own, never the
// producer's, because a consumer compiled against the producer's type has to
// be deployed with it.
func (l Listener) Payload() string { return l.Type() + "Payload" }

// Answers is the phrase the generated doc comment reads: the quoted event name,
// or "every event" when the listener was written without one.
//
// It is a method rather than a conditional in the template because the template
// is a Go string literal, and the conditional sat in a comment line that a
// reflow later split in half -- putting a comment marker inside the action and
// leaving the template unparseable for every run of the command.
func (l Listener) Answers() string {
	if l.EventOrAny() == "" {
		return "every event"
	}
	return "`" + l.EventOrAny() + "`"
}

// GenerateListener writes app/Listeners/<Name>.go.
//
// The shape differs from the usual one where the delivery does. In process, an
// event object is dispatched and a listener subscribes to its class; here the
// event was written to the outbox in the same transaction as the row it is
// about, and the relay hands it over after the commit, to the events.Listeners
// list it was built with.
//
// A listener of one event is listed as events.On(<Name>Event, New<Name>().Handle):
// On matches the name and decodes the payload into the listener's own type, so
// Handle receives a value and never a name to compare. A listener of every
// event implements events.Publisher and is listed as events.Every(...). Both
// answer a NAME rather than a Go type, which is what lets the producer and the
// consumer live in different binaries later without either of them changing.
//
// Delivery is at-least-once by design: the relay can hand the same event twice
// if a publish succeeded and the acknowledgement did not. The generated
// handler says so and leaves the idempotency where only the application can
// put it.
func GenerateListener(l Listener) ([]File, error) {
	if l.ModulePath == "" {
		return nil, errModulePath
	}
	if strings.TrimSpace(l.Name) == "" {
		return nil, fmt.Errorf("make:listener: the listener needs a name")
	}

	tmpl := everyListenerTemplate
	if l.EventOrAny() != "" {
		tmpl = oneEventListenerTemplate
	}
	content, err := render("listener.go", tmpl, l)
	if err != nil {
		return nil, err
	}
	return []File{{
		Path:    filepath.Join("app", "Listeners", l.Type()+".go"),
		Content: content,
	}}, nil
}

// listenerPreamble is the package clause and imports both shapes share.
const listenerPreamble = `// Package listeners holds what this application does after a write commits.
//
// The directory is app/Listeners, where people look for it. The package name
// follows Go and stays lowercase.
package listeners

import (
	"context"

	"github.com/arandu-io/hesape/events"
	"github.com/arandu-io/hesape/log"
)
`

// listenerDelivery is the doc comment both shapes carry on the method the
// relay reaches.
const listenerDelivery = `//
// # Delivery is at-least-once
//
// The relay can hand the same event twice: a publish that succeeded and an
// acknowledgement that did not is indistinguishable from a publish that failed.
// Whatever this does has to be safe to do again -- an upsert keyed by e.ID, a
// row that records what was already sent, an API call with an idempotency key.
//
// Nothing checks this for you. ` + "`aru doctor`" + ` reads the AST and never runs
// code, so it cannot see whether an effect repeats.
//
// # Returning an error
//
// An error means "not delivered": the relay keeps the event and tries again,
// backing off, and parks it in the dead letter after the attempts run out.
// Returning nil on a failure loses the event silently, which is the one outcome
// the outbox exists to prevent.`

// listenerCollaborators is the body comment of both shapes' struct.
const listenerCollaborators = `	// The collaborators this listener needs are fields, set by its constructor
	// where the events.Listeners list is built. A listener that reaches for a
	// global is a listener no test can pin.`

const oneEventListenerTemplate = listenerPreamble + `
// {{ .EventConst }} is the event {{ .Type }} answers: the name the producer
// stored, written once, here, and named by the events.Listeners list.
const {{ .EventConst }} = "{{ .EventOrAny }}"

// {{ .Payload }} is what {{ .Type }} reads from the event's payload.
//
// The payload is the JSON the producer stored, and this type is declared HERE
// rather than shared with the producer: a consumer that compiles against the
// producer's type is a consumer that has to be deployed with it. Declare the
// fields this listener needs, under the JSON names the producer wrote.
type {{ .Payload }} struct {
	// arandu:begin custom
	// arandu:end custom
}

// {{ .Type }} answers {{ .Answers }}.
//
// It is listed in the events.Listeners the relay is built with as
//
//	events.On({{ .EventConst }}, New{{ .Type }}().Handle)
//
// and the relay calls Handle once the event is committed and readable, never
// inside the transaction that wrote it.
type {{ .Type }} struct {
` + listenerCollaborators + `
}

// New{{ .Type }} returns the listener.
func New{{ .Type }}() *{{ .Type }} {
	return &{{ .Type }}{}
}

// Handle handles one committed event, its payload already decoded.
//
// A payload that does not decode into {{ .Payload }} never reaches it: the
// relay keeps the event and parks it after the attempts run out, with the
// decode failure as the reason.
` + listenerDelivery + `
func ({{ .Receiver }} *{{ .Type }}) Handle(ctx context.Context, event {{ .Payload }}, e events.Stored) error {
	log.For(ctx).Info("event received", "event", e.Name, "id", e.ID)

	// arandu:begin custom
	return nil
	// arandu:end custom
}
`

const everyListenerTemplate = listenerPreamble + `
// {{ .Type }} answers {{ .Answers }}.
//
// It implements events.Publisher, and is listed in the events.Listeners the
// relay is built with as
//
//	events.Every(New{{ .Type }}())
//
// so the relay hands it every event, undecoded, once it is committed and
// readable -- never inside the transaction that wrote it.
type {{ .Type }} struct {
` + listenerCollaborators + `
}

// New{{ .Type }} returns the listener.
func New{{ .Type }}() *{{ .Type }} {
	return &{{ .Type }}{}
}

// Compile-time proof that this satisfies what the relay calls.
var _ events.Publisher = (*{{ .Type }})(nil)

// Publish handles one committed event, whatever its name.
` + listenerDelivery + `
func ({{ .Receiver }} *{{ .Type }}) Publish(ctx context.Context, e events.Stored) error {
	// Every event passes through here. The payload is the JSON the producer
	// stored; a listener that needs its fields answers one event instead, with
	// a type of its own that events.On decodes into.
	log.For(ctx).Info("event received", "event", e.Name, "id", e.ID)

	// arandu:begin custom
	return nil
	// arandu:end custom
}
`

// ListenerList is the list of listeners the relay is built with: an
// events.Listeners literal, or the Each literal of app/Listeners an
// application wrote before events.Listeners existed.
type ListenerList struct {
	// File is where the literal is, relative to the project root:
	// app/Providers/AppServiceProvider.go.
	File string
	// Line is the line of File the literal opens on.
	Line int
	// Package is the name File refers to app/Listeners by.
	Package string
	// Events is the name File refers to the events component by, and empty
	// when File does not import it.
	Events string
	// Each reports a <Package>.Each literal: a list of Publishers, which a
	// listener of one event is not.
	Each bool
}

// EventsName is what the entry calls the events component: the name File
// imports it under, or events when File does not import it yet.
func (l ListenerList) EventsName() string {
	if l.Events == "" {
		return "events"
	}
	return l.Events
}

// Entry is the line a listener adds to an events.Listeners list: events.On
// over its Handle for a listener of one event, events.Every over it for a
// listener of every event, with the comma a composite literal written one
// element per line needs.
func (l ListenerList) Entry(listener Listener) string {
	pkg := l.Package
	if pkg == "" {
		pkg = "listeners"
	}
	if listener.EventOrAny() == "" {
		return l.EventsName() + ".Every(" + pkg + ".New" + listener.Type() + "()),"
	}
	return l.EventsName() + ".On(" + pkg + "." + listener.EventConst() + ", " + pkg + ".New" + listener.Type() + "().Handle),"
}

// listenerListFiles are the files the relay's list is looked for in, in this
// order: the providers the application builds its services in, then
// bootstrap/app.go, where an application composed them before it had
// providers.
func listenerListFiles(root string) []string {
	var out []string
	if entries, err := os.ReadDir(filepath.Join(root, "app", "Providers")); err == nil {
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			out = append(out, filepath.ToSlash(filepath.Join("app", "Providers", name)))
		}
	}
	sort.Strings(out)
	return append(out, "bootstrap/app.go")
}

// eventsComponent is the import path of the component events.Listeners is
// declared in.
const eventsComponent = "github.com/arandu-io/hesape/events"

// FindListenerList reads the files the relay is built in and answers the list
// it is built with, when one of them has one.
//
// The relay takes one Publisher, and a project that built it over a list has
// already chosen that list as the Publisher. A new listener is one more
// element of it: printing a relay built around the new listener alone would
// tell the person to replace the Publisher, and pasting that drops every
// listener the list held.
//
// Both spellings of a list are answered: events.Listeners, from the events
// component under whatever name the file imports it, and the Each type
// app/Listeners declares, read off the import of <modulePath>/app/Listeners.
// Each literal is matched by what it refers to rather than by a spelling. A
// file that does not parse, or has no such literal, is passed over, and none
// answers false.
func FindListenerList(root, modulePath string) (ListenerList, bool) {
	for _, rel := range listenerListFiles(root) {
		if list, ok := listenerListIn(root, rel, modulePath); ok {
			return list, true
		}
	}
	return ListenerList{}, false
}

// listenerListIn answers the first list literal of one file.
func listenerListIn(root, rel, modulePath string) (ListenerList, bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(rel)), nil, parser.SkipObjectResolution)
	if err != nil {
		return ListenerList{}, false
	}
	pkg, eventsName := "", ""
	for _, imp := range file.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		switch p {
		case modulePath + "/app/Listeners":
			pkg = "listeners"
			if imp.Name != nil {
				pkg = imp.Name.Name
			}
		case eventsComponent:
			eventsName = "events"
			if imp.Name != nil {
				eventsName = imp.Name.Name
			}
		}
	}
	usable := func(name string) bool { return name != "" && name != "_" && name != "." }
	if !usable(pkg) {
		pkg = ""
	}
	if !usable(eventsName) {
		eventsName = ""
	}
	if pkg == "" && eventsName == "" {
		return ListenerList{}, false
	}

	var found ListenerList
	ast.Inspect(file, func(n ast.Node) bool {
		if found.Line != 0 {
			return false
		}
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		sel, ok := lit.Type.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		switch {
		case eventsName != "" && x.Name == eventsName && sel.Sel.Name == "Listeners":
			found = ListenerList{File: rel, Line: fset.Position(lit.Pos()).Line, Package: pkg, Events: eventsName}
		case pkg != "" && x.Name == pkg && sel.Sel.Name == "Each":
			found = ListenerList{File: rel, Line: fset.Position(lit.Pos()).Line, Package: pkg, Events: eventsName, Each: true}
		default:
			return true
		}
		return false
	})
	return found, found.Line != 0
}
