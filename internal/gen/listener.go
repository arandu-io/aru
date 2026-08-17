package gen

import (
	"fmt"
	"path/filepath"
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
// about, and the relay hands it over after the commit.
//
// So a listener implements events.Publisher and answers a NAME, not a type.
// That is what lets the producer and the consumer live in different binaries
// later without either of them changing.
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

	content, err := render("listener.go", listenerTemplate, l)
	if err != nil {
		return nil, err
	}
	return []File{{
		Path:    filepath.Join("app", "Listeners", l.Type()+".go"),
		Content: content,
	}}, nil
}

const listenerTemplate = `// Package listeners holds what this application does after a write commits.
//
// The directory is app/Listeners, where people look for it. The package name
// follows Go and stays lowercase.
package listeners

import (
	"context"

	"github.com/arandu-io/framework/events"
	"github.com/arandu-io/framework/observability"
)

// {{ .Type }} answers {{ .Answers }}.
//
// It implements events.Publisher: the relay calls Publish once the event is
// committed and readable, never inside the transaction that wrote it.
type {{ .Type }} struct {
	// The collaborators this listener needs are fields, set where it is wired in
	// bootstrap/app.go. A listener that reaches for a global is a listener no
	// test can pin.
}

// New{{ .Type }} returns the listener.
func New{{ .Type }}() *{{ .Type }} {
	return &{{ .Type }}{}
}

// Compile-time proof that this satisfies what the relay calls.
var _ events.Publisher = (*{{ .Type }})(nil)

// Publish handles one committed event.
//
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
// the outbox exists to prevent.
func ({{ .Receiver }} *{{ .Type }}) Publish(ctx context.Context, e events.Stored) error {
{{- if .EventOrAny }}
	// One listener per event name, so this returns early rather than growing a
	// switch. A switch here is how a listener becomes the place every event
	// passes through.
	if e.Name != "{{ .EventOrAny }}" {
		return nil
	}
{{- end }}

	// The payload is the JSON the producer stored. Unmarshal it into a struct
	// declared HERE, not shared with the producer: a consumer that compiles
	// against the producer's type is a consumer that has to be deployed with it.
	observability.Log(ctx).Info("event received", "event", e.Name, "id", e.ID)

	// arandu:begin custom
	return nil
	// arandu:end custom
}
`
