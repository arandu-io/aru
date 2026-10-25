package gen

// The templates below emit into the conventional tree: app/Models, app/Policies,
// app/Services, app/Http/Requests, app/Http/Controllers, database/migrations and
// resources/views. There is no modules/<name>/, and the reason is recognition:
// the developer this framework is for opens a project and looks for
// app/Http/Controllers.
//
// Together they emit the mandatory path: Validate, Authorize, Grant, Model. The
// service owns the database handle and no generated controller reaches it.
//
// One consequence of the flat tree runs through all of them: a package holds
// every module's files, so no unexported package-level name can be generic. What
// would otherwise be `perPage` is `invoicePerPage`.

const modelTemplate = `package models

import (
	"log/slog"
	"time"

	"github.com/arandu-io/hesape/database/model"
)

// {{.Entity}} is one row of {{.Table}}.
//
// It embeds the model, so a row returned by a query carries the connection and
// can be saved again. Build a new row with {{.Constructor}}(db).New(): a struct
// literal has no connection and its write methods return model.ErrUnwired.
//
// {{.Constructor}}, {{.Entity}}Query and {{.Entity}}Collection
// are generated beside this file, in {{.Entity}}Query.go, by aru model:build.
type {{.Entity}} struct {
	model.Model

	ID        string    ` + "`" + `db:"id"` + "`" + `
{{- if .Tenant}}
	TenantID  string    ` + "`" + `db:"tenant_id"` + "`" + `
{{- end}}
{{- if .Parent}}
	{{.ParentField}} string ` + "`" + `db:"{{.ParentColumn}}"` + "`" + `
{{- end}}
{{- range .Fields}}
	{{.GoName}} {{.ModelType}} ` + "`" + `db:"{{.Column}}"` + "`" + `
{{- end}}
	CreatedAt time.Time ` + "`" + `db:"created_at"` + "`" + `
	UpdatedAt time.Time ` + "`" + `db:"updated_at"` + "`" + `
}

// {{.TableVar}} is the table {{.Entity}} is a row of.
//
// UniqueIDs makes the primary key text the model fills on insert.
{{- if .Tenant}}
// The tenant scope is left at its tenant_id default.
{{- else}}
// Global says the table is shared by every tenant: the tenant scope is off, on
// purpose and where a reviewer sees it.
{{- end}}
var {{.TableVar}} = model.NewTable(model.TableSpec{
	Name:      {{quote .Table}},
	New:       func() model.Entity { return new({{.Entity}}) },
	UniqueIDs: true,
{{- if not .Tenant}}
	Global:    true,
{{- end}}
	// arandu:begin custom
	// Hidden, PerPage, Scopes, Events and the rest of model.TableSpec go here.
	// arandu:end custom
})

// LogValue implements slog.LogValuer, so passing the whole entity to a log call
// records the identifiers and nothing else. Add any sensitive field to the
// custom block below and it stays out of logs, dumps and the debug page.
func ({{.Receiver}} {{.Entity}}) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("id", {{.Receiver}}.ID),
{{- if .Tenant}}
		slog.String("tenant", {{.Receiver}}.TenantID),
{{- end}}
	)
}

// arandu:begin custom
// Local scopes are methods on *{{.Entity}}Query, relations are registered
// on {{.TableVar}} in an init function, and MarshalJSON, computed fields
// and anything else about this entity go here too.
//
// So do the rules of the entity itself: an invariant, a derived value, a
// transition that changes only this row's fields. They are pure -- no
// database, no network, no Grant, and no clock read here: the time is an
// argument -- and the service calls them between the policy and the save.
//
//	func ({{.Receiver}} *{{.Entity}}) Approve(at time.Time) error {
//		if {{.Receiver}}.ApprovedAt != nil {
//			return errors.New("this {{.Human}} is already approved")
//		}
//		{{.Receiver}}.ApprovedAt = &at
//		return nil
//	}
// arandu:end custom
`

const policyTemplate = `package policies

import (
	"context"
	"fmt"

	"github.com/arandu-io/hesape/auth"

	models "{{.ModelsImport}}"
)

// The actions of {{.Entity}}. Constants rather than strings at the call site: a
// typo in an action name would silently authorize nothing, or worse, everything.
//
// They carry the entity in the name because every policy in the application
// lives in this package now, and five constants called ActionView would not
// compile past the first module.
const (
	// {{.Entity}}View is reading one record.
	{{.Entity}}View auth.Action = "{{.Name}}.view"
	// {{.Entity}}List is paging through the records.
	{{.Entity}}List auth.Action = "{{.Name}}.list"
	// {{.Entity}}Create is adding one.
	{{.Entity}}Create auth.Action = "{{.Name}}.create"
	// {{.Entity}}Update is changing one.
	{{.Entity}}Update auth.Action = "{{.Name}}.update"
	// {{.Entity}}Delete is removing one.
	{{.Entity}}Delete auth.Action = "{{.Name}}.delete"
)

{{if .Rules -}}
// {{.PolicyType}} is the only authority over who does what with {{.Entity}}.
//
// The rules below came from the specification, where somebody said them out
// loud. Everything not listed is denied -- an action with no rule is an action
// nobody can take.
{{- else -}}
// {{.PolicyType}} is the only authority over who does what with {{.Entity}}.
//
// IT DENIES EVERYTHING. That is deliberate: a generated policy that allowed
// anything would be a hole shipped by default, in every project that ran the
// generator. Open what this module actually needs, and nothing else.
{{- end}}
type {{.PolicyType}} struct{}

// Compile-time proof that the policy answers about this entity and no other.
var _ auth.Policy[models.{{.Entity}}] = {{.PolicyType}}{}

// Can decides whether the subject may perform the action.
func ({{.PolicyType}}) Can(ctx context.Context, s auth.Subject, a auth.Action, {{.Receiver}} models.{{.Entity}}) error {
{{- if .Tenant}}
	// Tenant isolation comes first and applies to every action. Without it every
	// check below would be pointless in a multi-tenant system.
	if {{.Receiver}}.ID != "" && {{.Receiver}}.TenantID != s.Tenant {
		return fmt.Errorf("{{.Name}} belongs to another tenant")
	}
{{- end}}

{{if .Rules}}
	switch a {
{{- range .Rules}}
	case {{$.Entity}}{{.Action}}:
		if {{range $i, $role := .Roles}}{{if $i}} || {{end}}s.HasRole({{quote $role}}){{end}} {
			return nil
		}
{{- end}}
	}
{{end}}
	// arandu:begin custom
	// Anything the specification cannot express goes here: a rule that depends
	// on the entity rather than only on the role, a time window, a limit.
	//
	//	if a == {{.Entity}}Delete && {{.Receiver}}.Approved {
	//		return fmt.Errorf("an approved {{.Name}} cannot be deleted")
	//	}
	// arandu:end custom

	return fmt.Errorf("no rule allows %s on {{.Name}}", a)
}

// Writes reports whether the action changes a {{.Name}}. A Grant issued for an
// action this answers false for is a Grant for reading, and inserting,
// updating or deleting with it is refused: a service that authorized
// {{.Entity}}View and then saved is stopped at the save instead of writing.
//
// An action that changes a row and is not one of the three goes in the custom
// block. Left out, it is a reading action, and every write made with its Grant
// is refused.
func ({{.PolicyType}}) Writes(a auth.Action) bool {
	switch a {
	case {{.Entity}}Create, {{.Entity}}Update, {{.Entity}}Delete:
		return true
	}

	// arandu:begin custom
	// The write actions this policy opens beyond the three:
	//
	//	if a == {{.Entity}}Approve {
	//		return true
	//	}
	// arandu:end custom

	return false
}
`

const serviceTemplate = `package services

import (
	"context"
	"fmt"
{{- if .HasEmail}}
	"strings"
{{- end}}

	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/database"
	"github.com/arandu-io/hesape/log"
	"github.com/arandu-io/hesape/pagination"

	models "{{.ModelsImport}}"
	policies "{{.PoliciesImport}}"
	requests "{{.RequestsImport}}"
)

// {{.ListingVar}} declares, once, what the listing of {{.Humans}} may search,
// order by, page by and hide. A listing is always a page, never everything: an
// unbounded query is how one page load takes a database down.
//
// Nothing the request carries names a column. The controller reads the request
// through this value, and it drops whatever it does not declare -- a sort that
// is not a key below, a page size that is not offered, a direction that is
// neither asc nor desc -- so a hostile query string falls back to the default
// instead of reaching the query. Every column below is written here, in code.
//
// The Search columns hold text. A facet -- a filter over a column with a short
// list of values, such as a status -- is one more entry of Facets, with the
// values listed in the spec:
//
//	Facets: []pagination.FacetSpec{{"{{"}}Key: "status", Column: "status", Values: []string{"open", "done"}{{"}}"}},
//
// and a DataTableFilter of the same Key in the index view draws it.
var {{.ListingVar}} = func() *pagination.Listing {
	listing, err := pagination.NewListing(pagination.ListingSpec{
{{- if .SearchFields}}
		Search: []string{
{{- range .SearchFields}}
			{{quote .Column}},
{{- end}}
		},
{{- end}}
		Sorts: map[string]string{
{{- range .Fields}}
			{{quote .Column}}: {{quote .Column}},
{{- end}}
			"created_at": "created_at",
		},
		DefaultSort: "created_at",
		DefaultDir:  pagination.SortDesc,
		TieBreak:    "id",
		PageSizes:   []int{10, 25, 50},
		DefaultPageSize: 25,
		Hideable: []string{
{{- range .RestFields}}
			{{quote .Column}},
{{- end}}
			"created_at",
		},
	})
	if err != nil {
		// A spec the package refuses is a mistake in this file, and it is found
		// when the program starts and not on the first request.
		panic(fmt.Sprintf("services: the {{.Humans}} listing: %v", err))
	}
	return listing
}()

{{template "serviceStruct" .}}{{template "serviceCreate" .}}// Get returns one {{.Name}}.
//
// It authorizes twice: once to look at all, and once with the row that was
// read, which is the decision about this row rather than about the action.
func (s *{{.ServiceType}}) Get(ctx context.Context, actor auth.Subject, id string) (*models.{{.Entity}}, error) {
	g, err := auth.Authorize(ctx, s.policy, actor, policies.{{.Entity}}View, models.{{.Entity}}{})
	if err != nil {
		return nil, err
	}
	found, err := models.{{.Constructor}}(s.db).FindOrFail(ctx, g, id)
	if err != nil {
		return nil, err
	}
	if _, err := auth.Authorize(ctx, s.policy, actor, policies.{{.Entity}}View, *found); err != nil {
		return nil, err
	}
	return found, nil
}

// Editable returns one {{.Name}} for the edit form, which is shown only to
// somebody who may change it. It reads the row as Get does and then asks the
// update action about the row that was read: a form that opened for anybody
// who may look would let them type into a save they are refused.
func (s *{{.ServiceType}}) Editable(ctx context.Context, actor auth.Subject, id string) (*models.{{.Entity}}, error) {
	found, err := s.Get(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	if _, err := auth.Authorize(ctx, s.policy, actor, policies.{{.Entity}}Update, *found); err != nil {
		return nil, err
	}
	return found, nil
}

// Listing is what the listing of {{.Humans}} accepts. The controller reads the
// request through it and hands the state it returns to List.
func (s *{{.ServiceType}}) Listing() *pagination.Listing { return {{.ListingVar}} }

{{- if .Parent}}

// List returns one page of the {{.Table}} of one {{.ParentParam}} as the listing
// state asks -- the search, the order, the page and its size -- and the result
// a table is drawn from, newest first unless the state orders otherwise. path
// is the address the listing is served at, which its links are built on. The
// {{.ParentParam}} is loaded under its own policy first, and the page is
// filtered by the one that was loaded -- not by the id the path carried.
func (s *{{.ServiceType}}) List(ctx context.Context, actor auth.Subject, {{.ParentArg}} string, state pagination.ListingState, path string) (models.{{.Entity}}Collection, *pagination.ListingResult, error) {
	parent, err := s.parents.Get(ctx, actor, {{.ParentArg}})
	if err != nil {
		return nil, nil, err
	}
	g, err := auth.Authorize(ctx, s.policy, actor, policies.{{.Entity}}List, models.{{.Entity}}{ {{- .ParentField}}: parent.ID})
	if err != nil {
		return nil, nil, err
	}
	return models.{{.Constructor}}(s.db).Where("{{.ParentColumn}}", parent.ID).Listing(ctx, g, state, path)
}
{{- else}}

// List returns one page of {{.Table}} as the listing state asks -- the search,
// the order, the page and its size -- and the result a table is drawn from,
// newest first unless the state orders otherwise. path is the address the
// listing is served at, which its links are built on.
//
// The state is the one {{.ServiceType}}.Listing read: a state read by another
// listing is refused, because it knows none of these columns.
func (s *{{.ServiceType}}) List(ctx context.Context, actor auth.Subject, state pagination.ListingState, path string) (models.{{.Entity}}Collection, *pagination.ListingResult, error) {
	g, err := auth.Authorize(ctx, s.policy, actor, policies.{{.Entity}}List, models.{{.Entity}}{})
	if err != nil {
		return nil, nil, err
	}
	// Listing runs the page, the total and the count of every facet value, each
	// under this Grant and so each filtered by its tenant.
	return models.{{.Constructor}}(s.db).Listing(ctx, g, state, path)
}
{{- end}}

// Update changes the mutable fields.
//
// It reads before writing, so the policy decides against the stored row rather
// than against what the client claims the row is. Skipping this is how a check
// passes on attacker-supplied data.
func (s *{{.ServiceType}}) Update(ctx context.Context, actor auth.Subject, id string, in requests.{{.Request}}) (*models.{{.Entity}}, error) {
	if errs := in.Validate(); errs.Any() {
		return nil, errs
	}

	stored, err := s.Get(ctx, actor, id)
	if err != nil {
		return nil, err
	}

	g, err := auth.Authorize(ctx, s.policy, actor, policies.{{.Entity}}Update, *stored)
	if err != nil {
		return nil, err
	}
	s.fill(stored, in)
	if _, err := stored.Save(ctx, g); err != nil {
		return nil, err
	}
	return stored, nil
}

// Delete removes a {{.Name}}.
func (s *{{.ServiceType}}) Delete(ctx context.Context, actor auth.Subject, id string) error {
	stored, err := s.Get(ctx, actor, id)
	if err != nil {
		return err
	}

	g, err := auth.Authorize(ctx, s.policy, actor, policies.{{.Entity}}Delete, *stored)
	if err != nil {
		return err
	}
	if _, err := stored.Delete(ctx, g); err != nil {
		return err
	}
	if col := log.FromContext(ctx); col != nil {
		col.RecordEvent("{{.Name}}.deleted", stored)
	}
	return nil
}

{{template "serviceFill" .}}// arandu:begin custom
// Business rules beyond CRUD go here, and survive regeneration.
// arandu:end custom
`

// serviceBlocks are the parts of a service that every service the generator
// writes shares: the type and its constructor, Create, and fill.
//
// ` + "`aru make:module`" + ` writes them with the rest of the use cases, and
// ` + "`aru make:service`" + ` writes them alone. One template, so the Create a
// person meets in either file is the same method -- validate, Authorize, Grant,
// Model -- and a correction to it reaches both.
const serviceBlocks = `{{define "serviceStruct"}}// {{.ServiceType}} holds the business rules. It receives its dependencies through
// the constructor -- explicit wiring, no container.
//
// Every method authorizes before it reaches the Model, and the errors it returns
// are the ones the router answers: validation.Errors back to the form, a missing
// row as 404, a refusal as 403{{if .UniqueFields}} and a duplicate on a unique column as 409{{end}}.
type {{.ServiceType}} struct {
	db     *database.DB
	policy policies.{{.PolicyType}}
{{- if .Parent}}
	// parents loads the {{.ParentParam}} a {{.Human}} belongs to, under the
	// {{.ParentParam}}'s own policy: the parent in a path is where the person
	// navigated, never whose data it is.
	parents *{{.ParentServiceType}}
{{- end}}
}

// New{{.ServiceType}} wires the service.
func New{{.ServiceType}}(db *database.DB{{if .Parent}}, parents *{{.ParentServiceType}}{{end}}) *{{.ServiceType}} {
	return &{{.ServiceType}}{db: db{{if .Parent}}, parents: parents{{end}}}
}

{{end}}{{define "serviceCreate"}}// Create walks the mandatory path: validate, Authorize, Grant, Model.
// There is no other order that compiles.
func (s *{{.ServiceType}}) Create(ctx context.Context, actor auth.Subject, {{if .Parent}}{{.ParentArg}} string, {{end}}in requests.{{.Request}}) (*models.{{.Entity}}, error) {
	if errs := in.Validate(); errs.Any() {
		return nil, errs
	}
{{- if .Parent}}

	// The {{.ParentParam}} is loaded through its own service, so one this
	// subject may not see is a refusal here and never a row under it.
	parent, err := s.parents.Get(ctx, actor, {{.ParentArg}})
	if err != nil {
		return nil, err
	}
{{- end}}

	var proposed models.{{.Entity}}
	s.fill(&proposed, in)
{{- if .Parent}}
	proposed.{{.ParentField}} = parent.ID
{{- end}}
	g, err := auth.Authorize(ctx, s.policy, actor, policies.{{.Entity}}Create, proposed)
	if err != nil {
		return nil, err
	}

	record, err := models.{{.Constructor}}(s.db).New()
	if err != nil {
		return nil, err
	}
	s.fill(record, in)
{{- if .Parent}}
	record.{{.ParentField}} = parent.ID
{{- end}}
{{- if .Tenant}}
	// The tenant comes from the Grant, never from the request or the subject
	// directly. The model writes the same value over the insert attributes.
	record.TenantID = auth.Tenant(g)
{{- end}}
	if _, err := record.Save(ctx, g); err != nil {
		return nil, err
	}
	// Guarded: the entity is a struct value, and boxing it into ` + "`" + `any` + "`" + ` allocates
	// at the call site even though RecordEvent is a no-op on a nil Collector.
	if col := log.FromContext(ctx); col != nil {
		col.RecordEvent("{{.Name}}.created", record)
	}
	return record, nil
}

{{end}}{{define "serviceFill"}}// fill writes the request onto the record. It is the one place a field of the
// form becomes a column, for Create and Update alike.
func (s *{{.ServiceType}}) fill({{.Receiver}} *models.{{.Entity}}, in requests.{{.Request}}) {
{{- range .Fields}}
{{- if .IsEmail}}
	{{$.Receiver}}.{{.GoName}} = strings.ToLower(in.{{.GoName}})
{{- else if .Nullable}}
	{{$.Receiver}}.{{.GoName}} = in.{{.GoName}}
{{- if .IsDate}}
	if in.{{.GoName}} != nil {
		day := database.Day(*in.{{.GoName}})
		{{$.Receiver}}.{{.GoName}} = &day
	}
{{- end}}
{{- else}}
	{{$.Receiver}}.{{.GoName}} = {{.Bind "in"}}
{{- end}}
{{- end}}
}

{{end}}`

const requestTemplate = `package requests

{{if .NeedsTimeParse}}import (
	"time"

	"github.com/arandu-io/hesape/validation"
){{else}}import "github.com/arandu-io/hesape/validation"{{end}}

// {{.Request}} is the input contract of creation and update, which take the
// same fields. ctx.Bind fills it through the form tags, and only those: there is
// no mass assignment, so a key the client sends and this struct does not
// declare goes nowhere.
type {{.Request}} struct {
{{- template "requestFields" .}}
}

// Validate reports the errors per field.
func (r {{.Request}}) Validate() validation.Errors {
	e := validation.Errors{}
{{- template "storeRules" .}}

	// arandu:begin custom
	// Domain rules go here: ranges, formats, cross-field checks.
	// arandu:end custom

	return e
}

// Compile-time proof that the request honors the validation contract.
var _ validation.Validatable = {{.Request}}{}
`

// requestRulesTemplate is the request's fields and their validation, whichever
// command wrote the request.
//
// `aru make:module` and `aru make:request` both emit a request, and the fields
// and the rules inside them are the same bytes because it is the same template.
// Copying it would have been shorter to write and would have diverged on the
// first correction nobody remembered to make twice.
//
// The form tag on each field is the name the input carries, which is the
// column: ctx.Bind reads that key and no other into the field.
//
// It renders against anything with a Fields slice, which both gen.Module and
// gen.Stub have.
const requestRulesTemplate = `{{define "requestFields"}}
{{- range .Fields}}
	{{.GoName}} {{.ModelType}} {{.FormTag}}
{{- end}}
{{- end}}{{define "storeRules"}}
{{- range .Fields}}
{{- if .Required}}
	{{if .IsString}}validation.Required(e, "{{.Column}}", r.{{.GoName}}){{else}}validation.NotZero(e, "{{.Column}}", r.{{.GoName}}){{end}}
{{- end}}
{{- if .IsEmail}}
	validation.Email(e, "{{.Column}}", r.{{.GoName}})
{{- end}}
{{- if .IsString}}
	validation.MaxLen(e, "{{.Column}}", r.{{.GoName}}, {{.MaxLength}})
{{- end}}
{{- end}}
{{- end}}`

const controllerTemplate = `package controllers

import (
	"net/http"
{{- if .NeedsStrconv}}
	"strconv"
{{- end}}

	hcontrollers "github.com/arandu-io/hesape/routing/controllers"
	hhttp "github.com/arandu-io/hesape/http"
	"github.com/arandu-io/hesape/view"

	requests "{{.RequestsImport}}"
	models "{{.ModelsImport}}"
	services "{{.ServicesImport}}"
	views "{{.ViewsImport}}"
)

// {{.Controller}} answers the seven routes of the {{.RouteResource}} resource.
{{- if .Parent}}
//
// It nests under {{.Parent}}, shallow: the listing, the form and the store
// read the {{.ParentParam}} from the path, and the four that act on one record
// read the record alone.
{{- end}}
//
// It is thin on purpose: read the request, call the service, render. There is no
// repository here and there cannot be one -- hhttp.Context carries no database
// handle, so a controller that reached the data layer would be a controller that
// skipped the service, and therefore skipped the policy.
//
// The routes sit behind middleware.RequireAuth, which puts who is asking on the
// request: ctx.User reads it. Without the guard there is nobody there, and the
// policy refuses the zero subject. An error an action returns is answered by the
// router -- validation.Errors back to the form with the messages and what was
// typed, a missing row as 404, a refusal as 403 -- so no action maps one itself.
//
// view.New is the whole of a page's chrome: the title, what a rejected attempt
// left in the flash, and the CSRF token the protecting middleware issued for this
// request. No action issues a token, so the service is all this needs.
type {{.Controller}} struct {
	Controller

	svc *services.{{.ServiceType}}
}

// New{{.Controller}} returns the controller. app/Providers builds it and hands
// it to the routes.
func New{{.Controller}}(svc *services.{{.ServiceType}}) *{{.Controller}} {
	return &{{.Controller}}{svc: svc}
}

// Compile-time proof of the seven actions hcontrollers.Router.Resource looks for. It
// registers the ones the controller implements and nothing else, so a route that
// exists is a route that answers -- and a renamed method fails the build here
// rather than answering 404 in production.
var (
	_ hcontrollers.Indexer   = (*{{.Controller}})(nil)
	_ hcontrollers.Creator   = (*{{.Controller}})(nil)
	_ hcontrollers.Storer    = (*{{.Controller}})(nil)
	_ hcontrollers.Shower    = (*{{.Controller}})(nil)
	_ hcontrollers.Editor    = (*{{.Controller}})(nil)
	_ hcontrollers.Updater   = (*{{.Controller}})(nil)
	_ hcontrollers.Destroyer = (*{{.Controller}})(nil)
)

// Index renders the listing: the search, the order, the facets, the page and its
// size, and the columns the reader hid, all read from the query string.
//
// The query string is read through the service's listing, which keeps what it
// declares and drops the rest, so nothing here names a column or a size. The
// table is the one part of the page the server also answers alone: a control of
// the table asks for it by naming it as the htmx target, and every other request
// to the same address -- a direct visit, a boosted link, a history restore --
// gets the whole page.
func (c *{{.Controller}}) Index(ctx *hhttp.Context) error {
{{- if .Parent}}
	// The {{.ParentParam}} the person navigated to. The service loads it under
	// its own policy and lists by the one it loaded.
	parent := ctx.Param("{{.ParentParam}}")
{{- end}}
	who, _ := ctx.User()
	listURL := ctx.URL("{{.RouteName "index"}}"{{if .Parent}}, parent{{end}})
	state := c.svc.Listing().ParseURL(ctx.Request.URL)
	found, result, err := c.svc.List(ctx.Ctx(), who, {{if .Parent}}parent, {{end}}state, listURL)
	if err != nil {
		return err
	}

	rows := make([]views.{{.RowStruct}}, 0, len(found))
	for _, {{.Receiver}} := range found {
		rows = append(rows, c.row(ctx, {{.Receiver}}))
	}
	data := views.{{.ViewData "index"}}{
		Page: view.New(ctx, "{{.HumansTitle}}"),
		{{.Plural}}: rows,
		Result:     result,
		ListURL:    listURL,
		NewURL:     ctx.URL("{{.RouteName "create"}}"{{if .Parent}}, parent{{end}}),
	}
	if ctx.Targets(views.{{.TableIDConst}}) {
		return ctx.Fragment(http.StatusOK, "{{.TablePartial}}", data)
	}
	return ctx.View("{{.ViewName "index"}}", data)
}

// Show renders one record.
func (c *{{.Controller}}) Show(ctx *hhttp.Context) error {
	who, _ := ctx.User()
	found, err := c.svc.Get(ctx.Ctx(), who, ctx.Param("{{.MemberParam}}"))
	if err != nil {
		return err
	}

	return ctx.View("{{.ViewName "show"}}", views.{{.ViewData "show"}}{
		Page: view.New(ctx, "{{.HumanTitle}}"),
		{{.Entity}}: c.row(ctx, found),
		IndexURL:  ctx.URL("{{.RouteName "index"}}"{{if .Parent}}, found.{{.ParentField}}{{end}}),
		EditURL:   ctx.URL("{{.RouteName "edit"}}", found.ID),
		DeleteURL: ctx.URL("{{.RouteName "destroy"}}", found.ID),
	})
}

// Create renders the empty form, or the rejected one: the page carries what
// was typed and the messages, from the flash the router left.
func (c *{{.Controller}}) Create(ctx *hhttp.Context) error {
{{- if .Parent}}
	parent := ctx.Param("{{.ParentParam}}")
{{- end}}
	return ctx.View("{{.ViewName "create"}}", views.{{.ViewData "create"}}{
		Page: view.New(ctx, "New {{.Human}}"),
		IndexURL: ctx.URL("{{.RouteName "index"}}"{{if .Parent}}, parent{{end}}),
		StoreURL: ctx.URL("{{.RouteName "store"}}"{{if .Parent}}, parent{{end}}),
	})
}

// Store takes the submitted form.
func (c *{{.Controller}}) Store(ctx *hhttp.Context) error {
	var in requests.{{.Request}}
	if err := ctx.Bind(&in); err != nil {
		return err
	}
	who, _ := ctx.User()
	created, err := c.svc.Create(ctx.Ctx(), who, {{if .Parent}}ctx.Param("{{.ParentParam}}"), {{end}}in)
	if err != nil {
		return err
	}
	return ctx.RedirectRoute("{{.RouteName "show"}}", created.ID)
}

// Edit renders the form filled in with the stored record, to somebody the
// policy lets update it.
func (c *{{.Controller}}) Edit(ctx *hhttp.Context) error {
	who, _ := ctx.User()
	found, err := c.svc.Editable(ctx.Ctx(), who, ctx.Param("{{.MemberParam}}"))
	if err != nil {
		return err
	}

	return ctx.View("{{.ViewName "edit"}}", views.{{.ViewData "edit"}}{
		Page: view.New(ctx, "Edit {{.Human}}"),
		Form:      c.row(ctx, found),
		ShowURL:   ctx.URL("{{.RouteName "show"}}", found.ID),
		UpdateURL: ctx.URL("{{.RouteName "update"}}", found.ID),
	})
}

// Update writes the submitted form onto the stored record.
func (c *{{.Controller}}) Update(ctx *hhttp.Context) error {
	var in requests.{{.Request}}
	if err := ctx.Bind(&in); err != nil {
		return err
	}
	who, _ := ctx.User()
	updated, err := c.svc.Update(ctx.Ctx(), who, ctx.Param("{{.MemberParam}}"), in)
	if err != nil {
		return err
	}
	return ctx.RedirectRoute("{{.RouteName "show"}}", updated.ID)
}

// Destroy removes the record.
func (c *{{.Controller}}) Destroy(ctx *hhttp.Context) error {
	who, _ := ctx.User()
{{- if .Parent}}
	// Read first: the listing to go back to is the {{.ParentParam}}'s, and the
	// path of a member route does not carry it.
	found, err := c.svc.Get(ctx.Ctx(), who, ctx.Param("{{.MemberParam}}"))
	if err != nil {
		return err
	}
	if err := c.svc.Delete(ctx.Ctx(), who, found.ID); err != nil {
		return err
	}
	return ctx.RedirectRoute("{{.RouteName "index"}}", found.{{.ParentField}})
{{- else}}
	if err := c.svc.Delete(ctx.Ctx(), who, ctx.Param("{{.MemberParam}}")); err != nil {
		return err
	}
	return ctx.RedirectRoute("{{.RouteName "index"}}")
{{- end}}
}

// row turns the entity into what the markup renders: the text a cell shows and
// an input of the edit form starts at.
//
// Formatting happens here rather than in the view: a view that formats a
// time.Time would need the time package, and what a date looks like on screen is
// a decision about presentation, which is this side of the line.
//
// The address is settled here too, for the same reason and one more: the view
// has no route table, so a link written there could only be a literal. This
// takes the context so it can ask for the route by name.
func (c *{{.Controller}}) row(ctx *hhttp.Context, {{.Receiver}} *models.{{.Entity}}) views.{{.RowStruct}} {
	{{if .HasNullable}}row := {{else}}return {{end}}views.{{.RowStruct}}{
		ID:  {{.Receiver}}.ID,
		URL: ctx.URL("{{.RouteName "show"}}", {{.Receiver}}.ID),
{{- range .Fields}}{{if not .Nullable}}
		{{.GoName}}: {{.RowValue $.Receiver}},
{{- end}}{{end}}
		Created: {{.Receiver}}.CreatedAt.Format("2006-01-02 15:04"),
	}
{{- if .HasNullable}}
	// A time nobody filled in is an empty cell and an empty input, never the
	// first day of year one.
{{- range .Fields}}{{if .Nullable}}
	if {{$.Receiver}}.{{.GoName}} != nil {
		row.{{.GoName}} = {{$.Receiver}}.{{.GoName}}.Format("{{.TimeLayout}}")
	}
{{- end}}{{end}}
	return row
{{- end}}
}

// arandu:begin custom
// Actions beyond the seven go here, and survive regeneration. Register them in
// the custom block of routes/web.go.
// arandu:end custom
`

const testTemplate = `package unit_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/database/model"
	"github.com/arandu-io/hesape/pagination"

	models "{{.ModelsImport}}"
	policies "{{.PoliciesImport}}"
	services "{{.ServicesImport}}"
)

// {{.Unexported}}Persistent is the Model-first persistence boundary this module
// depends on. The proofs below fail at compile time if the entity stops
// embedding the Hesape model -- nothing else satisfies model.Entity -- or if its
// generated query can be run without a Grant.
type {{.Unexported}}Persistent interface {
	model.Entity
	Save(context.Context, auth.Grant) (bool, error)
}

var (
	_ {{.Unexported}}Persistent = (*models.{{.Entity}})(nil)
	_ func(*models.{{.Entity}}Query, context.Context, auth.Grant, ...any) (models.{{.Entity}}Collection, error) = (*models.{{.Entity}}Query).Get
)

// TestEvery{{.Entity}}ReadRequiresAuthorization needs no database: the service
// authorizes each read before it asks the Model for a query. A nil handle turns
// an accidental query-before-policy into an immediate test failure.
func TestEvery{{.Entity}}ReadRequiresAuthorization(t *testing.T) {
	svc := services.New{{.ServiceType}}(nil{{if .Parent}}, services.New{{.ParentServiceType}}(nil){{end}})
	ctx := context.Background()
	var anonymous auth.Subject

	calls := map[string]func() error{
		"Get": func() error {
			_, err := svc.Get(ctx, anonymous, "id")
			return err
		},
		"Editable": func() error {
			_, err := svc.Editable(ctx, anonymous, "id")
			return err
		},
		"List": func() error {
			_, _, err := svc.List(ctx, anonymous, {{if .Parent}}"{{.ParentParam}}", {{end}}svc.Listing().Parse(nil), "")
			return err
		},
		"Delete": func() error {
			return svc.Delete(ctx, anonymous, "id")
		},
	}

	for name, call := range calls {
		t.Run(name+" with no subject", func(t *testing.T) {
			if err := call(); !errors.Is(err, auth.ErrForbidden) {
				t.Fatalf("error = %v, want ErrForbidden", err)
			}
		})
	}
}

// TestThe{{.Entity}}PolicyDeniesWhatItDoesNotKnow is the property that keeps a
// policy safe as it grows: an action nobody wrote a rule for is refused, rather
// than falling through to allowed.
//
// It uses an action that will never be opened, so it keeps passing after you open
// the real ones -- a test that breaks when you do what the generator told you to
// do is a test people delete.
func TestThe{{.Entity}}PolicyDeniesWhatItDoesNotKnow(t *testing.T) {
	admin := auth.Subject{ID: "a1", Tenant: "t1", Roles: []string{"admin", "staff"}}

	err := (policies.{{.PolicyType}}{}).Can(context.Background(), admin,
		"{{.Name}}.action_that_does_not_exist", models.{{.Entity}}{})

	if err == nil {
		t.Fatal("an action with no rule was allowed: the policy falls through to allowed")
	}
}

// TestThe{{.Entity}}ListingDropsWhatItDoesNotDeclare feeds the listing the query
// string a hostile reader would write -- a sort that is a statement, a direction
// that is not one, a page size and a page nobody offered, a search made only of
// wildcards, columns that were never hideable -- and asks what it kept. It keeps
// the defaults: the request cannot name a column, a size or a page the listing
// did not declare, and it needs no database to prove it.
func TestThe{{.Entity}}ListingDropsWhatItDoesNotDeclare(t *testing.T) {
	listing := services.New{{.ServiceType}}(nil{{if .Parent}}, services.New{{.ParentServiceType}}(nil){{end}}).Listing()

	state := listing.Parse(url.Values{
		"sort":     {"id; drop table users; --"},
		"dir":      {"sideways"},
		"per_page": {"1000000"},
		"page":     {"-4"},
		"q":        {strings.Repeat("%", 1000)},
		"hide":     {"id", "tenant_id", "password"},
	})

	for _, order := range state.Order() {
		// A column the spec declares sortable, or the tie-break on the key.
		if !listing.Sortable(order.Column) && order.Column != "id" {
			t.Errorf("the listing orders by %q, a column its spec does not declare", order.Column)
		}
	}
	if state.SortKey() != "created_at" || state.Direction() != pagination.SortDesc {
		t.Errorf("the sort is %q %s, want the default: created_at desc", state.SortKey(), state.Direction())
	}
	if state.PerPage() != listing.DefaultPageSize() {
		t.Errorf("the page size is %d, want the default %d", state.PerPage(), listing.DefaultPageSize())
	}
	if state.Page() != 1 {
		t.Errorf("the page is %d, want 1", state.Page())
	}
	if len([]rune(state.Search())) > 200 {
		t.Errorf("the search kept %d characters, want at most 200", len([]rune(state.Search())))
	}
	if hidden := state.Hidden(); len(hidden) != 0 {
		t.Errorf("the listing hid %v, columns it never declared hideable", hidden)
	}
}
// arandu:begin custom
// Tests for the rules you wrote go here, and survive regeneration.
// arandu:end custom
`

// tenantScopeTestTemplate is the feature test a tenant-scoped module ships
// with. It runs against the database the skeleton's feature suite migrates,
// through migratedDB, and stores and reads with system Grants because there is
// no request: what it proves is the model's scope, not the policy.
const tenantScopeTestTemplate = `package feature_test

import (
	"context"
	"testing"

	"github.com/arandu-io/hesape/auth"

	models "{{.ModelsImport}}"
	policies "{{.PoliciesImport}}"
	factories "{{.ModulePath}}/database/factories"
)

// Test{{.Plural}}AreScopedByTenant stores {{.Humans}} for one tenant and reads
// them with another tenant's Grant. The rows exist and the queries run, and
// nothing comes back: the model takes the tenant from the Grant and from
// nowhere else, so to a second tenant the table is empty.
//
// It is the evidence for the line TestEveryTableWithATenantColumnIsOneThatFiltersByTenant
// asks for: that one reads the catalogue and requires a claim for {{.Table}},
// this one proves the claim.
func Test{{.Plural}}AreScopedByTenant(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
{{if .Parent}}
	// Each {{.Human}} belongs to one {{.ParentHuman}}, so the rows are stored
	// under one that is stored too.
	parent, err := factories.{{.ParentConstructor}}(db).CreateOne(ctx, auth.SystemGrant(policies.{{.ParentEntity}}Create, "tenant-a"))
	if err != nil {
		t.Fatalf("storing the {{.ParentHuman}} for tenant-a: %v", err)
	}
	stored, err := factories.{{.Constructor}}(db).For{{.ParentEntity}}(parent.ID).Count(2).Create(ctx, auth.SystemGrant(policies.{{.Entity}}Create, "tenant-a"))
{{- else}}
	stored, err := factories.{{.Constructor}}(db).Count(2).Create(ctx, auth.SystemGrant(policies.{{.Entity}}Create, "tenant-a"))
{{- end}}
	if err != nil {
		t.Fatalf("storing {{.Humans}} for tenant-a: %v", err)
	}

	theirs := auth.SystemGrant(policies.{{.Entity}}List, "tenant-b")
	found, err := models.{{.Constructor}}(db).Get(ctx, theirs)
	if err != nil {
		t.Fatalf("listing as tenant-b: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("tenant-b read %d of tenant-a's {{.Humans}}", len(found))
	}
	for _, row := range stored {
		if _, err := models.{{.Constructor}}(db).FindOrFail(ctx, theirs, row.ID); err == nil {
			t.Errorf("tenant-b found tenant-a's {{.Human}} %s by its id", row.ID)
		}
	}

	ours, err := models.{{.Constructor}}(db).Get(ctx, auth.SystemGrant(policies.{{.Entity}}List, "tenant-a"))
	if err != nil || len(ours) < len(stored) {
		t.Errorf("tenant-a read %d of its %d {{.Humans}} (%v): the scope hides rows from their own tenant", len(ours), len(stored), err)
	}
}
// arandu:begin custom
// arandu:end custom
`
