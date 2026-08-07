package gen

import (
	"fmt"
	"path/filepath"
	"strings"
)

// EventSpec is one domain event.
//
// What comes out is not a dispatcher: an event here is a row in the outbox,
// written in the same transaction as the write that caused it, and delivery
// belongs to the relay. So the generated file is a constructor of events.Event
// plus the constant that names it.
type EventSpec struct {
	// Type is the Go type: "InvoicePaid". Past tense, always -- it is what the
	// events package requires and what makes a consumer able to read a log.
	Type string
	// Aggregate is what it happened to: "invoice". Required, because an empty
	// Aggregate produces an event no consumer can correlate and no error
	// anywhere reports.
	Aggregate string
	// EventName is the published key: "invoice.paid".
	EventName string
	// Fields are the payload's columns.
	Fields []Field
	// ModulePath is the project's module path.
	ModulePath string
}

// Const is the name of the constant that carries the published key.
func (s EventSpec) Const() string { return s.Type + "Name" }

// Path is where the file goes.
func (s EventSpec) Path() string { return filepath.Join("app", "Events", s.Type+".go") }

// NeedsTime reports whether the payload declares a date or a timestamp.
func (s EventSpec) NeedsTime() bool {
	for _, f := range s.Fields {
		if f.IsTime() {
			return true
		}
	}
	return false
}

// Validate reports what is wrong before a file is written.
func (s EventSpec) Validate() error {
	if !IsExportedIdentifier(s.Type) {
		return fmt.Errorf("%q is not a Go type name: it has to start with a capital letter and hold only letters, digits and underscore", s.Type)
	}
	if !isIdentifier(s.Aggregate) {
		return fmt.Errorf("--aggregate is required, and is the entity in lowercase: --aggregate=invoice. "+
			"An event with an empty aggregate is one no consumer can correlate, and nothing anywhere reports it (got %q)", s.Aggregate)
	}
	if s.EventName == "" {
		return fmt.Errorf("an event needs a name")
	}
	seen := map[string]bool{}
	for _, f := range s.Fields {
		if !isIdentifier(f.Name) {
			return fmt.Errorf("field name %q must be lowercase letters, digits and underscore, starting with a letter", f.Name)
		}
		if seen[f.Name] {
			return fmt.Errorf("field %q declared twice", f.Name)
		}
		seen[f.Name] = true
		if _, ok := types[f.Type]; !ok {
			return fmt.Errorf("field %q: unknown type %q (%s)", f.Name, f.Type, TypeList())
		}
	}
	return nil
}

// RenderEvent produces app/Events/<Type>.go.
func RenderEvent(s EventSpec) (File, error) {
	if err := s.Validate(); err != nil {
		return File{}, err
	}
	content, err := render(s.Type+".go", eventTemplate, s)
	if err != nil {
		return File{}, err
	}
	return File{Path: s.Path(), Content: content}, nil
}

// DefaultEventKey derives the published key from the type and the aggregate:
// InvoicePaid on invoice becomes "invoice.paid".
func DefaultEventKey(typeName, aggregate string) string {
	words := strings.Split(Normalize(typeName), "_")
	verb := words[len(words)-1]
	// The aggregate is usually the first word of the type, and repeating it
	// would read as "invoice.invoice-paid".
	if len(words) > 1 && strings.Join(words[:len(words)-1], "_") == aggregate {
		return aggregate + "." + verb
	}
	return aggregate + "." + strings.ReplaceAll(Normalize(typeName), "_", "-")
}

const eventTemplate = `package events

import (
{{- if .NeedsTime}}
	"time"

{{end}}
	fwevents "github.com/arandu-io/framework/events"
)

// {{.Const}} is the event, in the vocabulary of the domain rather than of
// the database: "{{.EventName}}", never "{{.Aggregate}}.updated". A consumer that has to
// diff two rows to learn what happened is a consumer coupled to your schema.
const {{.Const}} = "{{.EventName}}"

// {{.Type}} is what the consumer receives.
//
// Facts that were true when it happened, serialized as JSON. An event that says
// "look it up" is an event that reads a row which has already changed.
type {{.Type}} struct {
{{- range .Fields}}
	{{.GoName}} {{.GoType}} ` + "`" + `json:"{{.Column}}"` + "`" + `
{{- end}}
}

// Event turns the payload into the record the outbox stores.
//
// There is no Dispatch and no Publish, and that is the whole design: the entity
// records the event next to the rule that produced it, the service stores it in
// the SAME transaction as the write, and the relay publishes it afterwards.
//
//	{{.Aggregate}}.Record(events.{{.Type}}{
//		// the fields above
//	}.Event({{.Aggregate}}.ID))
//
//	// then, inside data.Transaction, in the service:
//	if err := outbox.Store(ctx, g, {{.Aggregate}}.PullEvents()); err != nil {
//		return err
//	}
//
// Store outside data.Transaction returns ErrNoTransaction on purpose. An event
// stored next to a row that then rolled back is worse than no event, and an
// event stored after the commit is one process crash away from being lost.
func (e {{.Type}}) Event(aggregateID string) fwevents.Event {
	return fwevents.Event{
		Name:        {{.Const}},
		Aggregate:   "{{.Aggregate}}",
		AggregateID: aggregateID,
		Payload:     e,
	}
}

// arandu:begin custom
// Anything about this event that the fields do not say: a computed total, a
// MarshalJSON that renames a key for a consumer you do not control.
// arandu:end custom
{{- if .NeedsTime}}

var _ = time.Time{}
{{- end}}
`
