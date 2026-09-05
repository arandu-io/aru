package enums

// ChargeKind is the closed set the column may hold.
type ChargeKind string

// The values, stored exactly as written.
const (
	ChargeKindOneOff    ChargeKind = "one_off"
	ChargeKindRecurring ChargeKind = "recurring"
)

// String is the stored value.
func (v ChargeKind) String() string { return string(v) }
