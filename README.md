<p align="center">
  <img src=".github/logo.svg" alt="Arandu" width="180">
</p>

<h1 align="center">arandu-io/aru</h1>

<p align="center">The command line: a project, a full module, and a lint that checks the architecture held.</p>

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

`aru` is the toolchain of a Go framework for web applications, services and
APIs: it generates a full module — Model-backed entity, policy, service,
request, controller, migration, screens — compiling and tested, in one command,
and its `doctor` reads a project's source to check that the architecture it
compiled against is still the one it is running.

## What it delivers

`aru help` lists every command, grouped here by what they do:

| group | commands |
|---|---|
| run the project | `serve` `dev` `build` `new` `doctor` `trace` `generate` `schema` |
| generate a module | `make:module` `make:model` `make:service` `make:resource` `make:notification` `make:client` `make:mcp-tool` `make:mcp-resource` `make:mcp-prompt` `make:migration` `make:controller` `make:middleware` `make:request` `make:factory` `make:seeder` `make:job` `make:mail` `make:command` `make:listener` `make:event` `make:enum` `make:policy` `make:test` |
| migrations | `migrate` `migrate:rollback` `migrate:status` `migrate:fresh` |
| queues | `queue:work` `queue:listen` `queue:restart` `queue:pause` `queue:resume` `queue:clear` `queue:monitor` `queue:failed` `queue:retry` `queue:forget` `queue:flush` `queue:prune-failed` `queue:retry-batch` `queue:prune-batches` |
| fonts | `font:add` `font:search` `font:info` `font:list` `font:remove` |
| native application | `native:run` `native:dev` `native:build` |
| modules, editor, assistant and inspection | `vendor:publish` `skills:sync` `lsp` `mcp` `action:list` `imports:catalog` `about` |
| everything else | `key:generate` `schedule:list` `schedule:run` `route:list` `db:seed` `model:build` `view:build` |

plus `help` and `version`. The native and inspection commands, in the words of `aru help`:

| command | what it does |
|---|---|
| `native:run` | build the native application and open its window |
| `native:dev` | rebuild the native application and reopen it on every change |
| `native:build` | compile the native application for one platform |
| `vendor:publish` | show what the registered modules would publish into the project, and write it |
| `skills:sync` | show how the project's skills differ from the skeleton's and its modules', and bring them in step |
| `lsp` | serve Kyse diagnostics and completion, the project map and its navigation to an editor |
| `mcp` | serve the doctor, the project map, the implementation contract and the generators to an assistant over MCP |
| `action:list` | list the actions the source declares, with the constant and the line of each |
| `imports:catalog` | print the import path each exported framework symbol should be named by, or rewrite the project's imports to it |
| `about` | report what the project is configured and wired with: versions, drivers, modules |

- **`aru make:module`** — a Model-backed entity with its policy, service,
  request, controller, migration and four screens, compiling and tested from
  the moment it lands. It writes no skill: the skeleton's skill families
  describe a module by kind, and a second description per module would be one
  more thing to keep true.
- **`aru generate`** — the same output, from a written specification: the
  model writes the spec, never the Go.
- **`aru doctor`** — 65 named rules read the AST of a project, without
  running it, and fail CI on the first error. Among them:
  `repository-without-policy`, `grant-not-checked`, `sql-without-tenant-scope`,
  `tenant-from-request`, `tenant-from-header`, `sql-built-by-concatenation`,
  `view-does-not-exist`, `session-not-rotated`. Three of them answer only to
  `--profile=performance`, where a join and a transaction across two aggregates
  stop being ordinary SQL and become findings.
- **`aru trace`** — a request reconstructed in the terminal, from the running
  application.
- **`aru font:add`** — vendors a font into the project: the unicode range and
  the metric overrides are read out of the font file itself, not guessed.

The view compiler is part of this binary rather than something it downloads:
one fewer thing to pin, verify and cache.

The direct dependencies are what `go.mod` requires without `// indirect`, and
CI refuses one it does not already allow. This prints them rather than a number
that would age:

```sh
GOWORK=off go list -m -f '{{if not .Indirect}}{{if not .Main}}{{.Path}}{{end}}{{end}}' all | grep .
```

How large it is moves with every commit, so these are the commands rather than
figures that would age without anybody noticing:

```sh
find . -name '*.go' -not -name '*_test.go' -not -path '*/testdata/*' \
	-not -name '*.kyse.go' -exec cat {} + | wc -l                  # lines of production code
find . -name '*_test.go' -not -path '*/testdata/*' -exec cat {} + | wc -l   # lines of test
find . -name '*_test.go' -not -path '*/testdata/*' | wc -l                  # test files
```

The authentication screens are not here — `go run github.com/arandu-io/ui@latest auth`
publishes them into your project, and they are yours to edit from the moment
they land.

## Install

```sh
brew install arandu-io/tap/aru
```

## The rest of Arandu

`arandu` is the skeleton this CLI clones; `arandu-io/framework` is what a
project runs on; `hesape` is the collection of packages the framework is built
from; `examples` is a complete application, generated the same way `aru
make:module` generates one.

## Learning Arandu

The API reference is generated from the doc comments and lives on
[pkg.go.dev](https://pkg.go.dev/github.com/arandu-io/aru). Every exported
symbol carries one, and that is deliberate: it is the documentation that cannot
drift from the code, because it sits in the same file.

The CLI documents itself. `aru help` lists every command, and each one explains
what it writes and what to do with it. `aru doctor` explains what it found and
what breaks, not which rule was violated.

The guide is published at [arandu.io/docs](https://arandu.io/docs), and the
site is itself an Arandu application. Where the guide and a doc comment
disagree, the doc comment sits next to the code and is the one to trust.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Before opening a pull request, the three
commands that file lists have to pass. CI runs them, and then four more: a
dependency budget, the build, a linter and govulncheck.

## Security Vulnerabilities

Please review [our security policy](SECURITY.md) on how to report a
vulnerability. Never open a public issue for one.

## License

Open-sourced software licensed under the [MIT license](LICENSE.md).
