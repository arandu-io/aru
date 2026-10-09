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
GOWORK=off go test -count=1 ./... -run 'TestTheSchemaIsValidJSONSchema|TestTheCommittedSchemaIsCurrent'
# PASS: six top-level properties and the ten field types documented below.
```

The properties are `description`, `fields`, `name`, `permissions`, `tenant` and
`version`. The field types are `bool`, `date`, `decimal`, `email`, `int`,
`money`, `string`, `text`, `timestamp` and `uuid`. The root also has
`additionalProperties: false`. Adding a property to
the schema without adding it to `Validate`, or the reverse, is the one failure
this arrangement exists to make impossible — so add the constant, not the JSON.

`Validate` reports **everything** wrong with a document at once rather than
stopping at the first problem. A model that got three fields wrong should learn
all three from one response. Keep that shape when you add a check.

## The golden corpus

Every byte the generator emits is pinned.

```sh
ls internal/gen/testdata/stubs  | wc -l      # 39  the granular commands
ls internal/gen/testdata/tenant | wc -l      # 15
ls internal/gen/testdata/global | wc -l      # 14
ls internal/gen/testdata/nested | wc -l      # 15
```

`TestGolden` (`tests/Unit/gen/golden_test.go`) renders one fixed
specification three times — tenant-scoped, global, and tenant-scoped nested
under `suppliers` — and compares against those directories. It checks the
**count** before the contents, and the count is written per case:

```go
{"tenant", true, 15},
{"global", false, 14},
{"nested", true, 15},
```

A module is twelve files, the factory and the seeder, and -- only when the
table has a tenant column -- the tenant-isolation feature test.

The number is written down rather than derived, so a file appearing or
disappearing stops the test instead of quietly rewriting the corpus. Adding a
file to `Generate` means changing that number by hand, which is the review the
number exists to force.

`TestGoldenStubs` (`tests/Unit/gen/stubs_test.go`) does the same for the
thirty-nine files the granular commands emit, and
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

## The query file is generated twice, from one renderer

`app/Models/<Entity>Query.go` -- the constructor, `<Entity>Query`,
`<Entity>Collection` and the typed `Fresh` and `Replicate` -- is rendered by
`gen.RenderQuery` from a `gen.QuerySpec`. `make:module` and `make:model` emit it
so the module compiles before any build, and `aru model:build` rewrites it from
the struct it finds, on every build. Both go through the same function, and the
spec holds only what the struct and its table say -- entity, constructor, table
variable, soft deletes -- so adding a column changes no generated byte.

`tests/Unit/gen/query_test.go` pins the surface by count (48 that build, 20 that
return rows, 19 that return a value, six more on a soft-deleting table, 14 on
the collection) and refuses a type parameter or a qualified instantiation in the
file. The constructor is `gen.Constructor`: the plural, or `<Entity>Records` when
the plural is the entity itself (`Media`), because a function and a type of one
name in one package do not compile.

## One import path per symbol

A template names each framework symbol by the path `aru imports:catalog` prints
for it, and never by package. The catalog is read from the framework's source:
a symbol the framework only aliases or calls through to is written with the
component's path -- `auth.Grant`, `database.DB`, `log.FromContext`, `*hhttp.Context`
-- and one it declares or envelops keeps the framework's: `fhttp.Router` and the
resource interfaces, `fhttp.Middleware`, the `mail` package. So a controller
imports both `http` packages, under `fhttp` and `hhttp`, beside `net/http`.

```sh
GOWORK=off go build -o /tmp/aru-src . && (cd <a project> && /tmp/aru-src imports:catalog)
GOWORK=off go test -count=1 ./tests/Unit/gen -run TestTheGeneratorNamesEverySymbolByItsCanonicalPath
```

The test reads every golden file against the catalog of the framework version
the compile harness pins, and `TestTheGeneratedModuleCompiles` builds the result
-- every generator's output in one project, from `generatedProject` -- against
the versions a new project requires: the skeleton's framework and hesape, and
`gen.MCPRelease` of the mcp module. It runs `go build`, `go vet` and then the
generated tests themselves, so a generated test that compiles and fails is a
failure here. A canonical path whose symbol the pinned component does not have
yet fails there, and the template keeps the bridge path for that symbol until
it does.

`TestTheDoctorIsSilentOnTheGeneratedProject` (`tests/Unit/gen/doctor_test.go`)
pastes the wiring over the same corpus -- every constructor named in
bootstrap, the migrations and the connector linked from main.go -- runs the
doctor, and fails on any error and on any warning `internal/doctor/structure.go`
declares. A new generator is added to `generatedProject`, and both tests then
hold it.

## One inflector for a field name

`Field.GoName` is the only place a column becomes a Go field, and it keeps an
initialism whole: `user_id` is `UserID`, `api_url` is `APIURL`, from the list
golint reads (`initialisms` in `internal/gen/spec.go`). The model, the request,
the job and the event name a field through it, so a payload and the row it came
from carry one name. `ServiceFields` reads names back off structs, where two
initialisms in a row do not survive snake case, and keeps the name it read.
`tests/Unit/gen/fieldname_test.go` holds the three.

## The one shape of each thing

`make:module` and the granular commands render the **same templates**, not two
copies that happen to match. `GenerateModel` and `Generate` write the same
model; the migration goes through `MigrationSpec` whichever command asked for
it.

`TestTheGranularCommandsAndMakeModuleAgree` (`stubs_test.go`) checks five of
those pairings — the page (`view.New`, and no helper, session or CSRF issuer in
either controller), the request's fields and rules, the migration, the model
and the unit test. If you add a template that both paths can reach, add it there
too.

`make:factory` reads the model and renders the factory template `make:module`
renders. A text field `<Parent>ID` whose `app/Models/<Parent>.go` declares the
type `<Parent>` is taken out of the default state by `gen.SplitParents` and
becomes `For<Parent>(id)`, the state a nested module's factory has, instead of
`f.UUID()` -- a key no row carries. A field naming no model of the project
keeps its UUID. The compile harness runs `make:factory Task` over the nested
module it generated, and `emit` refuses it unless the bytes are the ones
`make:module` wrote.

`make:service` renders the same `serviceStruct`, `serviceCreate` and
`serviceFill` blocks (`serviceBlocks` in `templates.go`) the module's service
is made of, so the Create a person meets in either file is one method, and
`make:model --all` writes the module's service byte for byte
(`TestEverythingWritesTheEntityAndThePathToIt`). `make:model --controller` and
`make:controller --resource` write the controller from one template, through
`Module.ControllerStub`.

`GenerateModel` deliberately writes no repository, and there is no
`--repository` flag: a repository pulls a policy with it, `aru doctor` reports
`repository-without-policy` as an error, and the generated policy denies
everything, which pulls a service to issue the Grant. The mandatory path —
validate, Authorize, Grant, Model — is indivisible by construction, so a
flag offering a subset of it would offer a broken project.

## The generators, one by one

Each writes into the tree the implementation contract names, with a test
beside it, and prints its wiring.

| command | writes | the shape |
| --- | --- | --- |
| `make:module <name> [--parent=<resource>]` | the twelve files, factory, seeder, and for `--tenant` `tests/Feature/<Entity>TenantScope_test.go` | `--parent` nests: the table carries `<parent>_id` in the type of the parent's key (`gen.ParentKeyOf` reads it off the parent's model and the migration that creates its table, and refuses when the parent is not written yet or is keyed by a non-text type), the service takes the parent's service and loads the parent through `Get` before `Create` and `List`, the controller reads the parent on the collection routes and the record on the member routes; the factory names no parent in its default state and gains `For<Parent>(id)`, the seeder loads the tenant's first parent or makes one through the parent's factory, and the tenant test stores a parent before its rows |
| `make:model <Name> [-c] [-a]` | the entity, and the parts asked for | `-c` is the resource controller of `make:controller --resource`; `--all` adds the service and builds the controller with it |
| `make:service <Name>` | `app/Services/<Name>Service.go`, `tests/Unit/<Name>Service_test.go` | one `Create(ctx, actor auth.Subject, in requests.X)`: validate, Authorize, Grant, Model; refuses without the model, policy and request; `fill` copies what the request and the model share by name and type |
| `make:resource <Name>` | `app/Http/Resources/<Name>Resource.go` and test | `ToArray` lists every db column but the tenant; a collection answers each row through it; the test writes through `ctx.JSON` and compares keys exactly |
| `make:notification <Name> [--channels=mail,database]` | `app/Notifications/<Name>.go` and test | on `hesape/notifications`: `Key`, `Via`, `ToMail` built from lines that `messages.Mail` renders itself, `ToDatabase`, an assertion per channel |
| `make:client <Vendor>` | `app/Clients/<Vendor>Client.go`, `<Vendor>Fake.go` and test | typed config that redacts its token, a small interface, the client over `hesape/http/client`, a fake; the test uses the fake and runs the client against a faked factory |
| `make:mcp-tool`, `make:mcp-resource`, `make:mcp-prompt` | `app/Mcp/<Name>.go` and test | a tool calls its service's `Get` with `r.Subject()`, a resource its `List`; the tests drive them through an `mcp.Server` |
| `make:job <Name> [--fields] [--services=<Entity>,...]` | `app/Jobs/<Name>.go` | the payload, `Dispatch<Name>` through `hjobs.New` with the Grant, and the handler: each service named is a field and a parameter of `New<Name>Handler`, and the printed line registers it in `registerHandlers` with the services from `app`, the App `bootstrap.Build` returned; `--services` refuses an entity with no `app/Services/<Entity>Service.go` |
| `make:event <Name> --aggregate=<entity> [--fields]` | `app/Events/<Name>.go` | the payload, its key constant and `Event(aggregateID)`; a field named `event` is refused, because `Event` is the type's method and Go refuses a field and a method of one name. The printed store -- imports, the `outbox` field and its value, and `outbox.Store` inside `database.Transaction` beside the row's `Save` -- is `gen.EventSpec.StoreSnippet`, the same text the event's doc comment carries and the compile harness pastes into a service; the row is `EventSpec.Row`, the aggregate as a Go variable |
| `make:controller <Name> [--resource\|--singleton\|--invokable] [--parent] [--action]` | one controller | `Router.Resource`, `Router.Singleton`, `Router.Invokable` with `fhttp.Invoker`; a parent's and a record's parameters named by `gen.ResourceParameter`, which is hesape's inflector; a named action for `Router.ResourceAction` |

Two things about the printed routes are deliberate. A method is written as the
string the route table already uses -- `"POST"` -- because `routes/web.go`
imports the framework's http package under the name `http`, and it has no
`MethodPost`. And the mcp module is never added by editing `go.mod`:
`make:mcp-*` reads it and, when the module is not required, prints
`go get github.com/arandu-io/mcp@` and `gen.MCPRelease`.

## The custom block

```go
content = Merge(f.Path, existing, f.Content)
```

In `Write`, and in `aru model:build` for the factories it re-renders.
`gen.Merge` is `publish.Merge`, which carries the existing file's blocks into
the newly generated one **by position** -- the honest limitation: reordering the
generated file would shuffle them -- followed, for Go, by settling the import
list on what the merged file uses, taken from the two files' imports. A custom
block may use a package the template never imports, and a merge that kept the
block and took the template's imports would stop compiling on the regeneration
that was supposed to be safe. That is why the marker appears at the end, where a
new block is appended rather than inserted; the entity file carries a second
one inside its `model.TableSpec` literal, and the order of the two is fixed.

Without it the generator is a one-time tool, because nobody regenerates a file
that eats their work. Two things follow for any template you touch:

- put the marker at the end, once;
- do not reorder the blocks in an existing template. Somebody's code is in them.

`Write` skips a file that already exists unless `--force`, and the message
`emit` prints for a skip spells out what `--force` actually does: running
`aru make:controller Invoice --force` after `aru make:module invoice` replaces
seven implemented actions with seven `501`s, and the only thing that survives is
the custom block.

That is why `make:controller <Name> --action=<verb>` on a controller that
already exists writes nothing and exits zero: it prints the method, rendered
from the same `controllerActionTemplate` a new controller carries and with the
receiver the file already uses, for its custom block, and the
`ResourceAction` line for `routes/web.go`. When the file declares the method
already, it prints only the route. `--force` keeps its meaning.
`TestMakeControllerActionOnAnExistingControllerPrintsWhatToPaste` holds the
three answers and that the file is unchanged byte for byte.

## What the output must keep guaranteeing

The generated tree is the answer to "what does correct code look like here", and
it is also `internal/doctor/testdata/clean`. Break one and you break the other.

- **Every service method asks the policy, and every Model read and write takes
  the Grant it issued.** The generator emits no repository: the service takes
  the acting `auth.Subject`, calls `auth.Authorize` before it touches a
  row, and passes the resulting `auth.Grant` to every Model terminal —
  `First`, `Get`, `Value`, `Save`, `Delete`. A loaded row is authorized again
  before it is returned or changed.
- **The tenant comes from `auth.Tenant(g)`** — never from a path segment, a
  body, a query or a header.
- **The generated policy denies every action**, with no allow-everything branch
  to delete later.
- **The generated request has no `Authorize`.**
  `TestTheGeneratedRequestHasNoAuthorize` (`stubs_test.go`) is the thesis of
  the product in one assertion: there is one path to a yes, and a form request is
  not on it.
- **A generated action answers 501, never 200.**
  `TestTheGeneratedActionsDoNotAnswerSuccess` (`stubs_test.go`) — an empty
  action that answered 200 would look like it worked in the browser, in the logs
  and on every dashboard, which is the failure nobody debugs.
- **The generated test file is `<Entity>_test.go`, not `<Entity>Test.go`**, in
  `tests/Unit/` and carrying `package unit_test`. A file that does not end in
  `_test.go` compiles into the package, so the test ships in the binary and
  never runs. `internal/testlayout` is what says so, and `aru doctor` reports
  it in the generated project — a template that emits a test anywhere else is
  the generator fighting the guard it ships. `gen.RenderTest` is the one place
  that file is rendered, for `aru make:module` and `aru make:test` alike.
- **Nothing keyed is declared `TEXT`.**
  `TestNothingKeyedIsDeclaredTEXT` (`tests/Unit/gen/audit_test.go:191`) exists
  because every test once ran against SQLite, which accepts `id TEXT PRIMARY
  KEY`; MySQL refuses it, so the first statement of the first migration failed
  in every project and nothing noticed.
- **An altering migration adds nothing `NOT NULL`.**
  `TestAnAlteringMigrationAddsNothingNotNull` (`stubs_test.go`) — a `NOT
  NULL` column added to a table with rows fails on every row already there, and
  during a rollout the previous binary does not fill it in.
- **Validation agrees with the column.** A value that passes validation has to
  fit the column it is written to (`audit_test.go:219`).
- **The generated `List` pages through the Model.** It is the model's
  `Latest()`, a tiebreak on the key and `SimplePaginate`, with no column taken
  from the request and no SQL written in the service.
  `TestTheListingPagesThroughTheModel` (`tests/Unit/gen/audit_test.go`) is what
  stops the hand-rolled keyset from coming back.
- **A generated controller loads no session, issues no token and maps no
  error.** Who is asking is `ctx.User()`, put there by the sign-in guard the
  printed route sits behind; the input is `ctx.Bind` into the request; the page
  is `view.New(ctx, title)`, which carries the CSRF token the middleware issued;
  an error is returned, and the router answers it with its status — a duplicate
  on a unique column as 409, so the service has no conflict helper. The
  constructor takes the service and nothing else, and the printed wiring calls
  it that way.
- **The key is the Model's.** The entry point chains `UseUniqueIDs()`, which
  fills an empty key on insert, so neither the service nor the factory writes
  an id, and no model sets `KeyType` or `Incrementing` by hand. `aru
  make:factory` says so when it reads a model that does not chain it.
- **A form input asks the page for its message.** `view.Page` has `FieldError`,
  so no page data declares one, and the show screen's delete button sends no
  `hx-headers`: the layout's `<body>` already sends the token on every htmx
  request.

## The generated module carries no skill

`make:module` and `aru generate` write code and nothing under `.agents/`. They
used to write `.agents/skills/<resource>/SKILL.md`, rendered from the
specification, and it repeated what the skeleton's skill families already say
about a model, a policy, a service, a controller and a screen -- by kind, for
every module at once. A second description per module is a second thing to
keep true, and a project with twenty modules carried twenty of them.

`TestMakeModuleWritesNoSkillOfItsOwn` (`make_internal_test.go`) holds it, and
the per-case counts of `TestGolden` would stop a file coming back. A skill a
project wrote about one of its modules is the project's, under its own name:
nothing here writes or rewrites it. One a project kept from an older aru still
records `source: aru@<version>`, and `aru doctor` reports it as
`generated-skill-retired` until it is deleted or its source line goes.

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
