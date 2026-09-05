package enums

// ReportState is a text-backed set, and the list beside a field of this type
// can be compared with it.
type ReportState string

// The values, stored exactly as written.
const (
	ReportStateQueued ReportState = "queued"
	ReportStateReady  ReportState = "ready"
)

// String is the stored value.
func (v ReportState) String() string { return string(v) }
