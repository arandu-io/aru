package gen

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/arandu-io/hesape/str"
)

// Kind is which shape of controller was asked for.
//
// Four shapes, and the set is closed for the same reason the type list is: a
// generator whose shapes grow on demand becomes a language. A nested resource
// and a named action are not shapes of their own -- they are a resource with a
// parent, and a resource with one more verb -- so they are fields of the Stub
// rather than kinds.
type Kind string

// The closed set.
const (
	// KindPlain is a controller with no actions yet, and it is the default.
	KindPlain Kind = "plain"
	// KindResource is the seven actions fhttp.Router.Resource looks for.
	KindResource Kind = "resource"
	// KindSingleton is the three fhttp.Router.Singleton looks for: show, edit
	// and update, with no id, for a thing there is one of where it is reached.
	KindSingleton Kind = "singleton"
	// KindInvokable is one action, Invoke, registered with fhttp.Router.Invokable.
	KindInvokable Kind = "invokable"
)

// Stub is one granular file: what `aru make:controller`, `aru make:middleware`
// and `aru make:request` know, which is deliberately less than a Module.
//
// A Module describes an entity and generates twelve files from it. A Stub
// describes one file, and the person this is for -- porting an application one
// class at a time -- asks for one file.
type Stub struct {
	// Type is the Go type the file declares: "InvoiceController".
	Type string
	// ModulePath is the project's module path, for the generated imports.
	ModulePath string
	// Resource is the URL segment a controller answers: "invoices". It names
	// the route in the wiring the command prints, never the file.
	Resource string
	// Entity is the name the field on routes.Deps takes: "Invoice". It is the
	// type without its suffix, and it appears in the generated example of the
	// route -- an example naming a field nobody would write is an example that
	// gets copied and then corrected.
	Entity string
	// Kind picks the shape of controller. It is ignored by the other stubs.
	Kind Kind
	// Parent is the resource a resource or a singleton nests under, as the
	// route table names it: "projects". Empty is a top-level one.
	Parent string
	// Action is one named action on a record of a resource, beyond the seven:
	// "publish". Empty is none.
	Action string
	// Service is the service a resource controller is built with, when the
	// command that writes it writes the service too: "InvoiceService". Empty
	// is a controller that takes nothing yet.
	Service string
	// Fields are the columns a request carries. Empty is legitimate: it is the
	// empty stub.
	Fields []Field
}

// Controller is the type name, under the name the shared controller template
// asks for. gen.Module answers the same question, which is what lets one
// template serve both.
func (s Stub) Controller() string { return s.Type }

// NeedsTimeParse reports whether the stub declares a date or a timestamp, which
// is the only reason a generated request imports time.
func (s Stub) NeedsTimeParse() bool {
	for _, f := range s.Fields {
		if f.IsTime() {
			return true
		}
	}
	return false
}

// Validate reports what is wrong with the stub, before any file is written.
//
// It does not reuse Module.Validate: that one requires snake_case and at least
// one field, and neither is true of a stub -- a controller has no fields, and
// its name is a Go type.
func (s Stub) Validate() error {
	if s.Type == "" {
		return fmt.Errorf("the stub needs a name")
	}
	if !IsExportedIdentifier(s.Type) {
		return fmt.Errorf("%q is not a Go type name: it has to start with a capital letter and hold only letters, digits and underscore", s.Type)
	}
	if s.ModulePath == "" {
		return errModulePath
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

// validateController reports what is wrong with the shape of a controller the
// stub asks for: a parent on a shape that cannot nest, an action on one that
// has no record to act on, a service on one with nothing to call it from.
func (s Stub) validateController() error {
	switch s.Kind {
	case KindPlain, KindResource, KindSingleton, KindInvokable:
	default:
		return fmt.Errorf("unknown controller kind %q", s.Kind)
	}
	if s.Parent != "" {
		if s.Kind != KindResource && s.Kind != KindSingleton {
			return fmt.Errorf("%s controllers do not nest: only a resource or a singleton has a parent", s.Kind)
		}
		if !isSegment(s.Parent) {
			return fmt.Errorf("parent %q is not a route segment: lowercase letters, digits and dashes, as the route table names it (projects)", s.Parent)
		}
	}
	if s.Action != "" {
		if s.Kind != KindResource {
			return fmt.Errorf("an action acts on a record of a resource, and %s controllers have none: ask for a resource", s.Kind)
		}
		if !isSegment(s.Action) {
			return fmt.Errorf("action %q is not a route segment: lowercase letters, digits and dashes (publish)", s.Action)
		}
		if seven[s.Action] {
			return fmt.Errorf("%q is one of the seven actions a resource already has", s.Action)
		}
	}
	if s.Service != "" && s.Kind != KindResource {
		return fmt.Errorf("only a resource controller is written with its service")
	}
	return nil
}

// seven are the actions of a resource, which a named action may not shadow.
var seven = map[string]bool{
	"index": true, "create": true, "store": true, "show": true, "edit": true, "update": true, "destroy": true,
}

// isSegment reports whether s can be one segment of a route: lowercase letters,
// digits and dashes, starting with a letter.
func isSegment(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// GenerateController produces app/Http/Controllers/<Type>.go.
//
// One file, always: a controller is not a module, and a command that also wrote
// a view and a migration would be `aru make:module` under another name.
func GenerateController(s Stub) ([]File, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if err := s.validateController(); err != nil {
		return nil, err
	}

	content, err := render(s.Type+".go", controllerStubTemplate, s)
	if err != nil {
		return nil, err
	}
	return []File{{Path: filepath.Join("app", "Http", "Controllers", s.Type+".go"), Content: content}}, nil
}

// RenderAction produces the named action of a resource controller on its own:
// the method, with its doc comment, as GenerateController writes it into a new
// controller. It is what make:controller --action prints for a controller that
// already exists, so the method pasted into its custom block is the one a new
// controller would have carried.
func RenderAction(s Stub) (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	if s.Kind != KindResource || s.Action == "" {
		return "", fmt.Errorf("a named action belongs to a resource controller, and needs a name")
	}
	if err := s.validateController(); err != nil {
		return "", err
	}
	out, err := render(s.Type+".action", controllerActionTemplate, s)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// GenerateMiddleware produces app/Http/Middleware/<Type>.go.
func GenerateMiddleware(s Stub) ([]File, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	content, err := render(s.Type+".go", middlewareTemplate, s)
	if err != nil {
		return nil, err
	}
	return []File{{Path: filepath.Join("app", "Http", "Middleware", s.Type+".go"), Content: content}}, nil
}

// GenerateRequest produces app/Http/Requests/<Type>.go.
func GenerateRequest(s Stub) ([]File, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	content, err := render(s.Type+".go", requestStubTemplate+requestRulesTemplate, s)
	if err != nil {
		return nil, err
	}
	return []File{{Path: filepath.Join("app", "Http", "Requests", s.Type+".go"), Content: content}}, nil
}

// IsResource reports whether the seven actions are emitted.
func (s Stub) IsResource() bool { return s.Kind == KindResource }

// IsSingleton reports whether show, edit and update are emitted, with no id.
func (s Stub) IsSingleton() bool { return s.Kind == KindSingleton }

// IsInvokable reports whether the single Invoke action is emitted.
func (s Stub) IsInvokable() bool { return s.Kind == KindInvokable }

// IsNested reports whether the controller answers under a parent.
func (s Stub) IsNested() bool { return s.Parent != "" }

// RouteResource is the name the route table registers the controller under:
// "invoices", or "projects.tasks" when it nests.
func (s Stub) RouteResource() string {
	if s.Parent != "" {
		return s.Parent + "." + s.Resource
	}
	return s.Resource
}

// ParentParam is the path parameter that carries the parent: "project".
func (s Stub) ParentParam() string { return ResourceParameter(s.Parent) }

// MemberParam is the path parameter that carries one record: "id" on a
// top-level resource, the singular of the segment on a nested one, as the
// router names it.
func (s Stub) MemberParam() string {
	if s.Parent != "" {
		return ResourceParameter(s.Resource)
	}
	return "id"
}

// ActionMethod is the Go method a named action is written as: "Publish".
func (s Stub) ActionMethod() string { return Exported(s.Action) }

// MemberPath is the path one record answers at, for the doc comments:
// "/invoices/{id}", or "/tasks/{task}" on a nested resource.
func (s Stub) MemberPath() string {
	return "/" + s.Resource + "/{" + s.MemberParam() + "}"
}

// CollectionPath is the path of the listing, for the doc comments:
// "/invoices", or "/projects/{project}/tasks".
func (s Stub) CollectionPath() string {
	if s.Parent != "" {
		return "/" + s.Parent + "/{" + s.ParentParam() + "}/" + s.Resource
	}
	return "/" + s.Resource
}

// ServicesImport is where the service a resource controller is built with
// lives.
func (s Stub) ServicesImport() string { return s.ModulePath + "/app/Services" }

// ResourceParameter is the path parameter the router names for one record of
// a route segment: the singular of its last element, with dashes as
// underscores -- "purchase-orders" gives purchase_order.
//
// It is hesape's inflector rather than one of this package's own, because the
// router reads the parameter by the name hesape gives it, and a generated
// ctx.Param that disagreed by one letter would read the empty string on every
// request.
func ResourceParameter(segment string) string {
	if i := strings.LastIndexByte(segment, '/'); i >= 0 {
		segment = segment[i+1:]
	}
	return strings.ReplaceAll(str.Singular(segment), "-", "_")
}

const controllerStubTemplate = `package controllers

{{if or .IsResource .IsSingleton .IsInvokable}}import (
	"net/http"

	fhttp "github.com/arandu-io/framework/http"
	hhttp "github.com/arandu-io/hesape/http"
{{- if .Service}}

	services "{{.ServicesImport}}"
{{- end}}
)

{{end -}}
{{if .IsResource -}}
// {{.Type}} answers the {{.RouteResource}} routes.
{{- if .IsNested}}
//
// It nests under {{.Parent}}, shallow: the listing, the form and the store sit
// at {{.CollectionPath}}, and the four that act on one record at
// {{.MemberPath}}. The {{.ParentParam}} in the path is where the person
// navigated, never whose data it is -- the service loads it under the Grant
// and filters by it.
{{- end}}
{{- else if .IsSingleton -}}
// {{.Type}} answers the {{.RouteResource}} singleton: show, edit and update,
// with no id, because there is one of it where it is reached.
{{- if .IsNested}} It nests under
// {{.Parent}}, and the {{.ParentParam}} in the path is where the person
// navigated, never whose data it is.
{{- end}}
{{- else if .IsInvokable -}}
// {{.Type}} answers the one route it is registered for.
{{- else -}}
// {{.Type}} answers the {{.Resource}} routes. It has none yet: write them in
// the custom block at the end, and register each one in routes/web.go.
{{- end}}
//
// It is thin on purpose: read the request, call a service, render. There is no
// repository here and there cannot be one -- the Context an action receives
// carries no database handle, so a controller that reached the data layer
// would be a controller that skipped the service, and therefore skipped the
// policy. ` + "`" + `aru doctor` + "`" + ` refuses it.
//
// An action answers with ctx.View for a screen and ctx.JSON for data. ctx.TOON
// answers the same JsonResource in the token-oriented form, for a payload going
// to a language model; it is chosen here in the code and never from a request
// header, and JSON stays the format everything else is written in.
//
// Who is asking is ctx.User, put on the request by middleware.RequireAuth on the
// route, and view.New(ctx, title) is a screen's page, with the CSRF token the
// protecting middleware issued. An error an action returns is answered by the
// router: validation.Errors back to the form, a missing row as 404, a refusal as
// 403, a duplicate on a unique column as 409, and an error with an HTTPStatus
// method as that status.
type {{.Type}} struct {
	Controller
{{if .Service}}
	svc *services.{{.Service}}
}

// New{{.Type}} returns the controller, with the service every action calls.
// bootstrap/app.go builds it and hands it to the routes.
func New{{.Type}}(svc *services.{{.Service}}) *{{.Type}} {
	return &{{.Type}}{svc: svc}
}
{{- else}}
	// The collaborators arrive through the constructor, never from a container
	// and never from a package-level variable: a controller that builds its own
	// dependencies is a controller no test can pin. Declare the service this
	// controller calls as a field here, take it as a parameter below, and pass
	// it in bootstrap/app.go.
}

// New{{.Type}} returns the controller. bootstrap/app.go builds it and
// hands it to the routes.
func New{{.Type}}() *{{.Type}} {
	return &{{.Type}}{}
}
{{- end}}
{{if .IsResource}}
// Compile-time proof of the seven actions fhttp.Router.Resource looks for. It
// registers the ones the controller implements and nothing else, so a route that
// exists is a route that answers -- and a renamed method fails the build here
// rather than answering 404 in production.
//
// Delete the pair -- the line below and the method -- for every action this
// resource does not have: a controller without Create and Edit registers five
// routes instead of seven.
var (
	_ fhttp.Indexer   = (*{{.Type}})(nil)
	_ fhttp.Creator   = (*{{.Type}})(nil)
	_ fhttp.Storer    = (*{{.Type}})(nil)
	_ fhttp.Shower    = (*{{.Type}})(nil)
	_ fhttp.Editor    = (*{{.Type}})(nil)
	_ fhttp.Updater   = (*{{.Type}})(nil)
	_ fhttp.Destroyer = (*{{.Type}})(nil)
)

// Index renders the listing.
//
// The body answers 501 and not an empty 200. A generated action that answered
// success with no body looks like it worked -- in the browser, in the logs and
// on every dashboard -- and that is the failure nobody debugs. Replace it with
// the screen.
func (c *{{.Type}}) Index(ctx *hhttp.Context) error {
{{- template "parentParam" .}}
	return ctx.Status(http.StatusNotImplemented)
}

// Create renders the empty form.
func (c *{{.Type}}) Create(ctx *hhttp.Context) error {
{{- template "parentParam" .}}
	return ctx.Status(http.StatusNotImplemented)
}

// Store takes the submitted form.
func (c *{{.Type}}) Store(ctx *hhttp.Context) error {
{{- template "parentParam" .}}
	return ctx.Status(http.StatusNotImplemented)
}

// Show renders one record.
func (c *{{.Type}}) Show(ctx *hhttp.Context) error {
{{- template "memberParam" .}}
	return ctx.Status(http.StatusNotImplemented)
}

// Edit renders the form filled in.
func (c *{{.Type}}) Edit(ctx *hhttp.Context) error {
{{- template "memberParam" .}}
	return ctx.Status(http.StatusNotImplemented)
}

// Update writes the submitted form onto the stored record.
func (c *{{.Type}}) Update(ctx *hhttp.Context) error {
{{- template "memberParam" .}}
	return ctx.Status(http.StatusNotImplemented)
}

// Destroy removes the record.
func (c *{{.Type}}) Destroy(ctx *hhttp.Context) error {
{{- template "memberParam" .}}
	return ctx.Status(http.StatusNotImplemented)
}
{{- if .Action}}

` + controllerActionTemplate + `
{{- end}}
{{end}}{{if .IsSingleton}}
// Compile-time proof of the three actions fhttp.Router.Singleton looks for. It
// registers the ones the controller implements and nothing else.
var (
	_ fhttp.Shower  = (*{{.Type}})(nil)
	_ fhttp.Editor  = (*{{.Type}})(nil)
	_ fhttp.Updater = (*{{.Type}})(nil)
)

// Show renders the one there is.
//
// The body answers 501 and not an empty 200. A generated action that answered
// success with no body looks like it worked -- in the browser, in the logs and
// on every dashboard -- and that is the failure nobody debugs.
func (c *{{.Type}}) Show(ctx *hhttp.Context) error {
{{- template "parentParam" .}}
	return ctx.Status(http.StatusNotImplemented)
}

// Edit renders the form filled in.
func (c *{{.Type}}) Edit(ctx *hhttp.Context) error {
{{- template "parentParam" .}}
	return ctx.Status(http.StatusNotImplemented)
}

// Update writes the submitted form onto it.
func (c *{{.Type}}) Update(ctx *hhttp.Context) error {
{{- template "parentParam" .}}
	return ctx.Status(http.StatusNotImplemented)
}
{{end}}{{if .IsInvokable}}
// Compile-time proof that fhttp.Router.Invokable takes this controller. A
// renamed Invoke fails the build here rather than registering nothing.
var _ fhttp.Invoker = (*{{.Type}})(nil)

// Invoke answers the one route this controller has.
//
// The body answers 501 and not an empty 200. A generated action that answered
// success with no body looks like it worked -- in the browser, in the logs and
// on every dashboard -- and that is the failure nobody debugs.
func (c *{{.Type}}) Invoke(ctx *hhttp.Context) error {
	return ctx.Status(http.StatusNotImplemented)
}
{{end}}
// arandu:begin custom
// Actions beyond the ones above go here, and survive regeneration. Register
// them in the custom block of routes/web.go.
// arandu:end custom
{{- define "parentParam"}}
{{- if .IsNested}}
	// The {{.ParentParam}} the person navigated to: where they are, never whose
	// data it is. The service loads it under the Grant.
	_ = ctx.Param("{{.ParentParam}}")
{{- end}}
{{- end}}
{{- define "memberParam"}}
{{- if .IsNested}}
	_ = ctx.Param("{{.MemberParam}}") // the record, for the service call that goes here
{{- end}}
{{- end}}
`

// controllerActionTemplate is the named action of a resource controller. It
// is part of the controller make:controller --action writes, and rendered on
// its own, through RenderAction, for a controller that already exists.
const controllerActionTemplate = `// {{.ActionMethod}} answers POST {{.MemberPath}}/{{.Action}}: one named action on a
// record, beyond the seven. It is registered with fhttp.Router.ResourceAction,
// under the route name {{.RouteResource}}.{{.Action}}, behind the same guard as
// the rest of the resource.
//
// It changes state, so its method is POST, PUT, PATCH or DELETE and never GET:
// a GET that changes state is one a prefetching browser fires without anybody
// choosing to. The transition itself is a rule of the entity, written in its
// model, and the service is what loads the record, asks the policy and saves.
func (c *{{.Type}}) {{.ActionMethod}}(ctx *hhttp.Context) error {
	_ = ctx.Param("{{.MemberParam}}") // the record, for the service call that goes here
	return ctx.Status(http.StatusNotImplemented)
}`

const middlewareTemplate = `package middleware

import (
	"net/http"

	fhttp "github.com/arandu-io/framework/http"
)

// {{.Type}} runs before the handler, and may answer instead of it.
//
// The usual shape is a class with a handle(request, next) method. Here it is
// the standard net/http signature -- func(http.Handler) http.Handler, which
// fhttp.Middleware names -- and that is what makes every middleware written
// for the Go ecosystem work in this pipeline unchanged, and this one work in
// any other.
//
// It is a constructor that returns the middleware rather than the middleware
// itself, so whatever it needs is a parameter and is visible at the wiring
// site:
//
//	func {{.Type}}(sessions *security.SessionStore) fhttp.Middleware
//
// A middleware that reached for a package-level variable would be a middleware
// no test can pin, and no reader of bootstrap/app.go can see the dependencies
// of.
//
// What it must not do is reach the database. A middleware is the request, one
// layer earlier: a query here skipped the service and therefore the policy,
// and ` + "`" + `aru doctor` + "`" + ` refuses it by the same rule it refuses
// a controller. Call a service, or read what an earlier middleware already put
// on the context.
func {{.Type}}() fhttp.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Before the handler.
			//
			// Refusing looks like this, and the return is not optional: without
			// it the handler runs anyway and the refusal is a status nobody sees.
			//
			//	http.Error(w, "this account is not active", http.StatusForbidden)
			//	return

			// arandu:begin custom
			// arandu:end custom

			next.ServeHTTP(w, r)

			// After the handler. Anything written here is written after the
			// status line and the body have gone out: a header set at this point
			// never reaches the client.
		})
	}
}
`

const requestStubTemplate = `package requests

{{if .NeedsTimeParse}}import (
	"time"

	"github.com/arandu-io/hesape/validation"
){{else}}import "github.com/arandu-io/hesape/validation"{{end}}

// {{.Type}} is an input contract: what the request is allowed to carry, and
// what makes it valid.
//
// The fields are explicit, and ctx.Bind fills each one through its form tag and
// nothing else. There is no mass assignment, so a field the client sends and
// this struct does not declare goes nowhere -- and a rule nobody wrote is not a
// rule somebody switched off.
//
// A request object often answers authorize() as well. This one does not, and
// that is the decision rather than an omission: authorization is the Policy, asked with
// auth.Authorize inside the service, and a second place to say yes is a
// second place to forget. See app/Policies.
type {{.Type}} struct {
{{- if .Fields}}
{{- template "requestFields" .}}
{{- else}}
	// arandu:begin custom
	// The fields this request carries, exported, typed as the domain has them
	// and tagged with the input's name: Amount int64 ` + "`" + `form:"amount"` + "`" + `.
	// ctx.Bind converts the text of the form into these.
	// arandu:end custom
{{- end}}
}

// Validate reports the errors per field.
//
// It returns all of them rather than the first: a form that rejects one field at
// a time is a form somebody submits five times.
func (r {{.Type}}) Validate() validation.Errors {
	e := validation.Errors{}
{{- template "storeRules" .}}
{{if .Fields}}
	// arandu:begin custom
	// Domain rules go here: ranges, formats, cross-field checks.
	// arandu:end custom
{{else}}
	// arandu:begin custom
	// The rules. The validation package is a short list on purpose --
	// Required, NotZero, MinLen, MaxLen, Email -- and everything the domain
	// knows is written here, in Go, by whoever knows it:
	//
	//	validation.Required(e, "reference", r.Reference)
	//	if r.Total <= 0 {
	//		e.Add("total", "must be greater than zero")
	//	}
	// arandu:end custom
{{end}}
	return e
}

// Compile-time proof that this request honors the validation contract. The first
// line of the service is in.Validate(), and this is what keeps that call
// compiling.
var _ validation.Validatable = {{.Type}}{}
`
