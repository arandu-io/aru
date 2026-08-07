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

	<!-- Alpine within its limit: which tab is open is client state, it dies on
	     reload without loss, and the server never sees it. -->
	<div x-data="{ tab: 0 }" class="mt-4 flex gap-2">
		<button @click="tab = 0" class="rounded px-3 py-1">All</button>
		<button @click="tab = 1" class="rounded px-3 py-1">Overdue</button>
	</div>

	<ul class="mt-4 divide-y">
		@foreach(.Invoices as invoice)
			<li class="py-2">{{ invoice.Reference }}</li>
		@endforeach
	</ul>
@endsection
