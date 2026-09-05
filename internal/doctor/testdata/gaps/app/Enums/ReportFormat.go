package enums

// ReportFormat is backed by a number, and the numbers are in the database.
//
// A rule string beside a field of this type lists shown spellings, and the
// column holds 1, 2 and 3. Nothing can compare the two without the switch
// below, so nothing should claim to.
type ReportFormat int

// The values. The numbers are explicit and never iota.
const (
	ReportFormatCSV  ReportFormat = 1
	ReportFormatPDF  ReportFormat = 2
	ReportFormatJSON ReportFormat = 3
)

// String is the name of the value.
func (v ReportFormat) String() string {
	switch v {
	case ReportFormatCSV:
		return "csv"
	case ReportFormatPDF:
		return "pdf"
	case ReportFormatJSON:
		return "json"
	}
	return "ReportFormat"
}
