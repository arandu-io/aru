---
name: aru-doctor-rule
description: Add, widen, weaken or remove a rule of `aru doctor`, the static checker that reads an Arandu application's AST. Use when the request is to "add a doctor rule", "make doctor catch X", "why does doctor not report this", "doctor fires on correct code", "add a lint for the generated app", "change a severity", or when a named rule such as grant-not-received, tenant-from-request, sql-without-tenant-scope or view-data-is-a-map has to change. Covers where a rule goes and where it does not, the finding a rule has to produce, the five fixtures under internal/doctor/testdata, the hand-written emits map that makes a deleted rule visible, and the reason a rule with no planted failure is a rule that cannot fail.
license: MIT
---

# Adding a rule to the doctor

`internal/doctor` is the architecture rules run as static analysis. It reads the
AST and never runs the code, so it works on a project that does not compile —
which is exactly when someone needs to be told what is wrong.

```sh
awk '/^var rules = /,/^}/' internal/doctor/rules.go | grep -cE '^\t[a-z]'      # 55  rule functions
grep -ohE 'Rule: *"[a-z0-9-]+"' internal/doctor/rules.go internal/doctor/structure.go \
	internal/testlayout/testlayout.go | sort -u | wc -l                   # 65  names a report can carry
grep -ohE 'Rule: *"[a-z0-9-]+"' internal/doctor/rules.go internal/doctor/structure.go \
	| sort -u | wc -l                                                     # 61  of them declared here
grep -ohE 'Rule: *"[a-z0-9-]+"' internal/testlayout/testlayout.go \
	| sort -u | wc -l                                                     # 4  forwarded, declared there
```

The two counts differ because one function can emit several names.
`repositoryMethodNeedsGrant` reports `grant-not-received`, `grant-not-checked`
and `grant-check-discarded`; `testsAreWhereTheyCanRun` hands back the four names
it does not own, because `internal/testlayout` answers the same four questions
for this repository's own tree and a second copy is how the two would come to
disagree in silence.

`aru doctor --list` prints every name a report can carry, one per line, with
the severity it reports at and the profile a rule is limited to. It reads
`internal/doctor/list.go`, which `TestTheListIsWhatTheSourceReports` holds to
the source, and it is what a table of the rules written anywhere else -- the
skeleton's `arandu-doctor` skill has one -- is checked against.

**Every figure above has a command beside it that can reach it, and no figure is
written anywhere twice.** This count has aged eight times, and the last one
failed differently in kind: the number was wrong because the command beside it
measured something narrower than the sentence it stood under. A `grep` over
`rules.go` alone cannot see `package-clause-is-capitalised`, `scaffolding-ships`,
`test-is-not-run` or `test-outside-the-tests-tree` — `testsAreWhereTheyCanRun`
forwards those, and `tests/Unit/doctor/doctor_test.go` proves all four fire in a
generated project. A number with a command beside it that cannot reach the
answer is worse than a number on its own, because it reads as verified.

`TestTheDocumentedRuleCountIsTheOneThisPackageHas`
(`internal/doctor/rules_internal_test.go`) is why none of the four can go stale
again. It derives all of them from the rules slice, from `emitsByRule` and from
both files that declare a name, then reads them back out of this file, of
`AGENTS.md` and of `README.md`. Adding or deleting a rule fails that test in the
same run, and the failure names the document and the figure to change.

The line range is gone from the first command for the same reason.
`sed -n '41,66p'` was right on the day it was written and had no way to stay
right: the `awk` above finds the slice wherever it moves.

## Where the rule goes, and where it does not

The tree decides, not the topic.

- **`internal/doctor/rules.go`** — the check is about the application `aru`
  generates: the tree with `app/`, `routes/` and `resources/views/` in it.
- **`internal/doctor/structure.go`** — the same tree, when the check is about
  where code lives rather than what it lets through: a structural warning (see
  below). The function still goes into the one `rules` slice in `rules.go`.
- **`.golangci.yml`** — the check is about `aru`'s own source. Which also means
  it is a check about Go, because an off-the-shelf linter cannot be taught what
  a `Grant` is.

The two never see the same file: `aru doctor` refuses a directory that is not an
Arandu project, and the linter never sees one.

```sh
go build -o /tmp/aru-src . && /tmp/aru-src doctor; echo $?
# this is not an Arandu project: no go.mod, main.go and arandu.toml together.
# 1
```

Two rules in `rules.go` find SQL assembled by hand, which a general linter also
finds, and they stay there: whoever runs `aru doctor` is not required to run
anything else, and a report that verified authorization and left injection to a
second tool would be half an answer.

## The five fixtures

They live in `internal/doctor/testdata`, each a whole small project.

| fixture | what it is for |
| --- | --- |
| `violations` | one planted instance of each mistake, written the way the mistake is actually written |
| `clean` | the shape the generator emits. It must produce no error |
| `gaps` | the cases a rule reported wrongly once, and the near misses that must stay quiet |
| `broken` | Go that does not parse, because the doctor has to answer honestly about a file it could not read |
| `orm` | a model-first project with no repository anywhere, because every authorization rule was once gated on one and reported nothing here |

`TestEveryRuleFiresOnAFixture` reads the first three and `clean`; `orm` has
its own test, `TestTheModelFirstProjectIsAudited`.

A rule that reads something outside the project's own tree gets that thing
inside the fixture, where a copy of the fixture still finds it. `violations`
and `gaps` each require a module under `github.com/hyz-is/` and replace it with
a directory under `third_party/`, which is where the two skill rules read the
skills that module hands out. The skeleton's half of those rules reads the
module cache and nothing else, so no fixture can hold it: it is proved in
`internal/doctor/skills_internal_test.go`, against a module cache the test
writes.

Measured, with a binary built from this tree and an empty `arandu.toml` added so
the CLI accepts the fixture as a project:

```sh
cp -R internal/doctor/testdata/violations /tmp/viol && touch /tmp/viol/arandu.toml
(cd /tmp/viol && /tmp/aru-src doctor > /tmp/viol.out 2>&1); echo $?   # 1
tail -1 /tmp/viol.out                                                 # 39 error(s), 69 warning(s)
grep -oE '^[^ ]+:[0-9]+: \[[a-z-]+\]' /tmp/viol.out | grep -oE '\[[a-z-]+\]' | sort -u | wc -l   # 58
```

`violations` carries a `vendor/` directory holding a slice of the framework:
the bridge packages it imports, declared the way the framework declares them.
`import-not-canonical` decides from the framework's own source, at the version
go.mod requires, and a vendor directory is the one place that source can sit
inside a fixture and survive the `cp -R` above. Sixteen of the sixty-nine
warnings are that rule. The other fixtures require a framework version no
module cache here holds, so the rule is silent on them -- which is its declared
limit, not an accident.

The rule reads views too. The Go `view:build` writes under
`storage/framework/views` is skipped, because the next build rewrites it, and a
`.kyse.go` source is read as text: its import lines and every `local.Name` below
them. The finding lands on the source's own import line.

```sh
cp -R internal/doctor/testdata/clean /tmp/clean && touch /tmp/clean/arandu.toml
(cd /tmp/clean && /tmp/aru-src doctor); echo $?                       # 1 warning(s), no errors — 0
(cd /tmp/clean && /tmp/aru-src doctor --profile=performance); echo $?  # 3 error(s), 2 warning(s) — 1
```

The clean fixture reporting findings on the performance profile is not a defect.
It holds a join and a cross-aggregate transaction that are correct SQL on the
conventional profile, and that is what the three profile rules exist to report.

## The procedure

**1. Write the fixture first.** Plant the mistake in
`internal/doctor/testdata/violations`, in the file and the directory where a
person would really write it. If the rule must also stay quiet on something that
looks similar, the near miss goes in `gaps`.

A rule that fires on nothing is indistinguishable from a rule somebody deleted,
and the only difference is how long it takes to find out. The suite says so by
command:

```go
// TestEveryRuleFiresOnAFixture walks the rule set and demands that each one
// produce at least one finding across the fixtures.
```

`internal/doctor/rules_internal_test.go:25`. It runs `Run` over `violations`,
`gaps` and `broken` on the conventional profile and over `clean` on the
performance profile, and reports by function name every rule that produced
nothing.

**2. Write the function.** It takes `*project` and returns `[]Finding`. Add it
to the `rules` slice at `internal/doctor/rules.go:46`, in the order the report
should read.

**3. Fill in every field of the finding, and treat `Why` as the one that
matters.**

This is the shape, from `internal/doctor/rules.go:408`:

```go
Finding{
	Rule: "grant-not-checked", Severity: Error,
	File: file, Line: line,
	Message: fn.Name.Name + " receives a Grant and never checks it",
	Why:     "a Grant issued for another action would pass. Start the method with: if err := g.Check(Action...); err != nil { return err }",
}
```

`Message` says what is wrong at that line. `Why` says what a user of the
application would experience. A finding that only says what is forbidden gets
suppressed; one that says what breaks gets fixed.
`TestFindingsAreActionable` (`tests/Unit/doctor/doctor_test.go:91`) enforces
this mechanically: a non-empty `File`, a non-empty `Message`, a `Why` of at
least 40 characters, and a `Message` that does not contain the word
"violation" — because a message that says a rule was violated instead of what
breaks is the message people learn to skip.

**4. Choose the severity honestly.** There are two, `Warning` and `Error`, and
no `Info`: a check that does not change what anyone does is noise that trains
people to ignore the output. `TestSeverityIsMeaningful` fails a fixture that is
all one or all the other.

Warning is right when the reported code compiles and passes today —
`policy-never-opened` on a freshly generated module is correct and expected, and
a project that is red on day zero teaches people to switch the tool off.

**5. Add it to the emits map, in both directions.** `emitsByRule` at
`internal/doctor/rules_internal_test.go:113` maps each rule function to the
names it can emit. It is written by hand because one function emits several and
the compiler cannot tell which, and it is checked both ways:

- a rule in the slice with no entry here is reported as silent;
- an entry here with no rule in the slice fails with
  `"… is written down as a rule and is not in the rules slice: it was removed,
  and nothing else noticed"`.

That second direction is the point. Deleting a security rule should look like a
deliberate two-line diff, not like a line that quietly disappeared.

**5b. Add it to `catalogue` in `internal/doctor/list.go`**, with its severity
and, for a profile rule, its profile. `TestTheListIsWhatTheSourceReports` reads
every `Finding` literal in `rules.go` and `structure.go` and fails on a name or a severity the list
does not carry, in either direction, and on a rule listed out of the order the
slice runs them.

**6. Run the fixture and read the output, not the exit code.** A rule that fires
for the wrong reason on the right fixture passes every test above.

**7. Run the gates.**

## Adding a rule is a breaking change

A rule that rejects code somebody already wrote enters as a `Warning` in a minor
release and becomes an `Error` in the next major. The comment above the slice
says so, and it is the only version policy this package has.

## Structural warnings

Nineteen rules in `internal/doctor/structure.go` report where code lives, not
what it lets through: a second way to do something the application tree
already has an owner for. They are all `Warning` and stay so until the
applications' reports show a rule is never wrong; promotion to `Error` is a
decision per rule. A file name or a line count says where to look and proves
nothing on its own.

Each one says in its comment, in this order: reason, scope, severity, the
positive and the negative case, the known false positive, the limit of the
analysis (function, file or project) and the correction. Keep that shape when
changing one. Every rule has a positive and a negative case in
`tests/Unit/doctor/structure_test.go`, a planted instance in `violations`,
and near misses in `gaps` and `clean`.

| rule | reads | limit |
| --- | --- | --- |
| `input-read-by-hand` | `ctx.Input`, `FormValue`, `PostFormValue`, `ParseForm`, `ParseMultipartForm`, `json.NewDecoder` in a controller | function, by name |
| `validate-called-by-controller` | `x.Validate()` with no argument, `validation.Validate` in a controller | function, by name |
| `json-written-by-hand` | `json.NewEncoder`, and `WriteHeader` beside JSON, in a controller | function |
| `invalid-form-answered-by-hand` | `http.StatusUnprocessableEntity` or `422` in a controller, outside a comparison | function |
| `session-loaded-in-controller` | a `Load` on a receiver named after a session, outside `Controllers/Auth` | function, by receiver name |
| `redirect-to-literal-path` | a `Redirect` whose argument opens with `/` | call |
| `html-template-in-app` | `import "html/template"` under `app/` | file |
| `service-takes-http` | names from `net/http`, `framework/http`, `hesape/http`, `hesape/session`, and `template.HTML`, in `app/Services`; status, method and byte functions excluded | file |
| `service-subpackage` | a directory under `app/Services` holding Go | file tree |
| `service-file-too-large` | a service file past 600 lines | file |
| `controller-too-many-actions` | a controller type with more than 12 handler-shaped methods | package |
| `operation-chosen-by-form-field` | a `switch` on a form field whose branches call two methods of the receiver's fields | function; a call graph would be needed to say more, so it says nothing more |
| `client-outside-clients` | outgoing `net/http` names and `hesape/http/client` under `app/`, outside `app/Clients` | file |
| `model-rule-touches-io` | database, network or `time.Now` in a function of a model's custom block; scopes on `*Query` and `init` excluded | function |
| `fragment-without-partial` | `ctx.Fragment` of a literal view outside `partials.` | call |
| `helper-reimplemented` | functions under `app/` named Slugify, or with BRL, CPF or CNPJ beside a validating or formatting word | declaration, by name |
| `raw-sql-outside-repository` | a body that runs a statement, outside `app/Repositories` and `database/` | body |
| `generated-not-wired` | a `New*` in Controllers or Services that no other non-test file names; a test double (Fake, Stub, Mock, Spy or Dummy in the constructor, its result type or its file) that a `_test.go` file constructs is exempt | project, by name |
| `subject-built-by-hand` | a `Subject` literal whose `Roles` or `Actions` the code chose, outside tests and `database/` | literal |

`aru doctor --list` prints them with the rest, and is what the skeleton's
`arandu-doctor` skill is checked against.

## The CSRF exemption rule

`csrf-exempt-without-signature` is a warning in `rules.go`, and it is the one
rule that reads the route table: `CSRFExcept` literals under `bootstrap/` give
the exempt prefixes, the project map built from the same AST gives every route
that changes state under one (matched the way the framework matches it: the
path itself, or anything below a prefix that ends in a slash), and the action
each reaches is read, alone, for a call of `Verify` through the file's import
of `hesape/webhook`. Its comment carries reason, scope, positive, negative,
known false positive (an action that hands the body to a helper that
verifies), limit (function-local) and fix, and
`tests/Unit/doctor/csrf_test.go` holds eleven cases. `violations` plants the
unverified webhook; `gaps` holds the verified one, a read under the prefix, and
the paths the framework does not exempt.

## Profile rules

Three rules answer only to `--profile=performance`:
`join-across-aggregates`, `transaction-across-aggregates` and
`profile-not-declared`. They live in the same slice as everything else and each
says in its own first lines that what it reports is correct code on the
conventional profile. That is a fact about the rule, not about how the set is
assembled, so it is written where somebody reading the rule will look — a second
slice would hide it.

The profile reaches the rules through `p.profile` rather than selecting them, so
`rules` stays the whole check surface.

## What the doctor cannot see, and what to write instead of pretending

It parses; it does not type-check and it does not resolve constants.

- **SQL held in a package-level constant, or assembled from a variable, is
  invisible.** Both `queriesReachOneAggregate` and `tenantMustScopeTheSQL` say so
  in their own doc comments. A clean report means no unscoped statement was
  *found*.
- **Partition keys are not checked**, because nothing in the code declares one.
- **The framework is read from disk, never fetched.** `import-not-canonical`
  needs the framework's source at the version go.mod requires -- a directory
  replace, `vendor/`, or the module cache -- and the doctor starts no
  toolchain to get it. On a machine that never downloaded that version the rule
  says nothing; `aru imports:catalog` fetches it and prints the same table.
  `skills-out-of-date` and `skills-missing` have the same limit for the
  skills a module or the skeleton hands out, and `aru skills:sync` is what
  downloads them. The skeleton's half also waits for the project to carry one
  skill that names the skeleton as its source: before that, a project that
  never had those skills and one that deleted them look the same.
- **A build tag is invisible**, so a file the compiler excludes is still read —
  except the views, which `doctor.go:443` skips by name for exactly that reason:
  a `.kyse.go` ends in `.go` and is not Go, and parsing one would report every
  view in the project.

If a rule you are asked for needs any of those, say so rather than writing a
regexp that is right on the fixture. The two ways to get a rule wrong both cost
more than having no rule: matching too little leaves the real cases unreported,
and matching too much puts an invented finding in a report that the next person
then skims.

## Never add a suppression

There is no ignore comment and no allow-list, deliberately. A finding somebody
cannot fix is a design question, and the right output is to say so.

## The four gates

```sh
export GOWORK=off
gofmt -l $(find . -name '*.go' -not -path '*/testdata/*' -not -name '*.kyse.go')
go build ./...
go vet ./...
go test -race -count=1 ./...
```

Both filters are load-bearing, and this package is why. `testdata/` holds
fixtures that are wrong on purpose — `broken/app/Policies/InvoicePolicy.go` has
no closing brace and `gofmt` reports `expected '}', found 'func'`; dropping that
filter takes the gate to exit 2. `.kyse.go` is a view source excluded from the
compiler by a build tag, and `gofmt` is the only tool in the chain that ignores
a build tag, so it parses what the compiler skips and fails on the `@` a
directive opens with. Every `.kyse.go` here happens to sit under `testdata/`
today, which is why the second filter alone does not hold the gate — and it stays
because it is the line every repository in the project runs.
