package buildcache

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestTheToolchainCompilesIntoTheProjectCache is the reason the package
// exists: what a build writes has to land in the cache this package trims.
//
// It reads the environment the command carries rather than running it, because
// what is under test is which cache the toolchain is pointed at, and a real
// build would answer that question by taking a minute to fill one.
func TestTheToolchainCompilesIntoTheProjectCache(t *testing.T) {
	want, err := Dir()
	if err != nil {
		t.Skip("this machine has no user cache directory")
	}
	// A cache the machine exports is the situation the ordering is for.
	t.Setenv("GOCACHE", filepath.Join(t.TempDir(), "inherited"))

	if got := effective("GOCACHE", Command("build", "./...").Env); got != want {
		t.Errorf("the toolchain compiles into %q, and this package trims %q.\n"+
			"A build that fills one cache and a trim that measures another is a trim that frees nothing.", got, want)
	}
}

// TestASettingAppendedByACallerKeepsTheCache: platforms add their own settings
// to the environment, and the cache has to survive the addition.
func TestASettingAppendedByACallerKeepsTheCache(t *testing.T) {
	want, err := Dir()
	if err != nil {
		t.Skip("this machine has no user cache directory")
	}
	cmd := Command("build", "./...")
	cmd.Env = append(cmd.Env, "GOOS=js", "GOARCH=wasm")

	if got := effective("GOCACHE", cmd.Env); got != want {
		t.Errorf("after a platform added its settings the toolchain compiles into %q, want %q", got, want)
	}
}

// TestTheProjectCacheIsNotTheSharedOne: the shared cache belongs to every Go
// project on the machine, and trimming it would cost the next build of all of
// them.
func TestTheProjectCacheIsNotTheSharedOne(t *testing.T) {
	ours, err := Dir()
	if err != nil {
		t.Skip("this machine has no user cache directory")
	}

	out, err := Command("env", "GOCACHE").Output()
	if err != nil {
		t.Skip("the toolchain did not answer where its cache is")
	}
	// Command sets GOCACHE, so what came back is ours -- which is the point.
	if got := strings.TrimSpace(string(out)); got != ours {
		t.Errorf("Command asked the toolchain for its cache and got %q, want %q", got, ours)
	}

	plain, err := os.UserCacheDir()
	if err != nil {
		return
	}
	if ours == filepath.Join(plain, "go-build") {
		t.Error("the project cache is the toolchain's own directory, so trimming it would cost every project")
	}
}

// TestTheStampIsBesideTheCache: inside the cache every name is the toolchain's,
// and it keeps a stamp of its own there.
func TestTheStampIsBesideTheCache(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "build")
	stamp := stampFor(dir)
	if filepath.Dir(stamp) != filepath.Dir(dir) {
		t.Errorf("the stamp is at %s, which is not beside the cache at %s", stamp, dir)
	}
	if strings.HasPrefix(stamp, dir+string(filepath.Separator)) {
		t.Errorf("the stamp %s is inside the cache %s", stamp, dir)
	}
}

// TestTheOldestOutputsGoFirstAndTheTrimStopsAtLowWater is the policy in one
// fixture: ten outputs of 100 bytes, one used each hour going back from two
// hours ago, under a ceiling of 1000 and a low-water mark of 600.
func TestTheOldestOutputsGoFirstAndTheTrimStopsAtLowWater(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	var paths []string
	for i := 0; i < 10; i++ {
		paths = append(paths, plantOutput(t, dir, i, 100, now.Add(-time.Duration(2+i)*time.Hour)))
	}

	var said bytes.Buffer
	trimAndSay(&said, dir, filepath.Join(t.TempDir(), "stamp"), 1000, 600, now)

	// Oldest first: paths[9] was used eleven hours ago, paths[0] two. Removing
	// the four oldest takes 1000 to 600, and the trim stops there rather than
	// at the ceiling or at nothing.
	for i, path := range paths {
		_, err := os.Stat(path)
		gone := os.IsNotExist(err)
		if i >= 6 && !gone {
			t.Errorf("output %d, used %d hours ago, survived while newer ones were kept", i, 2+i)
		}
		if i < 6 && gone {
			t.Errorf("output %d, used %d hours ago, was removed after the cache was already at the low-water mark", i, 2+i)
		}
	}
	if got := dirSize(dir); got != 600 {
		t.Errorf("the cache holds %d bytes after the trim, want the low-water mark of 600", got)
	}
	want := "trimmed 400 B of build cache for Arandu projects, least recently used first; kept 600 B\n"
	if said.String() != want {
		t.Errorf("the trim said %q, want %q", said.String(), want)
	}
}

// TestAnOutputUsedInTheLastHourSurvives: an output that recent may belong to a
// build running now, and the toolchain's own times are only accurate to the
// hour.
func TestAnOutputUsedInTheLastHourSurvives(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	recent := plantOutput(t, dir, 0, 500, now.Add(-10*time.Minute))
	old := plantOutput(t, dir, 1, 500, now.Add(-3*time.Hour))

	removed, kept := trim(dir, filepath.Join(t.TempDir(), "stamp"), 100, 50, now)

	if _, err := os.Stat(recent); err != nil {
		t.Errorf("an output used ten minutes ago was removed: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("an output used three hours ago survived a cache ten times over its ceiling")
	}
	if removed != 500 || kept != 500 {
		t.Errorf("the trim answered removed %d, kept %d; want 500 and 500", removed, kept)
	}
}

// TestAFreshStampSkipsTheWalk: most invocations must cost one stat, and a cache
// far over its ceiling is left alone until the stamp is an hour old.
func TestAFreshStampSkipsTheWalk(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	path := plantOutput(t, dir, 0, 1000, now.Add(-48*time.Hour))
	stamp := filepath.Join(t.TempDir(), "stamp")
	if err := os.WriteFile(stamp, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(stamp, now.Add(-30*time.Minute), now.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}

	if removed, kept := trim(dir, stamp, 10, 5, now); removed != 0 || kept != 0 {
		t.Errorf("a cache measured half an hour ago was measured again: removed %d, kept %d", removed, kept)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("an output was removed while the stamp said the cache had just been measured: %v", err)
	}

	// An hour later the same cache is measured, and the stamp moves to now.
	later := now.Add(31 * time.Minute)
	if removed, _ := trim(dir, stamp, 10, 5, later); removed != 1000 {
		t.Errorf("a stamp an hour old did not lead to a trim: removed %d, want 1000", removed)
	}
	if !measuredRecently(stamp, later) {
		t.Error("the trim did not record when it measured, so the next call walks the cache again")
	}
}

// TestAStampFromTheFutureIsNotTrusted: a clock that was wrong once must not
// keep the cache from being measured until it catches up.
func TestAStampFromTheFutureIsNotTrusted(t *testing.T) {
	now := time.Now()
	stamp := filepath.Join(t.TempDir(), "stamp")
	if err := os.WriteFile(stamp, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	future := now.Add(30 * 24 * time.Hour)
	if err := os.Chtimes(stamp, future, future); err != nil {
		t.Fatal(err)
	}
	if measuredRecently(stamp, now) {
		t.Error("a stamp a month in the future reads as a recent measurement")
	}
}

// TestAnExecutableIsRemovedWhole: the toolchain keeps an executable as a
// directory holding the one file, and a directory left empty is an entry it
// refuses to read rather than a miss.
func TestAnExecutableIsRemovedWhole(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	executable := filepath.Join(dir, "ab", strings.Repeat("ab", 32)+"-d")
	if err := os.MkdirAll(executable, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(executable, "app"), make([]byte, 800), 0o644); err != nil {
		t.Fatal(err)
	}
	// The time on the directory is the one the toolchain refreshes, so it is
	// the one that says when the executable was last used.
	if err := os.Chtimes(executable, now.Add(-5*time.Hour), now.Add(-5*time.Hour)); err != nil {
		t.Fatal(err)
	}
	kept := plantOutput(t, dir, 1, 100, now.Add(-2*time.Hour))

	removed, left := trim(dir, filepath.Join(t.TempDir(), "stamp"), 500, 200, now)

	if _, err := os.Stat(executable); !os.IsNotExist(err) {
		t.Errorf("the executable's directory is still there: %v", err)
	}
	if removed != 800 || left != 100 {
		t.Errorf("the trim answered removed %d, kept %d; want 800 and 100", removed, left)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Errorf("a newer output was removed after the cache was already under the low-water mark: %v", err)
	}
}

// TestOnlyOutputsAreRemoved: index entries and the toolchain's own files count
// toward the size and are never deleted.
func TestOnlyOutputsAreRemoved(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	old := now.Add(-100 * time.Hour)
	keep := []string{
		filepath.Join(dir, "README"),
		filepath.Join(dir, "trim.txt"),
		filepath.Join(dir, "00", strings.Repeat("00", 32)+"-a"),
		filepath.Join(dir, "fuzz", "pkg", "FuzzX", "corpus-d"),
	}
	for _, path := range keep {
		plantFile(t, path, 300, old)
	}
	output := plantOutput(t, dir, 2, 300, old)

	removed, kept := trim(dir, filepath.Join(t.TempDir(), "stamp"), 10, 0, now)

	for _, path := range keep {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s is not an output and was removed", path)
		}
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Error("the one output in the cache survived a trim to zero")
	}
	if removed != 300 || kept != 1200 {
		t.Errorf("the trim answered removed %d, kept %d; want 300 and 1200", removed, kept)
	}
}

// TestACacheUnderTheCeilingIsNotReported: the ordinary case, and a line printed
// on every build is a line people read past.
func TestACacheUnderTheCeilingIsNotReported(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	path := plantOutput(t, dir, 0, 100, now.Add(-48*time.Hour))

	var said bytes.Buffer
	trimAndSay(&said, dir, filepath.Join(t.TempDir(), "stamp"), 1000, 600, now)

	if said.Len() != 0 {
		t.Errorf("a cache under its ceiling printed %q", said.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("an output was removed from a cache under its ceiling: %v", err)
	}
}

// TestAMissingCacheIsLeftAlone: the first build on a machine finds no cache at
// all, and that must read as nothing to remove.
func TestAMissingCacheIsLeftAlone(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "absent")
	if size := dirSize(absent); size != 0 {
		t.Errorf("an absent directory measured %d bytes, want 0", size)
	}
	if removed, kept := trim(absent, filepath.Join(t.TempDir(), "stamp"), 0, 0, time.Now()); removed != 0 || kept != 0 {
		t.Errorf("an absent cache answered removed %d, kept %d", removed, kept)
	}
}

// TestSizesAreWrittenTheWayAPersonReadsThem: the number is the only part of the
// notice a person acts on.
func TestSizesAreWrittenTheWayAPersonReadsThem(t *testing.T) {
	for _, c := range []struct {
		bytes int64
		want  string
	}{
		{512, "512 B"},
		{1024, "1.0 KB"},
		{8 << 30, "8.0 GB"},
		{43 << 30, "43.0 GB"},
	} {
		if got := humanBytes(c.bytes); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.bytes, got, c.want)
		}
	}
}

// plantOutput writes the n-th output file of a fake cache in dir, size bytes
// long and last used at used, and answers its path.
func plantOutput(t *testing.T, dir string, n, size int, used time.Time) string {
	t.Helper()
	name := fmt.Sprintf("%064x", n+1)
	path := filepath.Join(dir, name[:2], name+"-d")
	plantFile(t, path, size, used)
	return path
}

func plantFile(t *testing.T, path string, size int, used time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, used, used); err != nil {
		t.Fatal(err)
	}
}

// effective answers what a variable is set to in an environment: the last
// entry wins, as it does for the process the environment is given to.
func effective(name string, env []string) string {
	value := ""
	for _, entry := range env {
		if setting, found := strings.CutPrefix(entry, name+"="); found {
			value = setting
		}
	}
	return value
}
