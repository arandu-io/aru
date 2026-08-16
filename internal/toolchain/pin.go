package toolchain

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Pins are the tool versions a project asks for.
//
// There is one, and it is not Node. The view compiler is deliberately absent:
// kyse is part of `aru` itself, so there is nothing to pin, verify or cache.
type Pins struct {
	Tailwind string

	// Aru is the oldest CLI this project can be built with, as "v0.17.0".
	//
	// It exists because the failure it prevents is unreadable. A project written
	// with a current CLI, opened with an older one, is refused view by view with
	// messages about markup that is correct -- sixty of them, each naming a line
	// that is fine -- and the person concludes the project is broken.
	//
	// Empty means no floor, which is every project generated before this
	// existed. Those keep working.
	Aru string

	// Retired holds the pins for tools that no longer exist. Reading them is not
	// an error -- see the templ case in ReadPins -- but the caller is expected to
	// say so, or the line stays in the file forever and the next reader assumes
	// the tool is still in play.
	Retired []retired
}

// retired is a pin that was valid once and is now dead weight.
type retired struct {
	line    int
	key     string
	because string
}

// Warn writes one line per retired pin, naming the file, the line and the fix.
//
// It takes a writer rather than logging, so `aru view:build` can put it on
// stderr and leave stdout for what the command produced.
func (p Pins) Warn(w io.Writer) {
	for _, r := range p.Retired {
		fmt.Fprintf(w, "arandu.toml:%d: %q is no longer a tool -- %s\n", r.line, r.key, r.because)
		fmt.Fprintf(w, "    Remove the line. It is ignored, and it will stop being accepted in the next major.\n")
	}
}

// ReadPins reads arandu.toml from the project root.
//
// The file is optional: without it the defaults in this package apply, which is
// what makes `aru new` produce something that builds before anyone has written a
// line of configuration. When it exists, it wins -- the version of a build tool
// belongs to the project, not to whoever happens to be running the command.
//
// Only [tools] is read. This is configuration, not a language: a file that grows
// its own syntax is a file somebody has to maintain forever.
func ReadPins(root string) (Pins, error) {
	b, err := os.ReadFile(filepath.Join(root, "arandu.toml"))
	if errors.Is(err, fs.ErrNotExist) {
		return Pins{}, nil
	}
	if err != nil {
		return Pins{}, fmt.Errorf("reading arandu.toml: %w", err)
	}

	var pins Pins
	var section string
	for i, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.Trim(line, "[]")
			continue
		}
		// [fonts.display] and [fonts.body] belong to `aru font:add`, which reads
		// them itself. Skipped here rather than rejected: this function knows
		// about build tools, and a section it does not own is not a typo.
		if section != "tools" && section != "arandu" {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return Pins{}, fmt.Errorf("arandu.toml:%d: expected key = \"value\"", i+1)
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		switch strings.TrimSpace(key) {
		case "aru":
			pins.Aru = value

		case "tailwindcss":
			pins.Tailwind = value

		case "templ":
			// Retired, not wrong. kyse replaced templ, and every project
			// generated before that has this line -- including the published
			// skeleton, which is what `aru new` clones today.
			//
			// Refusing it would break `aru view:build` and `aru build` in every
			// one of them at once, with no deprecation window. A retired key
			// warns in one minor and becomes an error in the next major, and a
			// build tool is the worst place to skip that: the first thing the
			// reader sees is a project that stopped compiling for a reason they
			// did not cause.
			pins.Retired = append(pins.Retired, retired{line: i + 1, key: "templ",
				because: "kyse replaced templ; the view compiler is part of aru and needs no download"})

		default:
			// A key that was never a tool is a typo, and a typo that is ignored
			// is a version that silently does not apply.
			return Pins{}, fmt.Errorf("arandu.toml:%d: unknown tool %q, want tailwindcss", i+1, strings.TrimSpace(key))
		}
	}
	return pins, nil
}
