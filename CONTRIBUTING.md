# Contributing

## Sign your commits

Every commit needs a `Signed-off-by` line:

```
git commit -s -m "what changed and why"
```

That line is the [Developer Certificate of Origin](https://developercertificate.org/):
you are stating that you wrote the change, or that you have the right to submit
it under this project's license. It is not a copyright assignment — you keep
your copyright, and this project can never be relicensed behind your back.

We use DCO rather than a CLA on purpose. A CLA would let the project relicense
later, and the price is that every contributor has to sign a legal document
before their first patch.

## Before you open a pull request

```
gofmt -l $(find . -name '*.go' -not -path '*/testdata/*' -not -name '*.kyse.go')
go vet ./...
go test -race ./...
```

The first line prints nothing when the tree is formatted. The filter is not
optional and it is what CI runs: `gofmt -l .` skips nothing, and two things here
are not valid Go on purpose -- the doctor fixtures under `testdata/`, one of
which does not parse because that is the test, and the `*.kyse.go` sources,
which the compiler excludes through their build tag. `gofmt` is the only tool in
the chain that ignores build tags.

CI runs exactly this, plus a check that no new dependency entered the core: the
framework depends on the standard library and `golang.org/x/crypto`, and nothing
else. A pull request that adds a dependency there needs to argue for it first,
in an issue.

## Where a test goes

One question decides it, and it is technical rather than a matter of taste:
**does the test read an identifier the package does not export?**

If it does, the test cannot live anywhere but beside the code, because Go says
so. Name it `<file>_internal_test.go`. The suffix is not decoration: it turns
"this is next to the code" into a claim `tests/Unit/structure_test.go` can
check, so a test that only uses the exported API and stayed beside the code is
reported rather than tolerated.

If it does not, it goes under `tests/`, in the suite that says what it does and
the directory that says what it is about:

```
tests/
  Unit/<package>/       one thing, with nothing running
  Unit/structure_test.go the guard: it checks this layout by command
  Fuzz/<package>/       arbitrary bytes at a target, with its corpus beside it
  testcase.go           the base the suites share: tests.Root, tests.Fixture
```

`tests/Unit/gen` answers for `internal/gen`. The tree is mirrored rather than
flat because a flat one is a single Go package, and two suites that each want a
helper called `write` collide on a name that has nothing to do with either.

Three things follow from Go and are not negotiable:

- **the file name ends in `_test.go`.** `go test` runs nothing in a file called
  `BrokerTest.go` -- or in `view_Test.go`, which is the one a pattern over names
  gets wrong. Either compiles into the package as ordinary code, and every test
  inside it is skipped with no error, no warning and a green build
- **the `package` clause is lowercase**, whatever the directory above it is
  called. A directory name is a label; an identifier is code
- **`-coverpkg=./...` is not optional** when running a suite on its own. Without
  it, `go test ./tests/...` reports the coverage of the test packages, which is
  near zero, and somebody concludes the wrong thing

The guard is four checks, and each of them is exercised against a tree with the
mistake planted in it -- a guard accepted because it passed is a guard nobody
measured. `testdata/` is out of its scope for the same reason it is out of the
`gofmt` line above: the go command never compiles it, so the Go in there that
does not parse on purpose is not a suite that silently does not run.

The checks themselves live in `internal/testlayout`, because `aru doctor` asks
the same four questions of the application this repository generates. Change a
check there and both subjects move together; a copy of one in either caller is
how the two would come to disagree, and they would disagree in silence.

A `package main` has no external form: it cannot be imported, so its tests are
internal and that is the end of it. Every test at the root of this repository is
one, which is why they all carry the suffix.

`plans/testpackages.go` in the arandu-io working tree checks the same question
from the other side, by intersecting the identifiers a test names with what its
package declares unexported, and the checklist runs it across every repository.

## What the commit message says

What changed and why. The why is the part that is not in the diff, and it is the
part someone will need in two years.

No AI attribution of any kind: no `Co-Authored-By` for an assistant, no
"generated with" footer. Commits are authored by the people who submit them.

## Architecture decisions

The decisions this project has already made live in `arandu-io/docs`, and every
one that closed a door has an ADR. If your change contradicts one, say so in the
pull request and argue for the change of decision — that is a normal thing to
do, and it is better than a patch that quietly works around it.
