package enums

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
)

// InvoiceStatus is the closed set of values the column may hold.
//
// Go has no enum keyword, so this is the shape that gets the guarantee anyway:
// a named type, constants that are the only valid values, and a Scan/Value pair
// so a value the application does not know about is an error at the read rather
// than a zero value that silently behaves like the first case.
//
// That last part is the whole reason the file is this long: nothing refuses an
// unknown value unless the type does, and a plain `type InvoiceStatus string`
// accepts anything the database hands it.
type InvoiceStatus string

// The values. Stored exactly as written -- renaming a constant must never
// rewrite a column.
const (
	InvoiceStatusDraft InvoiceStatus = "draft"
	InvoiceStatusSent  InvoiceStatus = "sent"
	InvoiceStatusPaid  InvoiceStatus = "paid"
	InvoiceStatusVoid  InvoiceStatus = "void"
)

// InvoiceStatusValues lists them in declaration order.
//
// It feeds a <select> in a kyse view and it is what a test asserts against, so
// adding a value without deciding what the form and the migration do about it
// shows up as a failure.
func InvoiceStatusValues() []InvoiceStatus {
	return []InvoiceStatus{
		InvoiceStatusDraft,
		InvoiceStatusSent,
		InvoiceStatusPaid,
		InvoiceStatusVoid,
	}
}

// Valid reports whether v is one of the values.
func (v InvoiceStatus) Valid() bool {
	switch v {
	case InvoiceStatusDraft, InvoiceStatusSent, InvoiceStatusPaid, InvoiceStatusVoid:
		return true
	}
	return false
}

// String is the stored value, so fmt and a view print it unchanged.
func (v InvoiceStatus) String() string { return string(v) }

// Label is what a form shows.
//
// Separate from String on purpose: the stored value is a contract with the
// database and with every consumer of an event, and the label is a contract
// with nobody. Changing a label must not be able to change a row.
func (v InvoiceStatus) Label() string {
	switch v {
	case InvoiceStatusDraft:
		return "Draft"
	case InvoiceStatusSent:
		return "Sent"
	case InvoiceStatusPaid:
		return "Paid"
	case InvoiceStatusVoid:
		return "Void"
	}
	return v.String()
}

// ParseInvoiceStatus turns request input into the type, or says why it cannot.
//
// app/Http/Requests calls it, so a value outside the set becomes a field error
// the form can show rather than a row.
func ParseInvoiceStatus(s string) (InvoiceStatus, error) {
	for _, v := range InvoiceStatusValues() {
		if v.String() == s {
			return v, nil
		}
	}
	return InvoiceStatusDraft, fmt.Errorf("invoice status: %q is not one of draft, sent, paid, void", s)
}

// Compile-time proof that the repository can read and write it directly.
var (
	_ driver.Valuer = InvoiceStatus("")
	_ sql.Scanner   = (*InvoiceStatus)(nil)
)

// Value implements driver.Valuer. It refuses to write a value outside the set,
// which is what keeps a zero-valued struct from putting an empty string in the column.
func (v InvoiceStatus) Value() (driver.Value, error) {
	if !v.Valid() {
		return nil, fmt.Errorf("invoice status: refusing to write %q", v.String())
	}
	return string(v), nil
}

// Scan implements sql.Scanner.
//
// A value the application does not know about is an error here, at the row that
// has it, rather than a silent fallback three layers up. That is usually a
// migration that added a value the binary being rolled out does not have yet,
// which is why a migration must stay compatible with the previous binary.
func (v *InvoiceStatus) Scan(src any) error {
	var s string
	switch raw := src.(type) {
	case string:
		s = raw
	case []byte:
		s = string(raw)
	default:
		return fmt.Errorf("invoice status: cannot read %T from the database", src)
	}
	parsed, err := ParseInvoiceStatus(s)
	if err != nil {
		return err
	}
	*v = parsed
	return nil
}

// arandu:begin custom
// Anything the set does not say: transitions (which status may follow which),
// a grouping predicate, a colour for the badge in the view.
// arandu:end custom
