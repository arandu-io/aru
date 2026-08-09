// Package gen turns a module specification into Go source.
//
// It is deterministic and it does not call a model: the same specification
// produces the same bytes, which is what makes golden files a real test rather
// than a formality. In phase 4 the specification comes from a YAML file a model
// wrote; today it comes from command-line flags. The generator does not care
// which, and that is the point -- the model never writes Go.
package gen

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Type is a field type. The set is closed by decision, not by omission: a
// generator whose type list grows on demand becomes a language, and a language
// has to be maintained forever. See docs/19-dsl-e-geracao.md.
type Type string

// The closed set.
const (
	TypeString    Type = "string"
	TypeText      Type = "text"
	TypeInt       Type = "int"
	TypeDecimal   Type = "decimal"
	TypeMoney     Type = "money"
	TypeBool      Type = "bool"
	TypeDate      Type = "date"
	TypeTimestamp Type = "timestamp"
	TypeUUID      Type = "uuid"
	TypeEmail     Type = "email"
)

// types maps each accepted type to how it is written in Go and in SQL.
//
// The SQL column types are the portable subset: every one of them spells the
// same in SQLite, PostgreSQL and MySQL, which is what lets one schema serve all
// three (ADR 0009).
var types = map[Type]struct {
	Go     string
	SQL    string
	Zero   string
	Import string
}{
	// VARCHAR for the short kinds, TEXT for the long one. That is the
	// distinction the DSL already draws -- "short text, up to a line" against
	// "long text" -- and the generator used to collapse both to TEXT, which
	// MySQL refuses in a UNIQUE or an index without a prefix length. See
	// data.KeyText. Found by audit.
	TypeString: {Go: "string", SQL: "VARCHAR(255)", Zero: `""`},
	TypeText:   {Go: "string", SQL: "TEXT", Zero: `""`},
	TypeInt:    {Go: "int64", SQL: "INTEGER", Zero: "0"},
	// DOUBLE PRECISION, not REAL: REAL is four bytes in PostgreSQL, so a
	// float64 written through it comes back rounded to about seven digits --
	// silently, on read, with no error anywhere. SQLite gives "DOUB" REAL
	// affinity and MySQL accepts the spelling, so one word serves all three.
	// Found by audit.
	TypeDecimal: {Go: "float64", SQL: "DOUBLE PRECISION", Zero: "0"},
	// Money is an integer of cents, never a float: 0.1 + 0.2 is not 0.3 in
	// binary floating point, and an invoice off by a cent is a support ticket.
	TypeMoney:     {Go: "int64", SQL: "INTEGER", Zero: "0"},
	TypeBool:      {Go: "bool", SQL: "BOOLEAN", Zero: "false"},
	TypeDate:      {Go: "time.Time", SQL: "DATE", Zero: "time.Time{}", Import: "time"},
	TypeTimestamp: {Go: "time.Time", SQL: "TIMESTAMP", Zero: "time.Time{}", Import: "time"},
	TypeUUID:      {Go: "string", SQL: "VARCHAR(255)", Zero: `""`},
	TypeEmail:     {Go: "string", SQL: "VARCHAR(255)", Zero: `""`},
}

// Field is one column of the entity.
type Field struct {
	Name     string // as written by the user: "full_name"
	Type     Type
	Required bool
	Unique   bool
}

// GoName is the exported Go identifier: "full_name" becomes "FullName".
func (f Field) GoName() string { return exported(f.Name) }

// GoType is the Go type.
func (f Field) GoType() string { return types[f.Type].Go }

// SQLType is the column type, in the portable subset.
func (f Field) SQLType() string { return types[f.Type].SQL }

// Column is the column name.
func (f Field) Column() string { return f.Name }

// IsEmail reports whether the field gets email validation and normalization.
func (f Field) IsEmail() bool { return f.Type == TypeEmail }

// Bind renders the field as an argument to a statement.
//
// A date goes through data.Day, which truncates to midnight UTC. DATE is the one
// type in the portable subset the engines do not agree about: PostgreSQL drops
// the time part on write and SQLite keeps it, so the same code returns different
// values on different engines. Everything else binds as it is.
func (f Field) Bind(receiver string) string {
	if f.Type == TypeDate {
		return "data.Day(" + receiver + "." + f.GoName() + ")"
	}
	return receiver + "." + f.GoName()
}

// IsString reports whether the field is text-like, and therefore gets length
// validation.
func (f Field) IsString() bool {
	return f.Type == TypeString || f.Type == TypeText || f.Type == TypeEmail
}

// IsTime reports whether the field is a date or a timestamp, which are the two
// that arrive as text and leave as time.Time.
func (f Field) IsTime() bool { return f.Type == TypeDate || f.Type == TypeTimestamp }

// IsBool reports whether the field is a checkbox in a form.
func (f Field) IsBool() bool { return f.Type == TypeBool }

// IsWholeNumber reports whether the field is parsed with ParseInt.
func (f Field) IsWholeNumber() bool { return f.Type == TypeInt || f.Type == TypeMoney }

// IsFraction reports whether the field is parsed with ParseFloat.
func (f Field) IsFraction() bool { return f.Type == TypeDecimal }

// IsLongText reports whether the field is rendered as a textarea.
func (f Field) IsLongText() bool { return f.Type == TypeText }

// Label is the field as a form label: "supplier_email" becomes "Supplier email".
//
// Sentence case, not Title Case: it is what a form label looks like in an
// application somebody designed, and Title Case On Every Word is what a
// generator looks like.
func (f Field) Label() string {
	words := strings.Split(f.Name, "_")
	label := strings.Join(words, " ")
	if label == "" {
		return label
	}
	return strings.ToUpper(label[:1]) + label[1:]
}

// TimeLayout is how a date or a timestamp is written in a form field.
//
// They are the layouts the HTML input types produce: "date" submits
// 2006-01-02 and "datetime-local" submits 2006-01-02T15:04. Parsing anything
// else would reject what the browser itself sent.
func (f Field) TimeLayout() string {
	if f.Type == TypeTimestamp {
		return "2006-01-02T15:04"
	}
	return "2006-01-02"
}

// InputType is the HTML input type of the field.
func (f Field) InputType() string {
	switch f.Type {
	case TypeEmail:
		return "email"
	case TypeInt, TypeMoney, TypeDecimal:
		return "number"
	case TypeDate:
		return "date"
	case TypeTimestamp:
		return "datetime-local"
	case TypeBool:
		return "checkbox"
	default:
		return "text"
	}
}

// InputStep is the step attribute of a numeric input: cents for money, any for
// a decimal, and whole units for an integer.
func (f Field) InputStep() string {
	switch f.Type {
	case TypeDecimal:
		return "any"
	default:
		return "1"
	}
}

// ViewType is how the field is declared in the view's row struct.
//
// Dates leave as text, already formatted: a view that formats a time.Time would
// need the time package imported into a generated file whose imports are fixed,
// and formatting is a decision about presentation that belongs to the
// controller anyway.
func (f Field) ViewType() string {
	switch f.Type {
	case TypeDate, TypeTimestamp:
		return "string"
	default:
		return types[f.Type].Go
	}
}

// Display is the expression that turns the entity's field into the row's.
func (f Field) Display(receiver string) string {
	if f.IsTime() {
		return receiver + "." + f.GoName() + `.Format("` + f.displayLayout() + `")`
	}
	return receiver + "." + f.GoName()
}

func (f Field) displayLayout() string {
	if f.Type == TypeTimestamp {
		return "2006-01-02 15:04"
	}
	return "2006-01-02"
}

// FormType is how the field is declared in the view's form struct.
//
// Everything is text except a boolean, because a form carries text: the value
// that comes back after a rejection has to be exactly what the person typed,
// including the "12,00" that failed to parse. A checkbox is the exception --
// it is checked or it is not.
func (f Field) FormType() string {
	if f.IsBool() {
		return "bool"
	}
	return "string"
}

// FormValue is the expression that fills the form struct from the entity, for
// the edit screen.
func (f Field) FormValue(receiver string) string {
	name := receiver + "." + f.GoName()
	switch {
	case f.IsBool():
		return name
	case f.IsTime():
		return name + `.Format("` + f.TimeLayout() + `")`
	case f.Type == TypeMoney || f.Type == TypeInt:
		return "strconv.FormatInt(" + name + ", 10)"
	case f.Type == TypeDecimal:
		return "strconv.FormatFloat(" + name + ", 'f', -1, 64)"
	default:
		return name
	}
}

// Parse is the expression a controller uses to read the field from the request.
//
// The helpers it names are methods on the controller rather than package
// functions: every module generates into the same package now, and two modules
// declaring parseInt would not compile.
func (f Field) Parse(receiver string) string {
	switch {
	case f.IsBool():
		// An unchecked box sends nothing at all, which is what makes presence
		// the whole test.
		return `ctx.Input("` + f.Column() + `") != ""`
	case f.IsWholeNumber():
		return receiver + `.whole(ctx, "` + f.Column() + `", errs)`
	case f.IsFraction():
		return receiver + `.fraction(ctx, "` + f.Column() + `", errs)`
	case f.IsTime():
		return receiver + `.moment(ctx, "` + f.Column() + `", "` + f.TimeLayout() + `", errs)`
	default:
		return `ctx.Input("` + f.Column() + `")`
	}
}

// MaxLength is the limit the generated validation enforces.
//
// It agrees with the column: a value that passes validation has to fit, or the
// rejection comes from the database driver instead of from the validator, in a
// message about a column rather than about a field somebody filled in.
func (f Field) MaxLength() int {
	switch f.Type {
	case TypeEmail:
		// The addr-spec limit, which is smaller than the column.
		return 254
	case TypeText:
		return 5000
	default:
		return 255
	}
}

// Module is the whole specification.
type Module struct {
	// Name is the module name as the user typed it: "purchase_order".
	Name   string
	Fields []Field
	// Tenant scopes every query by the Grant's tenant. It is a flag rather than
	// the default because a module can legitimately be global -- but the moment
	// it is set, there is no way to write a query that ignores it.
	Tenant bool
	// Module path of the project, for the generated imports.
	ModulePath string
	// Date is the migration id prefix, e.g. "2026_07_31". It is a field rather
	// than time.Now() so the generator stays deterministic and golden files mean
	// something.
	Date string
	// Sequence is the six-digit half of the migration id.
	//
	// Zero means one, so every existing caller and every golden file keeps the id
	// it has. It exists because two migrations written on the same day need two
	// numbers, and the number is read off the directory rather than off the
	// clock: a timestamp to the second collides when two commands run in the same
	// second, and a number read off the files is the same number on every machine.
	Sequence int
	// Permissions maps an action to the roles allowed to take it, and is what
	// opens the generated Policy.
	//
	// Empty means the policy denies everything, which is what `make:module`
	// produces: a generator that guessed at permissions would ship a hole by
	// default, in every project that ran it. `aru generate` fills this from the
	// specification, where a person or a model said so out loud.
	Permissions map[string][]string
}

// Rules returns the permissions in a fixed order, for the template.
//
// Sorted, because a map ranges differently on every run and the golden files
// would never match twice.
func (m Module) Rules() []Rule {
	if len(m.Permissions) == 0 {
		return nil
	}

	// The order actions are declared in, not alphabetical: reading view, list,
	// create, update, delete follows what the module does.
	order := []string{"view", "list", "create", "update", "delete"}
	out := make([]Rule, 0, len(m.Permissions))

	for _, action := range order {
		roles, listed := m.Permissions[action]
		if !listed || len(roles) == 0 {
			continue
		}
		sorted := append([]string(nil), roles...)
		sort.Strings(sorted)
		out = append(out, Rule{Action: exported(action), Roles: sorted})
	}
	return out
}

// Rule is one action and the roles that may take it.
type Rule struct {
	// Action is the constant name: "View", "Create".
	Action string
	Roles  []string
}

// Entity is the exported type name: "purchase_order" becomes "PurchaseOrder".
func (m Module) Entity() string { return exported(m.Name) }

// Table is the table name, pluralized the simple way. English pluralization has
// hundreds of exceptions; this handles the common ones and gets out of the way.
func (m Module) Table() string {
	n := strings.ReplaceAll(m.Name, "-", "_")
	switch {
	case strings.HasSuffix(n, "s"), strings.HasSuffix(n, "x"), strings.HasSuffix(n, "ch"), strings.HasSuffix(n, "sh"):
		return n + "es"
	case strings.HasSuffix(n, "y") && len(n) > 1 && !isVowel(n[len(n)-2]):
		return n[:len(n)-1] + "ies"
	default:
		return n + "s"
	}
}

// Route is the URL prefix of the module.
func (m Module) Route() string { return "/" + m.Resource() }

// Resource is the resource segment: the table with dashes instead of
// underscores. It names the URL, the route names and the view directory, so all
// three agree by construction -- "purchase-orders", /purchase-orders,
// purchase-orders.index.
func (m Module) Resource() string { return strings.ReplaceAll(m.Table(), "_", "-") }

// Plural is the exported plural of the entity: "PurchaseOrders". It names the
// view data types, which are per page and per module.
func (m Module) Plural() string { return exported(m.Table()) }

// Unexported is the entity with a lowercase initial: "purchaseOrder".
//
// Every module now generates into shared packages -- app/Models, app/Policies,
// app/Repositories -- so an unexported package-level name has to carry the
// entity or the second module fails to compile.
func (m Module) Unexported() string {
	e := m.Entity()
	return strings.ToLower(e[:1]) + e[1:]
}

// Controller is the type name of the controller: "PurchaseOrderController".
func (m Module) Controller() string { return m.Entity() + "Controller" }

// PolicyType is the type name of the policy.
func (m Module) PolicyType() string { return m.Entity() + "Policy" }

// RepositoryType is the type name of the repository.
func (m Module) RepositoryType() string { return m.Entity() + "Repository" }

// ServiceType is the type name of the service.
func (m Module) ServiceType() string { return m.Entity() + "Service" }

// StoreRequest is the type name of the request that creates: StorePurchaseOrder.
func (m Module) StoreRequest() string { return "Store" + m.Entity() }

// UpdateRequest is the type name of the request that updates.
func (m Module) UpdateRequest() string { return "Update" + m.Entity() }

// RowStruct is the view struct that carries one record to the markup.
func (m Module) RowStruct() string { return m.Entity() + "Row" }

// FormStruct is the view struct that carries the form fields.
//
// It is not called FormType, so that reading a template is unambiguous: a field
// has a FormType -- string or bool -- and the module has a FormStruct.
func (m Module) FormStruct() string { return m.Entity() + "Form" }

// Human is the entity as a person writes it in a sentence: "purchase order".
func (m Module) Human() string { return strings.ReplaceAll(m.Name, "_", " ") }

// Humans is the plural of Human: "purchase orders".
func (m Module) Humans() string { return strings.ReplaceAll(m.Table(), "_", " ") }

// HumanTitle is Human with a capital initial, for a heading: "Purchase order".
func (m Module) HumanTitle() string { return upperFirst(m.Human()) }

// HumansTitle is Humans with a capital initial: "Purchase orders".
func (m Module) HumansTitle() string { return upperFirst(m.Humans()) }

// ViewData is the data type of one page: PurchaseOrdersIndexData.
func (m Module) ViewData(page string) string { return m.Plural() + exported(page) + "Data" }

// ViewName is how a page is rendered: purchase-orders.index. It is the path
// under resources/views with dots, which is how a view is named.
func (m Module) ViewName(page string) string { return m.Resource() + "." + page }

// MigrationID is the immutable identifier of the generated migration.
func (m Module) MigrationID() string {
	return fmt.Sprintf("%s_%06d_create_%s_table", m.Date, m.seq(), m.Table())
}

// seq is the sequence, defaulting to one.
func (m Module) seq() int {
	if m.Sequence <= 0 {
		return 1
	}
	return m.Sequence
}

// MigrationVar is the name of the migration value, which database/migrations
// lists in All(). It is unexported: nothing outside that package names it.
func (m Module) MigrationVar() string { return "create" + m.Plural() + "Table" }

// ModelsImport is the import path of app/Models.
//
// The directories of the tree are CamelCase, as PSR-4 spells them, and the
// packages inside them are lowercase, as Go spells them. Every generated import
// is therefore written with an explicit alias, so nobody has to guess which
// identifier a path called ".../app/Models" binds.
func (m Module) ModelsImport() string { return m.ModulePath + "/app/Models" }

// PoliciesImport is the import path of app/Policies.
func (m Module) PoliciesImport() string { return m.ModulePath + "/app/Policies" }

// RepositoriesImport is the import path of app/Repositories.
func (m Module) RepositoriesImport() string { return m.ModulePath + "/app/Repositories" }

// ServicesImport is the import path of app/Services.
func (m Module) ServicesImport() string { return m.ModulePath + "/app/Services" }

// RequestsImport is the import path of app/Http/Requests.
func (m Module) RequestsImport() string { return m.ModulePath + "/app/Http/Requests" }

// ViewsImport is the package the compiled views live in.
//
// storage/framework/views and not resources/views, and that is not a detail: a
// view is WRITTEN in resources/views/<resource>/show.kyse.go and COMPILED to
// storage/framework/views/<resource>/show.go, which is the only one of the two
// the Go compiler ever sees -- the source carries `//go:build kyse` precisely so
// that it does not.
//
// Importing the source directory produces "build constraints exclude all Go
// files", which names the right directory for the wrong reason and sends the
// reader looking for a missing build tag. This generator emitted exactly that
// for one release. Found by generating a module and building it.
//
// One directory per resource, because one directory is one Go package.
func (m Module) ViewsImport() string {
	return m.ModulePath + "/storage/framework/views/" + m.Resource()
}

// NeedsWholeParse reports whether the controller parses an integer.
func (m Module) NeedsWholeParse() bool {
	for _, f := range m.Fields {
		if f.IsWholeNumber() {
			return true
		}
	}
	return false
}

// NeedsFractionParse reports whether the controller parses a decimal.
func (m Module) NeedsFractionParse() bool {
	for _, f := range m.Fields {
		if f.IsFraction() {
			return true
		}
	}
	return false
}

// NeedsTimeParse reports whether the controller parses a date or a timestamp,
// which is the only reason it imports time.
func (m Module) NeedsTimeParse() bool {
	for _, f := range m.Fields {
		if f.IsTime() {
			return true
		}
	}
	return false
}

// BoolFields returns the checkbox fields, which are the ones the form renders
// with a checked attribute.
func (m Module) BoolFields() []Field {
	var out []Field
	for _, f := range m.Fields {
		if f.IsBool() {
			out = append(out, f)
		}
	}
	return out
}

// FirstField is the column the listing links from. It is the first the
// specification declared, which is the one a person thinks of as the name of
// the record.
func (m Module) FirstField() Field { return m.Fields[0] }

// RestFields is everything after the first, for the remaining table columns.
func (m Module) RestFields() []Field {
	if len(m.Fields) < 2 {
		return nil
	}
	return m.Fields[1:]
}

// Receiver is the short receiver name used in generated methods.
func (m Module) Receiver() string {
	initial := strings.ToLower(m.Entity()[:1])

	// The generated Policy signature already binds ctx, s and a:
	//
	//	Can(ctx context.Context, s security.Subject, a security.Action, x Entity)
	//
	// So an entity whose initial is one of those -- Subscription, Account,
	// Category -- would shadow the parameter it needs, and the file would not
	// compile. Two letters is enough to get out of the way, and it reads like
	// what a person would have written: "su" for Subscription.
	//
	// Found by a real measurement: ten specifications written by models, two of
	// which named an entity starting with S. A generator tested on one module
	// never sees this.
	if taken(initial) && len(m.Entity()) > 1 {
		return strings.ToLower(m.Entity()[:2])
	}
	return initial
}

// taken reports whether an identifier collides with what the generated
// signatures already bind.
func taken(name string) bool {
	switch name {
	case "s", "a", "c", "w", "r":
		// s: security.Subject   a: security.Action   c: context, in some templates
		// w: http.ResponseWriter   r: *http.Request
		return true
	}
	return false
}

// Sortable returns the fields that may be used for ordering. Only text and
// timestamps: a sort field is a column name, and the allowlist is what keeps a
// column name from the request out of the SQL.
func (m Module) Sortable() []Field {
	var out []Field
	for _, f := range m.Fields {
		if f.IsString() || f.Type == TypeDate || f.Type == TypeTimestamp {
			out = append(out, f)
		}
	}
	return out
}

// UniqueFields returns the fields declared unique.
func (m Module) UniqueFields() []Field {
	var out []Field
	for _, f := range m.Fields {
		if f.Unique {
			out = append(out, f)
		}
	}
	return out
}

// Validate reports what is wrong with the specification, before any file is
// written. A specification error must never become broken code.
func (m Module) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("the module needs a name")
	}
	if !isIdentifier(m.Name) {
		return fmt.Errorf("module name %q must be lowercase letters, digits and underscore, starting with a letter", m.Name)
	}
	if len(m.Fields) == 0 {
		return fmt.Errorf("the module needs at least one field")
	}
	if m.ModulePath == "" {
		return fmt.Errorf("the project module path is required")
	}

	seen := map[string]bool{}
	for _, f := range m.Fields {
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
		switch f.Name {
		case "id", "tenant_id", "created_at":
			return fmt.Errorf("field %q is generated for every module; do not declare it", f.Name)
		}
	}
	return nil
}

// TypeList is the closed set, for error messages.
func TypeList() string {
	return "accepted types: string, text, int, decimal, money, bool, date, timestamp, uuid, email"
}

// ParseFields reads the --fields flag: "name:string!,email:email!u,total:money".
//
// The suffixes are "!" for required and "u" for unique. It is terse because it
// is typed on a command line; the YAML specification of phase 4 spells the same
// thing out, and produces the same Module.
func ParseFields(spec string) ([]Field, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf(`--fields is required, for example: --fields "name:string!,email:email!u"`)
	}

	var out []Field
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, rest, ok := strings.Cut(part, ":")
		if !ok {
			return nil, fmt.Errorf("field %q must be written as name:type", part)
		}

		f := Field{Name: strings.TrimSpace(name)}
		rest = strings.TrimSpace(rest)
		for len(rest) > 0 {
			switch rest[len(rest)-1] {
			case '!':
				f.Required = true
			case 'u', 'U':
				f.Unique = true
			default:
				goto done
			}
			rest = rest[:len(rest)-1]
		}
	done:
		f.Type = Type(rest)
		if _, known := types[f.Type]; !known {
			return nil, fmt.Errorf("field %q: unknown type %q (%s)", f.Name, f.Type, TypeList())
		}
		out = append(out, f)
	}
	return out, nil
}

// Normalize accepts the two spellings of one name and returns the module one.
//
// `aru make:module` takes a module name -- purchase_order -- while the habit
// from elsewhere is to type a class name -- PurchaseOrder. People type the
// second, and the generator needs the first, so both are accepted and
// converted here rather than in eight commands.
func Normalize(name string) string {
	rs := []rune(strings.ReplaceAll(strings.TrimSpace(name), "-", "_"))
	var b strings.Builder
	for i, r := range rs {
		if !unicode.IsUpper(r) {
			b.WriteRune(r)
			continue
		}
		// An underscore goes in front of a capital that starts a word: after a
		// lowercase letter or a digit, or at the end of a run of capitals that is
		// followed by a lowercase one -- so HTTPServer becomes http_server rather
		// than h_t_t_p_server.
		prevLower := i > 0 && (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1]))
		prevUpper := i > 0 && unicode.IsUpper(rs[i-1])
		nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
		if i > 0 && rs[i-1] != '_' && (prevLower || (prevUpper && nextLower)) {
			b.WriteRune('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// Exported turns any accepted spelling of a name into the Go type it names:
// "invoice_line", "invoice-line" and "InvoiceLine" all become InvoiceLine.
func Exported(name string) string { return exported(Normalize(name)) }

// IsExportedIdentifier reports whether s can be a Go type name in a generated
// file. A generator that emitted an identifier Go refuses would emit a file that
// does not compile, which is worse than refusing here.
func IsExportedIdentifier(s string) bool {
	if s == "" || !unicode.IsUpper(rune(s[0])) {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}

func exported(s string) string {
	var b strings.Builder
	up := true
	for _, r := range s {
		if r == '_' || r == '-' {
			up = true
			continue
		}
		if up {
			b.WriteRune(unicode.ToUpper(r))
			up = false
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isIdentifier(s string) bool {
	if s == "" || !unicode.IsLower(rune(s[0])) {
		return false
	}
	for _, r := range s {
		if !unicode.IsLower(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}

// upperFirst capitalizes the first letter and leaves the rest alone. Sentence
// case, not Title Case: Title Case On Every Word is what a generator looks like.
func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func isVowel(b byte) bool { return strings.IndexByte("aeiou", b) >= 0 }
