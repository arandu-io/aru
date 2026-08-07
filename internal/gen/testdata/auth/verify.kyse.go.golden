//go:build kyse

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
