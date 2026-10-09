package doctor

import (
	"sort"

	"github.com/arandu-io/aru/internal/skills"
)

// skillState is what the two skill rules read: the skills the project
// carries, by name, and what each origin whose source is on disk hands to an
// application.
type skillState struct {
	local    map[string][]byte
	received []skills.Received
}

// readSkills reads the project's skills and the sources of its origins from
// disk, and starts nothing.
//
// The skeleton is an origin only once the project carries a skill whose
// header names it. A project created before skills recorded their source, or
// one that removed every skill the skeleton gave it, looks the same from here
// as a project that never had them, and five findings about files somebody
// deleted on purpose would be a report people learn to skip. A module is an
// origin whenever go.mod requires it: requiring it is the project asking for
// what it hands out.
//
// Anything that cannot be read is an origin with nothing to say, never a
// failure of the whole report.
func readSkills(root string) skillState {
	local, err := skills.Local(root)
	if err != nil {
		return skillState{}
	}
	state := skillState{local: local}
	origins, err := skills.Origins(root)
	if err != nil {
		return state
	}
	for _, origin := range origins {
		if origin.Module == skills.SkeletonModule && !carriesFrom(local, origin) {
			continue
		}
		dir, ok := origin.Dir()
		if !ok {
			continue
		}
		found, err := origin.Read(dir)
		if err != nil {
			continue
		}
		state.received = append(state.received, skills.Received{Origin: origin, Skills: found})
	}
	return state
}

// carriesFrom reports whether any of the project's skills names origin as its
// source, at any version.
func carriesFrom(local map[string][]byte, origin skills.Origin) bool {
	for _, content := range local {
		if origin.Names(skills.ParseHeader(content).Source) {
			return true
		}
	}
	return false
}

// distributed walks every skill an origin hands out, once per name, in the
// order of the origins and then by name: a name two origins hand out is the
// first one's, as it is for `aru skills:sync`.
func (s skillState) distributed(visit func(origin skills.Origin, skill skills.Skill)) {
	seen := map[string]bool{}
	for _, r := range s.received {
		found := append([]skills.Skill(nil), r.Skills...)
		sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
		for _, skill := range found {
			if seen[skill.Name] {
				continue
			}
			seen[skill.Name] = true
			visit(r.Origin, skill)
		}
	}
}
