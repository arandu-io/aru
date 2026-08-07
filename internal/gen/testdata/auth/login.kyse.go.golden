//go:build kyse

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
