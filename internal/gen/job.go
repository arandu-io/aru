package gen

import (
	"fmt"
	"path/filepath"
	"strings"
)

// JobSpec is one background job.
//
// Two types come out of one command, and that is the only conceptual difference
// a Laravel developer has to swallow here: in Laravel one class is both the
// payload and the handler, because the container reinstantiates it from the
// serialized object and injects the dependencies into handle(). There is no
// container here and no object serialization -- the payload is JSON and the
// handler is a value with its dependencies in the constructor (ADR 0001).
type JobSpec struct {
	// Type is the Go type the payload takes: "SendInvoice".
	Type string
	// EventName is the routing key stored in Job.Name: "invoice.send". It is
	// what ties the push to the handler, which is why it is a constant.
	EventName string
	// Fields are the payload's columns, from the closed set of types.
	Fields []Field
	// ModulePath is the project's module path, for the import the command prints.
	ModulePath string
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

	fwjobs "github.com/arandu-io/framework/jobs"
	"github.com/arandu-io/framework/security"
)

// {{.Const}} routes the job to its handler.
//
// A constant rather than a literal at both ends: a typo in the push enqueues
// work nothing drains, and the worker parks it with "no handler registered"
// instead of failing where the mistake is.
const {{.Const}} = "{{.EventName}}"

// {{.Type}} is what the job carries.
//
// These are Laravel's __construct arguments, as a struct. Keep it to facts and
// ids: a payload that says "look it up" is a payload that reads a row which has
// already changed by the time the worker gets there.
type {{.Type}} struct {
{{- range .Fields}}
	{{.GoName}} {{.GoType}} ` + "`" + `json:"{{.Column}}"` + "`" + `
{{- end}}
}

// Dispatch{{.Type}} enqueues the job.
//
// This is Dispatchable::dispatch with the Grant made explicit. fwjobs.New is the
// only constructor, so every job in the system carries the tenant, an id and the
// Grant that authorized it -- there is no shape of Job that skipped any of the
// three (RULE 14).
//
// Called inside data.Transaction, the push is committed by the same transaction
// as the row that produced it. That is the outbox guarantee applied to work
// instead of to an event, and it is the reason the default queue is a table and
// not Redis (ADR 0016).
func Dispatch{{.Type}}(ctx context.Context, q fwjobs.Queue, g security.Grant, in {{.Type}}) error {
	j, err := fwjobs.New(g, fwjobs.DefaultQueue, {{.Const}}, in)
	if err != nil {
		return err
	}
	return q.Push(ctx, g, j)
}

// {{.Handler}} is what ` + "`" + `aru queue:work` + "`" + ` runs.
//
// Its collaborators arrive through the constructor -- there is no container, and
// a handler that built its own service would be a handler no test can pin
// (ADR 0001).
type {{.Handler}} struct {
	// arandu:begin custom
	// The services this job needs. Add the fields here and the parameters to
	// New{{.Handler}} below; bootstrap already built them.
	// arandu:end custom
}

// New{{.Handler}} wires the handler.
func New{{.Handler}}() *{{.Handler}} {
	return &{{.Handler}}{}
}

// Compile-time proof that the worker can register it.
var _ fwjobs.Handler = (*{{.Handler}})(nil)

// Handle does the work.
//
// The Grant is rebuilt by the worker from the row -- the action and the tenant
// the push was authorized under, and not one permission more -- so this reaches
// repositories on exactly the same authorized path a request does.
//
// Delivery is at-least-once. This body has to tolerate running twice: the
// process can die between doing the work and acknowledging it, and no queue
// anywhere solves that. j.ID is stable across retries and is the key to
// deduplicate on.
func (h *{{.Handler}}) Handle(ctx context.Context, g security.Grant, j fwjobs.Job) error {
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
