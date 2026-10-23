package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/arandu-io/aru/internal/buildcache"
	"github.com/arandu-io/aru/internal/testlayout"
)

// e2eTestsDir is where a project keeps its end-to-end journeys: the tests that
// boot the server and drive a real browser through it.
const e2eTestsDir = testlayout.E2EDir

// e2eTag is the build tag those tests carry. It keeps them out of the ordinary
// go test ./..., which has no browser to start.
const e2eTag = testlayout.E2ETag

// legacyBrowserDir is where the browser tests lived before the end-to-end
// layer absorbed them. It is named only to say where they go now.
const legacyBrowserDir = "tests/Browser"

// smokePrefix is what marks a journey as a smoke test: a top-level test whose
// name starts with TestSmoke. It is a name and not a label inside the test so
// that go test -run selects it without compiling anything twice, and so that a
// person reading the file list sees which journeys a pull request runs.
const smokePrefix = "TestSmoke"

// engines are the browser engines --browser takes as a value.
var engines = []string{"chromium", "chrome", "firefox", "webkit"}

// errNoE2ETests is the answer to --e2e in a project without the directory the
// journeys live in.
var errNoE2ETests = errors.New("there is no " + e2eTestsDir + " in this project: a journey is a Go test package " +
	"there, each file opening with //go:build " + e2eTag + ", and aru test --e2e runs it. aru make:e2e <Journey> writes one")

// errBrowserDirMoved is the answer when only the old tests/Browser exists.
var errBrowserDirMoved = errors.New(legacyBrowserDir + " is where browser tests used to live, and aru test --e2e runs " +
	e2eTestsDir + ": move the package there and change its first line to //go:build " + e2eTag)

// testRun is what aru test was asked for, read off its arguments.
type testRun struct {
	e2e   bool
	smoke bool
	// shard and shards are --shard i/n: run the i-th of n slices, 1-based.
	shard, shards int
	// driver and engine are exported as ARANDU_E2E_DRIVER and
	// ARANDU_E2E_BROWSER for the journeys to read.
	driver, engine string
	// deprecated is set by a bare --browser, the old name of --e2e.
	deprecated bool
	// list is --list: read the tests from the sources and print them instead
	// of running anything. json is --json beside it.
	list, json bool
	// rest is everything handed to go test as it was typed.
	rest []string
}

// runTests runs the project's tests with go test, from the project root.
//
// Without a flag of its own it is go test ./..., which the e2e build tag keeps
// away from tests/E2E. --e2e is go test -tags e2e ./tests/E2E/... and nothing
// else, because the journeys are slow and need a browser and a database.
// --smoke is the journeys whose names start with TestSmoke. --shard i/n lists
// the tests the run would execute, sorts them, and runs every n-th one
// starting at the i-th, so n machines share one suite without two running the
// same test. --driver and --browser name the driver and the engine the
// journeys launch, through ARANDU_E2E_DRIVER and ARANDU_E2E_BROWSER. A bare
// --browser is the old spelling of --e2e and still works, with a notice.
//
// Every other argument is handed to go test as it is -- -run, -race, -v,
// -count -- so this parses no flag set of its own; its flags are taken out
// wherever they stand before a bare --.
//
// The toolchain is started through the same cache and compiler flags as every
// command that runs the application, so a test after aru dev compiles nothing
// twice, and the view layer is built first when it is missing, because the
// tests import the application and the application imports its views.
func runTests(args []string, stdout, stderr io.Writer) error {
	run, err := parseTestArgs(args)
	if err != nil {
		return err
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}
	if run.list {
		if extra := run.listConflict(); extra != "" {
			return errors.New("aru test --list reads the test sources and runs nothing, so it takes no flag but --json: drop " +
				extra + ". go test's own -list is written -list=<regexp>")
		}
		return listTests(root, run.json, stdout, stderr)
	}
	if _, err := exec.LookPath("go"); err != nil {
		return errors.New("the go toolchain was not found in PATH, and aru needs it to run the tests")
	}
	if run.deprecated {
		fmt.Fprintln(stderr, "aru test: --browser with no engine is the old name of --e2e, and runs "+e2eTestsDir+"; write --e2e")
	}

	packages := []string{"./..."}
	if run.e2e {
		if !isDir(filepath.Join(root, filepath.FromSlash(e2eTestsDir))) {
			if isDir(filepath.Join(root, filepath.FromSlash(legacyBrowserDir))) {
				return errBrowserDirMoved
			}
			return errNoE2ETests
		}
		packages = []string{"./" + e2eTestsDir + "/..."}
	}

	rest, tags := takeTags(run.rest)
	if run.e2e {
		tags = append(tags, e2eTag)
	}
	if run.smoke {
		if hasFlag(rest, "run") {
			return errors.New("aru test --smoke chooses the tests itself, as -run '^" + smokePrefix + "': drop -run, or drop --smoke")
		}
		rest = append(rest, "-run", "^"+smokePrefix)
	}
	var tagArgs []string
	if len(tags) > 0 {
		tagArgs = []string{"-tags", strings.Join(tags, ",")}
	}

	if err := ensureViews(root, stderr); err != nil {
		return err
	}

	env := os.Environ()
	if run.driver != "" {
		env = append(env, "ARANDU_E2E_DRIVER="+run.driver)
	}
	if run.engine != "" {
		env = append(env, "ARANDU_E2E_BROWSER="+run.engine)
	}

	if run.shards == 0 {
		return goTest(root, env, concat(tagArgs, rest, packages), stdout, stderr)
	}

	pattern, rest := takeRun(rest)
	slice, err := shardTests(root, env, tagArgs, pattern, packages, run.shard, run.shards)
	if err != nil {
		return err
	}
	if len(slice) == 0 {
		fmt.Fprintf(stdout, "shard %d/%d has no test to run\n", run.shard, run.shards)
		return nil
	}
	failed := false
	for _, pkg := range sortedPackages(slice) {
		names := slice[pkg]
		quoted := make([]string, len(names))
		for i, n := range names {
			quoted[i] = regexp.QuoteMeta(n)
		}
		runArg := "^(" + strings.Join(quoted, "|") + ")$"
		if err := goTest(root, env, concat(tagArgs, rest, []string{"-run", runArg, pkg}), stdout, stderr); err != nil {
			failed = true
		}
	}
	if failed {
		return errors.New("the tests failed")
	}
	return nil
}

// goTest runs go test with args in root.
func goTest(root string, env, args []string, stdout, stderr io.Writer) error {
	cmd := buildcache.Application("test", args...)
	cmd.Dir = root
	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			// go test has already said which test failed and why.
			return errors.New("the tests failed")
		}
		return fmt.Errorf("running go test: %w", err)
	}
	return nil
}

// shardTests lists the tests the run would execute and keeps the slice this
// shard runs: the tests sorted by package and name, every shards-th one from
// the shard-th. Only Test functions are listed; a benchmark or an example is
// not part of a test run unless asked for.
func shardTests(root string, env, tagArgs []string, pattern string, packages []string, shard, shards int) (map[string][]string, error) {
	if pattern == "" {
		pattern = "."
	}
	var out, errOut bytes.Buffer
	cmd := buildcache.Application("test", concat(tagArgs, []string{"-list", pattern}, packages)...)
	cmd.Dir = root
	cmd.Env = env
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("listing the tests to shard: %v\n%s%s", err, out.String(), errOut.String())
	}
	type entry struct{ pkg, name string }
	var all []entry
	var pending []string
	for _, line := range strings.Split(out.String(), "\n") {
		fields := strings.Fields(line)
		switch {
		case len(fields) == 1 && strings.HasPrefix(fields[0], "Test"):
			pending = append(pending, fields[0])
		case len(fields) >= 2 && (fields[0] == "ok" || fields[0] == "?"):
			for _, name := range pending {
				all = append(all, entry{fields[1], name})
			}
			pending = nil
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].pkg != all[j].pkg {
			return all[i].pkg < all[j].pkg
		}
		return all[i].name < all[j].name
	})
	slice := map[string][]string{}
	for i, e := range all {
		if i%shards == shard-1 {
			slice[e.pkg] = append(slice[e.pkg], e.name)
		}
	}
	return slice, nil
}

func sortedPackages(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// parseTestArgs takes aru test's own flags out of the arguments. Scanning
// stops at a bare --: what follows is for go test, even a --e2e.
//
// --list without a value is aru's, and -list=<regexp> is go test's own. A
// --json is aru's only beside --list; without it, it is the -json go test
// streams its events with, and stays where it was typed.
func parseTestArgs(args []string) (testRun, error) {
	var run testRun
	run.rest = make([]string, 0, len(args))
	var jsonAt []int
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			run.rest = append(run.rest, args[i:]...)
			break
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !strings.HasPrefix(a, "-") {
			run.rest = append(run.rest, a)
			continue
		}
		switch name {
		case "list":
			if hasValue {
				run.rest = append(run.rest, a)
				continue
			}
			run.list = true
		case "json":
			if !hasValue {
				jsonAt = append(jsonAt, len(run.rest))
			}
			run.rest = append(run.rest, a)
		case "e2e":
			run.e2e = true
		case "smoke":
			run.e2e, run.smoke = true, true
		case "shard", "driver":
			if !hasValue {
				if i+1 >= len(args) {
					return testRun{}, fmt.Errorf("aru test: --%s needs a value", name)
				}
				i++
				value = args[i]
			}
			if name == "driver" {
				if strings.TrimSpace(value) == "" {
					return testRun{}, errors.New("aru test: --driver needs the name a driver is registered under, such as cdp")
				}
				run.driver, run.e2e = value, true
				continue
			}
			shard, shards, err := parseShard(value)
			if err != nil {
				return testRun{}, err
			}
			run.shard, run.shards = shard, shards
		case "browser":
			if !hasValue && i+1 < len(args) && isEngine(args[i+1]) {
				i++
				value, hasValue = args[i], true
			}
			if !hasValue {
				run.e2e, run.deprecated = true, true
				continue
			}
			if !isEngine(value) {
				return testRun{}, fmt.Errorf("aru test: --browser=%s is not an engine; want one of %s", value, strings.Join(engines, ", "))
			}
			run.engine, run.e2e = strings.ToLower(value), true
		default:
			run.rest = append(run.rest, a)
		}
	}
	if run.list && len(jsonAt) > 0 {
		kept := make([]string, 0, len(run.rest))
		for i, a := range run.rest {
			if len(jsonAt) > 0 && jsonAt[0] == i {
				jsonAt = jsonAt[1:]
				continue
			}
			kept = append(kept, a)
		}
		run.rest, run.json = kept, true
	}
	return run, nil
}

// listConflict names the first argument --list cannot be given with, or
// returns empty when there is none.
func (r testRun) listConflict() string {
	switch {
	case r.smoke:
		return "--smoke"
	case r.shards > 0:
		return "--shard"
	case r.driver != "":
		return "--driver"
	case r.engine != "" || r.deprecated:
		return "--browser"
	case r.e2e:
		return "--e2e"
	case len(r.rest) > 0:
		return r.rest[0]
	}
	return ""
}

// parseShard reads i/n, with 1 <= i <= n.
func parseShard(v string) (int, int, error) {
	a, b, ok := strings.Cut(v, "/")
	i, errI := strconv.Atoi(a)
	n, errN := strconv.Atoi(b)
	if !ok || errI != nil || errN != nil || n < 1 || i < 1 || i > n {
		return 0, 0, fmt.Errorf("aru test: --shard=%s is not i/n with 1 <= i <= n, such as --shard=2/4", v)
	}
	return i, n, nil
}

func isEngine(v string) bool {
	for _, e := range engines {
		if strings.EqualFold(v, e) {
			return true
		}
	}
	return false
}

// takeTags removes every -tags flag go test would read from the arguments and
// returns the tags they named, so the e2e tag joins them rather than a second
// -tags silently replacing the first.
func takeTags(args []string) ([]string, []string) {
	var tags []string
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !strings.HasPrefix(a, "-") || name != "tags" {
			rest = append(rest, a)
			continue
		}
		if !hasValue && i+1 < len(args) {
			i++
			value = args[i]
		}
		tags = append(tags, strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' })...)
	}
	return rest, tags
}

// takeRun removes -run and its value, returning the value.
func takeRun(args []string) (string, []string) {
	pattern := ""
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !strings.HasPrefix(a, "-") || name != "run" {
			rest = append(rest, a)
			continue
		}
		if !hasValue && i+1 < len(args) {
			i++
			value = args[i]
		}
		pattern = value
	}
	return pattern, rest
}

// hasFlag reports whether go test would read the flag name from args.
func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		n, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if strings.HasPrefix(a, "-") && n == name {
			return true
		}
	}
	return false
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func concat(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
