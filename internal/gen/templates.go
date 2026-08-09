package gen

// The templates below emit into the conventional tree: app/Models, app/Policies,
// app/Repositories, app/Services, app/Http/Requests, app/Http/Controllers,
// database/migrations and resources/views. There is no modules/<name>/ any
// more -- ADR 0019 -- and the reason is recognition: the developer this
// framework is for opens a project and looks for app/Http/Controllers.
//
// Every one of them emits the mandatory path: Validate, Authorize, Grant,
// Repository. There is no template that reaches a repository without a Grant,
// because there is no way to write one that compiles.
//
// One consequence of the flat tree runs through all of them: a package now
// holds every module's files, so no unexported package-level name can be
// generic. What used to be `columns`, `sortable` and `NewID` in a package of
// its own is now `invoiceColumns`, `invoiceSortable` and a method on the
// repository -- or, where the framework already has it, `data.NewID`.

const modelTemplate = `package models

import (
	"errors"
	"log/slog"
	"time"
)

// {{.Entity}} is the entity. It has no persistence methods: this is not Active
// Record, and a type that can save itself can save itself from anywhere.
//
// Coming from an ORM, this is the difference that costs the most to find late:
// there is no {{.Entity}}::find, no ->save() and no query builder on this type.
// The table is reached by {{.Entity}}Repository, and every method of a repository
// -- Find and List included -- takes a security.Grant that only a Policy can
// issue (RULE 17). The model is data; the Policy is the door.
type {{.Entity}} struct {
	ID        string
{{- if .Tenant}}
	TenantID  string
{{- end}}
{{- range .Fields}}
	{{.GoName}} {{.GoType}}
{{- end}}
	CreatedAt time.Time
}

// What can go wrong with a {{.Name}}, declared beside the entity rather than
// inside the repository.
//
// The controller maps them to a status code and the repository returns them, so
// both need to name them -- and a controller that imported the repository to
// reach an error would be a controller with a door to the data layer, which is
// the one thing the tree exists to prevent.
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

const repositoryTemplate = `package repositories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"

	models "{{.ModelsImport}}"
	policies "{{.PoliciesImport}}"
)

// Pagination bounds for {{.Table}}. A request that asks for everything gets the
// maximum, never everything: an unbounded query is how one page load takes a
// database down.
const (
	{{.Unexported}}DefaultLimit = 50
	{{.Unexported}}MaxLimit     = 200
)

// {{.RepositoryType}} is the only door to the {{.Table}} table.
//
// Every method starts with g.Check. The Grant is required by the signature, so
// forgetting it is a compile error, and the check proves the grant was issued for
// this exact action -- which catches copy-paste between methods.
//
// The SQL uses "?" placeholders and types every supported database shares, so
// these statements run unchanged on SQLite and PostgreSQL.
type {{.RepositoryType}} struct {
	db *data.DB
}

// New{{.RepositoryType}} returns a repository over an instrumented handle.
func New{{.RepositoryType}}(db *data.DB) *{{.RepositoryType}} { return &{{.RepositoryType}}{db: db} }

// Compile-time proof of the contract.
var _ data.Repository[models.{{.Entity}}, string] = (*{{.RepositoryType}})(nil)

const {{.Unexported}}Columns = ` + "`" + `id, {{if .Tenant}}tenant_id, {{end}}{{range .Fields}}{{.Column}}, {{end}}created_at` + "`" + `

// Find returns one {{.Name}} by id{{if .Tenant}}, scoped to the grant's tenant{{end}}.
func (r *{{.RepositoryType}}) Find(ctx context.Context, g security.Grant, id string) (models.{{.Entity}}, error) {
	if err := g.Check(policies.{{.Entity}}View); err != nil {
		return models.{{.Entity}}{}, err
	}
{{- if .Tenant}}
	// The tenant comes from the Grant, never from the request.
	row := r.db.QueryRowContext(ctx,
		` + "`" + `SELECT ` + "`" + `+{{.Unexported}}Columns+` + "`" + ` FROM {{.Table}} WHERE id = ? AND tenant_id = ?` + "`" + `,
		id, data.Tenant(g))
{{- else}}
	row := r.db.QueryRowContext(ctx,
		` + "`" + `SELECT ` + "`" + `+{{.Unexported}}Columns+` + "`" + ` FROM {{.Table}} WHERE id = ?` + "`" + `, id)
{{- end}}
	return r.scan(row)
}

// {{.Unexported}}Sortable is the ordering allowlist. A sort field is a column name, and a
// column name taken from the request is injection through a door nobody watches.
var {{.Unexported}}Sortable = map[string]string{
	"": "created_at",
	"created_at": "created_at",
{{- range .Sortable}}
	"{{.Column}}": "{{.Column}}",
{{- end}}
}

// List returns a page of {{.Table}}{{if .Tenant}} in the grant's tenant{{end}}.
//
// Pagination is keyset based: OFFSET grows more expensive with every page and
// skips rows when data changes underneath it.
func (r *{{.RepositoryType}}) List(ctx context.Context, g security.Grant, q data.Query) ([]models.{{.Entity}}, error) {
	// {{.Entity}}List, not {{.Entity}}View. The specification can grant them to
	// different roles -- "a support agent may open the record it was given, but
	// may not page through every record there is" -- and checking view here made
	// the list permission decorative: whoever could read one could read all of them.
	if err := g.Check(policies.{{.Entity}}List); err != nil {
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

{{if .Tenant}}	query := ` + "`" + `SELECT ` + "`" + ` + {{.Unexported}}Columns + ` + "`" + ` FROM {{.Table}} WHERE tenant_id = ?` + "`" + `
	args := []any{data.Tenant(g)}
	if q.Cursor != "" {
		// The predicate names the column the ORDER BY names. It used to be
		// hardcoded to created_at while the ordering followed q.Sort, so any
		// sort other than the default computed the page boundary on one
		// ordering and returned the rows in another -- pages that skipped rows
		// and pages that repeated them, silently. The column comes from the
		// allowlist above, never from the request.
		query += ` + "`" + ` AND (` + "`" + ` + column + ` + "`" + ` > (SELECT ` + "`" + ` + column + ` + "`" + ` FROM {{.Table}} WHERE id = ? AND tenant_id = ?)
		            OR (` + "`" + ` + column + ` + "`" + ` = (SELECT ` + "`" + ` + column + ` + "`" + ` FROM {{.Table}} WHERE id = ? AND tenant_id = ?) AND id > ?))` + "`" + `
		args = append(args, q.Cursor, args[0], q.Cursor, args[0], q.Cursor)
	}
{{else}}	query := ` + "`" + `SELECT ` + "`" + ` + {{.Unexported}}Columns + ` + "`" + ` FROM {{.Table}} WHERE 1 = 1` + "`" + `
	var args []any
	if q.Cursor != "" {
		// See the tenant branch: the predicate follows q.Sort, not created_at.
		query += ` + "`" + ` AND (` + "`" + ` + column + ` + "`" + ` > (SELECT ` + "`" + ` + column + ` + "`" + ` FROM {{.Table}} WHERE id = ?)
		            OR (` + "`" + ` + column + ` + "`" + ` = (SELECT ` + "`" + ` + column + ` + "`" + ` FROM {{.Table}} WHERE id = ?) AND id > ?))` + "`" + `
		args = append(args, q.Cursor, q.Cursor, q.Cursor)
	}
{{end}}	query += ` + "`" + ` ORDER BY ` + "`" + ` + column + ` + "`" + `, id LIMIT ?` + "`" + `
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.{{.Entity}}
	for rows.Next() {
		{{.Receiver}}, err := r.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, {{.Receiver}})
	}
	return out, rows.Err()
}

// Create inserts the {{.Name}} and returns it as stored.
func (r *{{.RepositoryType}}) Create(ctx context.Context, g security.Grant, {{.Receiver}} models.{{.Entity}}) (models.{{.Entity}}, error) {
	if err := g.Check(policies.{{.Entity}}Create); err != nil {
		return models.{{.Entity}}{}, err
	}

	var err error
	if {{.Receiver}}.ID == "" {
		// The id comes from the application, not from a database default:
		// gen_random_uuid, UUID() and randomblob are three spellings of one idea,
		// and depending on any of them would tie the schema to one engine.
		if {{.Receiver}}.ID, err = data.NewID(); err != nil {
			return models.{{.Entity}}{}, err
		}
	}
{{- if .Tenant}}
	{{.Receiver}}.TenantID = data.Tenant(g)
{{- end}}
{{- range .Fields}}{{if .IsEmail}}
	{{$.Receiver}}.{{.GoName}} = r.normalize({{$.Receiver}}.{{.GoName}})
{{- end}}{{end}}
	{{.Receiver}}.CreatedAt = time.Now().UTC()

	_, err = r.db.ExecContext(ctx,
		` + "`" + `INSERT INTO {{.Table}} (` + "`" + `+{{.Unexported}}Columns+` + "`" + `) VALUES ({{if .Tenant}}?, {{end}}?{{range .Fields}}, ?{{end}}, ?)` + "`" + `,
		{{.Receiver}}.ID, {{if .Tenant}}{{.Receiver}}.TenantID, {{end}}{{range .Fields}}{{.Bind $.Receiver}}, {{end}}{{.Receiver}}.CreatedAt)
	if err != nil {
		if r.conflict(err) {
			return models.{{.Entity}}{}, models.Err{{.Entity}}Conflict
		}
		return models.{{.Entity}}{}, err
	}
	return {{.Receiver}}, nil
}

// Update writes the mutable fields.{{if .Tenant}} The tenant is not one of them:
// moving a row between tenants is not an update, it is a migration.{{end}}
func (r *{{.RepositoryType}}) Update(ctx context.Context, g security.Grant, {{.Receiver}} models.{{.Entity}}) (models.{{.Entity}}, error) {
	if err := g.Check(policies.{{.Entity}}Update); err != nil {
		return models.{{.Entity}}{}, err
	}
{{- range .Fields}}{{if .IsEmail}}
	{{$.Receiver}}.{{.GoName}} = r.normalize({{$.Receiver}}.{{.GoName}})
{{- end}}{{end}}

	res, err := r.db.ExecContext(ctx,
		` + "`" + `UPDATE {{.Table}} SET {{range $i, $f := .Fields}}{{if $i}}, {{end}}{{$f.Column}} = ?{{end}} WHERE id = ?{{if .Tenant}} AND tenant_id = ?{{end}}` + "`" + `,
		{{range .Fields}}{{.Bind $.Receiver}}, {{end}}{{.Receiver}}.ID{{if .Tenant}}, data.Tenant(g){{end}})
	if err != nil {
		if r.conflict(err) {
			return models.{{.Entity}}{}, models.Err{{.Entity}}Conflict
		}
		return models.{{.Entity}}{}, err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return models.{{.Entity}}{}, models.Err{{.Entity}}NotFound
	}
	return {{.Receiver}}, nil
}

// Delete removes one {{.Name}}{{if .Tenant}} within the grant's tenant{{end}}.
func (r *{{.RepositoryType}}) Delete(ctx context.Context, g security.Grant, id string) error {
	if err := g.Check(policies.{{.Entity}}Delete); err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx,
		` + "`" + `DELETE FROM {{.Table}} WHERE id = ?{{if .Tenant}} AND tenant_id = ?{{end}}` + "`" + `, id{{if .Tenant}}, data.Tenant(g){{end}})
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return models.Err{{.Entity}}NotFound
	}
	return nil
}

// Health reports whether the repository can reach its storage.
//
// Nothing calls it out of the box. It is here so AppServiceProvider can
// implement kernel.Health over the repositories it owns, which is what puts this
// table on /_arandu/health and on the error page's diagnosis.
func (r *{{.RepositoryType}}) Health(ctx context.Context) error { return r.db.PingContext(ctx) }

// scan reads one row.
//
// It takes the interface inline rather than a named one, and it is a method
// rather than a function, for the same reason as the helpers below: every
// repository of the application shares this package, and a second module
// declaring rowScanner would not compile.
func (r *{{.RepositoryType}}) scan(row interface{ Scan(dest ...any) error }) (models.{{.Entity}}, error) {
	var {{.Receiver}} models.{{.Entity}}
	err := row.Scan(&{{.Receiver}}.ID, {{if .Tenant}}&{{.Receiver}}.TenantID, {{end}}{{range .Fields}}&{{$.Receiver}}.{{.GoName}}, {{end}}&{{.Receiver}}.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return models.{{.Entity}}{}, models.Err{{.Entity}}NotFound
	}
	if err != nil {
		return models.{{.Entity}}{}, err
	}
	return {{.Receiver}}, nil
}

// normalize lowercases and trims, which is what keeps a plain UNIQUE index
// case-insensitive on every engine.
func (r *{{.RepositoryType}}) normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// conflict recognizes a duplicate key across engines by message, which is the
// price of not importing a driver into a repository.
func (r *{{.RepositoryType}}) conflict(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "duplicate entry")
}

// arandu:begin custom
// Queries beyond the five above go here, and survive regeneration. Keep the
// g.Check as the first line of each one.
// arandu:end custom
`

const serviceTemplate = `package services

import (
	"context"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/framework/security"

	models "{{.ModelsImport}}"
	policies "{{.PoliciesImport}}"
	repositories "{{.RepositoriesImport}}"
	requests "{{.RequestsImport}}"
)

// {{.ServiceType}} holds the business rules. It receives its dependencies through
// the constructor -- explicit wiring, no container.
type {{.ServiceType}} struct {
	repo   *repositories.{{.RepositoryType}}
	policy policies.{{.PolicyType}}
}

// New{{.ServiceType}} wires the service.
func New{{.ServiceType}}(repo *repositories.{{.RepositoryType}}) *{{.ServiceType}} {
	return &{{.ServiceType}}{repo: repo}
}

// Create walks the mandatory path: validate, Authorize, Grant, Repository.
// There is no other order that compiles.
func (s *{{.ServiceType}}) Create(ctx context.Context, actor security.Subject, in requests.{{.StoreRequest}}) (models.{{.Entity}}, error) {
	if errs := in.Validate(); errs.Any() {
		return models.{{.Entity}}{}, errs
	}

	candidate := models.{{.Entity}}{
{{- if .Tenant}}
		TenantID: actor.Tenant,
{{- end}}
{{- range .Fields}}
		{{.GoName}}: in.{{.GoName}},
{{- end}}
	}

	g, err := security.Authorize(ctx, s.policy, actor, policies.{{.Entity}}Create, candidate)
	if err != nil {
		return models.{{.Entity}}{}, err
	}

	created, err := s.repo.Create(ctx, g, candidate)
	if err != nil {
		return models.{{.Entity}}{}, err
	}
	// Guarded: the entity is a struct value, and boxing it into ` + "`" + `any` + "`" + ` allocates
	// at the call site even though RecordEvent is a no-op on a nil Collector.
	if col := observability.FromContext(ctx); col != nil {
		col.RecordEvent("{{.Name}}.created", created)
	}
	return created, nil
}

// Get returns one {{.Name}}.
func (s *{{.ServiceType}}) Get(ctx context.Context, actor security.Subject, id string) (models.{{.Entity}}, error) {
	g, err := security.Authorize(ctx, s.policy, actor, policies.{{.Entity}}View, models.{{.Entity}}{})
	if err != nil {
		return models.{{.Entity}}{}, err
	}
	return s.repo.Find(ctx, g, id)
}

// List returns a page of {{.Table}}.
func (s *{{.ServiceType}}) List(ctx context.Context, actor security.Subject, q data.Query) ([]models.{{.Entity}}, error) {
	g, err := security.Authorize(ctx, s.policy, actor, policies.{{.Entity}}List, models.{{.Entity}}{})
	if err != nil {
		return nil, err
	}
	return s.repo.List(ctx, g, q)
}

// Update changes the mutable fields.
//
// It reads before writing, so the policy decides against the stored row rather
// than against what the client claims the row is. Skipping this is how a check
// passes on attacker-supplied data.
func (s *{{.ServiceType}}) Update(ctx context.Context, actor security.Subject, in requests.{{.UpdateRequest}}) (models.{{.Entity}}, error) {
	if errs := in.Validate(); errs.Any() {
		return models.{{.Entity}}{}, errs
	}

	view, err := security.Authorize(ctx, s.policy, actor, policies.{{.Entity}}View, models.{{.Entity}}{})
	if err != nil {
		return models.{{.Entity}}{}, err
	}
	stored, err := s.repo.Find(ctx, view, in.ID)
	if err != nil {
		return models.{{.Entity}}{}, err
	}

	g, err := security.Authorize(ctx, s.policy, actor, policies.{{.Entity}}Update, stored)
	if err != nil {
		return models.{{.Entity}}{}, err
	}

{{- range .Fields}}
	stored.{{.GoName}} = in.{{.GoName}}
{{- end}}
	return s.repo.Update(ctx, g, stored)
}

// Delete removes a {{.Name}}.
func (s *{{.ServiceType}}) Delete(ctx context.Context, actor security.Subject, id string) error {
	view, err := security.Authorize(ctx, s.policy, actor, policies.{{.Entity}}View, models.{{.Entity}}{})
	if err != nil {
		return err
	}
	stored, err := s.repo.Find(ctx, view, id)
	if err != nil {
		return err
	}

	g, err := security.Authorize(ctx, s.policy, actor, policies.{{.Entity}}Delete, stored)
	if err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, g, id); err != nil {
		return err
	}
	if col := observability.FromContext(ctx); col != nil {
		col.RecordEvent("{{.Name}}.deleted", stored)
	}
	return nil
}

// arandu:begin custom
// Business rules beyond CRUD go here, and survive regeneration.
// arandu:end custom
`

const requestTemplate = `package requests

import (
{{- if .NeedsTimeParse}}
	"time"

{{end}}
	"github.com/arandu-io/framework/validation"
)

// {{.StoreRequest}} is the input contract of creation. Fields are explicit: there
// is no mass assignment, so a field the client sends and this struct does not
// declare goes nowhere.
type {{.StoreRequest}} struct {
{{- range .Fields}}
	{{.GoName}} {{.GoType}}
{{- end}}
}

// Validate reports the errors per field.
func (r {{.StoreRequest}}) Validate() validation.Errors {
	e := validation.Errors{}
{{- template "storeRules" .}}

	// arandu:begin custom
	// Domain rules go here: ranges, formats, cross-field checks.
	// arandu:end custom

	return e
}

// {{.UpdateRequest}} carries the id as well, and the same rules.
type {{.UpdateRequest}} struct {
	ID string
{{- range .Fields}}
	{{.GoName}} {{.GoType}}
{{- end}}
}

// Validate reports the errors per field.
func (r {{.UpdateRequest}}) Validate() validation.Errors {
	e := {{.StoreRequest}}{
{{- range .Fields}}
		{{.GoName}}: r.{{.GoName}},
{{- end}}
	}.Validate()
	validation.Required(e, "id", r.ID)
	return e
}

// Compile-time proof that both requests honor the validation contract.
var (
	_ validation.Validatable = {{.StoreRequest}}{}
	_ validation.Validatable = {{.UpdateRequest}}{}
)
{{- if .NeedsTimeParse}}

var _ = time.Time{}
{{- end}}
`

// requestRulesTemplate is the per-field validation, whichever command wrote the
// request.
//
// `aru make:module` emits a Store/Update pair and `aru make:request` emits one
// type, and the rules inside them are the same bytes because it is the same
// template. Copying it would have been shorter to write and would have diverged
// on the first correction nobody remembered to make twice (RULE 9).
//
// It renders against anything with a Fields slice, which both gen.Module and
// gen.Stub have.
const requestRulesTemplate = `{{define "storeRules"}}
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
	"errors"
	"net/http"
	"strconv"
{{- if .NeedsTimeParse}}
	"time"
{{- end}}

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/httpx"
	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
	"github.com/arandu-io/framework/view"

	requests "{{.RequestsImport}}"
	models "{{.ModelsImport}}"
	services "{{.ServicesImport}}"
	views "{{.ViewsImport}}"
)

// {{.Controller}} answers the seven routes of the {{.Resource}} resource.
//
// It is thin on purpose: read the request, call the service, render. There is no
// repository here and there cannot be one -- httpx.Context carries no database
// handle, so a controller that reached the data layer would be a controller that
// skipped the service, and therefore skipped the policy.
type {{.Controller}} struct {
	Controller

	svc      *services.{{.ServiceType}}
	sessions *security.SessionStore
	csrf     *security.CSRF
}

// New{{.Controller}} returns the controller. bootstrap builds it and hands it to
// the routes.
//
// The session store and the CSRF issuer arrive through the constructor rather
// than through the service: a screen is allowed to know about a token and a
// cookie, and a service is not allowed to expose its own dependencies.
func New{{.Controller}}(svc *services.{{.ServiceType}}, sessions *security.SessionStore, csrf *security.CSRF) *{{.Controller}} {
	return &{{.Controller}}{svc: svc, sessions: sessions, csrf: csrf}
}

// Compile-time proof of the seven actions httpx.Router.Resource looks for. It
// registers the ones the controller implements and nothing else, so a route that
// exists is a route that answers -- and a renamed method fails the build here
// rather than answering 404 in production.
var (
	_ httpx.Indexer   = (*{{.Controller}})(nil)
	_ httpx.Creator   = (*{{.Controller}})(nil)
	_ httpx.Storer    = (*{{.Controller}})(nil)
	_ httpx.Shower    = (*{{.Controller}})(nil)
	_ httpx.Editor    = (*{{.Controller}})(nil)
	_ httpx.Updater   = (*{{.Controller}})(nil)
	_ httpx.Destroyer = (*{{.Controller}})(nil)
)

// {{.Unexported}}PerPage is how many records the listing asks for when the request
// does not say. The repository has a bound of its own: this one is about the
// screen, that one is about the database.
const {{.Unexported}}PerPage = 25

// Index renders the listing.
func (c *{{.Controller}}) Index(ctx *httpx.Context) error {
	actor, err := c.actor(ctx)
	if err != nil {
		return c.signIn(ctx)
	}

	// The page size is decided here rather than passed through blindly: asking
	// for a known number is what lets the next cursor be offered only when a
	// full page came back.
	limit := {{.Unexported}}PerPage
	if n, err := strconv.Atoi(ctx.Query("limit")); err == nil && n > 0 {
		limit = n
	}

	found, err := c.svc.List(ctx.Ctx(), actor, data.Query{
		Limit:  limit,
		Cursor: ctx.Query("cursor"),
		Sort:   ctx.Query("sort"),
	})
	if err != nil {
		return c.fail(ctx, err)
	}

	rows := make([]views.{{.RowStruct}}, 0, len(found))
	for _, {{.Receiver}} := range found {
		rows = append(rows, c.row({{.Receiver}}))
	}

	// The listing writes nothing, but the layout around it does: the sign-out
	// form and every hx- request read the token off the page data. A listing
	// rendered without one answers 200 and then refuses the next write with
	// 419, which reads like a broken session.
	token, err := c.token(ctx)
	if err != nil {
		return err
	}

	// Keyset pagination picks up after the last id of the page. A partial page
	// is the last page, and offering a cursor there would be a link to nothing.
	next := ""
	if len(rows) == limit {
		next = rows[len(rows)-1].ID
	}

	return ctx.View("{{.ViewName "index"}}", views.{{.ViewData "index"}}{
		Page:       view.Page{Title: "{{.HumansTitle}}", Token: token},
		{{.Plural}}: rows,
		NextCursor: next,
	})
}

// Show renders one record.
func (c *{{.Controller}}) Show(ctx *httpx.Context) error {
	actor, err := c.actor(ctx)
	if err != nil {
		return c.signIn(ctx)
	}

	found, err := c.svc.Get(ctx.Ctx(), actor, ctx.Param("id"))
	if err != nil {
		return c.fail(ctx, err)
	}

	// The token is for the delete button, which sends it as a header: an
	// hx-delete carries no form body, so the hidden field a form uses would
	// never arrive and the request would be refused with 419.
	token, err := c.token(ctx)
	if err != nil {
		return err
	}

	return ctx.View("{{.ViewName "show"}}", views.{{.ViewData "show"}}{
		Page:     view.Page{Title: "{{.HumanTitle}}", Token: token},
		{{.Entity}}: c.row(found),
	})
}

// Create renders the empty form.
func (c *{{.Controller}}) Create(ctx *httpx.Context) error {
	if _, err := c.actor(ctx); err != nil {
		return c.signIn(ctx)
	}
	token, err := c.token(ctx)
	if err != nil {
		return err
	}

	return ctx.View("{{.ViewName "create"}}", views.{{.ViewData "create"}}{
		Page:   view.Page{Title: "New {{.Human}}", Token: token},
		Errors: map[string][]string{},
	})
}

// Store takes the submitted form.
func (c *{{.Controller}}) Store(ctx *httpx.Context) error {
	actor, err := c.actor(ctx)
	if err != nil {
		return c.signIn(ctx)
	}

	in, form, errs := c.input(ctx)
	if !c.Validated(errs) {
		return c.rejectedCreate(ctx, form, errs)
	}

	created, err := c.svc.Create(ctx.Ctx(), actor, in)
	if err != nil {
		var invalid validation.Errors
		if errors.As(err, &invalid) {
			return c.rejectedCreate(ctx, form, invalid)
		}
		return c.fail(ctx, err)
	}
	return ctx.Redirect("{{.Route}}/" + created.ID)
}

// Edit renders the form filled in.
func (c *{{.Controller}}) Edit(ctx *httpx.Context) error {
	actor, err := c.actor(ctx)
	if err != nil {
		return c.signIn(ctx)
	}

	found, err := c.svc.Get(ctx.Ctx(), actor, ctx.Param("id"))
	if err != nil {
		return c.fail(ctx, err)
	}
	token, err := c.token(ctx)
	if err != nil {
		return err
	}

	return ctx.View("{{.ViewName "edit"}}", views.{{.ViewData "edit"}}{
		Page:   view.Page{Title: "Edit {{.Human}}", Token: token},
		Form:   c.form(found),
		Errors: map[string][]string{},
	})
}

// Update writes the submitted form onto the stored record.
func (c *{{.Controller}}) Update(ctx *httpx.Context) error {
	actor, err := c.actor(ctx)
	if err != nil {
		return c.signIn(ctx)
	}

	in, form, errs := c.input(ctx)
	form.ID = ctx.Param("id")
	if !c.Validated(errs) {
		return c.rejectedEdit(ctx, form, errs)
	}

	updated, err := c.svc.Update(ctx.Ctx(), actor, requests.{{.UpdateRequest}}{
		ID: ctx.Param("id"),
{{- range .Fields}}
		{{.GoName}}: in.{{.GoName}},
{{- end}}
	})
	if err != nil {
		var invalid validation.Errors
		if errors.As(err, &invalid) {
			return c.rejectedEdit(ctx, form, invalid)
		}
		return c.fail(ctx, err)
	}
	return ctx.Redirect("{{.Route}}/" + updated.ID)
}

// Destroy removes the record.
func (c *{{.Controller}}) Destroy(ctx *httpx.Context) error {
	actor, err := c.actor(ctx)
	if err != nil {
		return c.signIn(ctx)
	}
	if err := c.svc.Delete(ctx.Ctx(), actor, ctx.Param("id")); err != nil {
		return c.fail(ctx, err)
	}
	return ctx.Redirect("{{.Route}}")
}

{{template "controllerSession" .}}
// row turns the entity into what the markup renders.
//
// Formatting happens here rather than in the view: a view that formats a
// time.Time would need the time package, and what a date looks like on screen is
// a decision about presentation, which is this side of the line.
func (c *{{.Controller}}) row({{.Receiver}} models.{{.Entity}}) views.{{.RowStruct}} {
	return views.{{.RowStruct}}{
		ID: {{.Receiver}}.ID,
{{- range .Fields}}
		{{.GoName}}: {{.Display $.Receiver}},
{{- end}}
		Created: {{.Receiver}}.CreatedAt.Format("2006-01-02 15:04"),
	}
}

// form fills the edit form from the stored record.
func (c *{{.Controller}}) form({{.Receiver}} models.{{.Entity}}) views.{{.FormStruct}} {
	return views.{{.FormStruct}}{
		ID: {{.Receiver}}.ID,
{{- range .Fields}}
		{{.GoName}}: {{.FormValue $.Receiver}},
{{- end}}
	}
}

// input reads the submitted form.
//
// It returns three things: the typed request the service takes, the form as it
// was typed -- so a rejected submission comes back filled in rather than blank --
// and the errors parsing itself found. A number that is not a number is rejected
// here, naming the field, rather than reaching the service as a silent zero.
func (c *{{.Controller}}) input(ctx *httpx.Context) (requests.{{.StoreRequest}}, views.{{.FormStruct}}, validation.Errors) {
	errs := validation.Errors{}

	in := requests.{{.StoreRequest}}{
{{- range .Fields}}
		{{.GoName}}: {{.Parse "c"}},
{{- end}}
	}

	form := views.{{.FormStruct}}{
{{- range .Fields}}
{{- if .IsBool}}
		{{.GoName}}: ctx.Input("{{.Column}}") != "",
{{- else}}
		{{.GoName}}: ctx.Input("{{.Column}}"),
{{- end}}
{{- end}}
	}

	// arandu:begin custom
	// Anything the form carries that the fields above do not: a value composed
	// of two inputs, a default that depends on the actor.
	// arandu:end custom

	return in, form, errs
}

// rejectedCreate re-renders the creation form with its errors, as the 422
// fragment HTMX swaps back in.
func (c *{{.Controller}}) rejectedCreate(ctx *httpx.Context, form views.{{.FormStruct}}, errs validation.Errors) error {
	token, err := c.token(ctx)
	if err != nil {
		return err
	}
	return c.Invalid(ctx, "{{.ViewName "create"}}", views.{{.ViewData "create"}}{
		Page:   view.Page{Title: "New {{.Human}}", Token: token},
		Form:   form,
		Errors: errs,
	})
}

// rejectedEdit re-renders the edit form with its errors.
func (c *{{.Controller}}) rejectedEdit(ctx *httpx.Context, form views.{{.FormStruct}}, errs validation.Errors) error {
	token, err := c.token(ctx)
	if err != nil {
		return err
	}
	return c.Invalid(ctx, "{{.ViewName "edit"}}", views.{{.ViewData "edit"}}{
		Page:   view.Page{Title: "Edit {{.Human}}", Token: token},
		Form:   form,
		Errors: errs,
	})
}

// fail turns a domain error into a status, in one place.
//
// Note what it does not do: it never writes the authorization error into the
// response. Why a policy said no is information about the system, and it belongs
// in the log. Anything unrecognized is returned, and the router turns it into
// the error page in development and a 500 in production.
func (c *{{.Controller}}) fail(ctx *httpx.Context, err error) error {
	switch {
	case errors.Is(err, security.ErrForbidden):
		observability.Log(ctx.Ctx()).Warn("authorization denied", "error", err)
		return ctx.Status(http.StatusForbidden)
	case errors.Is(err, models.Err{{.Entity}}NotFound):
		return ctx.Status(http.StatusNotFound)
	case errors.Is(err, models.Err{{.Entity}}Conflict):
		return ctx.Status(http.StatusConflict)
	case errors.Is(err, models.Err{{.Entity}}Sort):
		return ctx.Status(http.StatusBadRequest)
	default:
		return err
	}
}
{{if .NeedsWholeParse}}
// whole reads an integer field, and names the field when it is not one.
func (c *{{.Controller}}) whole(ctx *httpx.Context, field string, e validation.Errors) int64 {
	raw := ctx.Input(field)
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		e.Add(field, "must be a whole number")
		return 0
	}
	return n
}
{{end}}{{if .NeedsFractionParse}}
// fraction reads a decimal field.
func (c *{{.Controller}}) fraction(ctx *httpx.Context, field string, e validation.Errors) float64 {
	raw := ctx.Input(field)
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		e.Add(field, "must be a number")
		return 0
	}
	return n
}
{{end}}{{if .NeedsTimeParse}}
// moment reads a date or a timestamp, in the layout the matching HTML input
// submits.
func (c *{{.Controller}}) moment(ctx *httpx.Context, field, layout string, e validation.Errors) time.Time {
	raw := ctx.Input(field)
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(layout, raw)
	if err != nil {
		e.Add(field, "is not a valid date")
		return time.Time{}
	}
	return t
}
{{end}}
// arandu:begin custom
// Actions beyond the seven go here, and survive regeneration. Register them in
// the custom block of routes/web.go.
// arandu:end custom
`

// controllerSessionTemplate is the boundary every controller shares, whichever
// command wrote it.
//
// `aru make:module` and `aru make:controller` both emit these three methods, and
// they emit the same bytes because it is the same template -- two ways to write
// one thing is the rule this generator is not allowed to break inside itself
// (RULE 9). They are methods rather than package functions, so two controllers in
// the package do not collide.
//
// The data it renders against needs a Controller field or method naming the type,
// which both gen.Module and gen.Stub have.
const controllerSessionTemplate = `{{define "controllerSession"}}
// actor is who is acting, from the session and never from the request body.
func (c *{{.Controller}}) actor(ctx *httpx.Context) (security.Subject, error) {
	return c.sessions.Load(ctx.Ctx(), ctx.Request)
}

// signIn sends an unauthenticated visitor to the sign-in screen. Under HTMX the
// redirect becomes HX-Redirect, so the browser navigates instead of nesting the
// whole page inside a fragment.
func (c *{{.Controller}}) signIn(ctx *httpx.Context) error {
	return ctx.Redirect("/auth/login")
}

// token issues a CSRF token for the session that is rendering the page.
//
// Every page needs it, including the ones that write nothing: the sign-out form
// and every hx- request read it off the page data. A page rendered without one
// answers 200 and then refuses the next write with 419, which reads like a
// broken session rather than a missing field.
func (c *{{.Controller}}) token(ctx *httpx.Context) (string, error) {
	return c.csrf.Issue(c.sessions.IDFromRequest(ctx.Request))
}
{{end}}`

const testTemplate = `package unit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"

	models "{{.ModelsImport}}"
	policies "{{.PoliciesImport}}"
	repositories "{{.RepositoriesImport}}"
)

// These tests need no database: every repository method checks the Grant before
// touching the handle, which is exactly the property under test.
func {{.Unexported}}RepoWithoutDB() *repositories.{{.RepositoryType}} {
	return repositories.New{{.RepositoryType}}(nil)
}

// TestEvery{{.Entity}}MethodRequiresItsGrant is the framework thesis at runtime:
// the zero Grant -- the only one a caller outside the security package can build
// -- never gets through, and a grant for one action does not open another.
func TestEvery{{.Entity}}MethodRequiresItsGrant(t *testing.T) {
	repo := {{.Unexported}}RepoWithoutDB()
	ctx := context.Background()
	var zero security.Grant

	calls := map[string]func(security.Grant) error{
		"Find": func(g security.Grant) error {
			_, err := repo.Find(ctx, g, "id")
			return err
		},
		"List": func(g security.Grant) error {
			_, err := repo.List(ctx, g, data.Query{})
			return err
		},
		"Create": func(g security.Grant) error {
			_, err := repo.Create(ctx, g, models.{{.Entity}}{})
			return err
		},
		"Update": func(g security.Grant) error {
			_, err := repo.Update(ctx, g, models.{{.Entity}}{})
			return err
		},
		"Delete": func(g security.Grant) error {
			return repo.Delete(ctx, g, "id")
		},
	}

	for name, call := range calls {
		t.Run(name+" with no grant", func(t *testing.T) {
			if err := call(zero); !errors.Is(err, security.ErrForbidden) {
				t.Fatalf("error = %v, want ErrForbidden", err)
			}
		})
		t.Run(name+" with a grant for another action", func(t *testing.T) {
			if err := call(security.SystemGrant("some.other.action", "t1")); !errors.Is(err, security.ErrForbidden) {
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

// Test{{.Entity}}ListRejectsSortOutsideTheAllowlist keeps the one door a column
// name could come through closed.
func Test{{.Entity}}ListRejectsSortOutsideTheAllowlist(t *testing.T) {
	repo := {{.Unexported}}RepoWithoutDB()
	// {{.Entity}}List, because listing is its own permission: a role may be
	// allowed to open the record it was given and not to page through every one.
	g := security.SystemGrant(policies.{{.Entity}}List, "t1")

	_, err := repo.List(context.Background(), g, data.Query{Sort: "1; DROP TABLE {{.Table}}"})

	if !errors.Is(err, models.Err{{.Entity}}Sort) {
		t.Fatalf("error = %v, want Err{{.Entity}}Sort", err)
	}
}

// arandu:begin custom
// Tests for the rules you wrote go here, and survive regeneration.
// arandu:end custom
`
