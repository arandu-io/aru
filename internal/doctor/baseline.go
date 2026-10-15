package doctor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// Baseline is the warnings a project carried on the day it was written,
// counted by rule and file.
//
// It is a ratchet, not a suppression: every finding is still reported, and
// what the baseline changes is only which warnings fail a strict run. A
// warning in a file whose count for that rule is within the baseline does not;
// one more than the baseline holds does, and an error always does.
//
// The key is the rule and the file, with a count, rather than the line or the
// message. A line moves with every edit above it, so a baseline keyed by line
// would report old warnings as new after any change to the file. A message
// names the symbols involved, so adding one more use of an already reported
// import would change it and read as a new warning. A count per file is what
// survives both, and what it cannot tell apart -- one warning fixed and
// another of the same rule added in the same file -- leaves the file no worse
// than the baseline already accepted.
type Baseline struct {
	// Profile is the profile the warnings were counted on. The profile rules
	// report on one and not the other, so a baseline is read only on its own.
	Profile Profile `json:"profile"`
	// Warnings is sorted by file and then by rule, one entry per pair, every
	// count above zero.
	Warnings []BaselineEntry `json:"warnings"`
}

// BaselineEntry is how many warnings of one rule one file carried.
type BaselineEntry struct {
	File  string `json:"file"`
	Rule  string `json:"rule"`
	Count int    `json:"count"`
}

// BaselineDelta is a rule and file whose count differs from the baseline.
type BaselineDelta struct {
	File     string
	Rule     string
	Found    int
	Baseline int
}

// BaselineComparison is a run measured against a baseline.
type BaselineComparison struct {
	// Above holds the pairs with more warnings than the baseline allows,
	// including the pairs it does not name at all.
	Above []BaselineDelta
	// Below holds the pairs with fewer, including the ones that are gone: the
	// entries the baseline can shrink to.
	Below []BaselineDelta
}

// New is how many warnings this run found above the baseline.
func (c BaselineComparison) New() int {
	n := 0
	for _, d := range c.Above {
		n += d.Found - d.Baseline
	}
	return n
}

type baselineKey struct{ file, rule string }

// NewBaseline counts the warnings among findings on profile. Errors are left
// out: an error fails every run, and a baseline that recorded one would be a
// file claiming to accept what nothing accepts.
func NewBaseline(profile Profile, findings []Finding) Baseline {
	counts := countWarnings(findings)
	b := Baseline{Profile: profile, Warnings: make([]BaselineEntry, 0, len(counts))}
	for k, n := range counts {
		b.Warnings = append(b.Warnings, BaselineEntry{File: k.file, Rule: k.rule, Count: n})
	}
	sort.Slice(b.Warnings, func(i, j int) bool {
		a, c := b.Warnings[i], b.Warnings[j]
		if a.File != c.File {
			return a.File < c.File
		}
		return a.Rule < c.Rule
	})
	return b
}

func countWarnings(findings []Finding) map[baselineKey]int {
	counts := map[baselineKey]int{}
	for _, f := range findings {
		if f.Severity == Warning {
			counts[baselineKey{f.File, f.Rule}]++
		}
	}
	return counts
}

// Compare measures findings against the baseline. Both lists come back sorted
// by file and then by rule, so a report reads the same on every run.
func (b Baseline) Compare(findings []Finding) BaselineComparison {
	found := countWarnings(findings)
	allowed := map[baselineKey]int{}
	for _, e := range b.Warnings {
		allowed[baselineKey{e.File, e.Rule}] = e.Count
	}
	var c BaselineComparison
	for k, n := range found {
		if n > allowed[k] {
			c.Above = append(c.Above, BaselineDelta{File: k.file, Rule: k.rule, Found: n, Baseline: allowed[k]})
		}
	}
	for k, n := range allowed {
		if found[k] < n {
			c.Below = append(c.Below, BaselineDelta{File: k.file, Rule: k.rule, Found: found[k], Baseline: n})
		}
	}
	for _, list := range [][]BaselineDelta{c.Above, c.Below} {
		sort.Slice(list, func(i, j int) bool {
			if list[i].File != list[j].File {
				return list[i].File < list[j].File
			}
			return list[i].Rule < list[j].Rule
		})
	}
	return c
}

// Marshal answers the baseline as JSON, one entry per line, sorted, with a
// trailing newline. One line per entry is what makes a review of the file
// read as the change it is: a count that went down is one line of the diff.
func (b Baseline) Marshal() []byte {
	var buf bytes.Buffer
	profile, _ := json.Marshal(string(b.Profile))
	fmt.Fprintf(&buf, "{\n  \"profile\": %s,\n  \"warnings\": [", profile)
	for i, e := range b.Warnings {
		file, _ := json.Marshal(e.File)
		rule, _ := json.Marshal(e.Rule)
		sep := ","
		if i == len(b.Warnings)-1 {
			sep = ""
		}
		fmt.Fprintf(&buf, "\n    {\"file\": %s, \"rule\": %s, \"count\": %d}%s", file, rule, e.Count, sep)
	}
	if len(b.Warnings) > 0 {
		buf.WriteString("\n  ")
	}
	buf.WriteString("]\n}\n")
	return buf.Bytes()
}

// WriteBaseline writes the baseline to path.
func WriteBaseline(path string, b Baseline) error {
	return os.WriteFile(path, b.Marshal(), 0o644)
}

// ReadBaseline reads the baseline at path.
//
// A file that is not exactly the shape Marshal writes is refused rather than
// read as far as it goes: an unknown field, a profile that does not exist, a
// pair written twice or a count below one is a file somebody edited by hand
// into something this command would otherwise have to guess about, and a
// guess here decides whether CI passes.
func ReadBaseline(path string) (Baseline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Baseline{}, err
	}
	var b Baseline
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return Baseline{}, fmt.Errorf("%s: %w", path, err)
	}
	if _, err := ParseProfile(string(b.Profile)); err != nil {
		return Baseline{}, fmt.Errorf("%s: %w", path, err)
	}
	seen := map[baselineKey]bool{}
	for _, e := range b.Warnings {
		k := baselineKey{e.File, e.Rule}
		switch {
		case e.File == "" || e.Rule == "":
			return Baseline{}, fmt.Errorf("%s: an entry has no file or no rule", path)
		case e.Count < 1:
			return Baseline{}, fmt.Errorf("%s: %s [%s] has a count of %d; an entry that allows nothing is removed instead", path, e.File, e.Rule, e.Count)
		case seen[k]:
			return Baseline{}, fmt.Errorf("%s: %s [%s] is written twice", path, e.File, e.Rule)
		}
		seen[k] = true
	}
	return b, nil
}
