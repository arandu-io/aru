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
{{- range .Fields}}
	{{.GoName}} {{.GoType}} ` + "`" + `db:"{{.Column}}"` + "`" + `
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
//		if !{{.Receiver}}.ApprovedAt.IsZero() {
//			return errors.New("this {{.Human}} is already approved")
//		}
//		{{.Receiver}}.ApprovedAt = at
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
`

const serviceTemplate = `package services

import (
	"context"
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

// {{.Unexported}}PerPage is how many {{.Table}} one page of the listing holds. A
// listing is always a page, never everything: an unbounded query is how one
// page load takes a database down.
const {{.Unexported}}PerPage = 25

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

// List returns one page of {{.Table}}, newest first, and where the
// previous and the next page are.
func (s *{{.ServiceType}}) List(ctx context.Context, actor auth.Subject, page int) (models.{{.Entity}}Collection, *pagination.Page, error) {
	g, err := auth.Authorize(ctx, s.policy, actor, policies.{{.Entity}}List, models.{{.Entity}}{})
	if err != nil {
		return nil, nil, err
	}
	return models.{{.Constructor}}(s.db).Latest().OrderBy("id").
		SimplePaginate(ctx, g, {{.Unexported}}PerPage, page, pagination.Options{})
}

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
}

// New{{.ServiceType}} wires the service.
func New{{.ServiceType}}(db *database.DB) *{{.ServiceType}} {
	return &{{.ServiceType}}{db: db}
}

{{end}}{{define "serviceCreate"}}// Create walks the mandatory path: validate, Authorize, Grant, Model.
// There is no other order that compiles.
func (s *{{.ServiceType}}) Create(ctx context.Context, actor auth.Subject, in requests.{{.Request}}) (*models.{{.Entity}}, error) {
	if errs := in.Validate(); errs.Any() {
		return nil, errs
	}

	var proposed models.{{.Entity}}
	s.fill(&proposed, in)
	g, err := auth.Authorize(ctx, s.policy, actor, policies.{{.Entity}}Create, proposed)
	if err != nil {
		return nil, err
	}

	record, err := models.{{.Constructor}}(s.db).New()
	if err != nil {
		return nil, err
	}
	s.fill(record, in)
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
	hhttp "github.com/arandu-io/hesape/http"
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

// New{{.Controller}} returns the controller. bootstrap builds it and hands it to
// the routes.
func New{{.Controller}}(svc *services.{{.ServiceType}}) *{{.Controller}} {
	return &{{.Controller}}{svc: svc}
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
func (c *{{.Controller}}) Index(ctx *hhttp.Context) error {
	who, _ := ctx.User()
	found, page, err := c.svc.List(ctx.Ctx(), who, pagination.ResolveCurrentPage(ctx.Request.URL, ""))
	if err != nil {
		return err
	}

	rows := make([]views.{{.RowStruct}}, 0, len(found))
	for _, {{.Receiver}} := range found {
		rows = append(rows, c.row(ctx, {{.Receiver}}))
	}
	return ctx.View("{{.ViewName "index"}}", views.{{.ViewData "index"}}{
		Page: view.New(ctx, "{{.HumansTitle}}"),
		{{.Plural}}: rows,
		NewURL:     ctx.URL("{{.RouteName "create"}}"),
		NextURL:    page.SetPath(ctx.URL("{{.RouteName "index"}}")).NextPageURL(),
	})
}

// Show renders one record.
func (c *{{.Controller}}) Show(ctx *hhttp.Context) error {
	who, _ := ctx.User()
	found, err := c.svc.Get(ctx.Ctx(), who, ctx.Param("id"))
	if err != nil {
		return err
	}

	return ctx.View("{{.ViewName "show"}}", views.{{.ViewData "show"}}{
		Page: view.New(ctx, "{{.HumanTitle}}"),
		{{.Entity}}: c.row(ctx, found),
		IndexURL:  ctx.URL("{{.RouteName "index"}}"),
		EditURL:   ctx.URL("{{.RouteName "edit"}}", found.ID),
		DeleteURL: ctx.URL("{{.RouteName "destroy"}}", found.ID),
	})
}

// Create renders the empty form, or the rejected one: the page carries what
// was typed and the messages, from the flash the router left.
func (c *{{.Controller}}) Create(ctx *hhttp.Context) error {
	return ctx.View("{{.ViewName "create"}}", views.{{.ViewData "create"}}{
		Page: view.New(ctx, "New {{.Human}}"),
		IndexURL: ctx.URL("{{.RouteName "index"}}"),
		StoreURL: ctx.URL("{{.RouteName "store"}}"),
	})
}

// Store takes the submitted form.
func (c *{{.Controller}}) Store(ctx *hhttp.Context) error {
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
func (c *{{.Controller}}) Edit(ctx *hhttp.Context) error {
	who, _ := ctx.User()
	found, err := c.svc.Get(ctx.Ctx(), who, ctx.Param("id"))
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
	updated, err := c.svc.Update(ctx.Ctx(), who, ctx.Param("id"), in)
	if err != nil {
		return err
	}
	return ctx.RedirectRoute("{{.RouteName "show"}}", updated.ID)
}

// Destroy removes the record.
func (c *{{.Controller}}) Destroy(ctx *hhttp.Context) error {
	who, _ := ctx.User()
	if err := c.svc.Delete(ctx.Ctx(), who, ctx.Param("id")); err != nil {
		return err
	}
	return ctx.RedirectRoute("{{.RouteName "index"}}")
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

const testTemplate = `package unit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/database/model"

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
	svc := services.New{{.ServiceType}}(nil)
	ctx := context.Background()
	var anonymous auth.Subject

	calls := map[string]func() error{
		"Get": func() error {
			_, err := svc.Get(ctx, anonymous, "id")
			return err
		},
		"List": func() error {
			_, _, err := svc.List(ctx, anonymous, 1)
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
// names the situation rather than the subject. Generate records the generator
// in it as the skill's source.
//
// The custom block at the end is where a project writes what the generator
// cannot know, and regenerating with --force keeps it: everything else in the
// file is the generator's to rewrite.
const skillTemplate = `---
name: {{ .Resource }}
description: Work with the {{ .Entity }} module of this Arandu application. Use when the request mentions {{ .Entity | lower }}s, when a {{ .Resource }} route is involved, or when reading or changing {{ .Entity | lower }} records. Covers what the module exposes, which roles may take which action, and the rule that the Service authorizes before it reaches the Hesape Model.
license: MIT
---

# The {{ .Entity }} module

It is what ` + "`" + `aru make:module` + "`" + ` wrote, or ` + "`" + `aru generate` + "`" + ` from
` + "`" + `database/specs/{{ .Name }}.yaml` + "`" + `. Running either again with ` + "`" + `--force` + "`" + ` rewrites
every file of the module. What sits between the ` + "`" + `// arandu:begin custom` + "`" + ` and
` + "`" + `// arandu:end custom` + "`" + ` markers of the Go, and in the custom block at the end of
this file, is kept; every edit outside them is dropped. A change that fits no
custom block is made in the files directly, and regenerating would drop it.

## What it is made of

| file | what it holds |
| --- | --- |
| ` + "`" + `app/Models/{{ .Entity }}.go` + "`" + ` | the entity and its table, with custom blocks for settings and local scopes |
| ` + "`" + `app/Models/{{ .Entity }}Query.go` + "`" + ` | ` + "`" + `{{ .Constructor }}` + "`" + `, the typed query and the collection, written by ` + "`" + `aru model:build` + "`" + ` and never by hand |
| ` + "`" + `app/Policies/{{ .Entity }}Policy.go` + "`" + ` | who may do what: the rule ` + "`" + `auth.Authorize` + "`" + ` asks before it issues a Grant |
| ` + "`" + `app/Services/{{ .Entity }}Service.go` + "`" + ` | the domain, and the only caller of the Model entry point on a request's path |
| ` + "`" + `app/Http/Controllers/{{ .Entity }}Controller.go` + "`" + ` | the actions the routes dispatch to |
| ` + "`" + `app/Http/Requests/{{ .Entity }}Request.go` + "`" + ` | the input contract of create and update, with its form tags. Authorization stays in the Policy |
| ` + "`" + `resources/views` + "`" + `, under the resource | the four screens, which share one row struct |
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
g, err := auth.Authorize(ctx, policy, subject, action, models.{{ .Entity }}{})
if err != nil {
    return err
}
record, err := models.{{ .Constructor }}(db).FindOrFail(ctx, g, id)
` + "```" + `

Every terminal of ` + "`" + `{{ .Entity }}Query` + "`" + ` takes ` + "`" + `auth.Grant` + "`" + `, and nothing outside the
auth package can build one. The Service owns the database handle, authorizes first,
and then spends that Grant on the Model. A Controller has neither dependency and
cannot grow a second persistence path.

Reads are not exempt. ` + "`" + `List` + "`" + `, ` + "`" + `FindOrFail` + "`" + `, a report and an export all require a Grant.

## A request, end to end

` + "```" + `go
var in requests.{{ .Request }}
if err := ctx.Bind(&in); err != nil {
    return err
}
who, _ := ctx.User()
created, err := c.svc.Create(ctx.Ctx(), who, in)
if err != nil {
    return err
}
return ctx.RedirectRoute("{{ .RouteName "show" }}", created.ID)
` + "```" + `

- **Who is asking** is ` + "`" + `ctx.User()` + "`" + `, which the sign-in guard on the routes puts
  on the request. There is no session lookup in the controller.
- **The input** is ` + "`" + `ctx.Bind` + "`" + ` into ` + "`" + `{{ .Request }}` + "`" + `: only the fields with a
  ` + "`" + `form` + "`" + ` tag are read, trimmed and converted. A new field is one line there,
  one in the model and one in the service's ` + "`" + `fill` + "`" + `, a new migration that adds
  the column, and its input on the create and edit forms.
- **An error is returned, never mapped.** The router answers it:
  ` + "`" + `validation.Errors` + "`" + ` goes back to the form with the messages and what was typed,
  a missing row is 404, a refusal is 403, and an error with an ` + "`" + `HTTPStatus() int` + "`" + `
  method is that status.{{ if .UniqueFields }} A duplicate on a unique column is 409, and
  nothing in the module maps it.{{ end }}
- **A screen's page** is ` + "`" + `view.New(ctx, title)` + "`" + `: the title, what a rejected form
  left in the flash, and the CSRF token the middleware issued. The controller
  takes the service and nothing else.
- **The listing** is the Model's ` + "`" + `SimplePaginate` + "`" + `, newest first, and the key
  is one the Model generates on insert.

## What the policy allows

{{ if .Permissions }}{{ range $action, $roles := .Permissions }}- ` + "`" + `{{ $action }}` + "`" + `: {{ range $i, $r := $roles }}{{ if $i }}, {{ end }}` + "`" + `{{ $r }}` + "`" + `{{ end }}
{{ end }}{{ else }}Nothing yet. The generated policy denies every action, with no
allow-everything branch to delete later. Open it one action at a time, and
` + "`" + `aru doctor` + "`" + ` reports ` + "`" + `policy-never-opened` + "`" + ` as a warning until you do.
{{ end }}
## Before calling a change finished

The gates, all of them, as ` + "`" + `AGENTS.md` + "`" + ` lists them. While iterating,
` + "`" + `go test ./...` + "`" + ` is enough; the ` + "`" + `-race` + "`" + ` run is the closing gate, run once,
because the race detector compiles every package a second time.

` + "```" + `sh
export GOWORK=off
aru model:build
aru view:build
gofmt -l $(find . -name '*.go' -not -path '*/testdata/*' -not -name '*.kyse.go')
go build ./...
go vet ./...
go test -race ./...
aru doctor
` + "```" + `

<!-- arandu:begin custom -->
What this module does that the generator cannot know -- a business rule, why a
field exists, who to ask -- goes here, and regenerating keeps it.
<!-- arandu:end custom -->
`
