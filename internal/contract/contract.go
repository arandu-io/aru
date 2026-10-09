// Package contract is the implementation contract of an Arandu application as
// data: for each kind of code, where it lives, what it imports, its
// signature, what it may and may not call, which generator writes it and
// which doctor rules verify it; and the recipes a feature is built by.
//
// It is one embedded file read by three callers -- `aru mcp`, the doctor and
// the skill make:module writes -- so the three answer the same question the
// same way. A copy of the table in any of them would be the second answer.
package contract

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
)

//go:embed contract.json
var source []byte

// Import is one package a kind of code imports, by its canonical path, with
// the names it uses from it.
type Import struct {
	Path  string   `json:"path"`
	Names []string `json:"names"`
}

// Card is the sheet of one kind of code.
type Card struct {
	Kind      string   `json:"kind"`
	Title     string   `json:"title"`
	Path      string   `json:"path"`
	Imports   []Import `json:"imports"`
	Signature string   `json:"signature"`
	CalledBy  []string `json:"calledBy"`
	May       []string `json:"may"`
	MayNot    []string `json:"mayNot"`
	Errors    string   `json:"errors"`
	// Generators are the aru commands that write it, empty when none does.
	Generators []string `json:"generators"`
	// Custom says where the region a regeneration keeps is, when there is one.
	Custom string `json:"custom,omitempty"`
	// Example is the file in the skeleton that has this shape, empty when the
	// skeleton carries none.
	Example string `json:"example"`
	// VerifiedBy are the doctor rules that report code of this kind written
	// another way.
	VerifiedBy []string `json:"verifiedBy"`
}

// Recipe is the order a kind of feature is built in.
type Recipe struct {
	Name  string   `json:"name"`
	Title string   `json:"title"`
	Steps []string `json:"steps"`
	// Cards are the kinds of code the recipe touches, in Card.Kind.
	Cards []string `json:"cards"`
}

type document struct {
	Cards   []Card   `json:"cards"`
	Recipes []Recipe `json:"recipes"`
}

var loaded = mustLoad(source)

// mustLoad decodes the embedded file. A file that does not decode, or that
// names a card a recipe cannot find, is a defect of this build, and saying so
// at start is better than answering half a contract.
func mustLoad(body []byte) document {
	var d document
	if err := json.Unmarshal(body, &d); err != nil {
		panic(fmt.Sprintf("contract: the embedded contract does not decode: %v", err))
	}
	kinds := map[string]bool{}
	for _, c := range d.Cards {
		if c.Kind == "" || kinds[c.Kind] {
			panic(fmt.Sprintf("contract: card %q is empty or declared twice", c.Kind))
		}
		kinds[c.Kind] = true
	}
	for _, r := range d.Recipes {
		for _, k := range r.Cards {
			if !kinds[k] {
				panic(fmt.Sprintf("contract: recipe %q names card %q, which does not exist", r.Name, k))
			}
		}
	}
	return d
}

// Cards answers every card, in the file's order.
func Cards() []Card { return append([]Card(nil), loaded.Cards...) }

// Lookup answers the card of one kind.
func Lookup(kind string) (Card, bool) {
	for _, c := range loaded.Cards {
		if c.Kind == kind {
			return c, true
		}
	}
	return Card{}, false
}

// Kinds answers every card's kind, in the file's order.
func Kinds() []string {
	out := make([]string, 0, len(loaded.Cards))
	for _, c := range loaded.Cards {
		out = append(out, c.Kind)
	}
	return out
}

// Recipes answers every recipe, in the file's order.
func Recipes() []Recipe { return append([]Recipe(nil), loaded.Recipes...) }

// RecipeNamed answers one recipe by name.
func RecipeNamed(name string) (Recipe, bool) {
	for _, r := range loaded.Recipes {
		if r.Name == name {
			return r, true
		}
	}
	return Recipe{}, false
}

// RecipeNames answers every recipe's name, in the file's order.
func RecipeNames() []string {
	out := make([]string, 0, len(loaded.Recipes))
	for _, r := range loaded.Recipes {
		out = append(out, r.Name)
	}
	return out
}

// ForRule answers the card a doctor rule verifies: the first card, in the
// file's order, whose VerifiedBy names it.
func ForRule(rule string) (Card, bool) {
	for _, c := range loaded.Cards {
		for _, r := range c.VerifiedBy {
			if r == rule {
				return c, true
			}
		}
	}
	return Card{}, false
}

// Rules answers every rule any card names, sorted and without repeats.
func Rules() []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range loaded.Cards {
		for _, r := range c.VerifiedBy {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	sort.Strings(out)
	return out
}
