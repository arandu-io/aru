package skills

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Action is what syncing one skill would do.
type Action uint8

const (
	// Create writes a skill the project does not have.
	Create Action = iota + 1
	// Update rewrites a stamped skill with what its origin distributes now,
	// custom blocks carried forward.
	Update
	// Unchanged is a skill already what a sync would write.
	Unchanged
	// Conflict is a stamped skill edited outside its custom blocks. It is left
	// as it is unless the plan is forced.
	Conflict
	// Kept is a skill a sync never touches: one with no source in its header,
	// which is the project's own, or one that names another origin.
	Kept
)

// String is the word a preview prints.
func (a Action) String() string {
	switch a {
	case Create:
		return "create"
	case Update:
		return "update"
	case Unchanged:
		return "unchanged"
	case Conflict:
		return "conflict"
	case Kept:
		return "kept"
	}
	return "unknown"
}

// Change is what syncing one skill would do, and what it would write.
type Change struct {
	// Path is where the skill sits, slash-separated and relative to the root.
	Path string
	// Origin is the label of the origin that distributes it.
	Origin string
	Action Action
	// Reason says why a Conflict or a Kept skill is left as it is.
	Reason string
	// Existing is what is on disk, nil when nothing is.
	Existing []byte
	// Content is what a sync would write.
	Content []byte
}

// Received is one origin and the skills it hands to an application.
type Received struct {
	Origin Origin
	Skills []Skill
}

// Plan works out what syncing the received skills into the project rooted at
// root would do, and writes nothing. Force turns each Conflict into an Update;
// a Kept skill stays kept.
//
// The changes come back ordered by path. A name two origins distribute is
// taken from the first, and the second is reported as Kept.
func Plan(root string, received []Received, force bool) ([]Change, error) {
	var out []Change
	taken := map[string]string{}
	for _, r := range received {
		for _, skill := range r.Skills {
			change, err := plan(root, r.Origin, skill, force)
			if err != nil {
				return nil, err
			}
			if first, seen := taken[change.Path]; seen {
				change.Action, change.Content = Kept, nil
				change.Reason = "also distributed by " + first + ", which is read first"
			} else {
				taken[change.Path] = r.Origin.Label()
			}
			out = append(out, change)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func plan(root string, origin Origin, skill Skill, force bool) (Change, error) {
	change := Change{Path: skill.Path(), Origin: origin.Label()}

	incoming, err := Stamp(skill.Content, origin.Label())
	if err != nil {
		change.Action = Kept
		change.Reason = fmt.Sprintf("%s distributes it with no place to record its source: %v", origin.Label(), err)
		return change, nil
	}

	existing, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(change.Path)))
	if errors.Is(err, fs.ErrNotExist) {
		change.Action, change.Content = Create, incoming
		return change, nil
	}
	if err != nil {
		return Change{}, err
	}
	change.Existing = existing

	source := ParseHeader(existing).Source
	switch {
	case source == "":
		change.Action = Kept
		change.Reason = "no source in its header, so it is the project's own; move it aside to receive the one " + origin.Label() + " distributes"
		return change, nil
	case !origin.Names(source):
		change.Action = Kept
		change.Reason = "its header names " + source + " as its source, not " + origin.repository()
		return change, nil
	}

	change.Content = Merge(existing, incoming)
	switch {
	case bytes.Equal(change.Content, existing):
		change.Action = Unchanged
	case Edited(existing) && !force:
		change.Action = Conflict
		change.Reason = "edited outside its custom block; --force replaces that edit and keeps the block"
	default:
		change.Action = Update
	}
	return change, nil
}

// Apply writes what the plan creates and updates, and answers the paths it
// wrote. Nothing else is touched.
func Apply(root string, changes []Change) ([]string, error) {
	var written []string
	for _, c := range changes {
		if c.Action != Create && c.Action != Update {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(c.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return written, err
		}
		if err := os.WriteFile(path, c.Content, 0o644); err != nil {
			return written, err
		}
		written = append(written, c.Path)
	}
	return written, nil
}
