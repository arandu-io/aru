---
name: aru-view-compiler
description: Change kyse, the view compiler inside aru — the parser, the code generator, an escape, a directive, or the output path. Use when the request is to "add a directive", "change how views compile", "fix view:build", "the generated Go does not compile", "a loop variable breaks the build", "escaping is wrong in an attribute", "add a template feature", or when a change touches internal/kyse, viewcompile.go or viewbuild.go. The generator writes Go that has to compile, into a file whose header says do not edit, so a mistake here is reported against a line the person did write. Covers the reserved namespace and the four rounds it took to close it, the escape chosen by position, the closed directive set, and the tests that compile the output rather than reading it.
license: MIT
---

# Changing the view compiler

`internal/kyse` turns `resources/views/home.kyse.go` into
`storage/framework/views/home.go`. There is no runtime: the output is a Go
function that writes strings, so a field that does not exist is a compile error
rather than a blank page.

```sh
wc -l internal/kyse/*.go
#  36 expr_internal_test.go   2459 generate.go   390 kyse.go
# 701 parse.go   684 scope.go   208 scope_internal_test.go
```

The one thing to hold on to while changing any of it: **the person reads the
error, and the error names their file.** `Generate` writes `//line` directives
so a type error inside `{{ }}` is reported at the line of the `.kyse.go`. When
that fails, somebody is looking at a compiler message about code they never
wrote, in a file whose header says not to edit it.

## The pipeline

`Generate(f *File, name, dataType, output string) ([]byte, error)`, at
`internal/kyse/generate.go:43`, in order:

1. refuse a view that reads page data and declares no type for it;
2. `validateExpressions`, `yieldInsideASection`, `pageDirectiveInAComponent`;
3. `g.emit()` — the walk that writes the Go;
4. the collected refusals, if any;
5. `format.Source` — a failure here is stated as a bug in the generator, never
   in the view;
6. `resolveLineMarkers` — the two `//line` placeholders become real positions,
   which is why they cannot be written final;
7. **`checkScopes(out, f, output)`** — the output is read back.

Step 7 is the one to understand before changing anything above it.

## The reserved namespace, and the four rounds it took

A view and the file compiled from it share one Go scope. The generator writes
names into it; so does the view, in a loop binding and in an `@go` block. When
the two collide the file still parses and still formats — so nothing inside the
generator notices. What changed is which declaration a name reaches, and nothing
asks that until something type-checks the result. Until then `aru view:build`
prints how many views it compiled and exits 0.

Four rounds of work went into closing that, and each round fixed the previous
one's blind spot.

**Round one: the variables got a prefix.** `hygienePrefix = "kyse__"`
(`generate.go:150`), and seven names under it: `kyse__w`, `kyse__data`,
`kyse__d`, `kyse__ok`, `kyse__err`, `kyse__sections`, `kyse__props`, plus
counter-suffixed temporaries so no two live at once. What found it:
`@foreach(.Steps as s)` renamed the temporary a URL escape writes into, and the
view was reported for `s.URL undefined (type string has no field or method
URL)`. The worst part was the reserved letter — nothing told the person which
letters were spoken for.

**Round two: the imports got the same prefix.** A package name is an ordinary
identifier in the file that imports it, so the fix that covered the variables
left the packages open. `@foreach(.Steps as view)` renamed the package every
escape is called through, and the view was reported for `view.TextURL undefined
(type Step has no field or method TextURL)`. Six imports moved:
`kyse__template`, `kyse__io`, `kyse__errors`, `kyse__fmt`, `kyse__strings`,
`kyse__view`.

**Round three: the plain names came back, for the view that asks.** Renaming
every import withdrew a name the view was already using — `view.URL("app.css")`
in a link, `view.Page` embedded in the struct a page draws — so a layout that had
asked for its stylesheet since the day it was generated stopped building, with
`undefined: view` against the line the person wrote. `republishedPackages`
(`generate.go:261`) gives a package its plain name back **only when the view's
own source calls it**. The table first held `view` alone, on the reasoning that
no view names the other five from its markup — and markup was the wrong place to
look: an `@go` block is Go the person writes, and a component declaring
`Icon template.HTML` there failed as `undefined: template`. All six are in the
table now, and the rule is the rename rather than the list.

```sh
sed -n '261,268p' internal/kyse/generate.go | grep -cE '^\t"'    # 6
```

**Round four: stop trusting the list.** `checkScopes` (`internal/kyse/scope.go`)
consults no list of names. It parses the bytes about to be written and checks
the discipline on them:

- every import the file declares answers either to a reserved name or to one the
  view wrote in its own import block;
- every local name the generated code introduces is reserved or is a word that
  appears in the view's own source;
- nothing the view writes reaches into the reserved namespace;
- every reserved name the code reads is one something declared.

So a package the generator brings in tomorrow and forgets to rename is refused
here rather than shadowed later. It is deliberately **not** a type check:
resolving what the output imports needs a warm module cache, costs seconds per
view, fails on an aeroplane and reports somebody else's compile error as a
failure of `aru view:build`.

**If you add a name to the generated Go, put it under the prefix.** If you
cannot — because the language chose the name — it belongs in `goUniverse`
instead.

```sh
sed -n '1918,1970p' internal/kyse/generate.go | grep -cE '^\t"'   # 44
```

`goUniverse` (`generate.go:1918`) is the universe block of the language
specification, not the subset the output happens to write today: `nil` guards
every write, `len` counts the subject of a `@forelse`, `string` types a
temporary. A binding that spells one shadows it for the whole loop body. Listing
the language's whole universe means a name added to the generated code tomorrow
is already covered; guarding them one at a time as somebody trips over them is a
list that is always one failure behind.

`loopBinding` (`generate.go:2005`) is the other end, and it refuses five things,
each with the message a person can act on: a name from `goUniverse`, the name of
the function this view compiles to, anything starting with `kyse__`, a package
the view itself calls outside the loop, and anything that is not a Go identifier
at all. Every other name is free — a single letter, a common word, whichever it
is. That freedom is the product of the prefix, and it is what a change here must
not spend.

## The escape is chosen by position, and five positions refuse

A view writes `{{ }}` and says nothing about escaping. `echo`
(`generate.go:1004`) reads the position out of the markup around it:

| position | what is emitted |
| --- | --- |
| body of an element | `template.HTMLEscapeString(view.Text(...))` |
| a quoted attribute value | `view.TextAttr(...)` |
| the front of a URL attribute | `view.TextURL(...)`, refusable at render time |
| inside a `script` element | `view.TextJS(...)` |
| a `style` attribute or a `style` element | `view.TextCSS(...)`, refusable at render time |

And five positions produce no escape at all, so the value is refused instead —
four at build time and one at render time:

- **an unquoted attribute value** — it ends at the first space, and no escape
  puts a space back inside it;
- **an attribute whose value is code** — `on*`, `hx-on*`, `x-on:`, `@`,
  `x-bind:`, `:`, and the ten closed `x-` directives. A character reference in
  an attribute is decoded before the script is read, so an escaped quote arrives
  as the quote it started as;
- **where an element name goes** — an element name carries no character
  references;
- **a position the markup left unknown** — a tag opened in one branch and closed
  in another, or a `{!! !!}`, `@include` or `@yield` written inside a tag;
- **where an attribute name goes** — refused at render time, because a page
  writes a name there today and only the value decides.

`attributeHoldsURL` is an exact list of the attributes that hold **one** URL.
`srcset` is deliberately absent: it holds several with a descriptor after each,
and checking the first would advertise a protection the rest do not have.
`attributeHoldsCode` groups by prefix where the family is open — an event exists
for every event name — and writes out the closed `x-` family.

If a change adds a position, it adds a case to `echo` and either an escape or a
refusal. There is no default that "probably escapes enough".

## The directive set is closed

```sh
sed -n '212,220p' internal/kyse/kyse.go | grep -c ':'      # 7 block directives
sed -n '223,233p' internal/kyse/kyse.go | grep -c 'true'   # 9 inline directives
```

`Directives()` returns all 23 names — each block directive plus its `end`, plus
the inline ones. A directive set that grows on demand becomes a language, and a
language has to be maintained forever. What does not fit is written in Go inside
`@go`.

Declaring one and forgetting its case in the generator has happened three times,
and each time the node was dropped in silence: `@else` made both halves of an
`if` appear at once, `@for` and `@while` emitted the loop with an empty body. The
build stayed green and the page was missing exactly what the author wrote.
`TestEveryDirectiveEmitsSomething` (`tests/Unit/kyse/kyse_test.go:412`) walks the
set and demands each one put something in the output. A closed set is only a
promise if something walks it.

## Where the output goes

`OutputPath` (`kyse.go:280`) mirrors the source tree under storage:

```
resources/views/auth/login.kyse.go  ->  storage/framework/views/auth/login.go
```

It is build output and gitignored. A view compiled beside its source would put
files nobody wrote into every `git status`, and a reviewer would skip them in
every review. The one fallback: a module with no `resources/views` — a component
library keeps its views at its own root — compiles in place, because there the
output is what `go get` serves.

`compileViews` (`viewcompile.go:51`) carries four decisions worth knowing before
changing it: layouts are compiled first so a page that declares no `@go` block
inherits the layout's type; every view is compiled even after one fails, so a
person fixing views sees all of them; a build that changes nothing writes
nothing, because rewriting every file with a fresh mtime defeats Tailwind's scan
cache and made the dev loop see its own output as a change; and `pruneCompiled`
deletes generated Go whose source is gone, because a generated file
self-registers and one left behind keeps a deleted view renderable and panics on
a duplicate registration when the name is reclaimed.

`findViews` matches `.kyse.go` and nothing else. A cleanup that globs `*.go` in
that directory deletes the sources, and that is not hypothetical.

## How to prove a change here

Reading the generated Go and checking its shape proves less than it looks,
because the failure this compiler is about is a **type** error and only a type
checker sees one.

- **`TestALoopBindingCannotTakeTheCompilersOwnNames`**
  (`tests/Unit/kyse/hygiene_test.go:218`) compiles four view shapes — page,
  layout, contract layout, component — once per colliding name, writes the whole
  corpus into a module of its own beside a stub framework, and hands it to the
  real Go toolchain with `GOWORK=off GOPROXY=off GOTOOLCHAIN=local`. Sixteen
  names by four shapes: ten variables (`s err w d sections item data v ok
  props`) and six packages (`view io fmt template strings errors`).
- **`TestTheViewsThatUsedToCompileToBrokenGoAreRefused`**
  (`tests/Unit/kyse/silentsuccess_test.go:112`) holds six shapes that used to
  reach the disk as Go that does not compile, each written by a `view:build`
  that reported success. They were found by reading the output back rather than
  by anybody thinking of them, which is the whole argument for step 7: the list
  of constructs nobody thought of cannot be written in advance.
- **`TestTheOutputIsReadBackBeforeItIsWritten`**
  (`internal/kyse/scope_internal_test.go:50`) and
  **`TestTheCheckAcceptsWhatTheGeneratorActuallyWrites`** (`:136`) are the two
  halves of `checkScopes`: it refuses what it should, and it accepts what the
  generator really emits.
- **`FuzzParse`** (`tests/Fuzz/kyse/fuzz_test.go:60`) runs arbitrary bytes at the
  parser, with 78 files of corpus beside it.

So: add the failing view to the corpus, watch it fail, then fix it. A change to
the generator that nothing compiles is a change nobody has checked.

## The four gates

```sh
export GOWORK=off
gofmt -l $(find . -name '*.go' -not -path '*/testdata/*' -not -name '*.kyse.go')
go build ./...
go vet ./...
go test -race -count=1 ./...
```

Both filters are load-bearing, and this package is the reason the second one
exists anywhere. A `.kyse.go` opens with `//go:build kyse` and continues in a
syntax that is not Go; `gofmt` is the only tool in the chain that ignores a
build tag, so it parses the file the compiler skips and fails with
`illegal character U+0040 '@'`. `testdata/` holds fixtures that are invalid on
purpose, including a Go file with no closing brace that the doctor has to report
honestly.

In this repository all five `.kyse.go` files happen to live under `testdata/`,
so the first filter covers them today — which is a fact about where the fixtures
are, not a reason to drop the second:

```sh
find . -name '*.kyse.go' | wc -l                             # 5
find . -name '*.kyse.go' -not -path '*/testdata/*'           # nothing
gofmt -l $(find . -name '*.go' -not -name '*.kyse.go'); echo $?   # 2
```
