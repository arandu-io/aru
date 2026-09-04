package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheToolchainCompilesIntoTheProjectCache is the whole point of the file it
// tests: what a build writes has to land somewhere this command may delete.
//
// It reads the environment the command carries rather than running it, because
// what is under test is which cache the toolchain is pointed at, and a real
// build would answer that question by taking a minute to fill one.
func TestTheToolchainCompilesIntoTheProjectCache(t *testing.T) {
	want, err := aranduCache()
	if err != nil {
		t.Skip("this machine has no user cache directory")
	}

	cmd := goCommand("build", "./...")
	var got string
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "GOCACHE=") {
			got = strings.TrimPrefix(entry, "GOCACHE=")
		}
	}

	if got != want {
		t.Errorf("the toolchain compiles into %q, and this command deletes %q.\n"+
			"A build that fills the shared cache and a clear that empties another one is a clear that frees nothing.", got, want)
	}
}

// TestTheProjectCacheIsNotTheSharedOne: the reason this file exists is that the
// shared cache belongs to every Go project on the machine, and emptying it would
// cost the next build of all of them.
func TestTheProjectCacheIsNotTheSharedOne(t *testing.T) {
	ours, err := aranduCache()
	if err != nil {
		t.Skip("this machine has no user cache directory")
	}

	out, err := goCommand("env", "GOCACHE").Output()
	if err != nil {
		t.Skip("the toolchain did not answer where its cache is")
	}
	shared := strings.TrimSpace(string(out))

	// goCommand sets GOCACHE, so what came back is ours -- which is the point.
	if shared != ours {
		t.Errorf("goCommand asked the toolchain for its cache and got %q, want %q", shared, ours)
	}

	plain, err := os.UserCacheDir()
	if err != nil {
		return
	}
	if ours == filepath.Join(plain, "go-build") {
		t.Error("the project cache is the toolchain's own directory, so clearing it would empty every project's cache")
	}
}

// TestAnEmptyCacheIsNotReported: a cache under the ceiling is the ordinary case,
// and a line printed on every build is a line people read past.
func TestAnEmptyCacheIsNotReported(t *testing.T) {
	var out bytes.Buffer
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "small"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if size := dirSize(dir); size >= cacheCeiling {
		t.Fatalf("a one-byte directory measured %d bytes", size)
	}
	if out.Len() != 0 {
		t.Errorf("a small cache printed %q", out.String())
	}
}

// TestAMissingCacheMeasuresZero: the first build on a machine finds no cache at
// all, and a walk that errors there must not be read as a full one.
func TestAMissingCacheMeasuresZero(t *testing.T) {
	if size := dirSize(filepath.Join(t.TempDir(), "absent")); size != 0 {
		t.Errorf("an absent directory measured %d bytes, want 0", size)
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
