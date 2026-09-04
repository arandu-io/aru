package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

// The build cache Arandu projects compile into, and why it is not the shared one.
//
// The Go toolchain keeps one cache per machine, under the user's cache
// directory, holding the compiled output of every Go project they have. It is
// keyed by a hash of the inputs, so changing a line writes a new object and
// leaves the old one: the toolchain cannot know the old inputs will never come
// back. It drops what has gone unused for a few days and enforces no ceiling on
// size.
//
// A framework whose deploy is one compiled binary reaches a large cache faster
// than most. Fifteen modules that depend on one another mean a version bump
// invalidates every object compiled against the version before it; a
// race-instrumented test binary per package is two to three times the size of a
// plain one; and nothing is interpreted, so everything is compiled.
//
// Emptying the shared cache to fix that would cost the next build of every other
// Go project on the machine, which is a bill this CLI has no business sending.
// So Arandu projects compile into a cache of their own. What is written here was
// written by this command, and is the only thing it removes.
//
// The cost is one cold build the first time, and a second copy of the standard
// library. The gain is that "clear the cache" means one project's cache.
func aranduCache() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "arandu", "build"), nil
}

// goCommand is every invocation of the toolchain this CLI makes.
//
// One function rather than four call sites, because the environment is the part
// that has to be the same in all of them: a build that used the shared cache
// while the tests used the project's would compile the same package twice and
// report a cache that is not the one it filled.
func goCommand(args ...string) *exec.Cmd {
	cmd := exec.Command("go", args...)
	cmd.Env = os.Environ()
	if dir, err := aranduCache(); err == nil {
		cmd.Env = append(cmd.Env, "GOCACHE="+dir)
	}
	return cmd
}

// cacheCeiling is where this command stops adding without saying so.
//
// Eight gigabytes is a working cache for the whole collection with room for a
// few versions behind: enough that an ordinary week never reaches it, and small
// enough that a week of releases does.
const cacheCeiling = 8 << 30

// trimCache empties the project cache once it passes the ceiling, and says so.
//
// It empties rather than trims by age. The toolchain writes the cache and knows
// which entries belong to which build; from outside, the only honest choices are
// all of it or none, and deleting a subset by timestamp would leave a cache the
// toolchain believes is complete with pieces missing from it.
//
// The removal is reported because a build that silently took longer than the
// last one is a build somebody debugs.
func trimCache(w io.Writer) {
	dir, err := aranduCache()
	if err != nil {
		return
	}
	size := dirSize(dir)
	if size < cacheCeiling {
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		return
	}
	fmt.Fprintf(w, "cleared %s of build cache for Arandu projects; this build is a cold one\n", humanBytes(size))
}

// dirSize answers zero for a directory it cannot read.
//
// A measurement that can fail the build it was taken for is worth less than no
// measurement, so an unreadable entry is skipped and an absent directory is
// zero -- which reads as "nothing to remove", the right answer for a cache that
// does not exist yet.
func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}

// humanBytes writes a size the way a person reads one.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
