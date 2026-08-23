---
name: aru-generator
description: Change what aru writes into somebody's project — the templates behind make:module, the granular make:* commands, `aru generate` and `aru new`, or the specification schema they read. Use when the request is to "change the generated code", "add a field type", "add a template", "the scaffold should also write X", "add a flag to make:model", "change the migration output", "update the golden files", or when a change touches internal/gen or internal/spec. Everything here is emitted into a repository somebody keeps, so a template change is a change to every project that regenerates. Covers the two closed sets, the golden corpus and how to update it, the custom block that survives regeneration, and the rules the output must keep satisfying.
license: MIT
---

# Changing what the generator writes

`internal/gen` renders the Go; `internal/spec` is the specification layer that
decides what a valid module is. Between them they produce a tree somebody keeps
in their repository and regenerates later, which is the constraint everything
below follows from.

```sh
GOWORK=off go build -o /tmp/aru-src . && /tmp/aru-src schema | head -20
```

## The two closed sets

Ten column types and five actions. Both are closed, and both are in
`internal/spec/spec.go`:

```sh
sed -n '86,97p' internal/spec/spec.go     # the ten types
sed -n '104p'   internal/spec/spec.go     # var Actions = []string{"view", "list", "create", "update", "delete"}
```

`money` is stored in cents as an integer, and `decimal` is never money — a
fractional binary number is the wrong shape for an amount, and the schema
description says so where a model will read it.

They are closed because an open set is a type system, and a type system is a
language somebody maintains forever. A module that needs a sixth action has a
business rule, and a business rule is written in Go inside the custom block with
its own `Action` constant and its own policy branch.

**Do not widen either set to fit one case.** If you are asked to, the answer is
the custom block, and saying so is the right output.

## The schema is generated, never written

`spec.Schema()` builds the JSON Schema from the same constants `Validate` uses,
so the two cannot disagree. A schema published as a hand-written file drifts on
the second change.

```sh
/tmp/aru-src schema > /tmp/module.schema.json; echo $?     # 0
python3 -c "import json;s=json.load(open('/tmp/module.schema.json'));print(sorted(s['properties']))"
# ['description', 'fields', 'name', 'permissions', 'tenant', 'version']
python3 -c "import json;s=json.load(open('/tmp/module.schema.json'));print(s['properties']['fields']['items']['properties']['type']['enum'])"
# ['bool', 'date', 'decimal', 'email', 'int', 'money', 'string', 'text', 'timestamp', 'uuid']
```

Six top-level properties and `additionalProperties: false`. Adding a property to
the schema without adding it to `Validate`, or the reverse, is the one failure
this arrangement exists to make impossible — so add the constant, not the JSON.

`Validate` reports **everything** wrong with a document at once rather than
stopping at the first problem. A model that got three fields wrong should learn
all three from one response. Keep that shape when you add a check.

## The golden corpus

Every byte the generator emits is pinned.

```sh
ls internal/gen/testdata/stubs  | wc -l      # 20  the granular commands
ls internal/gen/testdata/tenant | wc -l      # 14
ls internal/gen/testdata/global | wc -l      # 14
```

`TestGolden` (`tests/Unit/gen/golden_test.go:55`) renders one fixed
specification twice — tenant-scoped and global — and compares against those
directories. It checks the **count** before the contents:

```go
if len(files) != 13 {
	t.Fatalf("generated %d files, want 13", len(files))
}
```

The number is written down rather than derived, so a file appearing or
disappearing stops the test instead of quietly rewriting the corpus. Adding a
file to `Generate` means changing that number by hand, which is the review the
number exists to force.

`TestGoldenStubs` (`tests/Unit/gen/stubs_test.go:31`) does the same for the
twenty stubs the granular commands emit, and
`TestGeneratedCodeIsDeterministic` runs one specification twice and requires
identical bytes — without it the golden files would be flaky rather than useful.

**Updating them:**

```sh
GOWORK=off go test ./tests/Unit/gen -update
```

Then read the diff. The whole value of the corpus is that a template change
arrives in review as the change it is; regenerating without reading the diff
throws that away.

The fixture spec fixes the date rather than calling `time.Now()`, because a
migration id derived from today makes the goldens test the calendar.

## The one shape of each thing

`make:module` and the granular commands render the **same templates**, not two
copies that happen to match. `GenerateModel` and `Generate` write the same
model; the migration goes through `MigrationSpec` whichever command asked for
it.

`TestTheGranularCommandsAndMakeModuleAgree` (`stubs_test.go:203`) checks four of
those pairings — the session helpers, the validation rules, the migration and
the model. If you add a template that both paths can reach, add it there too.

`GenerateModel` deliberately writes no repository, and there is no
`--repository` flag: a repository pulls a policy with it, `aru doctor` reports
`repository-without-policy` as an error, and the generated policy denies
everything, which pulls a service to issue the Grant. The mandatory path —
validate, Authorize, Grant, Repository — is indivisible by construction, so a
flag offering a subset of it would offer a broken project.

## The custom block

```go
var customBlock = regexp.MustCompile(`(?s)// arandu:begin custom\n(.*?)// arandu:end custom`)
```

`internal/gen/generate.go:236`. `Merge` carries the existing file's blocks into
the newly generated one **by position**, which is the honest limitation:
reordering the generated file would shuffle them. That is why the marker appears
once per file, at the end, where a new block is appended rather than inserted.

Without it the generator is a one-time tool, because nobody regenerates a file
that eats their work. Two things follow for any template you touch:

- put the marker at the end, once;
- do not reorder the blocks in an existing template. Somebody's code is in them.

`Write` skips a file that already exists unless `--force`, and the message
`emit` prints for a skip spells out what `--force` actually does: running
`aru make:controller Invoice --force` after `aru make:module invoice` replaces
seven implemented actions with seven `501`s, and the only thing that survives is
the custom block.

## What the output must keep guaranteeing

The generated tree is the answer to "what does correct code look like here", and
it is also `internal/doctor/testdata/clean`. Break one and you break the other.

- **Every repository method takes `security.Grant` before the id**, and starts
  with `if err := g.Check(Action…); err != nil { return err }`.
- **The tenant comes from `data.Tenant(g)`** — never from a path segment, a
  body, a query or a header.
- **The generated policy denies every action**, with no allow-everything branch
  to delete later.
- **The generated request has no `Authorize`.**
  `TestTheGeneratedRequestHasNoAuthorize` (`stubs_test.go:307`) is the thesis of
  the product in one assertion: there is one path to a yes, and a form request is
  not on it.
- **A generated action answers 501, never 200.**
  `TestTheGeneratedActionsDoNotAnswerSuccess` (`stubs_test.go:289`) — an empty
  action that answered 200 would look like it worked in the browser, in the logs
  and on every dashboard, which is the failure nobody debugs.
- **The generated test file is `<Entity>_test.go`, not `<Entity>Test.go`.** A
  file that does not end in `_test.go` compiles into the package, so the test
  ships in the binary and never runs.
- **Nothing keyed is declared `TEXT`.**
  `TestNothingKeyedIsDeclaredTEXT` (`tests/Unit/gen/audit_test.go:120`) exists
  because every test once ran against SQLite, which accepts `id TEXT PRIMARY
  KEY`; MySQL refuses it, so the first statement of the first migration failed
  in every project and nothing noticed.
- **An altering migration adds nothing `NOT NULL`.**
  `TestAnAlteringMigrationAddsNothingNotNull` (`stubs_test.go:325`) — a `NOT
  NULL` column added to a table with rows fails on every row already there, and
  during a rollout the previous binary does not fill it in.
- **Validation agrees with the column.** A value that passes validation has to
  fit the column it is written to (`audit_test.go:154`).
- **The generated `List` still passes the performance profile.** Every `List`
  carries a keyset cursor written as a subquery over its own table.
  `TestTheGeneratedRepositoryPassesThePerformanceProfile`
  (`tests/Unit/doctor/doctor_test.go:1205`) is what stops a doctor rule and a
  template from drifting apart.

## The generated module carries its own skill

`Generate` writes `.agents/skills/<resource>/SKILL.md`
(`internal/gen/generate.go:72`), rendered from the same specification as the Go.
A description of a module written by hand stops being true at the next field;
this one cannot, because regenerating the module regenerates it.

If you change what a module is made of, change `skillTemplate` in the same
commit. The table of files in that template is a claim about a tree.

## Wiring is printed, never performed

The generator does not edit `bootstrap/app.go`, `routes/web.go` or
`database/seeders/seeders.go`. It prints the lines to paste, naming the file and
the place in it — `wiringSeeder` in `makeseeder.go` is the shape. A generator
that changes the wiring behind you is a generator whose output nobody can
explain, and those files have no custom block, so a patch would edit code
somebody wrote by hand.

## The four gates

```sh
export GOWORK=off
gofmt -l $(find . -name '*.go' -not -path '*/testdata/*' -not -name '*.kyse.go')
go build ./...
go vet ./...
go test -race -count=1 ./...
```

Both filters are load-bearing. `testdata/` is filtered because it holds fixtures
that are invalid on purpose — the doctor's `broken` project has a Go file with
no closing brace, and `gofmt` reports `expected '}', found 'func'` on it; without
the filter the gate exits 2. `.kyse.go` is filtered because a view source is
excluded from the compiler by a build tag and `gofmt` is the only tool in the
chain that ignores a build tag, so it parses what the compiler skips and fails
on the `@` a directive opens with — including the four `.kyse.go` screens this
generator emits per module.

Note that the golden files end in `.golden`, so `gofmt` never reads them. What
proves the generated Go is well-formed is `render` itself: it runs
`format.Source` and reports a failure as a bug in the template, with the source
numbered.
