package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/arandu-io/aru/internal/fonts"
)

// The [fonts.<role>] sections of arandu.toml, read and written.
//
// The font files are committed, so the manifest is not what makes the build
// reproducible -- the bytes are. What it is for is the three questions a diff
// cannot answer: which family this is, what licence came with it, and what
// `aru font:add` would fetch if somebody ran it again.
//
// It also carries the metrics, so `font:remove` can regenerate the stylesheet
// for the OTHER role without going back to the network to re-read numbers it
// already had.

// marker opens the block this file generates.
//
// A comment rather than a section, because what is generated is a comment AND
// the sections under it -- and a rule that only recognises sections leaves the
// comment behind, which is how four runs of `aru font:add` produced four copies
// of the same nine-line note.
const marker = "# --- aru font:add ---"

// legacyMarker is the first line of the block as it was written before the
// marker existed. It is recognised so that upgrading does not leave one orphan
// comment behind, and it costs a string compare per line of a file that is
// forty lines long.
const legacyMarker = "# The vendored faces."

// readInstalled parses the [fonts.*] sections.
//
// A missing file, or a file with no [fonts] section, is not an error: it is
// every project that has not added a font, which is most of them.
func readInstalled(root string) (map[fonts.Role]fonts.Installed, error) {
	out := map[fonts.Role]fonts.Installed{}

	b, err := os.ReadFile(filepath.Join(root, "arandu.toml"))
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading arandu.toml: %w", err)
	}

	var role fonts.Role
	current := fonts.Installed{}
	flush := func() {
		if role != "" && current.Family != "" {
			current.Role = role
			out[role] = current
		}
		current = fonts.Installed{}
	}

	for i, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flush()
			section := strings.Trim(line, "[]")
			name, ok := strings.CutPrefix(section, "fonts.")
			if !ok {
				role = ""
				continue
			}
			role = fonts.Role(name)
			if !role.Valid() {
				return nil, fmt.Errorf("arandu.toml:%d: %q is not a font role, want display or body", i+1, name)
			}
			continue
		}
		if role == "" {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("arandu.toml:%d: expected key = \"value\"", i+1)
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"`)

		switch key {
		case "family":
			current.Family = value
		case "weight":
			current.Weight = value
		case "subsets":
			current.Subsets = splitOn(value, ",")
		case "category":
			current.Category = value
		case "license":
			current.License = value
		case "files":
			// Semicolons, not commas: a unicode-range is a comma-separated list
			// itself, so splitting this on commas tore every entry into fifteen.
			// The round trip was broken from the first `aru font:add` onwards --
			// the file wrote correctly and the next read of it did not.
			current.FileList = splitOn(value, ";")
		case "ascent":
			current.Metrics.Ascent = number(value)
		case "descent":
			current.Metrics.Descent = number(value)
		case "line_gap":
			current.Metrics.LineGap = number(value)
		case "size_adjust":
			current.Metrics.SizeAdjust = number(value)
		default:
			// A key nobody reads is a key somebody expected to have an effect.
			return nil, fmt.Errorf("arandu.toml:%d: unknown font key %q", i+1, key)
		}
	}
	flush()
	return out, nil
}

// writeManifest replaces the [fonts.*] sections, leaving the rest of the file
// exactly as it was.
//
// Rewriting the whole file from a parsed model would drop every comment in it,
// and arandu.toml is mostly comments explaining why each pin exists. So the
// sections are cut out by line and the new ones appended.
func writeManifest(root string, list []fonts.Installed) error {
	path := filepath.Join(root, "arandu.toml")
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		b = nil
	} else if err != nil {
		return err
	}

	// Everything this function wrote last time comes out, and the marker is how
	// it knows where that started.
	//
	// It used to cut by section, which removed the [fonts.*] blocks and left the
	// comment above them -- so every run appended a second copy of a nine-line
	// note, and a project whose font was changed four times had four of them.
	// The generated block is not only sections, so a section-shaped rule cannot
	// find all of it.
	var kept []string
	dropping := false
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)

		// The marker, or the first line of a block written before the marker
		// existed. Without the second, the first run of a newer CLI cannot find
		// what an older one wrote, and leaves an orphan comment above the block
		// it just replaced -- the very duplication this fixes, once, on upgrade.
		if strings.HasPrefix(line, marker) || strings.HasPrefix(line, legacyMarker) {
			dropping = true
			continue
		}
		// A section that is not ours ends the generated block. Nothing writes
		// one after it today, and relying on "to the end of the file" would make
		// that permanent by accident.
		if dropping && strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") &&
			!strings.HasPrefix(strings.Trim(line, "[]"), "fonts.") {
			dropping = false
		}
		if !dropping {
			kept = append(kept, raw)
		}
	}
	// The generated block carries its own leading comment, so any blank lines
	// the removal left at the end are dropped rather than accumulating one per
	// `aru font:add`.
	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}

	var b2 strings.Builder
	b2.WriteString(strings.Join(kept, "\n"))
	if len(kept) > 0 {
		b2.WriteString("\n")
	}

	if len(list) > 0 {
		b2.WriteString(`
` + marker + `
# The vendored faces. Written by ` + "`aru font:add`" + `, and the files themselves are
# under assets/fonts/ and committed -- so this section is not what makes the
# build reproducible. The bytes are.
#
# What it records is what a diff cannot answer: which family this is, what
# licence arrived with it, and what would be fetched if somebody ran the command
# again. The metrics are here so removing one face does not send the other back
# to the network for numbers already known.
`)
		for _, in := range list {
			fmt.Fprintf(&b2, "\n[fonts.%s]\nfamily = %q\nweight = %q\nsubsets = %q\n",
				in.Role, in.Family, in.Weight, strings.Join(in.Subsets, ","))
			if in.Category != "" {
				fmt.Fprintf(&b2, "category = %q\n", in.Category)
			}
			if in.License != "" {
				fmt.Fprintf(&b2, "license = %q\n", in.License)
			}
			fmt.Fprintf(&b2, "files = %q\n", strings.Join(in.FileList, ";"))
			if in.Metrics.Ascent > 0 {
				fmt.Fprintf(&b2, "ascent = %q\ndescent = %q\nline_gap = %q\n",
					trim(in.Metrics.Ascent), trim(in.Metrics.Descent), trim(in.Metrics.LineGap))
				if in.Metrics.SizeAdjust > 0 {
					fmt.Fprintf(&b2, "size_adjust = %q\n", trim(in.Metrics.SizeAdjust))
				}
			}
		}
	}

	return os.WriteFile(path, []byte(b2.String()), 0o644)
}

func splitOn(s, sep string) []string {
	var out []string
	for _, part := range strings.Split(s, sep) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func number(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

func trim(f float64) string {
	return strconv.FormatFloat(f, 'f', 2, 64)
}
