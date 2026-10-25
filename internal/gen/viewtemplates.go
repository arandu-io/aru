package gen

// The five views: the listing, the table the listing draws, one record, the
// empty form and the filled one.
//
// They are written in kyse, under resources/views/<resource>/ -- with the
// directives kyse has and no others. The set is closed: what does not fit a
// directive is written in Go, inside @go.
//
// These templates are rendered with <% %> instead of {{ }}, because {{ }} is
// what a view interpolates with, and a generator sharing the delimiters would
// execute the markup it exists to emit.
//
// Five shapes repeat and are worth naming once:
//
//   - the page data is a struct per screen, never a map, and it declares itself
//     in the view's @go block. A field that does not exist stops the build,
//     which is the whole reason views are compiled rather than interpreted;
//   - every page embeds views.Page, which is what carries the state the layout
//     draws -- the title, the brand, the CSRF token and the navigation -- and
//     what makes the struct satisfy the layout's Layout interface. The `var _
//     Layout` line below each declaration is where a page that stopped fitting
//     stops the build, naming the page;
//   - dates and numbers arrive already formatted, as text, in one row struct the
//     listing, the record and the edit form share. A view that formatted a
//     time.Time would need the time package, and the generated file imports a
//     fixed set -- so formatting is the controller's, which is where a decision
//     about presentation belongs anyway;
//   - every address is a field on the page data, filled by the controller from
//     a route name. None of these templates writes a path. A view is handed no
//     route table, so a link written here could only be a literal -- and a
//     literal href compiles, renders and keeps pointing at the old address
//     after the route moves, with nothing failing until somebody clicks;
//   - everything goes in @section('content'), and nothing in any other section.
//     A section only one layout yields is a section that disappears without a
//     word when the layout is replaced. A back link or a "new" button placed in
//     @section('header') disappears from every screen the moment a layout that
//     does not yield that section takes over.

const viewIndexTemplate = `//go:build kyse

package <%.ViewsPackage%>

import (
	"fmt"

	"github.com/arandu-io/hesape/pagination"
	"github.com/arandu-io/hesape/view"
	"github.com/arandu-io/kyse/components"
)

@go
// <%.ViewData "index"%> is what <%.Controller%>.Index hands this page.
type <%.ViewData "index"%> struct {
	// view.Page is the chrome the layout draws: the title, the description, the
	// CSRF token and the navigation. Embedded rather than repeated, and what
	// makes this struct fit the layout.
	view.Page
	// <%.Plural%> is the page of records.
	<%.Plural%> []<%.RowStruct%>
	// Result is what the service's List returned beside the rows: the listing
	// state that was read, the page with its total, and the count of every facet
	// value. The table is drawn from it and nothing else about the listing is
	// copied out, so the pager, the search box and the sort arrows can never
	// disagree about what is on screen.
	Result *pagination.ListingResult
	// ListURL is the address of the listing itself, which every control of the
	// table is built on. It is addressed by route name in the controller rather
	// than written here as a path.
	ListURL string
	// NewURL is where the "new" button goes: the create screen, addressed by
	// route name in the controller rather than written here as a path. A view
	// has no route table, so a link written here could only be a literal -- and
	// a literal keeps rendering after the route moves.
	NewURL string
}

// Compile-time proof that this page fits the layout it extends.
var _ view.Layout = <%.ViewData "index"%>{}

// <%.TableIDConst%> is the id of the element the table is drawn into. A control
// of the table names it as the htmx target, and <%.Controller%>.Index reads it
// from HX-Target to answer the table alone instead of the whole page.
const <%.TableIDConst%> = "<%.TableID%>"

// <%.RowStruct%> is one record, formatted by the controller: what the listing
// and the record show, and what the edit form starts at.
type <%.RowStruct%> struct {
	// ID is what the row is addressed by.
	ID string
	// URL is where the row links to, built from the route name by the
	// controller.
	URL string
<%range .Fields%>	// <%.GoName%> is the <%.Label%> column.
	<%.GoName%> <%.RowType%>
<%end%>	// Created is the creation timestamp, already formatted.
	Created string
}

// <%.Entity%>IndexTable adapts the page the service listed to the Kyse DataTable.
//
// The state of the listing -- the search, the order, the page and its size, the
// hidden columns -- is the result's, and every address the table draws is built
// from it. What is written here is only the columns and how a row becomes cells.
// A column's Key is the public key the listing declares in the service, for the
// sort and for hiding: a key the listing does not declare changes nothing.
func <%.Entity%>IndexTable(data <%.ViewData "index"%>) components.DataTableProps {
	rows := make([]components.TableRow, 0, len(data.<%.Plural%>))
	for _, <%.Unexported%> := range data.<%.Plural%> {
		rows = append(rows, components.TableRow{
			Key: <%.Unexported%>.ID,
			Cells: []components.TableCell{
				{HTML: components.Link(components.LinkProps{
					Label: fmt.Sprint(<%.Unexported%>.<%.FirstField.GoName%>),
					URL: <%.Unexported%>.URL,
					Variant: "hover",
				})},
<%$row := .Unexported%><%range .RestFields%>				{Text: fmt.Sprint(<%$row%>.<%.GoName%>)},
<%end%>				{Text: <%.Unexported%>.Created},
			},
		})
	}
	return components.DataTableProps{
		ID: <%.TableIDConst%>,
		Label: data.Title,
		URL: data.ListURL,
		Result: data.Result,
		PageData: data.Page,<%if .SearchFields%>
		Searchable: true,<%end%>
		HeaderMenu: true,
		FullPager: true,
		SelectName: "ids",
		Columns: []components.TableColumn{
			{Label: "<%.FirstField.Label%>", Key: "<%.FirstField.Column%>", Sortable: true},
<%range .RestFields%>			{Label: "<%.Label%>", Key: "<%.Column%>", Sortable: true, Hideable: true},
<%end%>			{Label: "Created", Key: "created_at", Sortable: true, Hideable: true},
		},
		Rows: rows,
		Empty: components.EmptyProps{
			Title: "No <%.Human%> yet.",
			Message: "Add the first <%.Human%> to get started.",
			ActionLabel: "New <%.Human%>",
			ActionURL: data.NewURL,
		},
	}
}

// arandu:begin custom
// Anything else these pages need in Go goes here, and survives regeneration.
// arandu:end custom
@endgo

@extends('layouts.app')

@section('content')
	<div class="flex items-center justify-between gap-4">
		<h1 class="text-3xl font-semibold tracking-tight">{{ .Title }}</h1>
		<a class="rounded-md border border-slate-300 px-3 py-2 text-sm font-medium hover:bg-slate-50 dark:border-slate-700 dark:hover:bg-slate-900" href="{{ .NewURL }}">New <%.Human%></a>
	</div>

	{{-- The table is a file of its own, because it is the one part of this page
	     the server also answers alone: a search, a sort, a facet, a page step or
	     a column switch replaces it and nothing else. @include hands this page's
	     data over unchanged. The pager is the table's own; there is no second
	     one beside it. --}}
	<div class="mt-8">
		@include('<%.TablePartial%>')
	</div>
@endsection
`

// viewTableTemplate is the table of the listing, drawn on its own.
//
// It lives in resources/views/partials, because it is a piece of a page and not
// a page: the index draws it with @include, and the controller answers it alone
// when a control of the table asks for it. Both draw from this one template, so
// the fragment can never differ from the table inside the page. Its data is the
// index's own, under another name, so @include hands it over unchanged.
const viewTableTemplate = `//go:build kyse

// Package partials holds the parts of a screen the server answers on their own.
package partials

import (
	"github.com/arandu-io/kyse/components"

	<%.ViewsPackage%> "<%.ViewsImport%>"
)

@go
// <%.Plural%>TableData is what the table of the <%.Humans%> listing draws.
//
// It is the listing page's own data. The page draws this file with @include,
// which hands its data over unchanged, and <%.Controller%>.Index fills the same
// struct when it answers the table alone, so both draw from one template.
type <%.Plural%>TableData = <%.ViewsPackage%>.<%.ViewData "index"%>
@endgo

{!! components.DataTable(<%.ViewsPackage%>.<%.Entity%>IndexTable(.)) !!}
`

const viewShowTemplate = `//go:build kyse

package <%.ViewsPackage%>

import "github.com/arandu-io/hesape/view"

@go
// <%.ViewData "show"%> is what <%.Controller%>.Show hands this page.
type <%.ViewData "show"%> struct {
	// Page is the state the layout draws, the CSRF token included. The delete
	// button sends no token of its own: an hx-delete carries no form body, so
	// the header is what reaches the server, and the hx-headers the layout puts
	// on <body> is inherited by every htmx request on the page.
	view.Page
	// <%.Entity%> is the record.
	<%.Entity%> <%.RowStruct%>
	// IndexURL, EditURL and DeleteURL are the three addresses this screen
	// offers, built from the route names by the controller. A view has no route
	// table, so a path written here could only be a literal -- and a literal
	// keeps rendering after the route moves.
	IndexURL  string
	EditURL   string
	DeleteURL string
}

// Compile-time proof that this page fits the layout it extends.
var _ view.Layout = <%.ViewData "show"%>{}

// arandu:begin custom
// Anything else this page needs in Go goes here, and survives regeneration.
// arandu:end custom
@endgo

@extends('layouts.app')

@section('content')
	<nav class="text-sm text-slate-500 dark:text-slate-400">
		<a class="underline underline-offset-2 hover:text-slate-900 dark:hover:text-slate-100" href="{{ .IndexURL }}"><%.HumansTitle%></a>
	</nav>

	<div class="mt-2 flex items-center justify-between gap-4">
		<h1 class="text-3xl font-semibold tracking-tight">{{ .Title }}</h1>
		<div class="flex items-center gap-3">
			<a class="rounded-md border border-slate-300 px-3 py-2 text-sm font-medium hover:bg-slate-50 dark:border-slate-700 dark:hover:bg-slate-900" href="{{ .EditURL }}">Edit</a>
			<button class="rounded-md border border-red-300 px-3 py-2 text-sm font-medium text-red-700 hover:bg-red-50 dark:border-red-900 dark:text-red-400 dark:hover:bg-red-950" type="button" hx-delete="{{ .DeleteURL }}" hx-confirm="Delete this <%.Human%>?">Delete</button>
		</div>
	</div>

	<dl class="mt-8 divide-y divide-slate-100 border-t border-slate-200 text-sm dark:divide-slate-900 dark:border-slate-800">
<%$e := .Entity%>		<%range .Fields%>		<div class="grid grid-cols-3 gap-4 py-3">
			<dt class="text-slate-500 dark:text-slate-400"><%.Label%></dt>
			<dd class="col-span-2">{{ d.<%$e%>.<%.GoName%> }}</dd>
		</div>
<%end%>		<div class="grid grid-cols-3 gap-4 py-3">
			<dt class="text-slate-500 dark:text-slate-400">Created</dt>
			<dd class="col-span-2">{{ .<%.Entity%>.Created }}</dd>
		</div>
	</dl>
@endsection
`

const viewCreateTemplate = `//go:build kyse

package <%.ViewsPackage%>

import (
	"github.com/arandu-io/kyse/components"

	"github.com/arandu-io/hesape/view"
)

@go
// <%.ViewData "create"%> is what <%.Controller%>.Create hands this page. A
// rejected submission comes back to it too, and the page carries the messages
// and what was typed from the flash: the controller passes nothing for that.
type <%.ViewData "create"%> struct {
	// Page is the state the layout draws, and what every input asks for its
	// message and for what was typed. Its Token is what @csrf writes into the
	// hidden field -- it comes from the page data rather than from a global,
	// because a template that reaches for request state outside the data it was
	// given is how a form ends up carrying another session's token under load.
	view.Page
	// Form is what the inputs start at: empty here, and the stored record on the
	// edit screen, which shares these inputs.
	Form <%.RowStruct%>
	// IndexURL is the listing this screen came from, and StoreURL is where the
	// form submits. Both are built from the route names by the controller: a
	// view has no route table, so a path written here could only be a literal --
	// and a literal keeps rendering after the route moves.
	IndexURL string
	StoreURL string
}

// Compile-time proof that this page fits the layout it extends.
var _ view.Layout = <%.ViewData "create"%>{}

// arandu:begin custom
// Anything else this page needs in Go goes here, and survives regeneration.
// arandu:end custom
@endgo

@extends('layouts.app')

@section('content')
	<nav class="text-sm text-slate-500 dark:text-slate-400">
		<a class="underline underline-offset-2 hover:text-slate-900 dark:hover:text-slate-100" href="{{ .IndexURL }}"><%.HumansTitle%></a>
	</nav>

	<h1 class="mt-2 text-3xl font-semibold tracking-tight">{{ .Title }}</h1>

	<form class="mt-8 space-y-6" method="post" action="{{ .StoreURL }}" hx-post="{{ .StoreURL }}">
		@csrf
		<%template "fields" .%>
		<div class="flex items-center gap-3">
			<button class="rounded-md bg-slate-900 px-3 py-2 text-sm font-medium text-white hover:bg-slate-700 dark:bg-slate-100 dark:text-slate-900 dark:hover:bg-slate-300" type="submit">Save</button>
			<a class="text-sm text-slate-500 underline underline-offset-2 dark:text-slate-400" href="{{ .IndexURL }}">Cancel</a>
		</div>
	</form>
@endsection
`

const viewEditTemplate = `//go:build kyse

package <%.ViewsPackage%>

import (
	"github.com/arandu-io/kyse/components"

	"github.com/arandu-io/hesape/view"
)

@go
// <%.ViewData "edit"%> is what <%.Controller%>.Edit hands this page: the form
// filled in with a stored record. A rejected update comes back with what was
// typed in place of the stored values, from the flash on the page.
type <%.ViewData "edit"%> struct {
	// Page is the state the layout draws, and what every input asks for its
	// message and for what was typed. Its Token is what @csrf writes into the
	// hidden field.
	view.Page
	// Form is the stored record, which is what the inputs start at.
	Form <%.RowStruct%>
	// ShowURL is the record this form edits, and UpdateURL is where it submits.
	// Both are built from the route names by the controller: a view has no route
	// table, so a path written here could only be a literal -- and a literal
	// keeps rendering after the route moves.
	ShowURL   string
	UpdateURL string
}

// Compile-time proof that this page fits the layout it extends.
var _ view.Layout = <%.ViewData "edit"%>{}

// arandu:begin custom
// Anything else this page needs in Go goes here, and survives regeneration.
// arandu:end custom
@endgo

@extends('layouts.app')

@section('content')
	<nav class="text-sm text-slate-500 dark:text-slate-400">
		<a class="underline underline-offset-2 hover:text-slate-900 dark:hover:text-slate-100" href="{{ .ShowURL }}">Back</a>
	</nav>

	<h1 class="mt-2 text-3xl font-semibold tracking-tight">{{ .Title }}</h1>

	<!-- hx-put, and no action: a browser form can only send GET and POST, and
	the update route is PUT. HTMX sends the real method, which is why this
	stack does not need a hidden _method field. -->
	<form class="mt-8 space-y-6" hx-put="{{ .UpdateURL }}">
		@csrf
		<%template "fields" .%>
		<div class="flex items-center gap-3">
			<button class="rounded-md bg-slate-900 px-3 py-2 text-sm font-medium text-white hover:bg-slate-700 dark:bg-slate-100 dark:text-slate-900 dark:hover:bg-slate-300" type="submit">Save</button>
			<a class="text-sm text-slate-500 underline underline-offset-2 dark:text-slate-400" href="{{ .ShowURL }}">Cancel</a>
		</div>
	</form>
@endsection
`

// viewFieldsTemplate is the one piece the two forms share.
//
// It is shared at generation time rather than through @include, because the
// create screen and the edit screen take different data and @include hands the
// partial the page's data unchanged -- so a single partial would assert one type
// and fail on the other.
//
// Each input is handed the page and its starting value, and asks the page for
// the rest: a rejected attempt's message and what was typed come from the flash
// through view.Page, so neither screen carries a form model of its own.
const viewFieldsTemplate = `<%define "fields"%><%range .Fields%>
		<%if .IsLongText%>{!! components.Textarea(components.TextareaProps{
			Name:  "<%.Column%>",
			Label: "<%.Label%>",
			Value: .Form.<%.GoName%>,
			Page: .,
			Rows:  6,<%if .Required%>
			Required: true,<%end%>
		}) !!}<%else if .IsBool%>{!! components.Checkbox(components.CheckboxProps{
			Name:    "<%.Column%>",
			Label:   "<%.Label%>",
			Checked: .Form.<%.GoName%>,
			Page: .,
		}) !!}<%else%>{!! components.Field(components.FieldProps{
			Name:  "<%.Column%>",
			Label: "<%.Label%>",
			Type:  "<%.InputType%>",
			Value: .Form.<%.GoName%>,
			Page: .,<%if .Required%>
			Required: true,<%end%>
		}) !!}<%end%>
<%end%><%end%>`
