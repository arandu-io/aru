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
	TypeString:  {Go: "string", SQL: "TEXT", Zero: `""`},
	TypeText:    {Go: "string", SQL: "TEXT", Zero: `""`},
	TypeInt:     {Go: "int64", SQL: "INTEGER", Zero: "0"},
	TypeDecimal: {Go: "float64", SQL: "REAL", Zero: "0"},
	// Money is an integer of cents, never a float: 0.1 + 0.2 is not 0.3 in
	// binary floating point, and an invoice off by a cent is a support ticket.
	TypeMoney:     {Go: "int64", SQL: "INTEGER", Zero: "0"},
	TypeBool:      {Go: "bool", SQL: "BOOLEAN", Zero: "false"},
	TypeDate:      {Go: "time.Time", SQL: "DATE", Zero: "time.Time{}", Import: "time"},
	TypeTimestamp: {Go: "time.Time", SQL: "TIMESTAMP", Zero: "time.Time{}", Import: "time"},
	TypeUUID:      {Go: "string", SQL: "TEXT", Zero: `""`},
	TypeEmail:     {Go: "string", SQL: "TEXT", Zero: `""`},
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

// IsString reports whether the field is text-like, and therefore gets length
// validation.
func (f Field) IsString() bool {
	return f.Type == TypeString || f.Type == TypeText || f.Type == TypeEmail
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
}

// Entity is the exported type name: "purchase_order" becomes "PurchaseOrder".
func (m Module) Entity() string { return exported(m.Name) }

// Package is the Go package name: lowercase, no separators.
func (m Module) Package() string { return strings.ReplaceAll(m.Name, "_", "") }

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
func (m Module) Route() string { return "/" + strings.ReplaceAll(m.Table(), "_", "-") }

// Receiver is the short receiver name used in generated methods.
func (m Module) Receiver() string { return strings.ToLower(m.Entity()[:1]) }

// NeedsTime reports whether the generated entity imports time.
func (m Module) NeedsTime() bool {
	for _, f := range m.Fields {
		if types[f.Type].Import == "time" {
			return true
		}
	}
	return true // CreatedAt is always there
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

func isVowel(b byte) bool { return strings.IndexByte("aeiou", b) >= 0 }
