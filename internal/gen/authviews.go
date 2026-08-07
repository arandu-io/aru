package gen

import (
	"path/filepath"
)

// The nine screens of `laravel/ui`, translated to kyse.
//
// They are the ones a Laravel developer already knows by path: the layout, the
// dashboard, the welcome page, sign in, sign up, email verification, and the
// three password screens. Keeping the names and the tree is the whole point --
// somebody who has published Laravel's auth scaffolding opens
// `resources/views/auth/passwords/reset.kyse.go` and finds what they expect.
//
// What changed is everything underneath. Bootstrap became Tailwind utilities,
// `@vite` became the embedded assets the binary already serves, and the helpers
// Blade reaches for -- `config()`, `route()`, `auth()`, `__()` -- became fields
// of one typed struct. A view here cannot reach for request state on its own,
// so a link that drifts is a compile error rather than a dead anchor.
//
// # One struct, declared once
//
// Only the layout carries an `@go` block. A page that extends a layout and
// declares no type of its own renders with the layout's, which is the same
// contract Blade has: the page hands the layout what the layout asks for. So
// `AuthPage` holds everything the nine screens read, and a page that uses three
// of its fields leaves the rest at the zero value.
//
// # What is deliberately absent
//
// No `@auth`, no `@error`, no `@can`, no `<x-component>`: kyse's directive set
// is closed (RULE 15). The guest branch of the navigation bar is
// `@if(!d.Authenticated)`, and a validation message is `@if(.EmailError != "")`
// followed by the field itself. That is more characters and one less language.

// AuthViews returns the nine views, ready to be written into a project.
//
// The sources keep Laravel's tree under `resources/views`; `aru view:build`
// emits the generated Go flat in that directory, because Go has one package per
// directory and a nested layout would otherwise be invisible to the page that
// extends it.
func AuthViews() []File {
	dir := filepath.Join("resources", "views")
	return []File{
		{Path: filepath.Join(dir, "layouts", "app.kyse.go"), Content: []byte(authLayoutViewTemplate)},
		{Path: filepath.Join(dir, "home.kyse.go"), Content: []byte(authHomeViewTemplate)},
		{Path: filepath.Join(dir, "welcome.kyse.go"), Content: []byte(authWelcomeViewTemplate)},
		{Path: filepath.Join(dir, "auth", "login.kyse.go"), Content: []byte(authLoginViewTemplate)},
		{Path: filepath.Join(dir, "auth", "register.kyse.go"), Content: []byte(authRegisterViewTemplate)},
		{Path: filepath.Join(dir, "auth", "verify.kyse.go"), Content: []byte(authVerifyViewTemplate)},
		{Path: filepath.Join(dir, "auth", "passwords", "confirm.kyse.go"), Content: []byte(authPasswordConfirmViewTemplate)},
		{Path: filepath.Join(dir, "auth", "passwords", "email.kyse.go"), Content: []byte(authPasswordEmailViewTemplate)},
		{Path: filepath.Join(dir, "auth", "passwords", "reset.kyse.go"), Content: []byte(authPasswordResetViewTemplate)},
	}
}

// authLayoutViewTemplate is `layouts/app.blade.php`.
//
// It declares AuthPage, which every other screen renders from, and it is the
// only one of the nine with an `@go` block.
//
// Two lines here are load-bearing and easy to delete by accident. The first is
// hx-headers on <body>: without it every hx-post fails the CSRF check, and the
// failure reads like a broken session rather than a missing attribute. The
// second is the asset trio -- the stylesheet and the two scripts come from
// view.URL, which is content-addressed and same-origin, because the CSP is
// script-src 'self' and a CDN would mean loosening it.
const authLayoutViewTemplate = `//go:build kyse

package views

@go
// AuthPage is what every screen of the starter kit renders from.
//
// One struct for the nine views rather than one per page: they share a layout,
// and in kyse a page that extends a layout renders with the layout's type. A
// field a given page does not use stays at its zero value and is never read --
// which is cheaper than nine structs that repeat the same navigation state.
//
// Nothing here is a helper the view reaches for on its own. There is no
// route(), no config() and no auth(): a URL, the application name and the
// signed-in user are fields the handler filled in, so a name that drifts is a
// compile error instead of a blank link.
type AuthPage struct {
	// AppName is the brand in the navigation bar.
	AppName string
	// Title is the document title, already including the application name.
	Title string

	// Token is the CSRF token issued for this session. It reaches the markup
	// twice: as the hidden field @csrf writes, and as the hx-headers attribute
	// on <body> that makes every HTMX request carry it.
	Token string

	// Authenticated decides which half of the navigation bar is drawn.
	Authenticated bool
	// UserName is the signed-in person's display name.
	UserName string

	// HasRegister and HasPasswordReset are Laravel's Route::has checks, moved to
	// the data: an application that did not register those routes hides the
	// links rather than linking to a 404.
	HasRegister      bool
	HasPasswordReset bool

	// The addresses the screens post to and link to. They come from the router,
	// through the handler.
	HomeURL               string
	DashboardURL          string
	LoginURL              string
	LogoutURL             string
	RegisterURL           string
	PasswordRequestURL    string
	PasswordEmailURL      string
	PasswordUpdateURL     string
	PasswordConfirmURL    string
	VerificationResendURL string

	// Status is the one-shot message a redirect left behind, such as the
	// confirmation that a reset link was sent. Empty means nothing to say.
	Status string
	// Resent says a fresh verification link just went out.
	Resent bool

	// Name, Email and Remember are what the person typed on the attempt that was
	// rejected. The password is deliberately absent: it is never sent back.
	Name     string
	Email    string
	Remember bool

	// ResetToken is the one-time token carried by the link in the reset email.
	ResetToken string

	// The validation messages, one field at a time. Empty means the field was
	// accepted -- there is no @error directive to ask a bag on the side.
	NameError                 string
	EmailError                string
	PasswordError             string
	PasswordConfirmationError string
}

// CSRFToken is what @csrf reads to write the hidden field.
//
// It is a method rather than the field itself because the field is also
// interpolated into hx-headers, and one name cannot be both.
func (p AuthPage) CSRFToken() string { return p.Token }

// RememberAttribute is the checked attribute of the remember-me box, or nothing.
//
// A conditional attribute has no directive of its own, and inventing one would
// grow the DSL for a single case. What does not fit a directive is written in
// Go, which is here.
func (p AuthPage) RememberAttribute() string {
	if p.Remember {
		return "checked"
	}
	return ""
}
@endgo

<!doctype html>
<html lang="en" class="h-full">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{ .Title }}</title>
<link rel="stylesheet" href="{{ view.URL("app.css") }}">
<script src="{{ view.URL("htmx.min.js") }}" defer></script>
<script src="{{ view.URL("alpine.min.js") }}" defer></script>
</head>
<body hx-headers='{"X-CSRF-Token": "{{ .Token }}"}' class="h-full bg-slate-50 text-slate-900 antialiased dark:bg-slate-950 dark:text-slate-100">
<div class="flex min-h-full flex-col">
<header class="border-b border-slate-200 bg-white dark:border-slate-800 dark:bg-slate-900">
<nav class="mx-auto flex h-16 max-w-5xl items-center justify-between gap-4 px-6">
<a href="{{ .HomeURL }}" class="text-sm font-semibold tracking-tight text-slate-900 hover:text-slate-600 dark:text-slate-100 dark:hover:text-slate-300">{{ .AppName }}</a>
<div class="flex items-center gap-1 text-sm">
@if(!d.Authenticated)
<a href="{{ .LoginURL }}" class="rounded-md px-3 py-2 font-medium text-slate-600 hover:bg-slate-100 hover:text-slate-900 dark:text-slate-300 dark:hover:bg-slate-800 dark:hover:text-slate-100">Login</a>
@if(.HasRegister)
<a href="{{ .RegisterURL }}" class="rounded-md px-3 py-2 font-medium text-slate-600 hover:bg-slate-100 hover:text-slate-900 dark:text-slate-300 dark:hover:bg-slate-800 dark:hover:text-slate-100">Register</a>
@endif
@endif
@if(.Authenticated)
<span class="px-3 py-2 font-medium text-slate-600 dark:text-slate-300">{{ .UserName }}</span>
<form method="post" action="{{ .LogoutURL }}">
@csrf
<button type="submit" class="rounded-md px-3 py-2 font-medium text-slate-600 hover:bg-slate-100 hover:text-slate-900 dark:text-slate-300 dark:hover:bg-slate-800 dark:hover:text-slate-100">Logout</button>
</form>
@endif
</div>
</nav>
</header>
<main class="mx-auto w-full max-w-5xl grow px-6 py-10">
@yield('content')
</main>
</div>
</body>
</html>
`

// authHomeViewTemplate is `home.blade.php`, the screen you land on after
// signing in.
const authHomeViewTemplate = `//go:build kyse

package views

@extends('layouts.app')

@section('content')
<div class="mx-auto w-full max-w-2xl">
<section class="overflow-hidden rounded-lg border border-slate-200 bg-white shadow-sm dark:border-slate-800 dark:bg-slate-900">
<h1 class="border-b border-slate-200 px-6 py-4 text-base font-semibold tracking-tight dark:border-slate-800">Dashboard</h1>
<div class="px-6 py-6 text-sm text-slate-600 dark:text-slate-300">
@if(.Status != "")
<p role="status" class="mb-4 rounded-md border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-800 dark:border-emerald-900 dark:bg-emerald-950 dark:text-emerald-200">{{ .Status }}</p>
@endif
<p>You are logged in.</p>
</div>
</section>
</div>
@endsection
`

// authWelcomeViewTemplate is `welcome.blade.php`.
//
// Laravel's is a standalone document with its own <html> and an inlined
// stylesheet. This one extends the layout like every other page: a second page
// shell would be a second way to draw a page, and the shell is where the CSRF
// wiring lives.
const authWelcomeViewTemplate = `//go:build kyse

package views

@extends('layouts.app')

@section('content')
<div class="mx-auto flex w-full max-w-2xl flex-col items-start gap-6 py-12">
<h1 class="text-3xl font-semibold tracking-tight text-slate-900 dark:text-slate-100">{{ .AppName }}</h1>
<p class="text-base text-slate-600 dark:text-slate-300">The routing, the controllers and the markup are on the server. There is no API layer in between and no router in the browser, and what travels on an interaction is a fragment of HTML.</p>
<div class="flex flex-wrap items-center gap-3">
@if(.Authenticated)
<a href="{{ .DashboardURL }}" class="inline-flex items-center justify-center rounded-md bg-slate-900 px-4 py-2 text-sm font-medium text-white hover:bg-slate-700 focus:outline-none focus:ring-2 focus:ring-slate-900/20 dark:bg-slate-100 dark:text-slate-900 dark:hover:bg-white">Dashboard</a>
@endif
@if(!d.Authenticated)
<a href="{{ .LoginURL }}" class="inline-flex items-center justify-center rounded-md bg-slate-900 px-4 py-2 text-sm font-medium text-white hover:bg-slate-700 focus:outline-none focus:ring-2 focus:ring-slate-900/20 dark:bg-slate-100 dark:text-slate-900 dark:hover:bg-white">Login</a>
@if(.HasRegister)
<a href="{{ .RegisterURL }}" class="inline-flex items-center justify-center rounded-md border border-slate-300 px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-100 focus:outline-none focus:ring-2 focus:ring-slate-900/10 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800">Register</a>
@endif
@endif
</div>
</div>
@endsection
`

// authLoginViewTemplate is `auth/login.blade.php`.
//
// The remember-me box is the one place a conditional attribute is needed, and
// there is no directive for that. It is a method on AuthPage instead, which is
// what `@go` is for.
const authLoginViewTemplate = `//go:build kyse

package views

@extends('layouts.app')

@section('content')
<div class="mx-auto w-full max-w-md">
<section class="overflow-hidden rounded-lg border border-slate-200 bg-white shadow-sm dark:border-slate-800 dark:bg-slate-900">
<h1 class="border-b border-slate-200 px-6 py-4 text-base font-semibold tracking-tight dark:border-slate-800">Login</h1>
<!-- hx-target and hx-swap on the form itself: a rejected login answers 422 with
     this same fragment, and it has to replace itself. Swapping it in anywhere
     else would leave the old form on the page, with the token that was just
     spent -- and the second attempt fails a CSRF check nobody can see. -->
<form method="post" action="{{ .LoginURL }}" hx-post="{{ .LoginURL }}" hx-target="this" hx-swap="outerHTML" class="space-y-5 px-6 py-6">
@csrf
<div>
<label for="email" class="block text-sm font-medium text-slate-700 dark:text-slate-200">Email address</label>
<input id="email" name="email" type="email" value="{{ .Email }}" required autofocus autocomplete="email" class="mt-1 block w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 shadow-sm outline-none focus:border-slate-900 focus:ring-2 focus:ring-slate-900/10 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100 dark:focus:border-slate-100">
@if(.EmailError != "")
<p role="alert" class="mt-2 text-sm text-red-600 dark:text-red-400">{{ .EmailError }}</p>
@endif
</div>
<div>
<label for="password" class="block text-sm font-medium text-slate-700 dark:text-slate-200">Password</label>
<input id="password" name="password" type="password" required autocomplete="current-password" class="mt-1 block w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 shadow-sm outline-none focus:border-slate-900 focus:ring-2 focus:ring-slate-900/10 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100 dark:focus:border-slate-100">
@if(.PasswordError != "")
<p role="alert" class="mt-2 text-sm text-red-600 dark:text-red-400">{{ .PasswordError }}</p>
@endif
</div>
<div class="flex items-center gap-2">
<input id="remember" name="remember" type="checkbox" value="1" {{ d.RememberAttribute() }} class="size-4 rounded border-slate-300 text-slate-900 focus:ring-2 focus:ring-slate-900/20 dark:border-slate-700 dark:bg-slate-950">
<label for="remember" class="text-sm text-slate-600 dark:text-slate-300">Remember me</label>
</div>
<div class="flex items-center justify-between gap-4 pt-1">
<button type="submit" class="inline-flex items-center justify-center rounded-md bg-slate-900 px-4 py-2 text-sm font-medium text-white hover:bg-slate-700 focus:outline-none focus:ring-2 focus:ring-slate-900/20 dark:bg-slate-100 dark:text-slate-900 dark:hover:bg-white">Login</button>
@if(.HasPasswordReset)
<a href="{{ .PasswordRequestURL }}" class="text-sm font-medium text-slate-600 underline underline-offset-4 hover:text-slate-900 dark:text-slate-300 dark:hover:text-slate-100">Forgot your password?</a>
@endif
</div>
</form>
</section>
</div>
@endsection
`

// authRegisterViewTemplate is `auth/register.blade.php`.
const authRegisterViewTemplate = `//go:build kyse

package views

@extends('layouts.app')

@section('content')
<div class="mx-auto w-full max-w-md">
<section class="overflow-hidden rounded-lg border border-slate-200 bg-white shadow-sm dark:border-slate-800 dark:bg-slate-900">
<h1 class="border-b border-slate-200 px-6 py-4 text-base font-semibold tracking-tight dark:border-slate-800">Register</h1>
<form method="post" action="{{ .RegisterURL }}" class="space-y-5 px-6 py-6">
@csrf
<div>
<label for="name" class="block text-sm font-medium text-slate-700 dark:text-slate-200">Name</label>
<input id="name" name="name" type="text" value="{{ .Name }}" required autofocus autocomplete="name" class="mt-1 block w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 shadow-sm outline-none focus:border-slate-900 focus:ring-2 focus:ring-slate-900/10 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100 dark:focus:border-slate-100">
@if(.NameError != "")
<p role="alert" class="mt-2 text-sm text-red-600 dark:text-red-400">{{ .NameError }}</p>
@endif
</div>
<div>
<label for="email" class="block text-sm font-medium text-slate-700 dark:text-slate-200">Email address</label>
<input id="email" name="email" type="email" value="{{ .Email }}" required autocomplete="email" class="mt-1 block w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 shadow-sm outline-none focus:border-slate-900 focus:ring-2 focus:ring-slate-900/10 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100 dark:focus:border-slate-100">
@if(.EmailError != "")
<p role="alert" class="mt-2 text-sm text-red-600 dark:text-red-400">{{ .EmailError }}</p>
@endif
</div>
<div>
<label for="password" class="block text-sm font-medium text-slate-700 dark:text-slate-200">Password</label>
<input id="password" name="password" type="password" required autocomplete="new-password" class="mt-1 block w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 shadow-sm outline-none focus:border-slate-900 focus:ring-2 focus:ring-slate-900/10 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100 dark:focus:border-slate-100">
@if(.PasswordError != "")
<p role="alert" class="mt-2 text-sm text-red-600 dark:text-red-400">{{ .PasswordError }}</p>
@endif
</div>
<div>
<label for="password-confirm" class="block text-sm font-medium text-slate-700 dark:text-slate-200">Confirm password</label>
<input id="password-confirm" name="password_confirmation" type="password" required autocomplete="new-password" class="mt-1 block w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 shadow-sm outline-none focus:border-slate-900 focus:ring-2 focus:ring-slate-900/10 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100 dark:focus:border-slate-100">
@if(.PasswordConfirmationError != "")
<p role="alert" class="mt-2 text-sm text-red-600 dark:text-red-400">{{ .PasswordConfirmationError }}</p>
@endif
</div>
<div class="flex items-center justify-between gap-4 pt-1">
<button type="submit" class="inline-flex items-center justify-center rounded-md bg-slate-900 px-4 py-2 text-sm font-medium text-white hover:bg-slate-700 focus:outline-none focus:ring-2 focus:ring-slate-900/20 dark:bg-slate-100 dark:text-slate-900 dark:hover:bg-white">Register</button>
<a href="{{ .LoginURL }}" class="text-sm font-medium text-slate-600 underline underline-offset-4 hover:text-slate-900 dark:text-slate-300 dark:hover:text-slate-100">Already registered?</a>
</div>
</form>
</section>
</div>
@endsection
`

// authVerifyViewTemplate is `auth/verify.blade.php`.
const authVerifyViewTemplate = `//go:build kyse

package views

@extends('layouts.app')

@section('content')
<div class="mx-auto w-full max-w-md">
<section class="overflow-hidden rounded-lg border border-slate-200 bg-white shadow-sm dark:border-slate-800 dark:bg-slate-900">
<h1 class="border-b border-slate-200 px-6 py-4 text-base font-semibold tracking-tight dark:border-slate-800">Verify your email address</h1>
<div class="space-y-4 px-6 py-6 text-sm text-slate-600 dark:text-slate-300">
@if(.Resent)
<p role="status" class="rounded-md border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-800 dark:border-emerald-900 dark:bg-emerald-950 dark:text-emerald-200">A fresh verification link has been sent to your email address.</p>
@endif
<p>Before proceeding, please check your email for a verification link.</p>
<form method="post" action="{{ .VerificationResendURL }}">
@csrf
<p>If you did not receive the email, <button type="submit" class="font-medium text-slate-900 underline underline-offset-4 hover:text-slate-600 dark:text-slate-100 dark:hover:text-slate-300">click here to request another</button>.</p>
</form>
</div>
</section>
</div>
@endsection
`

// authPasswordConfirmViewTemplate is `auth/passwords/confirm.blade.php`, the
// re-authentication prompt in front of a sensitive action.
const authPasswordConfirmViewTemplate = `//go:build kyse

package views

@extends('layouts.app')

@section('content')
<div class="mx-auto w-full max-w-md">
<section class="overflow-hidden rounded-lg border border-slate-200 bg-white shadow-sm dark:border-slate-800 dark:bg-slate-900">
<h1 class="border-b border-slate-200 px-6 py-4 text-base font-semibold tracking-tight dark:border-slate-800">Confirm password</h1>
<form method="post" action="{{ .PasswordConfirmURL }}" class="space-y-5 px-6 py-6">
@csrf
<p class="text-sm text-slate-600 dark:text-slate-300">Please confirm your password before continuing.</p>
<div>
<label for="password" class="block text-sm font-medium text-slate-700 dark:text-slate-200">Password</label>
<input id="password" name="password" type="password" required autofocus autocomplete="current-password" class="mt-1 block w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 shadow-sm outline-none focus:border-slate-900 focus:ring-2 focus:ring-slate-900/10 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100 dark:focus:border-slate-100">
@if(.PasswordError != "")
<p role="alert" class="mt-2 text-sm text-red-600 dark:text-red-400">{{ .PasswordError }}</p>
@endif
</div>
<div class="flex items-center justify-between gap-4 pt-1">
<button type="submit" class="inline-flex items-center justify-center rounded-md bg-slate-900 px-4 py-2 text-sm font-medium text-white hover:bg-slate-700 focus:outline-none focus:ring-2 focus:ring-slate-900/20 dark:bg-slate-100 dark:text-slate-900 dark:hover:bg-white">Confirm password</button>
@if(.HasPasswordReset)
<a href="{{ .PasswordRequestURL }}" class="text-sm font-medium text-slate-600 underline underline-offset-4 hover:text-slate-900 dark:text-slate-300 dark:hover:text-slate-100">Forgot your password?</a>
@endif
</div>
</form>
</section>
</div>
@endsection
`

// authPasswordEmailViewTemplate is `auth/passwords/email.blade.php`, where the
// reset link is requested.
const authPasswordEmailViewTemplate = `//go:build kyse

package views

@extends('layouts.app')

@section('content')
<div class="mx-auto w-full max-w-md">
<section class="overflow-hidden rounded-lg border border-slate-200 bg-white shadow-sm dark:border-slate-800 dark:bg-slate-900">
<h1 class="border-b border-slate-200 px-6 py-4 text-base font-semibold tracking-tight dark:border-slate-800">Reset password</h1>
<form method="post" action="{{ .PasswordEmailURL }}" class="space-y-5 px-6 py-6">
@csrf
@if(.Status != "")
<p role="status" class="rounded-md border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-800 dark:border-emerald-900 dark:bg-emerald-950 dark:text-emerald-200">{{ .Status }}</p>
@endif
<div>
<label for="email" class="block text-sm font-medium text-slate-700 dark:text-slate-200">Email address</label>
<input id="email" name="email" type="email" value="{{ .Email }}" required autofocus autocomplete="email" class="mt-1 block w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 shadow-sm outline-none focus:border-slate-900 focus:ring-2 focus:ring-slate-900/10 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100 dark:focus:border-slate-100">
@if(.EmailError != "")
<p role="alert" class="mt-2 text-sm text-red-600 dark:text-red-400">{{ .EmailError }}</p>
@endif
</div>
<div class="pt-1">
<button type="submit" class="inline-flex items-center justify-center rounded-md bg-slate-900 px-4 py-2 text-sm font-medium text-white hover:bg-slate-700 focus:outline-none focus:ring-2 focus:ring-slate-900/20 dark:bg-slate-100 dark:text-slate-900 dark:hover:bg-white">Send password reset link</button>
</div>
</form>
</section>
</div>
@endsection
`

// authPasswordResetViewTemplate is `auth/passwords/reset.blade.php`, reached
// from the link in the email. The one-time token rides in a hidden field.
const authPasswordResetViewTemplate = `//go:build kyse

package views

@extends('layouts.app')

@section('content')
<div class="mx-auto w-full max-w-md">
<section class="overflow-hidden rounded-lg border border-slate-200 bg-white shadow-sm dark:border-slate-800 dark:bg-slate-900">
<h1 class="border-b border-slate-200 px-6 py-4 text-base font-semibold tracking-tight dark:border-slate-800">Reset password</h1>
<form method="post" action="{{ .PasswordUpdateURL }}" class="space-y-5 px-6 py-6">
@csrf
<input type="hidden" name="token" value="{{ .ResetToken }}">
<div>
<label for="email" class="block text-sm font-medium text-slate-700 dark:text-slate-200">Email address</label>
<input id="email" name="email" type="email" value="{{ .Email }}" required autofocus autocomplete="email" class="mt-1 block w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 shadow-sm outline-none focus:border-slate-900 focus:ring-2 focus:ring-slate-900/10 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100 dark:focus:border-slate-100">
@if(.EmailError != "")
<p role="alert" class="mt-2 text-sm text-red-600 dark:text-red-400">{{ .EmailError }}</p>
@endif
</div>
<div>
<label for="password" class="block text-sm font-medium text-slate-700 dark:text-slate-200">Password</label>
<input id="password" name="password" type="password" required autocomplete="new-password" class="mt-1 block w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 shadow-sm outline-none focus:border-slate-900 focus:ring-2 focus:ring-slate-900/10 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100 dark:focus:border-slate-100">
@if(.PasswordError != "")
<p role="alert" class="mt-2 text-sm text-red-600 dark:text-red-400">{{ .PasswordError }}</p>
@endif
</div>
<div>
<label for="password-confirm" class="block text-sm font-medium text-slate-700 dark:text-slate-200">Confirm password</label>
<input id="password-confirm" name="password_confirmation" type="password" required autocomplete="new-password" class="mt-1 block w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 shadow-sm outline-none focus:border-slate-900 focus:ring-2 focus:ring-slate-900/10 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100 dark:focus:border-slate-100">
@if(.PasswordConfirmationError != "")
<p role="alert" class="mt-2 text-sm text-red-600 dark:text-red-400">{{ .PasswordConfirmationError }}</p>
@endif
</div>
<div class="pt-1">
<button type="submit" class="inline-flex items-center justify-center rounded-md bg-slate-900 px-4 py-2 text-sm font-medium text-white hover:bg-slate-700 focus:outline-none focus:ring-2 focus:ring-slate-900/20 dark:bg-slate-100 dark:text-slate-900 dark:hover:bg-white">Reset password</button>
</div>
</form>
</section>
</div>
@endsection
`
