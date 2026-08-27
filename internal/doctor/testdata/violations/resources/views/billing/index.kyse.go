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

<!-- Violation, and the expensive kind: a directive that fetches. This should be
     an HTMX fragment -- the moment it loads data of its own, the application has
     two ways to do it, with two loading states and two places to forget the CSRF
     token. -->
<div x-data="{ open: false, invoices: [], async load() { const r = await fetch('/api/invoices'); this.invoices = await r.json() } }">
	<button @click="open = !open">Toggle</button>
</div>

<!-- Violation, and the ordinary kind: open/closed never leaves the page, and
     the directive is still a finding because nothing here evaluates one. The
     screen does nothing and says nothing about why. The behaviours file the
     layout loads owns this, on a data- attribute. -->
<div x-data="{ open: false }">
	<button @click="open = !open">Menu</button>
</div>
