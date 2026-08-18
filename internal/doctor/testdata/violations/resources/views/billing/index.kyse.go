//go:build kyse

package views

@go
// BillingIndexData is the data of this page.
type BillingIndexData struct {
	Title string
	Note  string
}
@endgo

<h1>{{ .Title }}</h1>

<!-- Violation: the raw form writes a value to the page with no escaping. Note
     is a string, it is rendered as markup, and the day it holds something a
     customer typed the page runs it. The escaped form is three characters
     away. -->
<div class="note">{!! .Note !!}</div>

<!-- Violation: Alpine reaching the server. This should be an HTMX fragment --
     x-data holds client state, and the moment it fetches, the application has
     two ways to load data with two loading states and two places to forget the
     CSRF token. -->
<div x-data="{ open: false, invoices: [], async load() { const r = await fetch('/api/invoices'); this.invoices = await r.json() } }">
	<button @click="open = !open">Toggle</button>
</div>

<!-- This one is fine: open/closed is client-only, ephemeral and invisible to
     the server, which is exactly what doc 14 permits. -->
<div x-data="{ open: false }">
	<button @click="open = !open">Menu</button>
</div>
