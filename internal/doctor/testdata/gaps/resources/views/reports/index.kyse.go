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

<!-- Client-only, and reported all the same: which panel is open dies on reload
     without loss, and the server never sees it -- but nothing this stack serves
     evaluates the directive, so the menu does not open and no error says so.
     This block is the widening, and the one below is where it stops. -->
<div x-data="{ open: false }">
	<button @click="open = !open">Menu</button>
</div>

<!-- The same menu, spelled the way the behaviours file reads it, and silent.
     A rule that fired here would be a rule teaching people to mute it: hx- is
     not the family, data- is not the family, and neither is an attribute that
     merely ends in one of the names. -->
<div data-menu>
	<button data-menu-trigger aria-expanded="false" hx-get="/reports/menu">Menu</button>
	<div data-x-id="not-a-directive" class="combobox-initialized"></div>
</div>
