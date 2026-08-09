package toolchain

import (
	"fmt"
	"strconv"
	"strings"
)

// CheckVersion refuses a CLI older than the project asks for.
//
// It is here because of what the alternative looks like. A project written with
// a current CLI, opened with an older one, is refused view by view -- sixty
// messages about comments and interpolations that are correct, each naming a
// line that is fine. Nothing in that output says "your aru is old", so the
// reader concludes the project is broken, and the first thing they try is
// editing markup that was never wrong.
//
// The message this returns names the two versions and the one command that
// fixes it. That is the whole feature.
//
// A CLI built from source reports "dev" and is never refused: somebody running
// their own build is somebody who knows which build it is.
func CheckVersion(running, floor string) error {
	if floor == "" || running == "" || running == "dev" {
		return nil
	}

	have, ok := parseVersion(running)
	if !ok {
		return nil
	}
	want, ok := parseVersion(floor)
	if !ok {
		// A floor nobody can parse is a floor nobody wrote on purpose. Refusing
		// on it would break a project over a typo in a file the person may not
		// have written.
		return nil
	}

	if older(have, want) {
		return fmt.Errorf(`this project needs aru %s and this is aru %s.

arandu.toml asks for it, and the difference is not cosmetic: an older CLI
refuses views that are correct, one message per line, and none of them says the
CLI is what is old.

    brew upgrade arandu-io/tap/aru

or, without Homebrew:

    go install github.com/arandu-io/aru@latest`, floor, running)
	}
	return nil
}

// parseVersion reads "v1.2.3" or "1.2.3".
func parseVersion(s string) ([3]int, bool) {
	var out [3]int

	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(s), "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// older reports whether a comes before b.
func older(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
