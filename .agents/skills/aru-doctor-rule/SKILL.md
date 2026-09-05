---
name: aru-doctor-rule
description: Add, widen, weaken or remove a rule of `aru doctor`, the static checker that reads an Arandu application's AST. Use when the request is to "add a doctor rule", "make doctor catch X", "why does doctor not report this", "doctor fires on correct code", "add a lint for the generated app", "change a severity", or when a named rule such as grant-not-received, tenant-from-request, sql-without-tenant-scope or view-data-is-a-map has to change. Covers where a rule goes and where it does not, the finding a rule has to produce, the four fixtures under internal/doctor/testdata, the hand-written emits map that makes a deleted rule visible, and the reason a rule with no planted failure is a rule that cannot fail.
license: MIT
---

# Adding a rule to the doctor

`internal/doctor` is the architecture rules run as static analysis. It reads the
AST and never runs the code, so it works on a project that does not compile —
which is exactly when someone needs to be told what is wrong.

```sh
awk '/^var rules = /,/^}/' internal/doctor/rules.go | grep -cE '^\t[a-z]'      # 28  rule functions
grep -ohE 'Rule: *"[a-z0-9-]+"' internal/doctor/rules.go internal/testlayout/testlayout.go \
	| sort -u | wc -l                                                     # 38  names a report can carry
grep -ohE 'Rule: *"[a-z0-9-]+"' internal/doctor/rules.go | sort -u | wc -l    # 34  of them declared here
grep -ohE 'Rule: *"[a-z0-9-]+"' internal/testlayout/testlayout.go \
	| sort -u | wc -l                                                     # 4  forwarded, declared there
```

The two counts differ because one function can emit several names.
`repositoryMethodNeedsGrant` reports `grant-not-received`, `grant-not-checked`
and `grant-check-discarded`; `testsAreWhereTheyCanRun` hands back the four names
it does not own, because `internal/testlayout` answers the same four questions
for this repository's own tree and a second copy is how the two would come to
disagree in silence.

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

## The four fixtures

They live in `internal/doctor/testdata`, each a whole small project.

| fixture | what it is for |
| --- | --- |
| `violations` | one planted instance of each mistake, written the way the mistake is actually written |
| `clean` | the shape the generator emits. It must produce no error |
| `gaps` | the cases a rule reported wrongly once, and the near misses that must stay quiet |
| `broken` | Go that does not parse, because the doctor has to answer honestly about a file it could not read |

Measured, with a binary built from this tree and an empty `arandu.toml` added so
the CLI accepts the fixture as a project:

```sh
cp -R internal/doctor/testdata/violations /tmp/viol && touch /tmp/viol/arandu.toml
(cd /tmp/viol && /tmp/aru-src doctor > /tmp/viol.out 2>&1); echo $?   # 1
tail -1 /tmp/viol.out                                                 # 28 error(s), 20 warning(s)
grep -oE '^[^ ]+:[0-9]+: \[[a-z-]+\]' /tmp/viol.out | grep -oE '\[[a-z-]+\]' | sort -u | wc -l   # 30
```

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

`internal/doctor/rules_internal_test.go:24`. It runs `Run` over `violations`,
`gaps` and `broken` on the conventional profile and over `clean` on the
performance profile, and reports by function name every rule that produced
nothing.

**2. Write the function.** It takes `*project` and returns `[]Finding`. Add it
to the `rules` slice at `internal/doctor/rules.go:41`, in the order the report
should read.

**3. Fill in every field of the finding, and treat `Why` as the one that
matters.**

This is the shape, from `internal/doctor/rules.go:353`:

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
`TestFindingsAreActionable` (`tests/Unit/doctor/doctor_test.go:81`) enforces
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
`internal/doctor/rules_internal_test.go:112` maps each rule function to the
names it can emit. It is written by hand because one function emits several and
the compiler cannot tell which, and it is checked both ways:

- a rule in the slice with no entry here is reported as silent;
- an entry here with no rule in the slice fails with
  `"… is written down as a rule and is not in the rules slice: it was removed,
  and nothing else noticed"`.

That second direction is the point. Deleting a security rule should look like a
deliberate two-line diff, not like a line that quietly disappeared.

**6. Run the fixture and read the output, not the exit code.** A rule that fires
for the wrong reason on the right fixture passes every test above.

**7. Run the gates.**

## Adding a rule is a breaking change

A rule that rejects code somebody already wrote enters as a `Warning` in a minor
release and becomes an `Error` in the next major. The comment above the slice
says so, and it is the only version policy this package has.

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
- **A build tag is invisible**, so a file the compiler excludes is still read —
  except the views, which `doctor.go:366` skips by name for exactly that reason:
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
