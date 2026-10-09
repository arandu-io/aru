//go:build kyse

package views

@go
// InvoiceRowData is one row, rendered as an HTMX fragment.
type InvoiceRowData struct {
	Invoice models.Invoice
}
@endgo

<li class="py-2" id="invoice-{{ .Invoice.ID }}">
	<span class="font-medium">{{ .Invoice.Reference }}</span>
	<span class="text-slate-500">{{ .Invoice.Total }}</span>
</li>
