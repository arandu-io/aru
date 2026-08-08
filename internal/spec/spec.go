// Package spec is the declarative form of a module: the DSL of phase 4.
//
// # The principle, repeated because everything depends on it
//
// The model never writes Go. The model produces a spec; a deterministic
// generator produces the code. Correctness does not depend on the model having
// been trained on Arandu -- it depends on our compiler.
//
// That is what answers the problem underneath: a new framework has zero examples
// in the training set, and a model fills the gap with the frameworks it does
// know
// patterns that do not exist here. With an intermediate spec that stops
// mattering: the schema fits in the context window, and it is verifiable before
// it becomes code.
//
// # The format
//
// YAML, because the training mass is enormous and the validation is mature. Not
// HCL, whose extra expressiveness is exactly what we do not want. Not CUE, whose
// validation is better and whose audience is small enough that models get it
// wrong.
//
// # A closed set
//
// What the DSL expresses is fixed, and this file is the whole of it. Anything
// outside the shape is written in Go, inside the `arandu:begin custom` markers,
// and survives regeneration.
//
// That is the decision the product lives or dies on. A DSL that grows to fit
// each case becomes a language somebody maintains forever; one too narrow and
// the user hits the wall on the first requirement that is not CRUD, and leaves.
// The escape hatch is the answer to both (RULE 15, doc 19).
package spec

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Version is the schema version a document declares.
//
// Declared rather than inferred, so a document written today still generates the
// same code when the schema has moved on -- and so a model that saw v1 in its
// training does not silently produce something a v2 generator misreads.
const Version = "1"

// Module is one module, as a specification.
//
// The field names are what a model writes, so they are the ones a person would
// guess: `name`, `fields`, `permissions`. Nothing here is abbreviated.
type Module struct {
	// Version of the schema this document is written against.
	Version string `yaml:"version" json:"version"`
	// Name is the module: "purchase_order", lowercase with underscores.
	Name string `yaml:"name" json:"name"`
	// Description is free text. It is not used for generation -- it is there
	// because a spec a person cannot read is a spec a person cannot approve,
	// and approving the spec instead of the diff is the whole pipeline.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// Tenant scopes every query by the Grant's tenant.
	Tenant bool `yaml:"tenant" json:"tenant"`
	// Fields are the entity's columns.
	Fields []Field `yaml:"fields" json:"fields"`
	// Permissions maps an action to the roles allowed to take it. It is what
	// opens the generated Policy, which denies everything by default.
	Permissions map[string][]string `yaml:"permissions,omitempty" json:"permissions,omitempty"`
}

// Field is one column.
type Field struct {
	Name string `yaml:"name" json:"name"`
	// Type is one of the closed set in Types.
	Type string `yaml:"type" json:"type"`
	// Required makes it NOT NULL and validated as present.
	Required bool `yaml:"required,omitempty" json:"required,omitempty"`
	// Unique adds a unique index, scoped by tenant when the module is.
	Unique bool `yaml:"unique,omitempty" json:"unique,omitempty"`
}

// Types is the closed set, with what each one means.
//
// Closed because an open set is a type system, and a type system is a language.
// Adding one is a decision with an ADR, not a pull request.
var Types = map[string]string{
	"string":    "short text, up to a line",
	"text":      "long text",
	"int":       "whole number",
	"decimal":   "fractional number -- never money",
	"money":     "an amount, stored in cents as an integer",
	"bool":      "true or false",
	"date":      "a day, with no time",
	"timestamp": "a moment",
	"uuid":      "an identifier",
	"email":     "an address, validated as one",
}

// Actions is the closed set of permissions a module can grant.
//
// Five, because they are what a CRUD module does. A module that needs a sixth
// has a business rule, and a business rule is written in Go inside the custom
// block -- with its own Action constant and its own Policy branch.
var Actions = []string{"view", "list", "create", "update", "delete"}

// Validate reports everything wrong with a specification, at once.
//
// At once, not at the first error: a model that gets three fields wrong should
// learn all three from one response, and a person fixing a file should not
// discover the problems one run at a time. This is the step where a bad spec
// dies -- an error here never becomes broken code.
func (m Module) Validate() error {
	var problems []string

	if m.Version == "" {
		problems = append(problems, "version is required: write `version: \"1\"`")
	} else if m.Version != Version {
		problems = append(problems, fmt.Sprintf(
			"version %q is not supported by this generator, which writes schema %s", m.Version, Version))
	}

	switch {
	case m.Name == "":
		problems = append(problems, "name is required")
	case !isSnakeCase(m.Name):
		problems = append(problems, fmt.Sprintf(
			"name %q must be lowercase with underscores: purchase_order, not PurchaseOrder", m.Name))
	case isGoReserved(m.Name):
		problems = append(problems, fmt.Sprintf(
			"name %q is a Go keyword, and the module name becomes the package name -- `package %s` does not compile. Pick another: %s_module, or the plural",
			m.Name, m.Name, m.Name))
	case isSQLReserved(m.Name):
		problems = append(problems, fmt.Sprintf(
			"name %q is reserved in SQL, and the module name becomes the table name -- `CREATE TABLE %s` is a syntax error on PostgreSQL and MySQL. Use the plural: %ss",
			m.Name, m.Name, m.Name))
	case isGeneratedName(m.Name):
		problems = append(problems, fmt.Sprintf(
			"name %q collides with an identifier this generator writes into the module's own package (%s). Pick a name from the domain: invoice, purchase_order",
			m.Name, strings.Join(generatedNames, ", ")))
	}

	if len(m.Fields) == 0 {
		problems = append(problems, "fields is required: a module with no fields has nothing to store")
	}

	seen := map[string]bool{}
	// byExported maps the Go field name back to the first column that produced
	// it, so a collision can name both sides.
	byExported := map[string]string{}
	for i, f := range m.Fields {
		where := fmt.Sprintf("fields[%d]", i)
		if f.Name != "" {
			where = fmt.Sprintf("field %q", f.Name)
		}

		switch {
		case f.Name == "":
			problems = append(problems, where+": name is required")
		case !isSnakeCase(f.Name):
			problems = append(problems, fmt.Sprintf("%s: must be lowercase with underscores", where))
		case seen[f.Name]:
			problems = append(problems, fmt.Sprintf("%s: declared twice", where))
		case isReserved(f.Name):
			// id, tenant_id and the timestamps are generated. A field with the
			// same name would produce a struct with two of them, and the error
			// would come from the Go compiler pointing at generated code.
			problems = append(problems, fmt.Sprintf(
				"%s: %s is generated for every module -- remove it", where, f.Name))
		case isSQLReserved(f.Name):
			problems = append(problems, fmt.Sprintf(
				"%s: %s is reserved in SQL, and a field name becomes a column name -- `%s TEXT NOT NULL` is a syntax error on PostgreSQL and MySQL. Rename it: %s_name, or say what it is",
				where, f.Name, f.Name, f.Name))
		case isGoReserved(f.Name):
			problems = append(problems, fmt.Sprintf(
				"%s: %s is a Go keyword and would become a struct field of that name, which does not compile. Rename it",
				where, f.Name))
		case exportedName(f.Name) != "" && byExported[exportedName(f.Name)] != "":
			// full_name and fullname are different columns and the same Go
			// field. The compiler reports "FullName redeclared" against a file
			// the author never opened.
			problems = append(problems, fmt.Sprintf(
				"%s: it and %q both become the Go field %s. Two columns cannot share one struct field -- rename one",
				where, byExported[exportedName(f.Name)], exportedName(f.Name)))
		}
		seen[f.Name] = true
		if e := exportedName(f.Name); e != "" && byExported[e] == "" {
			byExported[e] = f.Name
		}

		if f.Type == "" {
			problems = append(problems, where+": type is required. "+TypeList())
		} else if _, known := Types[f.Type]; !known {
			problems = append(problems, fmt.Sprintf("%s: unknown type %q. %s", where, f.Type, TypeList()))
		} else if f.Unique && f.Type == "text" {
			// A long text column cannot carry a portable unique index: MySQL
			// needs a prefix length for TEXT in a key, and a prefix makes the
			// constraint mean something different from what it says.
			problems = append(problems, fmt.Sprintf(
				"%s: a text field cannot be unique -- long text has no portable unique index. Use `type: string` if the value is short enough to be an identifier",
				where))
		} else if f.Required && f.Type == "bool" {
			// There is no way to tell "the user said false" from "the user said
			// nothing": both decode to false. A required bool would either
			// reject a legitimate answer or be ignored, and both are worse than
			// saying so here.
			problems = append(problems, fmt.Sprintf(
				"%s: a bool cannot be required -- false is an answer, not an absence, and nothing can tell them apart. Drop `required`, or make the field an int with the meanings written down",
				where))
		}
	}

	for action, roles := range m.Permissions {
		for _, role := range roles {
			if isRole(role) {
				continue
			}
			// A role reaches the generated Policy as a Go string literal. One
			// that closes the quote rewrites the authorization:
			//
			//   permissions: {view: ['viewer") || true || s.HasRole("nobody']}
			//
			// produced `if s.HasRole("viewer") || true || s.HasRole("nobody")`,
			// which opens the action to every subject of every tenant. The
			// template quotes it too -- this is the first of two locks, and the
			// one that says why.
			problems = append(problems, fmt.Sprintf(
				"permissions.%s: %q is not a role name. A role is letters, digits, and _ . : - (admin, team.lead, org:owner) -- anything else would be written into the generated Policy as code",
				action, role))
		}

		if !known(Actions, action) {
			problems = append(problems, fmt.Sprintf(
				"permissions: unknown action %q. The set is %s -- anything else is a business rule, written in Go inside the custom block",
				action, strings.Join(Actions, ", ")))
		}
		if len(roles) == 0 {
			problems = append(problems, fmt.Sprintf(
				"permissions.%s: no roles. Remove the entry rather than leaving it empty -- an action nobody can take reads like an oversight",
				action))
		}
	}

	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("this specification cannot be generated:\n  - %s", strings.Join(problems, "\n  - "))
}

// TypeList is the closed set, for an error message.
func TypeList() string {
	names := make([]string, 0, len(Types))
	for name := range Types {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("The types are:")
	for _, name := range names {
		fmt.Fprintf(&b, "\n    %-10s %s", name, Types[name])
	}
	return b.String()
}

// reserved are the columns every module gets.
var reserved = []string{"id", "tenant_id", "created_at", "updated_at"}

func isReserved(name string) bool { return known(reserved, name) }

// sqlReserved is the union of the reserved words of SQLite, PostgreSQL and
// MySQL that a person plausibly names a column after.
//
// A name from this list reaches the generated DDL unquoted -- `order TEXT NOT
// NULL` -- and the engine rejects the migration with a syntax error pointing at
// a character position in a file nobody wrote by hand. Found by audit.
//
// Refused rather than quoted. Quoting is spelled differently on each engine
// ("order" here, `+"`order`"+` there), and one schema that serves all three is the
// whole point of the portable subset (ADR 0009). Renaming the field costs the
// author five seconds; carrying a per-engine quoting rule costs forever.
var sqlReserved = []string{
	"add", "all", "alter", "and", "as", "asc", "between", "by", "case", "cast",
	"check", "column", "constraint", "create", "cross", "current_date",
	"current_time", "current_timestamp", "database", "default", "delete", "desc",
	"distinct", "drop", "else", "end", "escape", "except", "exists", "explain",
	"false", "for", "foreign", "from", "full", "group", "having", "in", "index",
	"inner", "insert", "intersect", "interval", "into", "is", "join", "key",
	"left", "like", "limit", "lock", "natural", "not", "null", "offset", "on",
	"or", "order", "outer", "primary", "range", "references", "rename",
	"replace", "restrict", "returning", "right", "row", "rows", "select", "set",
	"table", "then", "to", "transaction", "trigger", "true", "union", "unique",
	"update", "using", "values", "when", "where", "window", "with",
}

func isSQLReserved(name string) bool { return known(sqlReserved, name) }

// goReserved is the Go keyword set.
//
// A module named "range" produces `package range`, and a field named "type"
// produces a struct member the compiler refuses. The generator used to get as
// far as writing the file and then report a template bug, which sends the
// author looking in the wrong place entirely.
var goReserved = []string{
	"break", "case", "chan", "const", "continue", "default", "defer", "else",
	"fallthrough", "for", "func", "go", "goto", "if", "import", "interface",
	"map", "package", "range", "return", "select", "struct", "switch", "type",
	"var",
}

func isGoReserved(name string) bool { return known(goReserved, name) }

// generatedNames are the identifiers the generator writes into the module's own
// package. A module named after one of them collides with what is generated for
// it, and the Go compiler reports the redeclaration against a file the author
// did not write.
var generatedNames = []string{
	"policy", "repo", "service", "module", "columns", "scan", "sortable",
	"entity", "handler", "routes", "validate",
}

func isGeneratedName(name string) bool { return known(generatedNames, name) }

// exportedName is the Go identifier a column name becomes.
//
// It has to agree with gen.exported, and the golden tests hold them together:
// this one exists so a collision is reported here, in the specification, rather
// than by the Go compiler against generated code.
func exportedName(s string) string {
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

func known(set []string, value string) bool {
	for _, s := range set {
		if s == value {
			return true
		}
	}
	return false
}

// roleName is what a role may contain.
//
// Closed on purpose, and checked before the value ever reaches a template: this
// string is written into generated Go, and a permissive alphabet here is an
// authorization bypass there.
var roleName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)

func isRole(s string) bool { return roleName.MatchString(s) }

// isSnakeCase accepts what the generator can turn into both a Go identifier and
// a column name without guessing.
func isSnakeCase(s string) bool {
	if s == "" || s[0] == '_' || s[len(s)-1] == '_' {
		return false
	}
	if s[0] >= '0' && s[0] <= '9' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_':
		default:
			return false
		}
	}
	return true
}
