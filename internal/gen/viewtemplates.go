package gen

// The four screens: the listing, one record, the empty form and the filled one.
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
//   - dates and numbers arrive already formatted, as text. A view that formatted
//     a time.Time would need the time package, and the generated file imports a
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

import "github.com/arandu-io/hesape/view"

@go
// <%.ViewData "index"%> is what <%.Controller%>.Index hands this page.
type <%.ViewData "index"%> struct {
	// view.Page is the chrome the layout draws: the title, the description, the
	// CSRF token and the navigation. Embedded rather than repeated, and what
	// makes this struct fit the layout.
	view.Page
	// <%.Plural%> is the page of records.
	<%.Plural%> []<%.RowStruct%>
	// NewURL is where the "new" button goes: the create screen, addressed by
	// route name in the controller rather than written here as a path. A view
	// has no route table, so a link written here could only be a literal -- and
	// a literal keeps rendering after the route moves.
	NewURL string
	// NextURL is the following page of the listing, cursor included. It is
	// empty on the last page, and the link is not rendered then.
	NextURL string
}

// Compile-time proof that this page fits the layout it extends.
var _ view.Layout = <%.ViewData "index"%>{}

// <%.RowStruct%> is one record, formatted for display by the controller.
type <%.RowStruct%> struct {
	// ID is what the row is addressed by.
	ID string
	// URL is where the row links to, built from the route name by the
	// controller.
	URL string
<%range .Fields%>	// <%.GoName%> is the <%.Label%> column.
	<%.GoName%> <%.ViewType%>
<%end%>	// Created is the creation timestamp, already formatted.
	Created string
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

	@if(len(d.<%.Plural%>) == 0)
		<p class="mt-8 text-sm text-slate-500 dark:text-slate-400">
			No <%.Human%> yet. <a class="underline underline-offset-2" href="{{ .NewURL }}">Add the first one</a>.
		</p>
	@endif

	@if(len(d.<%.Plural%>) > 0)
		<div class="mt-8 overflow-x-auto">
			<table class="w-full border-collapse text-left text-sm">
				<thead class="border-b border-slate-200 text-slate-500 dark:border-slate-800 dark:text-slate-400">
					<tr>
						<th class="py-2 pr-4 font-medium"><%.FirstField.Label%></th>
<%range .RestFields%>						<th class="py-2 pr-4 font-medium"><%.Label%></th>
<%end%>						<th class="py-2 font-medium">Created</th>
					</tr>
				</thead>
				<tbody>
					@foreach(.<%.Plural%> as <%.Unexported%>)
						<tr class="border-b border-slate-100 dark:border-slate-900">
							<td class="py-2 pr-4">
								<a class="font-medium underline underline-offset-2" href="{{ <%.Unexported%>.URL }}">{{ <%.Unexported%>.<%.FirstField.GoName%> }}</a>
							</td>
<%$row := .Unexported%>							<%range .RestFields%>					<td class="py-2 pr-4">{{ <%$row%>.<%.GoName%> }}</td>
<%end%>							<td class="py-2 text-slate-500 dark:text-slate-400">{{ <%.Unexported%>.Created }}</td>
						</tr>
					@endforeach
				</tbody>
			</table>
		</div>
	@endif

	@if(d.NextURL != "")
		<a class="mt-6 inline-block text-sm underline underline-offset-2" href="{{ .NextURL }}">Next page</a>
	@endif
@endsection
`

const viewShowTemplate = `//go:build kyse

package <%.ViewsPackage%>

import "github.com/arandu-io/hesape/view"

@go
// <%.ViewData "show"%> is what <%.Controller%>.Show hands this page.
type <%.ViewData "show"%> struct {
	// Page is the state the layout draws. Its Token is also what the delete
	// button sends as a header: an hx-delete carries no form body, so the
	// hidden field a form uses would never arrive and the request would be
	// refused with 419.
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
			<button class="rounded-md border border-red-300 px-3 py-2 text-sm font-medium text-red-700 hover:bg-red-50 dark:border-red-900 dark:text-red-400 dark:hover:bg-red-950" type="button" hx-delete="{{ .DeleteURL }}" hx-headers='{"X-CSRF-Token": "{{ .Token }}"}' hx-confirm="Delete this <%.Human%>?">Delete</button>
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
// <%.ViewData "create"%> is what <%.Controller%>.Create hands this page, and what
// Store hands it back when the submission was rejected: same view, same data,
// with the messages filled in.
type <%.ViewData "create"%> struct {
	// Page is the state the layout draws. Its Token is what @csrf writes into
	// the hidden field, through Page.CSRFToken -- it comes from the page data
	// rather than from a global, because a template that reaches for request
	// state outside the data it was given is how a form ends up carrying
	// another session's token under load.
	view.Page
	// Form is what was typed, so a rejected submission comes back filled in.
	Form <%.FormStruct%>
	// Errors is the message per field, as validation produced it.
	Errors map[string][]string
	// IndexURL is the listing this screen came from, and StoreURL is where the
	// form submits. Both are built from the route names by the controller: a
	// view has no route table, so a path written here could only be a literal --
	// and a literal keeps rendering after the route moves.
	IndexURL string
	StoreURL string
}

// FieldError is the first message for a field, or empty.
//
// A method rather than a lookup in the markup: a view that indexes a map has to
// check the length first, and d.Errors["title"][0] without that check panics
// on the happy path -- which is the request where nothing was wrong.
func (d <%.ViewData "create"%>) FieldError(field string) string {
	if msgs := d.Errors[field]; len(msgs) > 0 {
		return msgs[0]
	}
	return ""
}

// Compile-time proof that this page fits the layout it extends.
var _ view.Layout = <%.ViewData "create"%>{}

// <%.FormStruct%> is the form as text, which is what a form carries.
//
// The value that comes back after a rejection is exactly what was typed,
// including the number that failed to parse -- retyping a whole form because one
// field was wrong is how a screen becomes unpleasant.
type <%.FormStruct%> struct {
	// ID is empty on creation and set on edit, where it addresses the record.
	ID string
<%range .Fields%>	// <%.GoName%> is the <%.Label%> input.
	<%.GoName%> <%.FormType%>
<%end%>}
<%range .BoolFields%>
// <%.GoName%>Attr renders the checked attribute of the <%.Label%> checkbox.
func (f <%$.FormStruct%>) <%.GoName%>Attr() string {
	if f.<%.GoName%> {
		return "checked"
	}
	return ""
}
<%end%>
// arandu:begin custom
// Anything else these forms need in Go goes here, and survives regeneration.
// arandu:end custom
@endgo

@extends('layouts.app')

@section('content')
	<nav class="text-sm text-slate-500 dark:text-slate-400">
		<a class="underline underline-offset-2 hover:text-slate-900 dark:hover:text-slate-100" href="{{ .IndexURL }}"><%.HumansTitle%></a>
	</nav>

	<h1 class="mt-2 text-3xl font-semibold tracking-tight">{{ .Title }}</h1>

	<form class="mt-8 space-y-6" method="post" action="{{ .StoreURL }}" hx-post="{{ .StoreURL }}" hx-target="this" hx-swap="outerHTML">
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
// filled in with a stored record, or with what was typed when Update rejected it.
type <%.ViewData "edit"%> struct {
	// Page is the state the layout draws. Its Token is what @csrf writes into
	// the hidden field.
	view.Page
	// Form is the record as text.
	Form <%.FormStruct%>
	// Errors is the message per field, as validation produced it.
	Errors map[string][]string
	// ShowURL is the record this form edits, and UpdateURL is where it submits.
	// Both are built from the route names by the controller: a view has no route
	// table, so a path written here could only be a literal -- and a literal
	// keeps rendering after the route moves.
	ShowURL   string
	UpdateURL string
}

// FieldError is the first message for a field, or empty.
//
// A method rather than a lookup in the markup: a view that indexes a map has to
// check the length first, and d.Errors["title"][0] without that check panics
// on the happy path -- which is the request where nothing was wrong.
func (d <%.ViewData "edit"%>) FieldError(field string) string {
	if msgs := d.Errors[field]; len(msgs) > 0 {
		return msgs[0]
	}
	return ""
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
	<form class="mt-8 space-y-6" hx-put="{{ .UpdateURL }}" hx-target="this" hx-swap="outerHTML">
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
const viewFieldsTemplate = `<%define "fields"%><%range .Fields%>
		<%if .IsLongText%>{!! components.Textarea(components.TextareaProps{
			Name:  "<%.Column%>",
			Label: "<%.Label%>",
			Value: .Form.<%.GoName%>,
			Page: .,
			Rows:  6,<%if .Required%>
			Required: true,<%end%>
		}) !!}<%else if .IsBool%><label class="flex items-center gap-2 text-sm">
			<input class="input" id="<%.Column%>" name="<%.Column%>" type="checkbox" value="1" {{ .Form.<%.GoName%>Attr() }}>
			<%.Label%>
		</label><%else%>{!! components.Field(components.FieldProps{
			Name:  "<%.Column%>",
			Label: "<%.Label%>",
			Type:  "<%.InputType%>",
			Value: .Form.<%.GoName%>,
			Page: .,<%if .Required%>
			Required: true,<%end%>
		}) !!}<%end%>
<%end%><%end%>`
