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
// would otherwise be `sortable` is `invoiceSortable` -- or, where the framework
// already has it, `data.NewID`.

const modelTemplate = `package models

import (
	"errors"
	"log/slog"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/hesape/database/model"
)

// {{.Entity}} is one row of {{.Table}}.
//
// It embeds the model, so a row returned by a query carries the connection and
// can be saved again. Build new rows through {{.Plural}}: a struct literal has
// no connection and its write methods return model.ErrUnwired.
type {{.Entity}} struct {
	model.Model[{{.Entity}}]

	ID        string    ` + "`" + `db:"id"` + "`" + `
{{- if .Tenant}}
	TenantID  string    ` + "`" + `db:"tenant_id"` + "`" + `
{{- end}}
{{- range .Fields}}
	{{.GoName}} {{.GoType}} ` + "`" + `db:"{{.Column}}"` + "`" + `
{{- end}}
	CreatedAt time.Time ` + "`" + `db:"created_at"` + "`" + `
	UpdatedAt time.Time ` + "`" + `db:"updated_at"` + "`" + `
}

// {{.Plural}} returns the configured model for {{.Table}}.
//
// The primary key is application-generated text, so it does not increment.
// The tenant scope is {{if .Tenant}}left at its tenant_id default{{else}}disabled explicitly because this table is global{{end}}.
func {{.Plural}}(db *data.DB) *model.Model[{{.Entity}}] {
	m := model.NewModel[{{.Entity}}]({{quote .Table}}, db, db.GetQueryGrammar(), db.GetPostProcessor())
	m.KeyType = "string"
	m.Incrementing = false
{{- if not .Tenant}}
	m.TenantColumn = ""
{{- end}}
	return m
}

// What can go wrong with a {{.Name}}, declared beside the entity.
//
// The controller maps them to a status code and the service returns them, so
// both need to name them without giving the controller a door to the data layer.
var (
	// Err{{.Entity}}NotFound is returned when no row matches{{if .Tenant}}, including when the row
	// exists in another tenant -- the two are deliberately indistinguishable{{end}}.
	Err{{.Entity}}NotFound = errors.New("{{.Name}}: not found")
	// Err{{.Entity}}Conflict is a unique constraint refusing a duplicate.
	Err{{.Entity}}Conflict = errors.New("{{.Name}}: already exists")
	// Err{{.Entity}}Sort is an ordering the allowlist does not contain.
	Err{{.Entity}}Sort = errors.New("{{.Name}}: sort field not allowed")
)

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
// MarshalJSON, computed fields and anything else about this entity go here.
// arandu:end custom
`

const policyTemplate = `package policies

import (
	"context"
	"fmt"

	"github.com/arandu-io/framework/security"

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
	{{.Entity}}View security.Action = "{{.Name}}.view"
	// {{.Entity}}List is paging through the records.
	{{.Entity}}List security.Action = "{{.Name}}.list"
	// {{.Entity}}Create is adding one.
	{{.Entity}}Create security.Action = "{{.Name}}.create"
	// {{.Entity}}Update is changing one.
	{{.Entity}}Update security.Action = "{{.Name}}.update"
	// {{.Entity}}Delete is removing one.
	{{.Entity}}Delete security.Action = "{{.Name}}.delete"
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
var _ security.Policy[models.{{.Entity}}] = {{.PolicyType}}{}

// Can decides whether the subject may perform the action.
func ({{.PolicyType}}) Can(ctx context.Context, s security.Subject, a security.Action, {{.Receiver}} models.{{.Entity}}) error {
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
`

const serviceTemplate = `package services

import (
	"context"
	"fmt"
	"strings"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/database/model"

	models "{{.ModelsImport}}"
	policies "{{.PoliciesImport}}"
	requests "{{.RequestsImport}}"
)

// Pagination bounds for {{.Table}}. A request that asks for everything gets the
// maximum, never everything: an unbounded query is how one page load takes a
// database down.
const (
	{{.Unexported}}DefaultLimit = 50
	{{.Unexported}}MaxLimit     = 200
)

// {{.Unexported}}Sortable is the ordering allowlist. A sort field is a column
// name, and a column name taken from the request is injection through a door
// nobody watches.
var {{.Unexported}}Sortable = map[string]string{
	"":           "created_at",
	"created_at": "created_at",
{{- range .Sortable}}
	"{{.Column}}": "{{.Column}}",
{{- end}}
}

// {{.ServiceType}} holds the business rules. It receives its dependencies through
// the constructor -- explicit wiring, no container.
type {{.ServiceType}} struct {
	db     *data.DB
	policy policies.{{.PolicyType}}
}

// New{{.ServiceType}} wires the service.
func New{{.ServiceType}}(db *data.DB) *{{.ServiceType}} {
	return &{{.ServiceType}}{db: db}
}

// Create walks the mandatory path: validate, Authorize, Grant, Model.
// There is no other order that compiles.
func (s *{{.ServiceType}}) Create(ctx context.Context, actor security.Subject, in requests.{{.StoreRequest}}) (*models.{{.Entity}}, error) {
	if errs := in.Validate(); errs.Any() {
		return nil, errs
	}

	proposed := models.{{.Entity}}{}
{{- range .Fields}}
{{- if .IsEmail}}
	proposed.{{.GoName}} = s.normalize(in.{{.GoName}})
{{- else}}
	proposed.{{.GoName}} = {{.Bind "in"}}
{{- end}}
{{- end}}

	g, err := security.Authorize(ctx, s.policy, actor, policies.{{.Entity}}Create, proposed)
	if err != nil {
		return nil, err
	}
	if proposed.ID, err = data.NewID(); err != nil {
		return nil, err
	}
{{- if .Tenant}}
	// The tenant comes from the Grant, never from the request or the subject
	// directly. The model writes the same value over the insert attributes.
	proposed.TenantID = data.Tenant(g)
{{- end}}

	instance, err := models.{{.Plural}}(s.db).NewInstance(nil, false)
	if err != nil {
		return nil, err
	}
	candidate := instance.Entity
	candidate.ID = proposed.ID
{{- if .Tenant}}
	candidate.TenantID = proposed.TenantID
{{- end}}
{{- range .Fields}}
	candidate.{{.GoName}} = proposed.{{.GoName}}
{{- end}}
	if _, err := candidate.Save(ctx, g); err != nil {
		if s.conflict(err) {
			return nil, models.Err{{.Entity}}Conflict
		}
		return nil, err
	}
	// Guarded: the entity is a struct value, and boxing it into ` + "`" + `any` + "`" + ` allocates
	// at the call site even though RecordEvent is a no-op on a nil Collector.
	if col := observability.FromContext(ctx); col != nil {
		col.RecordEvent("{{.Name}}.created", candidate)
	}
	return candidate, nil
}

// Get returns one {{.Name}}.
func (s *{{.ServiceType}}) Get(ctx context.Context, actor security.Subject, id string) (*models.{{.Entity}}, error) {
	g, err := security.Authorize(ctx, s.policy, actor, policies.{{.Entity}}View, models.{{.Entity}}{})
	if err != nil {
		return nil, err
	}
	found, err := models.{{.Plural}}(s.db).NewQuery().WhereKey(id).First(ctx, g)
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, models.Err{{.Entity}}NotFound
	}
	if _, err := security.Authorize(ctx, s.policy, actor, policies.{{.Entity}}View, *found); err != nil {
		return nil, err
	}
	return found, nil
}

// List returns a page of {{.Table}}.
func (s *{{.ServiceType}}) List(ctx context.Context, actor security.Subject, q data.Query) ([]*models.{{.Entity}}, error) {
	g, err := security.Authorize(ctx, s.policy, actor, policies.{{.Entity}}List, models.{{.Entity}}{})
	if err != nil {
		return nil, err
	}

	column, ok := {{.Unexported}}Sortable[q.Sort]
	if !ok {
		return nil, fmt.Errorf("%w: %q", models.Err{{.Entity}}Sort, q.Sort)
	}

	limit := q.Limit
	switch {
	case limit <= 0:
		limit = {{.Unexported}}DefaultLimit
	case limit > {{.Unexported}}MaxLimit:
		limit = {{.Unexported}}MaxLimit
	}

	rows := models.{{.Plural}}(s.db)
	page := rows.NewQuery()
	if q.Cursor != "" {
		anchor, err := rows.NewQuery().WhereKey(q.Cursor).Value(ctx, g, column)
		if err != nil {
			return nil, err
		}
		if anchor == nil {
			return nil, nil
		}
		page = page.Where(func(after *model.Builder[models.{{.Entity}}]) {
			after.Where(column, ">", anchor).
				OrWhere(func(equal *model.Builder[models.{{.Entity}}]) {
					equal.Where(column, "=", anchor).Where("id", ">", q.Cursor)
				})
		})
	}

	return page.OrderBy(column).OrderBy("id").Limit(limit).Get(ctx, g)
}

// Update changes the mutable fields.
//
// It reads before writing, so the policy decides against the stored row rather
// than against what the client claims the row is. Skipping this is how a check
// passes on attacker-supplied data.
func (s *{{.ServiceType}}) Update(ctx context.Context, actor security.Subject, in requests.{{.UpdateRequest}}) (*models.{{.Entity}}, error) {
	if errs := in.Validate(); errs.Any() {
		return nil, errs
	}

	stored, err := s.Get(ctx, actor, in.ID)
	if err != nil {
		return nil, err
	}

	g, err := security.Authorize(ctx, s.policy, actor, policies.{{.Entity}}Update, *stored)
	if err != nil {
		return nil, err
	}

{{- range .Fields}}
{{- if .IsEmail}}
	stored.{{.GoName}} = s.normalize(in.{{.GoName}})
{{- else}}
	stored.{{.GoName}} = {{.Bind "in"}}
{{- end}}
{{- end}}
	if _, err := stored.Save(ctx, g); err != nil {
		if s.conflict(err) {
			return nil, models.Err{{.Entity}}Conflict
		}
		return nil, err
	}
	return stored, nil
}

// Delete removes a {{.Name}}.
func (s *{{.ServiceType}}) Delete(ctx context.Context, actor security.Subject, id string) error {
	stored, err := s.Get(ctx, actor, id)
	if err != nil {
		return err
	}

	g, err := security.Authorize(ctx, s.policy, actor, policies.{{.Entity}}Delete, *stored)
	if err != nil {
		return err
	}
	deleted, err := stored.Delete(ctx, g)
	if err != nil {
		return err
	}
	if !deleted {
		return models.Err{{.Entity}}NotFound
	}
	if col := observability.FromContext(ctx); col != nil {
		col.RecordEvent("{{.Name}}.deleted", stored)
	}
	return nil
}

// normalize lowercases and trims text whose uniqueness is case-insensitive.
func (s *{{.ServiceType}}) normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// conflict recognizes a duplicate key across engines by message, which is the
// price of keeping database drivers outside the service.
func (s *{{.ServiceType}}) conflict(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") ||
		strings.Contains(message, "duplicate key") ||
		strings.Contains(message, "duplicate entry")
}

// arandu:begin custom
// Business rules beyond CRUD go here, and survive regeneration.
// arandu:end custom
`

const requestTemplate = `package requests

{{if .NeedsTimeParse}}import (
	"time"

	"github.com/arandu-io/framework/validation"
){{else}}import "github.com/arandu-io/framework/validation"{{end}}

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
	{{.GoName}} {{.GoType}} {{.FormTag}}
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
{{- if .NeedsStrconv}}
	"strconv"

{{end}}
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/pagination"
	"github.com/arandu-io/hesape/view"

	requests "{{.RequestsImport}}"
	models "{{.ModelsImport}}"
	services "{{.ServicesImport}}"
	views "{{.ViewsImport}}"
)

// {{.Controller}} answers the seven routes of the {{.Resource}} resource.
//
// It is thin on purpose: read the request, call the service, render. There is no
// repository here and there cannot be one -- fhttp.Context carries no database
// handle, so a controller that reached the data layer would be a controller that
// skipped the service, and therefore skipped the policy.
//
// The routes sit behind middleware.RequireAuth, which puts who is asking on the
// request: ctx.User reads it. Without the guard there is nobody there, and the
// policy refuses the zero subject. An error an action returns is answered by the
// router -- validation.Errors back to the form with the messages and what was
// typed, a missing row as 404, a refusal as 403 -- so no action maps one itself.
type {{.Controller}} struct {
	Controller

	svc      *services.{{.ServiceType}}
	sessions *security.SessionStore
	csrf     *security.CSRF
}

// New{{.Controller}} returns the controller. bootstrap builds it and hands it to
// the routes.
//
// The session store and the CSRF issuer are here for one thing, the token every
// page carries: a screen is allowed to know about a token and a cookie, and a
// service is not.
func New{{.Controller}}(svc *services.{{.ServiceType}}, sessions *security.SessionStore, csrf *security.CSRF) *{{.Controller}} {
	return &{{.Controller}}{svc: svc, sessions: sessions, csrf: csrf}
}

// Compile-time proof of the seven actions fhttp.Router.Resource looks for. It
// registers the ones the controller implements and nothing else, so a route that
// exists is a route that answers -- and a renamed method fails the build here
// rather than answering 404 in production.
var (
	_ fhttp.Indexer   = (*{{.Controller}})(nil)
	_ fhttp.Creator   = (*{{.Controller}})(nil)
	_ fhttp.Storer    = (*{{.Controller}})(nil)
	_ fhttp.Shower    = (*{{.Controller}})(nil)
	_ fhttp.Editor    = (*{{.Controller}})(nil)
	_ fhttp.Updater   = (*{{.Controller}})(nil)
	_ fhttp.Destroyer = (*{{.Controller}})(nil)
)

// Index renders the listing, one page at a time.
func (c *{{.Controller}}) Index(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	found, err := c.svc.List(ctx.Ctx(), who, pagination.ResolveCurrentPage(ctx.Request.URL, ""))
	if err != nil {
		return err
	}
	page, err := c.page(ctx, "{{.HumansTitle}}")
	if err != nil {
		return err
	}

	rows := make([]views.{{.RowStruct}}, 0, found.Count())
	for _, {{.Receiver}} := range found.Items() {
		rows = append(rows, c.row(ctx, {{.Receiver}}))
	}
	return ctx.View("{{.ViewName "index"}}", views.{{.ViewData "index"}}{
		Page:       page,
		{{.Plural}}: rows,
		NewURL:     ctx.URL("{{.RouteName "create"}}"),
		NextURL:    found.SetPath(ctx.URL("{{.RouteName "index"}}")).NextPageURL(),
	})
}

// Show renders one record.
func (c *{{.Controller}}) Show(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	found, err := c.svc.Get(ctx.Ctx(), who, ctx.Param("id"))
	if err != nil {
		return err
	}
	page, err := c.page(ctx, "{{.HumanTitle}}")
	if err != nil {
		return err
	}

	return ctx.View("{{.ViewName "show"}}", views.{{.ViewData "show"}}{
		Page:      page,
		{{.Entity}}: c.row(ctx, found),
		IndexURL:  ctx.URL("{{.RouteName "index"}}"),
		EditURL:   ctx.URL("{{.RouteName "edit"}}", found.ID),
		DeleteURL: ctx.URL("{{.RouteName "destroy"}}", found.ID),
	})
}

// Create renders the empty form, or the rejected one: the page carries what
// was typed and the messages, from the flash the router left.
func (c *{{.Controller}}) Create(ctx *fhttp.Context) error {
	page, err := c.page(ctx, "New {{.Human}}")
	if err != nil {
		return err
	}

	return ctx.View("{{.ViewName "create"}}", views.{{.ViewData "create"}}{
		Page:     page,
		IndexURL: ctx.URL("{{.RouteName "index"}}"),
		StoreURL: ctx.URL("{{.RouteName "store"}}"),
	})
}

// Store takes the submitted form.
func (c *{{.Controller}}) Store(ctx *fhttp.Context) error {
	var in requests.{{.Request}}
	if err := ctx.Bind(&in); err != nil {
		return err
	}
	who, _ := ctx.User()
	created, err := c.svc.Create(ctx.Ctx(), who, in)
	if err != nil {
		return err
	}
	return ctx.RedirectRoute("{{.RouteName "show"}}", created.ID)
}

// Edit renders the form filled in with the stored record.
func (c *{{.Controller}}) Edit(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	found, err := c.svc.Get(ctx.Ctx(), who, ctx.Param("id"))
	if err != nil {
		return err
	}
	page, err := c.page(ctx, "Edit {{.Human}}")
	if err != nil {
		return err
	}

	return ctx.View("{{.ViewName "edit"}}", views.{{.ViewData "edit"}}{
		Page:      page,
		Form:      c.row(ctx, found),
		ShowURL:   ctx.URL("{{.RouteName "show"}}", found.ID),
		UpdateURL: ctx.URL("{{.RouteName "update"}}", found.ID),
	})
}

// Update writes the submitted form onto the stored record.
func (c *{{.Controller}}) Update(ctx *fhttp.Context) error {
	var in requests.{{.Request}}
	if err := ctx.Bind(&in); err != nil {
		return err
	}
	who, _ := ctx.User()
	updated, err := c.svc.Update(ctx.Ctx(), who, ctx.Param("id"), in)
	if err != nil {
		return err
	}
	return ctx.RedirectRoute("{{.RouteName "show"}}", updated.ID)
}

// Destroy removes the record.
func (c *{{.Controller}}) Destroy(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	if err := c.svc.Delete(ctx.Ctx(), who, ctx.Param("id")); err != nil {
		return err
	}
	return ctx.RedirectRoute("{{.RouteName "index"}}")
}
{{template "controllerPage" .}}
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
func (c *{{.Controller}}) row(ctx *fhttp.Context, {{.Receiver}} *models.{{.Entity}}) views.{{.RowStruct}} {
	return views.{{.RowStruct}}{
		ID:  {{.Receiver}}.ID,
		URL: ctx.URL("{{.RouteName "show"}}", {{.Receiver}}.ID),
{{- range .Fields}}
		{{.GoName}}: {{.RowValue $.Receiver}},
{{- end}}
		Created: {{.Receiver}}.CreatedAt.Format("2006-01-02 15:04"),
	}
}

// arandu:begin custom
// Actions beyond the seven go here, and survive regeneration. Register them in
// the custom block of routes/web.go.
// arandu:end custom
`

// controllerPageTemplate is the page chrome every controller hands its views,
// whichever command wrote the controller.
//
// `aru make:module` and `aru make:controller` both emit it, and they emit the
// same bytes because it is the same template -- two ways to write one thing is
// what this generator is not allowed to create inside itself. It is a method
// rather than a package function, so two controllers in the package do not
// collide.
//
// The data it renders against needs a Controller field or method naming the type,
// which both gen.Module and gen.Stub have.
const controllerPageTemplate = `{{define "controllerPage"}}
// page is the chrome a screen hands the layout: the title, what a rejected
// attempt left in the flash -- the messages and what was typed -- and the CSRF
// token for this session.
//
// Every page needs the token, including the ones that write nothing: the
// sign-out form and every hx- request read it off the page data. A page
// rendered without one answers 200 and then refuses the next write with 419,
// which reads like a broken session rather than a missing field.
func (c *{{.Controller}}) page(ctx *fhttp.Context, title string) (view.Page, error) {
	token, err := c.csrf.Issue(c.sessions.IDFromRequest(ctx.Request))
	return view.New(ctx, title).WithToken(token), err
}
{{end}}`

const testTemplate = `package unit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/database/model"

	models "{{.ModelsImport}}"
	policies "{{.PoliciesImport}}"
	services "{{.ServicesImport}}"
)

// {{.Unexported}}Persistent is the Model-first persistence boundary this module
// depends on. The proof below fails at compile time if the generated entity
// stops embedding the Hesape model or if CRUD grows a second persistence path.
type {{.Unexported}}Persistent interface {
	Save(context.Context, security.Grant) (bool, error)
	NewQuery() *model.Builder[models.{{.Entity}}]
}

var _ {{.Unexported}}Persistent = (*models.{{.Entity}})(nil)

// TestEvery{{.Entity}}ReadRequiresAuthorization needs no database: the service
// authorizes each read before it asks the Model for a query. A nil handle turns
// an accidental query-before-policy into an immediate test failure.
func TestEvery{{.Entity}}ReadRequiresAuthorization(t *testing.T) {
	svc := services.New{{.ServiceType}}(nil)
	ctx := context.Background()
	var anonymous security.Subject

	calls := map[string]func() error{
		"Get": func() error {
			_, err := svc.Get(ctx, anonymous, "id")
			return err
		},
		"List": func() error {
			_, err := svc.List(ctx, anonymous, data.Query{})
			return err
		},
		"Delete": func() error {
			return svc.Delete(ctx, anonymous, "id")
		},
	}

	for name, call := range calls {
		t.Run(name+" with no subject", func(t *testing.T) {
			if err := call(); !errors.Is(err, security.ErrForbidden) {
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
	admin := security.Subject{ID: "a1", Tenant: "t1", Roles: []string{"admin", "staff"}}

	err := (policies.{{.PolicyType}}{}).Can(context.Background(), admin,
		"{{.Name}}.action_that_does_not_exist", models.{{.Entity}}{})

	if err == nil {
		t.Fatal("an action with no rule was allowed: the policy falls through to allowed")
	}
}
// arandu:begin custom
// Tests for the rules you wrote go here, and survive regeneration.
// arandu:end custom
`

// skillTemplate is what an assistant reads when it meets this module.
//
// It is generated with the rest because the alternative is a file somebody
// writes afterwards, and a description of a module written by hand is a
// description that stops being true at the next field. Everything in it is
// rendered from the same specification the Go was rendered from, so the two
// cannot disagree.
//
// It is Markdown rather than Go, and the frontmatter is the part that matters:
// a tool reads the description to decide whether the skill is relevant, so it
// names the situation rather than the subject.
const skillTemplate = `---
name: {{ .Resource }}
description: Work with the {{ .Entity }} module of this Arandu application. Use when the request mentions {{ .Entity | lower }}s, when a {{ .Resource }} route is involved, or when reading or changing {{ .Entity | lower }} records. Covers what the module exposes, which roles may take which action, and the rule that the Service authorizes before it reaches the Hesape Model.
license: MIT
---

# The {{ .Entity }} module

Generated from a specification. If it needs to change, change the specification
and generate again rather than editing the files by hand -- what falls outside
what the specification can say goes between the ` + "`" + `// arandu:begin custom` + "`" + ` and
` + "`" + `// arandu:end custom` + "`" + ` markers, which survive regeneration.

## What it is made of

| file | what it holds |
| --- | --- |
| ` + "`" + `app/Models/{{ .Entity }}.go` + "`" + ` | the entity and its Hesape Model entry point |
| ` + "`" + `app/Policies/{{ .Entity }}Policy.go` + "`" + ` | who may do what, and the only thing that issues a Grant |
| ` + "`" + `app/Services/{{ .Entity }}Service.go` + "`" + ` | the domain and the only consumer of the Model entry point |
| ` + "`" + `app/Http/Controllers/{{ .Entity }}Controller.go` + "`" + ` | the actions the routes dispatch to |
| ` + "`" + `app/Http/Requests/{{ .Entity }}Request.go` + "`" + ` | the input contract. Authorization stays in the Policy |
| ` + "`" + `tests/Unit/{{ .Entity }}_test.go` + "`" + ` | that reads authorize before the Model is queried |

## Its fields

| field | type |
| --- | --- |
{{ range .Fields }}| ` + "`" + `{{ .Name }}` + "`" + ` | ` + "`" + `{{ .Type }}` + "`" + ` |
{{ end }}
{{- if .Tenant }}
Every query uses the Model's ` + "`" + `tenant_id` + "`" + ` scope. Builder terminals take the
Grant, and its tenant never comes from a path segment, a body, a query or a header.
{{- else }}
This module is not tenant-scoped. That was declared in the specification, so a
query here is global on purpose rather than by omission.
{{- end }}

## Reaching a record

There is one way, and the compiler is what says so.

` + "```" + `go
g, err := security.Authorize(ctx, policy, subject, action, models.{{ .Entity }}{})
if err != nil {
    return err
}
record, err := models.{{ .Plural }}(db).NewQuery().WhereKey(id).First(ctx, g)
` + "```" + `

Every Builder terminal takes ` + "`" + `security.Grant` + "`" + `, and nothing outside the security
package can build one. The Service owns the database handle, authorizes first,
and then spends that Grant on the Model. A Controller has neither dependency and
cannot grow a second persistence path.

Reads are not exempt. ` + "`" + `List` + "`" + `, ` + "`" + `First` + "`" + `, a report and an export all require a Grant.

## What the policy allows

{{ if .Permissions }}{{ range $action, $roles := .Permissions }}- ` + "`" + `{{ $action }}` + "`" + `: {{ range $i, $r := $roles }}{{ if $i }}, {{ end }}` + "`" + `{{ $r }}` + "`" + `{{ end }}
{{ end }}{{ else }}Nothing yet. The generated policy denies every action, with no
allow-everything branch to delete later. Open it one action at a time, and
` + "`" + `aru doctor` + "`" + ` reports ` + "`" + `policy-never-opened` + "`" + ` as a warning until you do.
{{ end }}
## Before calling a change finished

` + "```" + `sh
export GOWORK=off
aru view:build && go build ./... && go vet ./... && go test -race ./... && aru doctor
` + "```" + `
`
