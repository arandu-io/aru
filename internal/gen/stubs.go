package gen

import (
	"fmt"
	"path/filepath"
)

// Kind is which shape of controller was asked for.
//
// Three shapes, and the set is closed for the same reason the type list is: a
// generator whose shapes grow on demand becomes a language (RULE 15).
type Kind string

// The closed set.
const (
	// KindPlain is a controller with no actions yet, and it is the default.
	KindPlain Kind = "plain"
	// KindResource is the seven actions httpx.Router.Resource looks for.
	KindResource Kind = "resource"
	// KindInvokable is one action, Handle.
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

// GenerateController produces app/Http/Controllers/<Type>.go.
//
// One file, always: a controller is not a module, and a command that also wrote
// a view and a migration would be `aru make:module` under another name.
func GenerateController(s Stub) ([]File, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	switch s.Kind {
	case KindPlain, KindResource, KindInvokable:
	default:
		return nil, fmt.Errorf("unknown controller kind %q", s.Kind)
	}

	content, err := render(s.Type+".go", controllerStubTemplate+controllerSessionTemplate, s)
	if err != nil {
		return nil, err
	}
	return []File{{Path: filepath.Join("app", "Http", "Controllers", s.Type+".go"), Content: content}}, nil
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

// IsInvokable reports whether the single Handle action is emitted.
func (s Stub) IsInvokable() bool { return s.Kind == KindInvokable }

const controllerStubTemplate = `package controllers

import (
	"errors"
	"net/http"

	"github.com/arandu-io/framework/httpx"
	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/framework/security"
)

{{if .IsResource -}}
// {{.Type}} answers the {{.Resource}} routes.
{{- else if .IsInvokable -}}
// {{.Type}} answers the one route it is registered for.
{{- else -}}
// {{.Type}} answers the {{.Resource}} routes. It has none yet: write them in
// the custom block at the end, and register each one in routes/web.go.
{{- end}}
//
// It is thin on purpose: read the request, call a service, render. There is no
// repository here and there cannot be one -- httpx.Context carries no database
// handle, so a controller that reached the data layer would be a controller
// that skipped the service, and therefore skipped the policy. ` + "`" + `aru
// doctor` + "`" + ` refuses it.
type {{.Type}} struct {
	Controller

	// The collaborators arrive through the constructor, never from a container
	// and never from a package-level variable: a controller that builds its own
	// dependencies is a controller no test can pin. Declare the service this
	// controller calls as a field here, and pass it in bootstrap/app.go.
	sessions *security.SessionStore
	csrf     *security.CSRF
}

// New{{.Type}} returns the controller. bootstrap/app.go builds it and
// hands it to the routes.
//
// The session store and the CSRF issuer are here because a screen is allowed to
// know about a token and a cookie. Everything else a page needs arrives through
// a service.
func New{{.Type}}(sessions *security.SessionStore, csrf *security.CSRF) *{{.Type}} {
	return &{{.Type}}{sessions: sessions, csrf: csrf}
}
{{if .IsResource}}
// Compile-time proof of the seven actions httpx.Router.Resource looks for. It
// registers the ones the controller implements and nothing else, so a route that
// exists is a route that answers -- and a renamed method fails the build here
// rather than answering 404 in production.
//
// Delete the pair -- the line below and the method -- for every action this
// resource does not have: a controller without Create and Edit registers five
// routes instead of seven.
var (
	_ httpx.Indexer   = (*{{.Type}})(nil)
	_ httpx.Creator   = (*{{.Type}})(nil)
	_ httpx.Storer    = (*{{.Type}})(nil)
	_ httpx.Shower    = (*{{.Type}})(nil)
	_ httpx.Editor    = (*{{.Type}})(nil)
	_ httpx.Updater   = (*{{.Type}})(nil)
	_ httpx.Destroyer = (*{{.Type}})(nil)
)

// Index renders the listing.
//
// The body answers 501 and not an empty 200. A generated action that answered
// success with no body looks like it worked -- in the browser, in the logs and
// on every dashboard -- and that is the failure nobody debugs. Replace it with
// the screen.
func (c *{{.Type}}) Index(ctx *httpx.Context) error {
	return ctx.Status(http.StatusNotImplemented)
}

// Create renders the empty form.
func (c *{{.Type}}) Create(ctx *httpx.Context) error {
	return ctx.Status(http.StatusNotImplemented)
}

// Store takes the submitted form.
func (c *{{.Type}}) Store(ctx *httpx.Context) error {
	return ctx.Status(http.StatusNotImplemented)
}

// Show renders one record.
func (c *{{.Type}}) Show(ctx *httpx.Context) error {
	return ctx.Status(http.StatusNotImplemented)
}

// Edit renders the form filled in.
func (c *{{.Type}}) Edit(ctx *httpx.Context) error {
	return ctx.Status(http.StatusNotImplemented)
}

// Update writes the submitted form onto the stored record.
func (c *{{.Type}}) Update(ctx *httpx.Context) error {
	return ctx.Status(http.StatusNotImplemented)
}

// Destroy removes the record.
func (c *{{.Type}}) Destroy(ctx *httpx.Context) error {
	return ctx.Status(http.StatusNotImplemented)
}
{{end}}{{if .IsInvokable}}
// Handle answers the one route this controller has.
//
// The usual shape registers the class itself and calls a magic method. Here the
// route names the method, which is the same idea with a compiler behind it:
//
//	r.Action("GET", "/{{.Resource}}", d.{{.Entity}}.Handle).Name("{{.Resource}}")
//
// There is no interface to assert against and none is needed: that line is
// itself the proof, and it fails the build if this method is renamed.
//
// The body answers 501 and not an empty 200. A generated action that answered
// success with no body looks like it worked -- in the browser, in the logs and
// on every dashboard -- and that is the failure nobody debugs.
func (c *{{.Type}}) Handle(ctx *httpx.Context) error {
	return ctx.Status(http.StatusNotImplemented)
}
{{end}}{{template "controllerSession" .}}
// fail turns a domain error into a status, in one place.
//
// Note what it does not do: it never writes the authorization error into the
// response. Why a policy said no is information about the system, and it belongs
// in the log. Anything unrecognized is returned, and the router turns it into
// the error page in development and a 500 in production.
func (c *{{.Type}}) fail(ctx *httpx.Context, err error) error {
	switch {
	case errors.Is(err, security.ErrForbidden):
		observability.Log(ctx.Ctx()).Warn("authorization denied", "error", err)
		return ctx.Status(http.StatusForbidden)
	default:
		// The errors app/Models declares go here: NotFound is 404, Conflict is
		// 409, an ordering outside the allowlist is 400.
		return err
	}
}

// arandu:begin custom
// Actions beyond the ones above go here, and survive regeneration. Register
// them in the custom block of routes/web.go.
// arandu:end custom
`

const middlewareTemplate = `package middleware

import (
	"net/http"

	"github.com/arandu-io/framework/httpx"
)

// {{.Type}} runs before the handler, and may answer instead of it.
//
// The usual shape is a class with a handle(request, next) method. Here it is
// the standard net/http signature -- func(http.Handler) http.Handler, which
// httpx.Middleware names -- and that is what makes every middleware written
// for the Go ecosystem work in this pipeline unchanged, and this one work in
// any other.
//
// It is a constructor that returns the middleware rather than the middleware
// itself, so whatever it needs is a parameter and is visible at the wiring
// site:
//
//	func {{.Type}}(sessions *security.SessionStore) httpx.Middleware
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
func {{.Type}}() httpx.Middleware {
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

	"github.com/arandu-io/framework/validation"
){{else}}import "github.com/arandu-io/framework/validation"{{end}}

// {{.Type}} is an input contract: what the request is allowed to carry, and
// what makes it valid.
//
// The fields are explicit. There is no mass assignment and there is no struct
// tag, so a field the client sends and this struct does not declare goes
// nowhere -- and a rule nobody wrote is not a rule somebody switched off.
//
// A request object often answers authorize() as well. This one does not, and
// that is the decision rather than an omission: authorization is the Policy, asked with
// security.Authorize inside the service, and a second place to say yes is a
// second place to forget. See app/Policies.
type {{.Type}} struct {
{{- if .Fields}}
{{- range .Fields}}
	{{.GoName}} {{.GoType}}
{{- end}}
{{- else}}
	// arandu:begin custom
	// The fields this request carries, exported and typed as the domain has
	// them. The controller is what turns the text of a form into these.
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
