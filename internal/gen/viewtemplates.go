package gen

// The four screens: the listing, one record, the empty form and the filled one.
//
// They are written in kyse, in Laravel's tree -- resources/views/<resource>/ --
// with the directives kyse has and no others. The set is closed (RULE 15): what
// does not fit a directive is written in Go, inside @go.
//
// These templates are rendered with <% %> instead of {{ }}, because {{ }} is
// what a view interpolates with, and a generator sharing the delimiters would
// execute the markup it exists to emit.
//
// Three shapes repeat and are worth naming once:
//
//   - the page data is a struct per screen, never a map, and it declares itself
//     in the view's @go block. A field that does not exist stops the build,
//     which is the whole reason views are compiled rather than interpreted;
//   - every page satisfies the layout's Layout interface by having PageTitle,
//     and the two forms also carry CSRFToken, which is what @csrf reads;
//   - dates and numbers arrive already formatted, as text. A view that formatted
//     a time.Time would need the time package, and the generated file imports a
//     fixed set -- so formatting is the controller's, which is where a decision
//     about presentation belongs anyway.

const viewIndexTemplate = `//go:build kyse

package views

@go
// <%.ViewData "index"%> is what <%.Controller%>.Index hands this page.
type <%.ViewData "index"%> struct {
	// Title is the browser tab and the heading.
	Title string
	// <%.Plural%> is the page of records.
	<%.Plural%> []<%.RowStruct%>
	// NextCursor is the keyset cursor of the following page. It is empty on the
	// last page, and the link is not rendered then.
	NextCursor string
}

// PageTitle satisfies the layout's contract.
func (d <%.ViewData "index"%>) PageTitle() string { return d.Title }

// <%.RowStruct%> is one record, formatted for display by the controller.
type <%.RowStruct%> struct {
	// ID is what the row links to.
	ID string
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

@section('header')
	<span class="text-sm font-semibold tracking-tight">{{ .Title }}</span>
	<nav class="text-sm text-slate-500 dark:text-slate-400">
		<a class="hover:text-slate-900 dark:hover:text-slate-100" href="<%.Route%>/create">New <%.Human%></a>
	</nav>
@endsection

@section('content')
	<h1 class="text-3xl font-semibold tracking-tight">{{ .Title }}</h1>

	@if(len(d.<%.Plural%>) == 0)
	<p class="mt-8 text-sm text-slate-500 dark:text-slate-400">
		No <%.Human%> yet. <a class="underline underline-offset-2" href="<%.Route%>/create">Add the first one</a>.
	</p>
	@endif

	@if(len(d.<%.Plural%>) > 0)
	<div class="mt-8 overflow-x-auto">
		<table class="w-full border-collapse text-left text-sm">
			<thead class="border-b border-slate-200 text-slate-500 dark:border-slate-800 dark:text-slate-400">
				<tr>
					<th class="py-2 pr-4 font-medium"><%.FirstField.Label%></th>
<%range .RestFields%>					<th class="py-2 pr-4 font-medium"><%.Label%></th>
<%end%>					<th class="py-2 font-medium">Created</th>
				</tr>
			</thead>
			<tbody>
				@foreach(.<%.Plural%> as <%.Unexported%>)
				<tr class="border-b border-slate-100 dark:border-slate-900">
					<td class="py-2 pr-4">
						<a class="font-medium underline underline-offset-2" href="<%.Route%>/{{ <%.Unexported%>.ID }}">{{ <%.Unexported%>.<%.FirstField.GoName%> }}</a>
					</td>
<%$row := .Unexported%><%range .RestFields%>					<td class="py-2 pr-4">{{ <%$row%>.<%.GoName%> }}</td>
<%end%>					<td class="py-2 text-slate-500 dark:text-slate-400">{{ <%.Unexported%>.Created }}</td>
				</tr>
				@endforeach
			</tbody>
		</table>
	</div>
	@endif

	@if(d.NextCursor != "")
	<a class="mt-6 inline-block text-sm underline underline-offset-2" href="<%.Route%>?cursor={{ .NextCursor }}">Next page</a>
	@endif
@endsection
`

const viewShowTemplate = `//go:build kyse

package views

@go
// <%.ViewData "show"%> is what <%.Controller%>.Show hands this page.
type <%.ViewData "show"%> struct {
	// Title is the browser tab and the heading.
	Title string
	// CSRF is the token the delete button sends as a header. An hx-delete
	// carries no form body, so the hidden field a form uses would never arrive
	// and the request would be refused with 419.
	CSRF string
	// <%.Entity%> is the record.
	<%.Entity%> <%.RowStruct%>
}

// PageTitle satisfies the layout's contract.
func (d <%.ViewData "show"%>) PageTitle() string { return d.Title }

// arandu:begin custom
// Anything else this page needs in Go goes here, and survives regeneration.
// arandu:end custom
@endgo

@extends('layouts.app')

@section('header')
	<span class="text-sm font-semibold tracking-tight">{{ .Title }}</span>
	<nav class="text-sm text-slate-500 dark:text-slate-400">
		<a class="hover:text-slate-900 dark:hover:text-slate-100" href="<%.Route%>"><%.HumansTitle%></a>
	</nav>
@endsection

@section('content')
	<div class="flex items-center justify-between gap-4">
		<h1 class="text-3xl font-semibold tracking-tight">{{ .Title }}</h1>
		<div class="flex items-center gap-3">
			<a class="rounded-md border border-slate-300 px-3 py-2 text-sm font-medium hover:bg-slate-50 dark:border-slate-700 dark:hover:bg-slate-900" href="<%.Route%>/{{ .<%.Entity%>.ID }}/edit">Edit</a>
			<button class="rounded-md border border-red-300 px-3 py-2 text-sm font-medium text-red-700 hover:bg-red-50 dark:border-red-900 dark:text-red-400 dark:hover:bg-red-950" type="button" hx-delete="<%.Route%>/{{ .<%.Entity%>.ID }}" hx-headers='{"X-CSRF-Token": "{{ .CSRF }}"}' hx-confirm="Delete this <%.Human%>?">Delete</button>
		</div>
	</div>

	<dl class="mt-8 divide-y divide-slate-100 border-t border-slate-200 text-sm dark:divide-slate-900 dark:border-slate-800">
<%$e := .Entity%><%range .Fields%>		<div class="grid grid-cols-3 gap-4 py-3">
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

package views

@go
// <%.ViewData "create"%> is what <%.Controller%>.Create hands this page, and what
// Store hands it back when the submission was rejected: same view, same data,
// with the messages filled in.
type <%.ViewData "create"%> struct {
	// Title is the browser tab and the heading.
	Title string
	// CSRF is the token the hidden field carries. @csrf reads it through
	// CSRFToken below.
	CSRF string
	// Form is what was typed, so a rejected submission comes back filled in.
	Form <%.FormStruct%>
	// Errors is the message per field, as validation produced it.
	Errors map[string][]string
}

// PageTitle satisfies the layout's contract.
func (d <%.ViewData "create"%>) PageTitle() string { return d.Title }

// CSRFToken is what @csrf reads.
//
// It comes from the page data rather than from a global: a template that reaches
// for request state outside the data it was given is how a form ends up carrying
// another session's token under load.
func (d <%.ViewData "create"%>) CSRFToken() string { return d.CSRF }

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

@section('header')
	<span class="text-sm font-semibold tracking-tight">{{ .Title }}</span>
	<nav class="text-sm text-slate-500 dark:text-slate-400">
		<a class="hover:text-slate-900 dark:hover:text-slate-100" href="<%.Route%>"><%.HumansTitle%></a>
	</nav>
@endsection

@section('content')
	<h1 class="text-3xl font-semibold tracking-tight">{{ .Title }}</h1>

	<form class="mt-8 space-y-6" method="post" action="<%.Route%>" hx-post="<%.Route%>" hx-target="this" hx-swap="outerHTML">
		@csrf
<%template "fields" .%>
		<div class="flex items-center gap-3">
			<button class="rounded-md bg-slate-900 px-3 py-2 text-sm font-medium text-white hover:bg-slate-700 dark:bg-slate-100 dark:text-slate-900 dark:hover:bg-slate-300" type="submit">Save</button>
			<a class="text-sm text-slate-500 underline underline-offset-2 dark:text-slate-400" href="<%.Route%>">Cancel</a>
		</div>
	</form>
@endsection
`

const viewEditTemplate = `//go:build kyse

package views

@go
// <%.ViewData "edit"%> is what <%.Controller%>.Edit hands this page: the form
// filled in with a stored record, or with what was typed when Update rejected it.
type <%.ViewData "edit"%> struct {
	// Title is the browser tab and the heading.
	Title string
	// CSRF is the token the hidden field carries.
	CSRF string
	// Form is the record as text.
	Form <%.FormStruct%>
	// Errors is the message per field, as validation produced it.
	Errors map[string][]string
}

// PageTitle satisfies the layout's contract.
func (d <%.ViewData "edit"%>) PageTitle() string { return d.Title }

// CSRFToken is what @csrf reads.
func (d <%.ViewData "edit"%>) CSRFToken() string { return d.CSRF }

// arandu:begin custom
// Anything else this page needs in Go goes here, and survives regeneration.
// arandu:end custom
@endgo

@extends('layouts.app')

@section('header')
	<span class="text-sm font-semibold tracking-tight">{{ .Title }}</span>
	<nav class="text-sm text-slate-500 dark:text-slate-400">
		<a class="hover:text-slate-900 dark:hover:text-slate-100" href="<%.Route%>/{{ .Form.ID }}">Back</a>
	</nav>
@endsection

@section('content')
	<h1 class="text-3xl font-semibold tracking-tight">{{ .Title }}</h1>

	<!-- hx-put, and no action: a browser form can only send GET and POST, and
	     the update route is PUT. HTMX sends the real method, which is why this
	     stack does not need Laravel's @method spoofing. -->
	<form class="mt-8 space-y-6" hx-put="<%.Route%>/{{ .Form.ID }}" hx-target="this" hx-swap="outerHTML">
		@csrf
<%template "fields" .%>
		<div class="flex items-center gap-3">
			<button class="rounded-md bg-slate-900 px-3 py-2 text-sm font-medium text-white hover:bg-slate-700 dark:bg-slate-100 dark:text-slate-900 dark:hover:bg-slate-300" type="submit">Save</button>
			<a class="text-sm text-slate-500 underline underline-offset-2 dark:text-slate-400" href="<%.Route%>/{{ .Form.ID }}">Cancel</a>
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
		<div class="space-y-1">
			<label class="block text-sm font-medium" for="<%.Column%>"><%.Label%></label>
<%if .IsLongText%>			<textarea class="block w-full rounded-md border border-slate-300 px-3 py-2 text-sm dark:border-slate-700 dark:bg-slate-900" id="<%.Column%>" name="<%.Column%>" rows="4"<%if .Required%> required<%end%>>{{ .Form.<%.GoName%> }}</textarea>
<%else if .IsBool%>			<input class="size-4 rounded border-slate-300 dark:border-slate-700" id="<%.Column%>" name="<%.Column%>" type="checkbox" value="1" {{ .Form.<%.GoName%>Attr() }}>
<%else%>			<input class="block w-full rounded-md border border-slate-300 px-3 py-2 text-sm dark:border-slate-700 dark:bg-slate-900" id="<%.Column%>" name="<%.Column%>" type="<%.InputType%>"<%if eq .InputType "number"%> step="<%.InputStep%>"<%end%> value="{{ .Form.<%.GoName%> }}"<%if .Required%> required<%end%>>
<%end%>			@if(len(d.Errors["<%.Column%>"]) > 0)
			<p class="text-sm text-red-600 dark:text-red-400">{{ d.Errors["<%.Column%>"][0] }}</p>
			@endif
		</div>
<%end%><%end%>`
