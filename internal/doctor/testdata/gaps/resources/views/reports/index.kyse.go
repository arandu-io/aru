//go:build kyse

package views

@go
// ReportsIndexData is the data of this page.
type ReportsIndexData struct {
	Title string
}
@endgo

<h1>{{ .Title }}</h1>

<!-- x-on: with double quotes. The audit matched x-data, x-init and x-effect
     only, and an event handler is where a network call is actually written. -->
<div x-on:click="fetch('/api/totals')">Load</div>

<!-- The shorthand, with single quotes. Same directive, two spellings, and
     neither one was matched. -->
<div @click='fetch("/api/totals")'>Load</div>

<!-- x-data with single quotes: the directive the audit did match, in the quote
     style it did not. -->
<div x-data='{ socket: new WebSocket("/ws") }'>Live</div>

<!-- Client-only, and it stays silent: which panel is open dies on reload
     without loss, and the server never sees it. -->
<div x-data="{ open: false }">
	<button @click="open = !open">Menu</button>
</div>
