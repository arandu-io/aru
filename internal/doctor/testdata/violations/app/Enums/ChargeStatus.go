package enums

// ChargeStatus is the closed set the column may hold.
type ChargeStatus string

// The values, stored exactly as written.
const (
	ChargeStatusDraft    ChargeStatus = "draft"
	ChargeStatusSettled  ChargeStatus = "settled"
	ChargeStatusRefunded ChargeStatus = "refunded"
)

// Valid reports whether v is one of the values.
func (v ChargeStatus) Valid() bool {
	switch v {
	case ChargeStatusDraft, ChargeStatusSettled, ChargeStatusRefunded:
		return true
	}
	return false
}

// String is the stored value.
func (v ChargeStatus) String() string { return string(v) }
