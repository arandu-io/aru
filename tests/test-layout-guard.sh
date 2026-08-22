#!/usr/bin/env bash
# The mechanical guard for the test layout, in order of importance.
#
# The first check is the one that matters most, and it is not a style rule: go
# test only runs a file whose name ends in _test.go. A file named DiskTest.go --
# or disk_Test.go, which is the same mistake with a different hand -- compiles
# into the package as ordinary code and none of its tests ever run. No error, no
# warning, a green build with the suite switched off.
#
# Every check that asks the toolchain a question treats a question that could
# not be asked as a failure. A guard that goes green because `go list` broke has
# checked nothing and said everything was fine.
set -uo pipefail

cd "$(dirname "$0")/.." || exit 1

fail=0

# in_testdata answers whether a tracked path sits inside a testdata/ tree, which
# is the one adaptation this repository needs and the reason it needs it is the
# product: `aru doctor` is a checker, so its fixtures are broken code on purpose.
#
# internal/doctor/testdata/ holds four modules -- broken, clean, gaps and
# violations -- read by internal/doctor/rules_internal_test.go and by
# tests/Unit/doctor/doctor_test.go. The violations fixture plants two of the
# checks below:
#
#     app/Jobs/RetryInvoiceTest.go        the capital T of check 1
#     app/Services/BillingService_test.go the misplaced test of check 2
#
# app/Ledger/Ledger.go declares `package Ledger` and looks like a third, and it
# is not: check 3 reads only paths that start with tests/ inside their module,
# and that file is under app/. Running the framework's guard here verbatim
# reports checks 1, 2 and 4, never 3 -- which is how the count was measured
# rather than assumed.
#
# They are the corpus the doctor is measured against. Unfiltered, this guard
# reports them as violations of the repository, and satisfying it would mean
# deleting the test the fixture exists for. Three of the four modules also fail
# `go list` by design, which check 4 would report as unmeasurable.
#
# The exclusion is not a hole cut for convenience: the go tool ignores testdata/
# everywhere for the same reason, so nothing under it is a package and no rule
# about where a package's tests live can be true or false of it.
in_testdata() {
	case "/$1" in
	*/testdata/*) return 0 ;;
	esac
	return 1
}

# The two tracked listings every check reads, with the fixture corpus removed
# once here rather than at four call sites.
sources=$(git ls-files '*.go' | while IFS= read -r f; do in_testdata "$f" || printf '%s\n' "$f"; done)
suite=$(git ls-files '*_test.go' | while IFS= read -r f; do in_testdata "$f" || printf '%s\n' "$f"; done)

# The modules of this repository, by where their go.mod sits. Five go.mod are
# tracked and four of them are the doctor fixtures above, so one is walked; the
# loop is what makes the answer survive a second real one, because every rule
# below is relative to a module and not to this directory.
#
# Held as lines rather than an array: the bash macOS ships is 3.2, which has no
# mapfile, and a guard that only runs on the build machine is a guard nobody
# runs before pushing.
modules=$(git ls-files '*go.mod' | while IFS= read -r f; do in_testdata "$f" || dirname "$f"; done | sort -u)
if [ -z "$modules" ]; then
	echo "[FAILED] no go.mod is tracked outside testdata/, so there is no module to check"
	exit 1
fi

# nearest_module answers the module a path belongs to: the closest go.mod at or
# above its directory.
#
# This is the whole of check 2. Anchoring the tests/ directory at the top of the
# repository is wrong the moment a second module exists -- its tests/ tree is
# not at the top -- and matching tests/ anywhere in the path is wrong in the
# other direction, because it accepts app/services/tests/ as a test tree.
nearest_module() {
	local dir=$1
	while :; do
		if [ -f "$dir/go.mod" ]; then
			printf '%s\n' "$dir"
			return 0
		fi
		[ "$dir" = "." ] && return 1
		dir=$(dirname "$dir")
	done
}

# module_relative answers a path as its module sees it, or nothing if no module
# claims it.
module_relative() {
	local file=$1 root
	root=$(nearest_module "$(dirname "$file")") || return 1
	if [ "$root" = "." ]; then
		printf '%s\n' "$file"
	else
		printf '%s\n' "${file#"$root"/}"
	fi
}

# Nothing below may pass by having nothing to look at. Every one of these checks
# is a statement about a set of files, and every one of them is true of the
# empty set: pointed at an empty tree the guard reports success and has read
# nothing. The counts are what turn that into a failure.
#
# They are counted after the testdata filter and not before, because the filter
# is the thing most likely to grow a mistake -- a pattern that swallowed the
# whole tree would otherwise leave every check below trivially satisfied.
# `grep -c .` and not `grep -c ''`: an empty variable still feeds one blank line
# through printf, and the empty pattern would count it as a file.
source_count=$(printf '%s\n' "$sources" | grep -c '.')
suite_count=$(printf '%s\n' "$suite" | grep -c '.')

if [ "$source_count" -eq 0 ] || [ "$suite_count" -eq 0 ]; then
	echo "[FAILED] $source_count Go files and $suite_count test files are tracked here outside testdata/."
	echo "         Every check below is true of nothing, so none of them ran."
	exit 1
fi

# 1. A test file the toolchain does not recognise as one.
#
# The pattern is Tests?\.go with a capital T, which is every shape of the
# mistake -- DiskTest.go, disk_Test.go, DiskTests.go, Test.go -- and no false
# positive: latest.go ends in lowercase test.go and a real test file does too.
if offenders=$(printf '%s\n' "$sources" | grep -E 'Tests?\.go$'); then
	echo "[FAILED] go test does not run these, and will not say so:"
	printf '%s\n' "$offenders" | sed 's/^/    /'
	fail=1
fi

# 2. A test outside tests/ has to need an unexported identifier, and says so in
#    its name. Anything else belongs in a category.
while IFS= read -r file; do
	[ -z "$file" ] && continue

	if ! relative=$(module_relative "$file"); then
		echo "[FAILED] $file is under no module, so its place cannot be judged"
		fail=1
		continue
	fi

	case "$relative" in
	tests/*) continue ;;
	esac
	case "$file" in
	*_internal_test.go) continue ;;
	esac

	echo "[FAILED] $file is outside tests/ and is not _internal_test.go"
	fail=1
done < <(printf '%s\n' "$suite")

# 3. The directories are capitalised; the package clause is not.
inspected=0
while IFS= read -r file; do
	[ -z "$file" ] && continue

	relative=$(module_relative "$file") || continue
	case "$relative" in
	tests/*) ;;
	*) continue ;;
	esac
	inspected=$((inspected + 1))

	if clause=$(grep -n '^package [A-Z]' "$file"); then
		echo "[FAILED] capitalised package clause in $file:"
		printf '%s\n' "$clause" | sed 's/^/    /'
		fail=1
	fi
done < <(printf '%s\n' "$sources")

if [ "$inspected" -eq 0 ]; then
	echo "[FAILED] no module has a tests/ tree, so the package clauses of one were not read"
	fail=1
fi

# 4. Nothing outside the tests reaches the tests tree. It imports testing, and a
#    package that reaches it registers the flags of a test binary into whatever
#    imports it.
#
#    The question is asked of the PRODUCTION packages and not of ./..., which
#    also lists the test packages themselves -- and every one of those reaches
#    the tests tree, which is what it is for. Asked that way the check reports a
#    failure on any module that has a tests tree at all, which is every module
#    it is meant to protect.
#
#    The tags are passed because a suite behind one is invisible without them,
#    and so is a production package that ever grows a build tag of its own. This
#    repository has no tests/Integration or tests/E2E today -- tests/ holds Unit
#    and Fuzz -- so the two tags currently select nothing; they are passed
#    anyway, because the cost is nothing and the day one of those trees is added
#    is not the day anyone remembers to come back here.
#
#    The kyse tag is NOT passed, and that is the one tag this repository has to
#    withhold. A .kyse.go file opens with //go:build kyse precisely so the
#    compiler never sees it: its body is template syntax, not Go. Selecting the
#    tag here would hand the toolchain a file that cannot parse and turn every
#    check below into "go list failed".
asked=0
while IFS= read -r module; do
	[ -z "$module" ] && continue
	asked=$((asked + 1))

	# The cache is warmed before anything is measured, and it is not a
	# convenience. `go list` writes "go: downloading <module> <version>" to
	# stderr, which is captured here on purpose so that a module that does not
	# build is reported instead of read as an empty package list -- and those two
	# words then arrive as package names, which is what "no required module
	# provides package v0.12.0" is. CI has a cold cache every run, so this failed
	# there and passed here.
	(cd "$module" && GOWORK=off go mod download all >/dev/null 2>&1) || true

	if ! packages=$(cd "$module" && GOWORK=off go list -tags 'integration e2e' ./... 2>&1); then
		echo "[FAILED] go list failed in $module, so nothing was checked there:"
		printf '%s\n' "$packages" | sed 's/^/    /'
		fail=1
		continue
	fi

	production=$(printf '%s\n' "$packages" | grep -vE '/tests(/|$)')
	if [ -z "$production" ]; then
		continue
	fi

	# One import path per line and none of them has a space: the split is wanted.
	# shellcheck disable=SC2086
	if ! dependencies=$(cd "$module" && GOWORK=off go list -tags 'integration e2e' -deps $production 2>&1); then
		echo "[FAILED] go list -deps failed in $module, so nothing was checked there:"
		printf '%s\n' "$dependencies" | sed 's/^/    /'
		fail=1
		continue
	fi

	if reached=$(printf '%s\n' "$dependencies" | grep -E '/tests(/|$)'); then
		echo "[FAILED] a production package in $module reaches the tests tree:"
		printf '%s\n' "$reached" | sed 's/^/    /'
		fail=1
	fi
done < <(printf '%s\n' "$modules")

if [ "$asked" -eq 0 ]; then
	echo "[FAILED] the toolchain was asked about no module, so check 4 did not run"
	fail=1
fi

exit $fail
