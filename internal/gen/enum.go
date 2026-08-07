package gen

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

// EnumValue is one member of the closed set.
type EnumValue struct {
	// Name is the value as stored: "draft". It is what a column holds and what
	// every consumer of an event reads, so it is never derived from the constant.
	Name string
	// Number is the stored value when the enum is backed by an integer.
	Number int
	// Type is the enum's type name, so a value can spell its own constant.
	Type string
}

// Const is the constant that names it: InvoiceStatusDraft.
func (v EnumValue) Const() string { return v.Type + exported(v.Name) }

// Label is what a form shows: "Draft".
func (v EnumValue) Label() string {
	words := strings.Split(strings.ReplaceAll(v.Name, "-", "_"), "_")
	label := strings.Join(words, " ")
	if label == "" {
		return label
	}
	return strings.ToUpper(label[:1]) + label[1:]
}

// EnumSpec is one enum.
type EnumSpec struct {
	// Type is the Go type name: "InvoiceStatus".
	Type string
	// Values are the members, in declaration order.
	Values []EnumValue
	// Int backs the enum with an integer instead of a string.
	Int bool
}

// Base is the underlying type.
func (s EnumSpec) Base() string {
	if s.Int {
		return "int"
	}
	return "string"
}

// Human is the type in a sentence: "invoice status".
func (s EnumSpec) Human() string { return strings.ReplaceAll(Normalize(s.Type), "_", " ") }

// Names lists the stored values, for the error message a parse failure carries.
func (s EnumSpec) Names() string {
	out := make([]string, 0, len(s.Values))
	for _, v := range s.Values {
		out = append(out, v.Name)
	}
	return strings.Join(out, ", ")
}

// Path is where the file goes.
func (s EnumSpec) Path() string {
	return filepath.Join("app", "Enums", s.Type+".go")
}

// Validate reports what is wrong before a file is written.
func (s EnumSpec) Validate() error {
	if !IsExportedIdentifier(s.Type) {
		return fmt.Errorf("%q is not a Go type name: it has to start with a capital letter and hold only letters, digits and underscore", s.Type)
	}
	if len(s.Values) == 0 {
		return fmt.Errorf("an enum needs its values: --values draft,sent,paid")
	}
	seen := map[string]bool{}
	for _, v := range s.Values {
		if v.Name == "" {
			return fmt.Errorf("an enum value cannot be empty")
		}
		for _, r := range v.Name {
			if !unicode.IsLower(r) && !unicode.IsDigit(r) && r != '_' && r != '-' {
				return fmt.Errorf("enum value %q must be lowercase letters, digits, underscore or dash", v.Name)
			}
		}
		if seen[v.Name] {
			return fmt.Errorf("enum value %q declared twice", v.Name)
		}
		seen[v.Name] = true
	}
	return nil
}

// ParseEnumValues reads the --values flag: "draft,sent,paid,void".
func ParseEnumValues(spec, typeName string, asInt bool) ([]EnumValue, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf(`--values is required, for example: --values "draft,sent,paid,void"`)
	}
	var out []EnumValue
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, EnumValue{Name: part, Number: len(out) + 1, Type: typeName})
	}
	return out, nil
}

// RenderEnum produces app/Enums/<Type>.go.
func RenderEnum(s EnumSpec) (File, error) {
	if err := s.Validate(); err != nil {
		return File{}, err
	}
	content, err := render(s.Type+".go", enumTemplate, s)
	if err != nil {
		return File{}, err
	}
	return File{Path: s.Path(), Content: content}, nil
}

const enumTemplate = `package enums

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
)

// {{.Type}} is the closed set of values the column may hold.
//
// Go has no enum keyword, so this is the shape that gets the guarantee anyway:
// a named type, constants that are the only valid values, and a Scan/Value pair
// so a value the application does not know about is an error at the read rather
// than a zero value that silently behaves like the first case.
//
// That last part is the whole reason the file is longer than a PHP enum. In PHP
// the backed enum refuses an unknown value at from(); here nothing refuses it
// unless the type does, and a plain ` + "`" + `type {{.Type}} {{.Base}}` + "`" + ` accepts anything
// the database hands it.
type {{.Type}} {{.Base}}

{{if .Int -}}
// The values. The numbers are explicit and never iota: iota renumbers everything
// below an insertion, and the numbers are already in the database.
{{- else -}}
// The values. Stored exactly as written -- renaming a constant must never
// rewrite a column.
{{- end}}
const (
{{- range .Values}}
	{{.Const}} {{$.Type}} = {{if $.Int}}{{.Number}}{{else}}"{{.Name}}"{{end}}
{{- end}}
)

// {{.Type}}Values lists them in declaration order.
//
// This is PHP's Enum::cases(). It feeds a <select> in a kyse view and it is what
// a test asserts against, so adding a value without deciding what the form and
// the migration do about it shows up as a failure.
func {{.Type}}Values() []{{.Type}} {
	return []{{.Type}}{
{{- range .Values}}
		{{.Const}},
{{- end}}
	}
}

// Valid reports whether v is one of the values.
func (v {{.Type}}) Valid() bool {
	switch v {
	case {{range $i, $v := .Values}}{{if $i}}, {{end}}{{$v.Const}}{{end}}:
		return true
	}
	return false
}

{{if .Int -}}
// String is the name of the value, because the stored one is a number and a
// number in a log is a lookup somebody has to do at three in the morning.
func (v {{.Type}}) String() string {
	switch v {
{{- range .Values}}
	case {{.Const}}:
		return "{{.Name}}"
{{- end}}
	}
	return fmt.Sprintf("{{$.Type}}(%d)", int(v))
}
{{- else -}}
// String is the stored value, so fmt and a view print it unchanged.
func (v {{.Type}}) String() string { return string(v) }
{{- end}}

// Label is what a form shows.
//
// Separate from String on purpose: the stored value is a contract with the
// database and with every consumer of an event, and the label is a contract
// with nobody. Changing a label must not be able to change a row.
func (v {{.Type}}) Label() string {
	switch v {
{{- range .Values}}
	case {{.Const}}:
		return "{{.Label}}"
{{- end}}
	}
	return v.String()
}

// Parse{{.Type}} turns request input into the type, or says why it cannot.
//
// This is PHP's from(). app/Http/Requests calls it, so a value outside the set
// becomes a field error the form can show rather than a row.
func Parse{{.Type}}(s string) ({{.Type}}, error) {
	for _, v := range {{.Type}}Values() {
		if v.String() == s {
			return v, nil
		}
	}
	return {{(index .Values 0).Const}}, fmt.Errorf("{{.Human}}: %q is not one of {{.Names}}", s)
}

// Compile-time proof that the repository can read and write it directly.
var (
	_ driver.Valuer = {{.Type}}({{if .Int}}0{{else}}""{{end}})
	_ sql.Scanner   = (*{{.Type}})(nil)
)

// Value implements driver.Valuer. It refuses to write a value outside the set,
// which is what keeps a zero-valued struct from putting {{if .Int}}a zero{{else}}an empty string{{end}} in the column.
func (v {{.Type}}) Value() (driver.Value, error) {
	if !v.Valid() {
		return nil, fmt.Errorf("{{.Human}}: refusing to write %q", v.String())
	}
	return {{if .Int}}int64(v){{else}}string(v){{end}}, nil
}

// Scan implements sql.Scanner.
//
// A value the application does not know about is an error here, at the row that
// has it, rather than a silent fallback three layers up. That is usually a
// migration that added a value the binary being rolled out does not have yet --
// and RULE 16 is what keeps that window short.
func (v *{{.Type}}) Scan(src any) error {
{{- if .Int}}
	var n int64
	switch raw := src.(type) {
	case int64:
		n = raw
	default:
		return fmt.Errorf("{{.Human}}: cannot read %T from the database", src)
	}
	parsed := {{.Type}}(n)
	if !parsed.Valid() {
		return fmt.Errorf("{{.Human}}: %d is not one of the known values", n)
	}
	*v = parsed
	return nil
{{- else}}
	var s string
	switch raw := src.(type) {
	case string:
		s = raw
	case []byte:
		s = string(raw)
	default:
		return fmt.Errorf("{{.Human}}: cannot read %T from the database", src)
	}
	parsed, err := Parse{{.Type}}(s)
	if err != nil {
		return err
	}
	*v = parsed
	return nil
{{- end}}
}

// arandu:begin custom
// Anything the set does not say: transitions (which status may follow which),
// a grouping predicate, a colour for the badge in the view.
// arandu:end custom
`
