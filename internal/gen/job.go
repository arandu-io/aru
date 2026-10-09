package gen

import (
	"fmt"
	"path/filepath"
	"strings"
)

// JobSpec is one background job.
//
// Two types come out of one command, and that is the only conceptual difference
// worth explaining: the usual shape is one class that is both the payload and
// the handler, because a container reinstantiates it from the serialized object
// and injects the dependencies. There is no container here and no object
// serialization -- the payload is JSON and the handler is a value with its
// dependencies in the constructor.
type JobSpec struct {
	// Type is the Go type the payload takes: "SendInvoice".
	Type string
	// EventName is the routing key stored in Job.Name: "invoice.send". It is
	// what ties the push to the handler, which is why it is a constant.
	EventName string
	// Fields are the payload's columns, from the closed set of types.
	Fields []Field
	// ModulePath is the project's module path, for the import the command
	// prints and for app/Services when the handler takes a service.
	ModulePath string
	// Services are the entities whose services the handler calls: "Note"
	// takes a *services.NoteService. Each one is a field of the handler and a
	// parameter of its constructor, so the dependencies are declared where the
	// handler is built and nowhere else.
	Services []string
}

// JobService is one service the handler takes.
type JobService struct {
	// Type is the service's type: NoteService.
	Type string
	// Field is the handler's field and the constructor's parameter: notes.
	Field string
	// AppField is the field of the App bootstrap.Build returns that holds the
	// built service: Notes.
	AppField string
}

// String is the parameter as the constructor declares it: notes *services.NoteService.
func (s JobService) String() string { return s.Field + " *services." + s.Type }

// Deps are the services the handler takes, in the order they were named.
func (s JobSpec) Deps() []JobService {
	out := make([]JobService, 0, len(s.Services))
	for _, entity := range s.Services {
		m := Module{Name: Normalize(entity)}
		plural := m.Plural()
		out = append(out, JobService{
			Type:     m.ServiceType(),
			Field:    strings.ToLower(plural[:1]) + plural[1:],
			AppField: plural,
		})
	}
	return out
}

// ServicesImport is the package the services come from.
func (s JobSpec) ServicesImport() string { return s.ModulePath + "/app/Services" }

// Constructor is how registerHandlers builds the handler, from the App it
// receives: appjobs.NewSendInvoiceHandler(app.Invoices).
func (s JobSpec) Constructor() string {
	args := make([]string, 0, len(s.Services))
	for _, d := range s.Deps() {
		args = append(args, "app."+d.AppField)
	}
	return "appjobs.New" + s.Handler() + "(" + strings.Join(args, ", ") + ")"
}

// Handler is the type that runs the work.
func (s JobSpec) Handler() string { return s.Type + "Handler" }

// Const is the name of the routing constant.
func (s JobSpec) Const() string { return s.Type + "Name" }

// Path is where the file goes.
func (s JobSpec) Path() string { return filepath.Join("app", "Jobs", s.Type+".go") }

// Validate reports what is wrong before a file is written.
func (s JobSpec) Validate() error {
	if !IsExportedIdentifier(s.Type) {
		return fmt.Errorf("%q is not a Go type name: it has to start with a capital letter and hold only letters, digits and underscore", s.Type)
	}
	if s.EventName == "" {
		return fmt.Errorf("a job with no name cannot be routed to a handler")
	}
	if len(s.Services) > 0 && s.ModulePath == "" {
		return errModulePath
	}
	taken := map[string]bool{}
	for _, entity := range s.Services {
		if !IsExportedIdentifier(Exported(entity)) {
			return fmt.Errorf("service %q is not an entity name: --services=Note,Invoice", entity)
		}
		if taken[Exported(entity)] {
			return fmt.Errorf("service %q named twice", entity)
		}
		taken[Exported(entity)] = true
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

// NeedsTime reports whether the payload declares a date or a timestamp.
func (s JobSpec) NeedsTime() bool {
	for _, f := range s.Fields {
		if f.IsTime() {
			return true
		}
	}
	return false
}

// RenderJob produces app/Jobs/<Type>.go.
func RenderJob(s JobSpec) (File, error) {
	if err := s.Validate(); err != nil {
		return File{}, err
	}
	content, err := render(s.Type+".go", jobTemplate, s)
	if err != nil {
		return File{}, err
	}
	return File{Path: s.Path(), Content: content}, nil
}

// DefaultEventName derives the routing key from the type: SendInvoice becomes
// "send-invoice". It is a default rather than the only option, because the name
// somebody wants is usually "invoice.send" and only they know that.
func DefaultEventName(typeName string) string {
	return strings.ReplaceAll(Normalize(typeName), "_", "-")
}

const jobTemplate = `package jobs

import (
	"context"
{{- if .NeedsTime}}
	"time"
{{- end}}

	"github.com/arandu-io/hesape/auth"
	hqueue "github.com/arandu-io/hesape/queue"
	hjobs "github.com/arandu-io/hesape/queue/jobs"
{{- if .Services}}

	services "{{.ServicesImport}}"
{{- end}}
)

// {{.Const}} routes the job to its handler.
//
// A constant rather than a literal at both ends: a typo in the push enqueues
// work nothing drains, and the worker parks it with "no handler registered"
// instead of failing where the mistake is.
const {{.Const}} = "{{.EventName}}"

// {{.Type}} is what the job carries.
//
// These are the arguments the job was created with, as a struct. Keep it to facts and
// ids: a payload that says "look it up" is a payload that reads a row which has
// already changed by the time the worker gets there.
type {{.Type}} struct {
{{- range .Fields}}
	{{.GoName}} {{.GoType}} ` + "`" + `json:"{{.Column}}"` + "`" + `
{{- end}}
}

// Dispatch{{.Type}} enqueues the job.
//
// The Grant is explicit, and hjobs.New is the only constructor, so every job in
// the system carries the tenant, an id and the Grant that authorized it -- there
// is no shape of Job that skipped any of the three.
//
// The caller holds the queue: a listener, once the write that stored its event
// has committed, or a task in a provider's Schedule(). A service the handler
// calls never dispatches it -- this package imports app/Services as soon as a
// handler takes a service, and a service importing it back is an import cycle.
// The write that leads to the job stores an event in the outbox, in its own
// transaction, and the listener the relay hands it to dispatches this.
func Dispatch{{.Type}}(ctx context.Context, q hqueue.Queue, g auth.Grant, in {{.Type}}) error {
	j, err := hjobs.New(g, hjobs.DefaultQueue, {{.Const}}, in)
	if err != nil {
		return err
	}
	return q.Push(ctx, g, j)
}

// {{.Handler}} is what ` + "`" + `aru queue:work` + "`" + ` runs.
//
// Its collaborators arrive through the constructor -- there is no container, and
// a handler that built its own service would be a handler no test can pin.
type {{.Handler}} struct {
{{- range .Deps}}
	// {{.Field}} is the {{.Type}} this job calls.
	{{.String}}
{{- end}}
	// arandu:begin custom
	// Anything else the handler keeps goes here. A service is a parameter of
	// New{{.Handler}} instead, so registerHandlers is where it comes from.
	// arandu:end custom
}

// New{{.Handler}} wires the handler with the services it calls.
//
// registerHandlers, in bootstrap/background.go, calls it with the services the
// App it receives already holds -- the ones bootstrap.Build made at boot, the
// same a request reaches -- and the handler never builds one of its own. A
// service the job needs later is one more parameter here and one more field
// above: regenerate with --services, or add both by hand.
func New{{.Handler}}({{range $i, $d := .Deps}}{{if $i}}, {{end}}{{$d.String}}{{end}}) *{{.Handler}} {
	return &{{.Handler}}{ {{- range $i, $d := .Deps}}{{if $i}}, {{end}}{{$d.Field}}: {{$d.Field}}{{end}}}
}

// Compile-time proof that the worker can register it.
var _ hqueue.Handler = (*{{.Handler}})(nil)

// Handle does the work.
//
// The Grant is rebuilt by the worker from the row -- the action and the tenant
// the push was authorized under, and not one permission more -- so this reaches
// repositories on exactly the same authorized path a request does.
//
// The job arrives as a pointer because on this contract a job settles itself:
// releasing it, or parking it, is a call on j rather than on the queue.
//
// Delivery is at-least-once. This body has to tolerate running twice: the
// process can die between doing the work and acknowledging it, and no queue
// anywhere solves that. j.UUID is stable across retries and is the key to
// deduplicate on.
func (h *{{.Handler}}) Handle(ctx context.Context, g auth.Grant, j *hjobs.Job) error {
	var in {{.Type}}
	if err := j.Decode(&in); err != nil {
		return err
	}

	// arandu:begin custom
	_, _ = g, in
	return nil
	// arandu:end custom
}
{{- if .NeedsTime}}

var _ = time.Time{}
{{- end}}
`
