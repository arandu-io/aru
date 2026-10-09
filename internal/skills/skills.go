// Package skills reads, stamps and compares the skills an Arandu project
// receives from somewhere else: the skeleton it was created from, and the
// modules its go.mod requires.
//
// A skill is a directory under .agents/skills holding one SKILL.md, whose
// frontmatter a coding assistant reads to decide whether the procedure applies.
// A skill that arrived from somewhere says so in that frontmatter, under
// metadata, the one key the format leaves to whoever writes the file:
//
//	metadata:
//	  source: owner/repo@v1.2.3
//	  digest: sha256:9f2c...
//
// source is the module and the version the file came from. digest is the
// sha256 of the file with those two lines and every custom block left out,
// taken when the file was written. Comparing a file with its own digest tells
// a skill nobody touched from one somebody edited, and an edit made between
// the custom markers is not an edit.
package skills

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
)

// Dir is where a project keeps its skills, slash-separated and relative to the
// project root. It is the directory the coding assistants read from.
const Dir = ".agents/skills"

// File is the name of the one file a skill directory holds.
const File = "SKILL.md"

// The markers of a custom block in a skill.
//
// They are HTML comments because a skill is Markdown, and a comment is the one
// thing Markdown renders as nothing. Each sits alone on its line: a marker
// quoted inside a sentence is prose about the marker, not a block.
const (
	BeginCustom = "<!-- arandu:begin custom -->"
	EndCustom   = "<!-- arandu:end custom -->"
)

// customBlock matches one custom block: the opening marker on a line of its
// own, the body, and the closing marker on a line of its own. The body is the
// first group and ends with the line break before the closing marker.
var customBlock = regexp.MustCompile(`(?ms)^[ \t]*<!-- arandu:begin custom -->[ \t]*\n(.*?)^[ \t]*<!-- arandu:end custom -->[ \t]*$`)

// wholeBlock is a custom block with the line break that ends it, which is what
// canonical takes out.
var wholeBlock = regexp.MustCompile(customBlock.String() + `\n?`)

// blankRun is two or more blank lines in a row.
var blankRun = regexp.MustCompile(`\n{3,}`)

// The two metadata keys a stamp writes.
const (
	sourceKey = "source"
	digestKey = "digest"
)

// ErrNoFrontmatter is returned by Stamp for a file that does not open with a
// frontmatter block: there is nowhere to record where it came from, and a
// coding assistant would not read it as a skill either.
var ErrNoFrontmatter = errors.New("the file does not open with a --- frontmatter block")

// ErrInlineMetadata is returned by Stamp when the frontmatter's metadata is
// written on one line, as in `metadata: {}`. The stamp is two lines of a block,
// and rewriting a flow mapping would mean parsing one.
var ErrInlineMetadata = errors.New("the frontmatter's metadata is written inline rather than as indented key: value lines")

// Header is what a skill's frontmatter says about the skill and about where it
// came from.
type Header struct {
	// Name is the frontmatter name.
	Name string
	// Source is metadata.source: owner/repo@version for a skill that came from
	// a module, aru@version for one the generator wrote, empty for a skill the
	// project wrote itself.
	Source string
	// Digest is metadata.digest, "sha256:" and the hex digest.
	Digest string
	// Audience is metadata.audience. A module marks the skills an application
	// receives with "app"; its other skills are for whoever maintains it.
	Audience string
}

// ParseHeader reads the header of a skill. A file without frontmatter has an
// empty header, which is the header of a skill nobody stamped.
//
// The frontmatter is read as lines rather than as YAML: the fields it needs
// are a top-level name and the key: value lines under metadata, and a skill's
// description is free text that a strict YAML parser refuses more often than a
// coding assistant does.
func ParseHeader(content []byte) Header {
	lines := strings.Split(normalize(content), "\n")
	end, ok := closing(lines)
	if !ok {
		return Header{}
	}
	var h Header
	inMetadata := false
	for _, line := range lines[1:end] {
		if line == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		key, value, found := keyValue(line)
		if !indented(line) {
			inMetadata = found && key == "metadata" && value == ""
			if found && key == "name" {
				h.Name = value
			}
			continue
		}
		if !inMetadata || !found {
			continue
		}
		switch key {
		case sourceKey:
			h.Source = value
		case digestKey:
			h.Digest = value
		case "audience":
			h.Audience = value
		}
	}
	return h
}

// Digest is "sha256:" and the hex sha256 of the file's canonical form: line
// endings as line feeds, the source and digest lines of the frontmatter left
// out, every custom block left out with its markers, a run of blank lines read
// as one, and no trailing blank space.
//
// The markers go with the block, and blank lines count once, so that where a
// block sits is not an edit: Merge appends a block to the end of a file whose
// source has no place for it, and somebody adding one sets it apart with a
// blank line on each side. Neither changes what the file says.
func Digest(content []byte) string {
	sum := sha256.Sum256([]byte(canonical(content)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Edited reports whether a stamped skill was changed outside its custom
// blocks since it was written. A skill with no digest was never stamped, and
// is not edited in this sense: it is not a copy of anything.
func Edited(content []byte) bool {
	recorded := ParseHeader(content).Digest
	return recorded != "" && recorded != Digest(content)
}

// Stamp records in the frontmatter where the file came from, and its digest.
//
// A source and a digest already there are replaced, so stamping twice is
// stamping once. Line endings come back as line feeds.
func Stamp(content []byte, source string) ([]byte, error) {
	text := normalize(content)
	lines := strings.Split(text, "\n")
	end, ok := closing(lines)
	if !ok {
		return nil, ErrNoFrontmatter
	}
	front, metadata, indent, err := strip(lines[1:end])
	if err != nil {
		return nil, err
	}
	stamp := []string{indent + sourceKey + ": " + source, indent + digestKey + ": " + Digest([]byte(text))}
	if metadata < 0 {
		front = append(front, "metadata:")
		front = append(front, stamp...)
	} else {
		front = append(front[:metadata+1], append(stamp, front[metadata+1:]...)...)
	}

	out := append([]string{lines[0]}, front...)
	out = append(out, lines[end:]...)
	return []byte(strings.Join(out, "\n")), nil
}

// Merge carries the custom blocks of the file on disk into the one about to
// replace it, by position.
//
// A block the incoming file has no place for is appended at its end, markers
// and all, rather than dropped: what is written between the markers belongs to
// whoever wrote it, and a source that has no block yet is not a reason to lose
// it.
func Merge(existing, incoming []byte) []byte {
	old := customBlock.FindAllSubmatchIndex(existing, -1)
	if len(old) == 0 {
		return incoming
	}

	var out bytes.Buffer
	last := 0
	slots := customBlock.FindAllSubmatchIndex(incoming, -1)
	for i, slot := range slots {
		out.Write(incoming[last:slot[2]])
		if i < len(old) {
			out.Write(existing[old[i][2]:old[i][3]])
		} else {
			out.Write(incoming[slot[2]:slot[3]])
		}
		last = slot[3]
	}
	out.Write(incoming[last:])
	if len(old) <= len(slots) {
		return out.Bytes()
	}

	merged := bytes.TrimRight(out.Bytes(), "\n")
	for _, block := range old[len(slots):] {
		merged = append(merged, "\n\n"...)
		merged = append(merged, existing[block[0]:block[1]]...)
	}
	return append(merged, '\n')
}

// canonical is the form Digest hashes. See Digest for what it leaves out.
func canonical(content []byte) string {
	text := normalize(content)
	lines := strings.Split(text, "\n")
	if end, ok := closing(lines); ok {
		front, metadata, _, _ := strip(lines[1:end])
		if metadata >= 0 && !hasChildren(front, metadata) {
			front = append(front[:metadata], front[metadata+1:]...)
		}
		out := append([]string{lines[0]}, front...)
		text = strings.Join(append(out, lines[end:]...), "\n")
	}
	text = wholeBlock.ReplaceAllString(text, "")
	text = blankRun.ReplaceAllString(text, "\n\n")
	return strings.TrimRight(text, " \t\n")
}

// strip answers the frontmatter lines without the source and digest of a
// stamp, the index of the metadata key among them (-1 when there is none), and
// the indentation its children use.
func strip(front []string) (kept []string, metadata int, indent string, err error) {
	metadata, indent = -1, "  "
	inMetadata, sawChild := false, false
	for _, line := range front {
		key, value, found := keyValue(line)
		if !indented(line) && line != "" {
			inMetadata = found && key == "metadata"
			if inMetadata {
				if value != "" {
					return nil, 0, "", ErrInlineMetadata
				}
				metadata = len(kept)
			}
			kept = append(kept, line)
			continue
		}
		if inMetadata && found {
			if !sawChild {
				indent, sawChild = line[:len(line)-len(strings.TrimLeft(line, " \t"))], true
			}
			if key == sourceKey || key == digestKey {
				continue
			}
		}
		kept = append(kept, line)
	}
	return kept, metadata, indent, nil
}

// hasChildren reports whether the key at index has an indented line under it.
func hasChildren(front []string, index int) bool {
	for _, line := range front[index+1:] {
		if line == "" {
			continue
		}
		return indented(line)
	}
	return false
}

// closing answers the index of the line that closes the frontmatter, which
// opens on the first line.
func closing(lines []string) (int, bool) {
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t") != "---" {
		return 0, false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") == "---" {
			return i, true
		}
	}
	return 0, false
}

// keyValue splits a `key: value` line, with the value unquoted.
func keyValue(line string) (key, value string, ok bool) {
	key, value, ok = strings.Cut(strings.TrimSpace(line), ":")
	if !ok || key == "" || strings.ContainsAny(key, " \t") {
		return "", "", false
	}
	value = strings.TrimSpace(value)
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		value = value[1 : len(value)-1]
	}
	return key, value, true
}

func indented(line string) bool {
	return strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
}

// normalize answers the content with line feeds for line endings, so a file
// checked out on Windows has the digest it had where it was written.
func normalize(content []byte) string {
	return strings.ReplaceAll(string(content), "\r\n", "\n")
}
