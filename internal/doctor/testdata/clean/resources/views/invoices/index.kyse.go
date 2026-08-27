//go:build kyse

package views

@go
// InvoicesIndexData is the data of this page, and only of this page.
type InvoicesIndexData struct {
	Title    string
	Invoices []models.Invoice
}
@endgo

@extends('layouts.app')

@section('content')
	<h1 class="text-2xl font-semibold">{{ .Title }}</h1>

	<!-- Which tab is open is client state: it dies on reload without loss and
	     the server never sees it, so it is the one thing the browser owns. It
	     is spelled the way the behaviours file reads it -- data- attributes it
	     dispatches on, and the selected tab in the ARIA the markup carries
	     anyway. Nothing here is an expression, which is why it survives
	     script-src 'self'. -->
	<div data-tabs class="mt-4 flex gap-2" role="tablist">
		<button data-tab="all" role="tab" aria-selected="true" class="rounded px-3 py-1">All</button>
		<button data-tab="overdue" role="tab" aria-selected="false" class="rounded px-3 py-1">Overdue</button>
	</div>

	<ul class="mt-4 divide-y">
		@foreach(.Invoices as invoice)
			<!-- The raw form, used the one way it is entitled to be: a component
			     is a function returning template.HTML, and what it interpolated
			     was escaped when it was generated. -->
			<li class="py-2">{{ invoice.Reference }}
				{!! components.Badge(components.BadgeProps{Label: invoice.State}) !!}</li>
		@endforeach
	</ul>
@endsection
