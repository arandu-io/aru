//go:build kyse

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
@if(.RegisterURL != "")
<a href="{{ .RegisterURL }}" class="inline-flex items-center justify-center rounded-md border border-slate-300 px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-100 focus:outline-none focus:ring-2 focus:ring-slate-900/10 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800">Register</a>
@endif
@endif
</div>
</div>
@endsection
