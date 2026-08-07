//go:build kyse

package views

@go
// LayoutData is what every page hands the layout.
type LayoutData struct {
	Title string
}
@endgo

<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<title>{{ .Title }}</title>
	<link rel="stylesheet" href="/assets/app.css">
</head>
<body class="bg-white text-slate-900">
	<main class="mx-auto max-w-3xl p-6">
		@yield('content')
	</main>
</body>
</html>
