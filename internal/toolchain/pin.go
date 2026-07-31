package toolchain

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Pins are the tool versions a project asks for.
type Pins struct {
	Templ    string
	Tailwind string
}

// ReadPins reads arandu.toml from the project root.
//
// The file is optional: without it the defaults in this package apply, which is
// what makes `aru new` produce something that builds before anyone has written a
// line of configuration. When it exists, it wins -- the version of a build tool
// belongs to the project, not to whoever happens to be running the command.
//
// Only [tools] is read. This is configuration, not a language: a file that grows
// its own syntax is a file somebody has to maintain forever (RULE 15).
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
		if section != "tools" {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return Pins{}, fmt.Errorf("arandu.toml:%d: expected key = \"value\"", i+1)
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		switch strings.TrimSpace(key) {
		case "templ":
			pins.Templ = value
		case "tailwindcss":
			pins.Tailwind = value
		default:
			// An unknown key is a typo, and a typo that is ignored is a version
			// that silently does not apply.
			return Pins{}, fmt.Errorf("arandu.toml:%d: unknown tool %q, want templ or tailwindcss", i+1, strings.TrimSpace(key))
		}
	}
	return pins, nil
}
