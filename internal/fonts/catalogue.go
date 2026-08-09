package fonts

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The catalogue: what is available, so that choosing a font is not a matter of
// already knowing the name of one.
//
// There are close to two thousand families. A command that only accepts an exact
// name is a command you cannot use without a browser open beside it, which
// defeats the point of the font being installable from the terminal.

// catalogueURL is the index the source publishes. One request, about a
// megabyte, and it carries every family with its weights, its axes and its
// licence -- so `font:search` and `font:info` need nothing else.
const catalogueURL = "https://fonts.google.com/metadata/fonts"

// Entry is one family in the catalogue.
type Entry struct {
	Family string
	// Category is the source's classification: Serif, Sans Serif, Display,
	// Handwriting, Monospace.
	Category string
	// Subsets are the scripts it covers. "menu" is the source's own preview
	// subset and is filtered out -- it is not something anybody installs.
	Subsets []string
	// Weights are the static instances, ascending.
	Weights []int
	// Axes are the variable axes, by tag: wght, opsz, slnt. Empty means the
	// family is static, and the difference decides what --weight accepts.
	Axes []string
	// Designers is who drew it.
	Designers []string
	// Popularity is the source's rank, 1 being the most used. It is the sort
	// order because a search for "sans" that answers alphabetically answers with
	// families nobody has heard of.
	Popularity int
	// Size is the whole family in bytes, every weight and every subset. It is
	// NOT what installing costs -- one weight of one subset is a fraction of it
	// -- and it is here as the only comparable number the catalogue publishes.
	Size int
	// OpenSource is false for the handful the source hosts under other terms.
	OpenSource bool
}

// Variable reports whether the family has axes.
func (e Entry) Variable() bool { return len(e.Axes) > 0 }

// WeightRange is what --weight would take: "400" for a static family with one
// instance, "400..700" for a variable one, "400;700" for a static one with two.
func (e Entry) WeightRange() string {
	if len(e.Weights) == 0 {
		return "400"
	}
	if e.Variable() {
		return fmt.Sprintf("%d..%d", e.Weights[0], e.Weights[len(e.Weights)-1])
	}
	parts := make([]string, 0, len(e.Weights))
	for _, w := range e.Weights {
		parts = append(parts, strconv.Itoa(w))
	}
	return strings.Join(parts, ";")
}

// Catalogue downloads and parses the index.
func Catalogue(client *http.Client) ([]Entry, error) {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	body, err := get(client, catalogueURL, modernAgent)
	if err != nil {
		return nil, err
	}

	// The answer is JSON behind an anti-hijacking prefix, which is not JSON and
	// has to come off before anything can decode it.
	raw := strings.TrimLeft(string(body), ")]}'\n\r ")

	var doc struct {
		List []struct {
			Family     string                 `json:"family"`
			Category   string                 `json:"category"`
			Subsets    []string               `json:"subsets"`
			Fonts      map[string]any         `json:"fonts"`
			Axes       []struct{ Tag string } `json:"axes"`
			Designers  []string               `json:"designers"`
			Popularity int                    `json:"popularity"`
			Size       int                    `json:"size"`
			OpenSource bool                   `json:"isOpenSource"`
		} `json:"familyMetadataList"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, fmt.Errorf("the catalogue is not the shape this expects: %w", err)
	}

	out := make([]Entry, 0, len(doc.List))
	for _, f := range doc.List {
		e := Entry{
			Family: f.Family, Category: f.Category, Designers: f.Designers,
			Popularity: f.Popularity, Size: f.Size, OpenSource: f.OpenSource,
		}
		for _, s := range f.Subsets {
			// "menu" is the source's preview subset: a handful of glyphs for
			// drawing the family's own name on its listing page. Nobody installs
			// it, and offering it would be offering a font that draws six
			// letters.
			if s != "menu" {
				e.Subsets = append(e.Subsets, s)
			}
		}
		for weight := range f.Fonts {
			// The keys are "400", "700italic", and italics are a separate
			// decision this command does not make -- one face, one style.
			if n, err := strconv.Atoi(weight); err == nil {
				e.Weights = append(e.Weights, n)
			}
		}
		sort.Ints(e.Weights)
		for _, a := range f.Axes {
			e.Axes = append(e.Axes, a.Tag)
		}
		out = append(out, e)
	}
	return out, nil
}

// Search filters the catalogue.
//
// The query matches the family name, case-insensitively, anywhere in it --
// "grot" finds Space Grotesk and Bricolage Grotesque. An empty query matches
// everything, which is what makes `font:search --category serif` a way to
// browse rather than a way to fail.
//
// The order is the source's popularity. Alphabetical would answer a search for
// "sans" with families nobody has heard of, and a list nobody recognises is a
// list nobody picks from.
func Search(all []Entry, query, category string, variableOnly bool) []Entry {
	query = strings.ToLower(strings.TrimSpace(query))
	category = strings.ToLower(strings.TrimSpace(category))

	var out []Entry
	for _, e := range all {
		if query != "" && !strings.Contains(strings.ToLower(e.Family), query) {
			continue
		}
		if category != "" && !strings.EqualFold(compact(e.Category), compact(category)) {
			continue
		}
		if variableOnly && !e.Variable() {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Popularity < out[j].Popularity })
	return out
}

// Find returns one family by exact name, case-insensitively.
func Find(all []Entry, family string) (Entry, bool) {
	for _, e := range all {
		if strings.EqualFold(e.Family, family) {
			return e, true
		}
	}
	return Entry{}, false
}

// Nearest is what to suggest when a name matched nothing.
//
// A refusal that only says "no such family" is a refusal that sends somebody to
// a browser. These are the families whose names contain any word of what was
// asked for, which catches the two mistakes people actually make: a missing word
// and a wrong one.
func Nearest(all []Entry, family string, limit int) []string {
	words := strings.Fields(strings.ToLower(family))
	seen := map[string]bool{}
	var out []string

	for _, e := range Search(all, "", "", false) {
		lower := strings.ToLower(e.Family)
		for _, w := range words {
			if len(w) >= 3 && strings.Contains(lower, w) && !seen[e.Family] {
				seen[e.Family] = true
				out = append(out, e.Family)
			}
		}
		if len(out) >= limit {
			break
		}
	}
	return out
}

// compact removes the spaces and hyphens a category is spelled with, so
// "sans-serif", "Sans Serif" and "sansserif" are one answer.
func compact(s string) string {
	return strings.NewReplacer(" ", "", "-", "", "_", "").Replace(strings.ToLower(s))
}

// Categories is the closed set the source classifies with, for the help text.
var Categories = []string{"serif", "sans-serif", "display", "handwriting", "monospace"}
