---
name: aru-command
description: Add, rename or change a command of the aru CLI, its flags, or what `aru help` prints. Use when the request is to "add a command", "add a flag to aru", "make aru do X", "rename this subcommand", "wire up make:something", "why does aru say unknown command", or when a new verb has to find a place in the 39-entry dispatch table. Covers the slice in commands.go and why it is not a map, the one signature every command has, why the positional name is taken before the flags are parsed, when a command runs in this binary and when it forwards to the project's own, the wiring a generator prints rather than performs, and the tests that fail on a command added wrong.
license: MIT
---

# Adding a command

The whole CLI surface is one slice in `commands.go`, and everything about a
command is in its entry: the name a person types, the usage line, the one-line
description `aru help` prints, and the function that runs it.

```sh
grep -c '^\t\tname:' commands.go        # 52
```

It is a slice and not a map on purpose. The order of `aru help` is part of the
interface, and a map would shuffle it on every run.

## The two kinds of command, and which one you are writing

Ask one question: **does this need to know which modules the project
registered?**

- **No** — it runs here. `key:generate`, `new`, every `make:*`, `generate`,
  `schema`, `doctor`, `build`, `dev`, `view:build`, `trace` and the five
  `font:*` commands touch files and nothing else.
- **Yes** — it forwards. Modules are wired explicitly in `bootstrap/app.go`,
  with no container and no plugin loading, so a separately compiled binary
  cannot know them. `delegate("serve")` runs `go run . serve` in the project.
  So does anything reading a table the application owns: the fourteen `queue:*`
  commands forward because the failed job list, the batch list and the queue
  itself are the application's, and this binary has no connection to any of
  them.

```sh
grep -c 'run:   delegate(' commands.go  # 23
```

Two of those twenty-three forward under a different word than the one typed:
`queue:work` calls `work` and `route:list` calls `routes`. The name a person
types is the half that needs parity with what they already know; the string
handed to the project binary is an internal protocol that promises nothing, so
changing it would break every project generated before today for no gain.

A forwarded command is proved by running it.
`TestTheQueueCommandsReachTheProject` (`main_internal_test.go`) writes a project
whose binary prints the argv it was handed, runs each queue command through the
CLI and reads what arrived. Asserting that the entry is in the slice would pass
on a command wired to a subcommand nobody dispatches, which is the mistake that
is silent: it appears in `aru help`, it exits, and it does nothing.

## The procedure

**1. Write the function, in the root package.** One signature, no exceptions:

```go
func makeSeeder(args []string, stdout, stderr io.Writer) error
```

Writers rather than `os.Stdout`, because `main_internal_test.go` drives the
whole CLI through `run(args, stdout, stderr)` without a subprocess. A command
that prints to a package-level writer is a command no test can read.

**2. Take the positional name before parsing the flags.**

```go
name, args := takeName(args)
if err := fs.Parse(args); err != nil { ... }
```

`aru make:seeder Invoice --force` is how everybody types it, and the `flag`
package stops parsing at the first non-flag argument. `takeName` in `make.go` is
the one implementation; do not write a second.

**3. Use the flag set, with `ContinueOnError` and stderr.**

```go
fs := flag.NewFlagSet("make:seeder", flag.ContinueOnError)
fs.SetOutput(stderr)
```

`ExitOnError` calls `os.Exit` from inside a library function and takes the test
process with it.

**4. Find the root with the right one of the two.**

- `projectRoot()` walks up for `go.mod`, `main.go` and `arandu.toml`
  **together**. The `arandu.toml` is what tells an Arandu project apart from any
  other Go module — without it, running `aru` inside an unrelated repository
  would walk up and act on it.
- `moduleRoot()` walks up to the nearest `go.mod`. It is what the view commands
  use, because a component library has views and no `main.go`.

**5. Reuse the shared behaviour in `make.go`.** `takeName`, `checkFlatTree`,
`suffixed`, `unsuffixed` and `emit` exist so that the thirteen granular `make:*`
commands read a name, refuse a nested one and report an existing file the same
way. A command that refuses `Admin/UserController` with its own message is a
second way to do one thing inside the generator itself.

```sh
ls make*.go | grep -v _test | wc -l                    # 16: make.go and 15 commands
grep -l 'emit("' *.go | grep -v _test | wc -l          # 13
```

`make:module` and `make:policy` are the two that do not go through `emit`: one
writes a whole module and the other is reached by it. If you are adding a
fifteenth granular command, it goes through the shared path.

**6. Print the wiring; do not perform it.** `wiringSeeder` in `makeseeder.go` is
the shape: the command writes the file and then prints the two lines to paste,
naming the file and the place in it. `bootstrap/app.go` and
`database/seeders/seeders.go` are hand-written code with no custom block, and a
generator that patches them is a generator whose output nobody can explain.

**7. Register it in the slice.** Name, usage, one-line description, `run`. The
description is a phrase in lower case with no full stop, because `aru help`
prints it in a column.

**8. Run the gates.**

## What fails if you get it wrong

These already exist; you do not write them for a new command, you make them
pass.

- **`TestEveryCommandIsImplemented`** (`main_internal_test.go:117`) runs every
  entry in the slice with no arguments and fails if the output contains
  `not implemented`. A command that is a stub is caught the moment it is listed.
- **`TestCommandNamesAreUnique`** (`main_internal_test.go:276`) guards the
  dispatch table itself: `lookup` returns the first match, so a duplicate name
  silently disables the second.
- **`TestUsageIsSober`** (`main_internal_test.go:34`) reads `aru help` and fails
  on `!`, `🚀`, `✨` or `🎉`. The tone rule is checked at the one place users
  read it.
- **`TestUnknownCommandFails`** and **`TestDelegationRequiresAProject`** cover
  the two errors a person actually meets.

## The fall-through, and why an unknown name is not always an error

`run` in `main.go` looks the name up, and on a miss checks whether it is inside
a project. If it is, it delegates: `routes/console.go` is where a project
declares its own commands, and without this the command somebody just generated
with `aru make:command` would answer "unknown command" from the tool that
generated it. Outside a project the miss falls through to the usage text.

So a new command name can shadow a project's own. Check `aru help` before
choosing one.

## Naming

The prefix says the family: `make:` writes one file, `migrate:` moves the
schema, `font:` manages vendored faces, `schedule:` and `queue:` and `route:`
and `db:` are the conventional Laravel spellings kept on purpose. A verb with no
family gets no prefix — `new`, `dev`, `build`, `serve`, `doctor`, `trace`,
`generate`, `schema`.

A name is not retractable. It goes into somebody's `Makefile`, their CI and
their muscle memory on the day they read it.

## The four gates

```sh
export GOWORK=off
gofmt -l $(find . -name '*.go' -not -path '*/testdata/*' -not -name '*.kyse.go')
go build ./...
go vet ./...
go test -race -count=1 ./...
```

Both filters are load-bearing. `testdata/` holds fixtures that are invalid on
purpose — `internal/doctor/testdata/broken/app/Policies/InvoicePolicy.go` has no
closing brace, because a doctor that answers honestly about a file it could not
read is the test. `.kyse.go` is a view source excluded from the compiler by a
build tag, and `gofmt` is the only tool in the chain that ignores a build tag,
so it parses what the compiler skips and fails on the `@` a directive opens
with.

Then check the command by running it, from a binary built out of this tree:

```sh
go build -o /tmp/aru-src . && /tmp/aru-src help; echo $?    # 0
```
