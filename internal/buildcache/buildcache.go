// Package buildcache is where this command starts the Go toolchain, and the
// cache that toolchain compiles Arandu projects into.
//
// Every compile, list, vet, test and run this command asks for goes through
// Command, so the environment is decided in one place. A build that used the
// shared cache while the tests used this one would compile the same package
// twice and report a cache that is not the one it filled.
package buildcache

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Ceiling is the size at which the cache is trimmed.
//
// Eight gigabytes is a working cache for the whole collection with room for a
// few versions behind: enough that an ordinary week never reaches it, and small
// enough that a week of releases does.
const Ceiling = 8 << 30

// LowWater is the size a trim stops at.
//
// It sits below the ceiling so that a trim buys room for more than the next
// build. Stopping at the ceiling itself would leave the cache one build away
// from the next trim, and every measurement after that would remove a few
// megabytes and say so.
const LowWater = 6 << 30

// recentUse is how recently an output must have been used to be safe from a
// trim, however full the cache is.
//
// The toolchain refreshes an output's modification time when it reads it, but
// at most once an hour, so the time on disk is the last use to within an hour.
// An output inside that window may belong to a build running right now, and
// removing it under that build is the one deletion that could fail something
// rather than slow it down.
const recentUse = time.Hour

// measureEvery is how often the cache is measured at all.
//
// Measuring is a walk over every file in the cache; asking whether it is due is
// one stat of the stamp. Once an hour keeps every other invocation at that
// stat, and a cache that grew past the ceiling in the meantime is brought back
// at the next measurement.
const measureEvery = time.Hour

// Dir is the build cache Arandu projects compile into, and why it is not the
// shared one.
//
// The Go toolchain keeps one cache per machine, under the user's cache
// directory, holding the compiled output of every Go project they have. It is
// keyed by a hash of the inputs, so changing a line writes a new object and
// leaves the old one: the toolchain cannot know the old inputs will never come
// back. It drops what has gone unused for a few days and enforces no ceiling on
// size.
//
// A framework whose deploy is one compiled binary reaches a large cache faster
// than most. Modules that depend on one another mean a version bump invalidates
// every object compiled against the version before it; a race-instrumented test
// binary per package is two to three times the size of a plain one; and nothing
// is interpreted, so everything is compiled.
//
// Trimming the shared cache to fix that would cost the next build of every
// other Go project on the machine, which is a bill this command has no business
// sending. So Arandu projects compile into a cache of their own. What is
// written here was written through Command, and it is the only cache this
// command removes anything from.
//
// The cost is one cold build the first time, and a second copy of the standard
// library. The gain is that a trim here costs Arandu projects and nothing else.
func Dir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "arandu", "build"), nil
}

// Command is every invocation of the toolchain this command makes.
//
// It answers `go` with the given arguments, the inherited environment, and the
// cache set to Dir. The cache is appended last, so a GOCACHE the machine
// exports does not win over it; a caller that adds settings of its own appends
// to cmd.Env rather than replacing it, or the build lands in the shared cache.
//
// The cache is trimmed here, before the toolchain is asked to write more,
// because this is the one function every build, run, test and packaging step
// passes through: a trim called from each command would be a trim the next
// command forgets. Most calls cost one stat, see measureEvery. When something
// is removed it is said on standard error, because a build that silently took
// longer than the last one is a build somebody debugs.
//
// Without a user cache directory the toolchain keeps its own default, and
// nothing is trimmed: there is no cache here to keep small.
func Command(args ...string) *exec.Cmd {
	cmd := exec.Command("go", args...)
	cmd.Env = os.Environ()
	dir, err := Dir()
	if err != nil {
		return cmd
	}
	trimAndSay(os.Stderr, dir, stampFor(dir), Ceiling, LowWater, time.Now())
	cmd.Env = append(cmd.Env, "GOCACHE="+dir)
	return cmd
}

// stampFor is the file recording when the cache in dir was last measured.
//
// It sits beside the cache rather than inside it. The toolchain owns every name
// in its cache directory and keeps a stamp of its own there, under a name this
// command has no say over; a file of ours inside would be one rename away from
// being read as the toolchain's, or removed as it.
func stampFor(dir string) string {
	return dir + "-trim.txt"
}

// trimAndSay trims the cache in dir and writes one line to w when anything was
// removed.
//
// The parameters are the whole policy, which is what lets a test run it on a
// temporary directory with a ceiling measured in bytes.
func trimAndSay(w io.Writer, dir, stamp string, ceiling, lowWater int64, now time.Time) {
	removed, kept := trim(dir, stamp, ceiling, lowWater, now)
	if removed == 0 {
		return
	}
	fmt.Fprintf(w, "trimmed %s of build cache for Arandu projects, least recently used first; kept %s\n",
		humanBytes(removed), humanBytes(kept))
}

// output is one compiled output in the cache: a file, or a directory holding
// one executable.
type output struct {
	path string
	size int64
	used time.Time
	dir  bool
}

// trim removes the least recently used outputs from the cache in dir, once it
// has reached ceiling, until it is at or below lowWater. It answers how many
// bytes it removed and how many the cache still holds; a cache that was not
// measured, because stamp says it was measured recently, answers zero for both.
//
// It removes outputs one by one, oldest use first, and that is safe for a
// reason in the toolchain's own reading of its cache: an index entry whose
// output is missing, or shorter than the index says, is a cache miss. The
// package is compiled again and the output written back, so a removed output
// costs one compile the next time it is needed and never a wrong build. It is
// the same thing the toolchain does to its own cache when it drops what has gone
// unused for days; what this adds is a ceiling, which the toolchain does not
// have.
//
// The index entries are left in place. Each is a line of under two hundred
// bytes, so removing them would free nothing worth the walk that matches one to
// its output -- the name of an output is the hash of its content, and only the
// index entry's own content says which output it points at. One whose output is
// gone is a miss, rewritten by the next compile that needs it, and one nobody
// needs again ages out with the toolchain's own trim.
//
// Two outputs are never removed: one used within recentUse, which may belong to
// a build that is running, and one whose time changed between the walk and the
// removal, because a build just used it.
func trim(dir, stamp string, ceiling, lowWater int64, now time.Time) (removed, kept int64) {
	if measuredRecently(stamp, now) {
		return 0, 0
	}
	if _, err := os.Stat(dir); err != nil {
		return 0, 0
	}
	// Written before the walk rather than after it, so a second command started
	// while this one walks finds the cache already being measured and skips.
	// The time on the file is what is read back, and it is set to now rather
	// than left to the clock, so the stamp and the walk agree on when it was.
	if err := os.WriteFile(stamp, []byte(strconv.FormatInt(now.Unix(), 10)+"\n"), 0o644); err == nil {
		_ = os.Chtimes(stamp, now, now)
	}

	total, outputs := measure(dir)
	if total < ceiling {
		return 0, total
	}

	sort.Slice(outputs, func(i, j int) bool { return outputs[i].used.Before(outputs[j].used) })
	for _, o := range outputs {
		if total <= lowWater || now.Sub(o.used) < recentUse {
			// Sorted by last use, so the first output too recent to remove is
			// followed only by more recent ones.
			break
		}
		if !unusedSince(o) {
			continue
		}
		var err error
		if o.dir {
			err = os.RemoveAll(o.path)
		} else {
			err = os.Remove(o.path)
		}
		if err != nil {
			continue
		}
		removed += o.size
		total -= o.size
	}
	return removed, total
}

// measuredRecently reports whether stamp was written within measureEvery of
// now.
//
// A stamp from the future by more than that is not trusted: a clock that was
// wrong once would otherwise keep the cache from being measured until the
// clock caught up with it.
func measuredRecently(stamp string, now time.Time) bool {
	info, err := os.Stat(stamp)
	if err != nil {
		return false
	}
	age := now.Sub(info.ModTime())
	return age < measureEvery && age > -measureEvery
}

// measure answers the size of everything under dir and the outputs a trim may
// remove.
//
// Every file counts toward the size, because every file is disk the cache
// takes. Only an output is a candidate: a name ending in -d, directly inside
// one of the two-hex-digit directories the toolchain spreads its entries over.
// A directory with such a name is an executable the toolchain keeps whole, and
// it is measured and removed as one output, by the time on the directory, which
// is the one the toolchain refreshes. Anything else -- index entries, the
// toolchain's own files, the fuzzing corpus -- is counted and left alone.
//
// An entry that cannot be read is skipped rather than failing the walk: a
// measurement that can fail the build it was taken for is worth less than no
// measurement.
func measure(dir string) (total int64, outputs []output) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		candidate := isOutput(dir, path)
		if d.IsDir() {
			if !candidate {
				return nil
			}
			size := dirSize(path)
			total += size
			outputs = append(outputs, output{path: path, size: size, used: info.ModTime(), dir: true})
			return fs.SkipDir
		}
		total += info.Size()
		if candidate && info.Mode().IsRegular() {
			outputs = append(outputs, output{path: path, size: info.Size(), used: info.ModTime()})
		}
		return nil
	})
	return total, outputs
}

// isOutput reports whether path names an output entry of the cache in dir:
// <dir>/<two hex digits>/<hash>-d.
func isOutput(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	shard, name, found := strings.Cut(filepath.ToSlash(rel), "/")
	if !found || strings.Contains(name, "/") || !strings.HasSuffix(name, "-d") {
		return false
	}
	if len(shard) != 2 {
		return false
	}
	for _, c := range shard {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// unusedSince reports whether o is still where the walk found it, with the
// time the walk read.
//
// The walk and the removal are apart by as long as the walk took, and a build
// that read the output in between moved its time forward. That build may still
// be about to open it.
func unusedSince(o output) bool {
	info, err := os.Stat(o.path)
	if err != nil {
		return false
	}
	return !info.ModTime().After(o.used)
}

// dirSize answers zero for a directory it cannot read.
//
// An unreadable entry is skipped and an absent directory is zero, which reads
// as "nothing to remove": the right answer for a cache that does not exist yet.
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
