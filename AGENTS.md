# Working in this repository

`aru` is the Arandu command line. This file is for somebody changing the tool,
not for somebody using it: what a user needs is in `aru help`, and every command
explains itself there.

It is one Go module with one direct third-party dependency, and three things
live inside it that share almost no code:

| | where | what it is |
| --- | --- | --- |
| the commands | the root package `main` | 39 entries in one slice, 10 of which forward to the project's own binary |
| the view compiler | `internal/kyse` | a `.kyse.go` becomes Go, and the Go it writes has to compile |
| the checker | `internal/doctor` | 24 rule functions reading a project's parsed AST, emitting 29 rule names of their own and 4 borrowed from `internal/testlayout` |

```sh
grep -c '^\t\tname:' commands.go                                      # 39
grep -c 'run:   delegate(' commands.go                                # 10
grep -ohE 'Rule: *"[a-z0-9-]+"' internal/doctor/rules.go | sort -u | wc -l   # 29
```

Read `.agents/skills/` before writing code. Each file is a procedure, named by
the situation you are in.

## The four gates

Nothing is finished until all four exit zero.

```sh
export GOWORK=off
gofmt -l $(find . -name '*.go' -not -path '*/testdata/*' -not -name '*.kyse.go')
go build ./...
go vet ./...
go test -race -count=1 ./...
```

Both filters on `gofmt` are load-bearing, and in this repository they are
load-bearing to different degrees. Measure them rather than assuming, because
the answer is not the same here as in the other repositories that run the same
line:

- **`-not -path '*/testdata/*'`** is what the gate depends on today.
  `internal/doctor/testdata/broken/app/Policies/InvoicePolicy.go` has no closing
  brace, on purpose — the doctor has to answer honestly about a file it could
  not read, and that fixture is the test. `gofmt` reports it as
  `expected '}', found 'func'`. A second fixture,
  `internal/doctor/testdata/violations/app/Services/BillingService.go`, ends
  without a newline and is listed as unformatted. Dropping this filter takes the
  gate to exit 2.
- **`-not -name '*.kyse.go'`** is the discipline rather than the current
  necessity. A view source opens with `//go:build kyse` and continues in a
  syntax that is not Go; `gofmt` is the only tool in this chain that ignores a
  build constraint, so it parses the file the compiler skips and fails on the
  `@` a directive begins with. All five `.kyse.go` files in this repository sit
  inside `testdata/`, so the first filter already covers them — which is why the
  measurement below shows this one alone is not enough, and why removing it
  would still be wrong: it is the line every repository in the project runs, and
  the day a view lands outside `testdata/` the gate has to already be right.

```sh
gofmt -l .; echo $?                                                        # 2
gofmt -l $(find . -name '*.go' -not -path '*/testdata/*'); echo $?         # 0
gofmt -l $(find . -name '*.go' -not -name '*.kyse.go'); echo $?            # 2
gofmt -l $(find . -name '*.go' -not -path '*/testdata/*' \
             -not -name '*.kyse.go'); echo $?                              # 0
find . -name '*.kyse.go' -not -path '*/testdata/*'                         # nothing
```

Never judge one of these through a pipe. `gofmt -l … | head` reports `head`'s
exit status, which is 0 whatever `gofmt` said; that is how the third line above
first read as a pass.

## The fifth gate, and why it is not one here

`aru doctor` is a gate in every Arandu application and it is not a gate in this
repository. It refuses a directory that is not an Arandu project, so pointing it
at its own source produces nothing to act on:

```sh
go build -o /tmp/aru-src . && /tmp/aru-src doctor; echo $?
# this is not an Arandu project: no go.mod, main.go and arandu.toml together.
# Run it from inside a project, or create one with `aru new`
# 1
```

Build the binary from source when you need to exercise it. A binary at a fixed
path is a binary somebody installed months ago, and it will answer for code that
is no longer here.

What checks this repository's own source is `.golangci.yml`, with three linters
on — `errcheck`, `gosec`, `staticcheck` — and its header states the line: a
check about `aru`'s own Go goes there, a check about the application `aru`
generates goes in `internal/doctor/rules.go`, and neither ever sees the other's
files.

## The dependency budget

```sh
GOWORK=off go list -deps -f '{{if .Module}}{{if not .Standard}}{{.Module.Path}}{{end}}{{end}}' ./... | sort -u
# github.com/arandu-io/aru
# gopkg.in/yaml.v3
```

One direct dependency, for the specification format, because the standard
library has no YAML parser. `.github/workflows/ci.yml` runs exactly that query
and fails a pull request that adds a second. There is no CLI framework here:
`flag` from the standard library, and a slice of structs for the dispatch table.

The CLI may have dependencies and the framework may not — that separation is the
reason the CLI is a module of its own, and it only holds while this list stays
short.

## What does not exist here

Each of these was considered and refused. Reaching for one is the most common
way to spend an afternoon on something that will be rejected.

| A model reaches for | What is here instead |
| --- | --- |
| a CLI framework with subcommand registration | `commands`, a slice in `commands.go`. The order is the help output, which is why it is not a map |
| a plugin mechanism, dynamically discovered commands | a command not in the slice is forwarded to the project's binary, which knows its own; outside a project it is an error |
| a suppression comment or allow-list for a doctor finding | nothing. A finding you cannot fix is a design question |
| a downloaded binary for the view compiler | `internal/kyse`, compiled into `aru`. One fewer thing to pin, verify and cache |
| a template engine with a runtime | a Go function that writes strings. A missing field is a compile error |
| npm, a bundler, `node_modules` | nothing. CSS is the Tailwind standalone binary, downloaded and verified by `internal/toolchain` |
| a generator that edits `bootstrap/app.go` for you | the wiring is printed for you to paste. A generator that changes wiring behind you is one whose output nobody can explain |
| `time.Now()` inside a generator | a date passed in. A golden file that tests the calendar fails on the first of the month |

## Where a test goes

One question decides it, and it is technical rather than a matter of taste:
**does the test read an identifier its package does not export?**

- If it does, it lives beside the code and is named `<file>_internal_test.go`.
  The suffix turns "this is next to the code" into a claim
  `tests/Unit/structure_test.go` can check by command.
- If it does not, it goes under `tests/Unit/<package>/`, mirroring the tree.
  Flat would be one Go package, and two suites that each want a helper called
  `write` would collide over a name that belongs to neither.

Every test at the root of this repository is internal, because `package main`
has no external form.

```sh
find . -name '*_test.go' -not -path '*/testdata/*' | wc -l   # 33
```

Three things follow from Go and are not negotiable: the file name ends in
`_test.go` (`go test` runs nothing in `BrokerTest.go`, with no error and no
warning), the `package` clause is lowercase whatever the directory is called,
and `-coverpkg=./...` is not optional when running one suite on its own.

Those four checks live in `internal/testlayout` rather than in the test that
runs them, because `aru doctor` asks the same four questions of the application
this repository generates. A copy in either caller is how the two would come to
disagree in silence.

## Comments, and what does not go in one

Comments, identifiers, error messages, log lines, CLI output and test names are
in English. `aru help` is the tone rule at the only place a user reads it, and a
test fails on `!` or an emoji in it.

A doc comment documents its symbol and nothing else: what it does, what it
takes, what it returns, what it guarantees, and the reason a signature is the
shape it is — said in terms of the code. A date, a decision record number, a
rule number, the name of another repository in this project, or the version in
progress do not go in one. `pkg.go.dev` publishes these, and the reader there is
a user of the package rather than an archaeologist of the project.

The exception that is not one: an import path is data. `internal/doctor` names
`github.com/arandu-io/framework/security` and
`github.com/arandu-io/hesape/queue/jobs` because those strings are what the rule
matches, not because they are references.
