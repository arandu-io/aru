package gen

import (
	"fmt"
	"go/token"
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

// EventsImport is the import path of the package the event is declared in.
func (s EventSpec) EventsImport() string { return s.ModulePath + "/app/Events" }

// Row is the variable the example in the event's doc comment, and the wiring
// make:event prints, hold the row the event happened to in: invoice for
// "invoice", purchaseOrder for "purchase_order". The aggregate is a column-like
// name, and pasted as written it is not an identifier Go accepts in an
// expression a person would write.
//
// A name the snippet already binds, or a keyword, takes a Row suffix, so the
// pasted code never shadows the context, the service, the Grant or a package.
func (s EventSpec) Row() string {
	name := lowerFirst(exported(s.Aggregate))
	if token.IsKeyword(name) || eventSnippetBinds[name] {
		return name + "Row"
	}
	return name
}

// eventSnippetBinds are the names the printed wiring declares or imports.
var eventSnippetBinds = map[string]bool{
	"ctx": true, "s": true, "g": true, "err": true,
	"database": true, "events": true, "appevents": true, "frameevents": true,
}

// StoreImports are the import lines the store snippet needs in the service it
// is pasted into, in the order goimports groups them.
func (s EventSpec) StoreImports() []string {
	return []string{
		`"github.com/arandu-io/hesape/database"`,
		`"github.com/arandu-io/hesape/events"`,
		`frameevents "github.com/arandu-io/framework/events"`,
		`appevents "` + s.EventsImport() + `"`,
	}
}

// EventOutboxField and EventOutboxValue are the service's field holding the
// outbox and its value in the constructor, where db is the *database.DB the
// constructor takes.
const (
	EventOutboxField = "outbox *events.Outbox"
	EventOutboxValue = "outbox: frameevents.NewOutbox(db),"
)

// StoreSnippet is the code that stores the event, in a service method that
// holds ctx, the Grant g and the row: the row saved and the event stored in
// one database.Transaction, assigned to err. It is the one text both the doc
// comment of the generated event and the wiring make:event prints carry, and
// the compile harness builds it inside a service.
func (s EventSpec) StoreSnippet() string {
	return "err := database.Transaction(ctx, s.db, func(ctx context.Context) error {\n" +
		"\tif _, err := " + s.Row() + ".Save(ctx, g); err != nil {\n" +
		"\t\treturn err\n" +
		"\t}\n" +
		"\treturn s.outbox.Store(ctx, g, []events.Event{appevents." + s.Type + "{\n" +
		"\t\t// the payload's fields\n" +
		"\t}.Event(" + s.Row() + ".ID)})\n" +
		"})"
}

// StoreComment is StoreSnippet as a block of a doc comment.
func (s EventSpec) StoreComment() string {
	lines := strings.Split(s.StoreSnippet(), "\n")
	for i, line := range lines {
		lines[i] = "//\t" + line
	}
	return strings.Join(lines, "\n")
}

// eventMethods are the methods the generated type declares. A payload field of
// one of these names is a field and a method of one name on one type, which
// Go refuses.
var eventMethods = map[string]bool{"Event": true}

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
		if eventMethods[f.GoName()] {
			return fmt.Errorf("field %q would be the Go field %s, and %s is the method that turns the payload into "+
				"the record the outbox stores: a type cannot have a field and a method of one name, so the file would "+
				"not compile. Name the field for what it holds, such as kind (--fields \"kind:string\")",
				f.Name, f.GoName(), f.GoName())
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
	hevents "github.com/arandu-io/hesape/events"
)

// {{.Const}} is the event, in the vocabulary of the domain rather than of the
// database: "{{.EventName}}", never "{{.Aggregate}}.updated". A consumer that
// has to diff two rows to learn what happened is a consumer coupled to your
// schema.
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
// There is no Dispatch and no Publish, and that is the whole design: the service
// that makes the change stores the event in the SAME transaction as the write,
// and the relay publishes it afterwards.
//
{{.StoreComment}}
//
// Store outside database.Transaction returns ErrNoTransaction on purpose. An
// event stored next to a row that then rolled back is worse than no event, and
// an event stored after the commit is one process crash away from being lost.
func (e {{.Type}}) Event(aggregateID string) hevents.Event {
	return hevents.Event{
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
