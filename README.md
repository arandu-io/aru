<p align="center">
  <img src=".github/logo.png" alt="Arandu" width="140" height="140">
</p>

<h1 align="center">arandu-io/aru</h1>

<p align="center">The Arandu command line.</p>

<p align="center">
<a href="https://github.com/arandu-io/aru/actions/workflows/ci.yml"><img src="https://github.com/arandu-io/aru/actions/workflows/ci.yml/badge.svg" alt="Build Status"></a>
<a href="https://pkg.go.dev/github.com/arandu-io/aru"><img src="https://pkg.go.dev/badge/github.com/arandu-io/aru.svg" alt="Go Reference"></a>
<a href="https://github.com/arandu-io/aru/tags"><img src="https://img.shields.io/github/v/tag/arandu-io/aru?label=version" alt="Latest Version"></a>
<a href="LICENSE.md"><img src="https://img.shields.io/github/license/arandu-io/aru" alt="License"></a>
</p>


## About `aru`

```sh
brew install arandu-io/tap/aru
aru new my-app
cd my-app && aru dev
```

`aru help` lists every command. The ones that carry the most:

- `aru new` — a project, ready to run
- `aru dev` — build the views, run the application, restart on every change
- `aru make:module` — an entity with its policy, repository, service, request, migration, seeder and four screens, compiling and tested
- `aru generate` — the same, from a written specification
- `aru doctor` — twenty-two checks that a project honours the framework's contracts, in CI
- `aru trace` — a request reconstructed in the terminal, from the running application

The view compiler is part of this binary rather than something it downloads: one
fewer thing to pin, verify and cache.

One direct dependency: `gopkg.in/yaml.v3`, for the specification format. CI
refuses the second.

The authentication screens are not here — they are
[arandu-io/ui](https://github.com/arandu-io/ui), published into your project and
yours to edit.

## Learning Arandu

The API reference is generated from the doc comments and lives on
[pkg.go.dev](https://pkg.go.dev/github.com/arandu-io/framework). Every exported
symbol carries one, and that is deliberate: it is the documentation that cannot
drift from the code, because it sits in the same file.

The CLI documents itself. `aru help` lists every command, and each one explains
what it writes and what to do with it. `aru doctor` explains what it found and
what breaks, not which rule was violated.

A guide and a website do not exist yet, and that is a decision rather than a
gap: a guide written against an API that still moves is work done twice, and the
second time is worse — there is wrong documentation published. The site is the
next phase, and it will be an Arandu application.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Before opening a pull request, the three
commands at the top of that file have to pass, and CI runs exactly them.

## Security Vulnerabilities

Please review [our security policy](SECURITY.md) on how to report a
vulnerability. Never open a public issue for one.

## License

Open-sourced software licensed under the [MIT license](LICENSE.md).
