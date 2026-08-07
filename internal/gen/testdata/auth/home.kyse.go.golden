//go:build kyse

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
