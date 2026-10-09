//go:build kyse

package views

@go
// ReportRowData is one row of the listing, answered on its own to the element
// that asked for it.
type ReportRowData struct {
	Title string
}
@endgo

<li>{{ .Title }}</li>
