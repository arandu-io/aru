// Command ypygen writes the YPY attribute table the language server offers.
//
// The table was typed by hand once and covered thirteen of the attributes the
// runtime reads, which is the failure mode a hand-maintained list has: it is
// correct on the day it is written and silently short forever after. This
// reads the editor metadata YPY publishes for its own tooling and emits the
// same facts as Go.
//
// It refuses to emit an attribute the served runtime does not read. Completion
// that offers an attribute the bundle in the page ignores is worse than no
// completion, because nothing reports it: the markup is valid HTML, the build is
// green, and the element simply does nothing. The metadata describes the core
// and every extension, and the application serves the core alone, so what the
// bundle does not read is left out and listed on the standard error stream. The
// check is textual and answers about the file that is actually served rather
// than about a version number written down somewhere: an attribute is read
// when the bundle spells its name as a whole string, when it is a request verb
// the bundle builds from its list of them, when the bundle spells the
// attribute's prefix and its last word apart, or when it is the name inside a
// selector.
//
// Usage:
//
//	go run ./internal/lsp/ypygen -metadata <ypy.web-types.json> -runtime <ypy.min.js> -out <file.go>
//
// The metadata and runtime paths are arguments because neither file belongs to
// this repository: they are read where they are, and nothing of them is
// vendored.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
)

func main() {
	metadata := flag.String("metadata", "", "path to the YPY web-types JSON")
	runtime := flag.String("runtime", "", "path to the ypy.min.js the project serves")
	out := flag.String("out", "", "path of the Go file to write")
	flag.Parse()

	if *metadata == "" || *runtime == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: ypygen -metadata <ypy.web-types.json> -runtime <ypy.min.js> -out <file.go>")
		os.Exit(2)
	}
	if err := run(*metadata, *runtime, *out); err != nil {
		fmt.Fprintln(os.Stderr, "ypygen:", err)
		os.Exit(1)
	}
}

func run(metadataPath, runtimePath, outPath string) error {
	metadataBytes, err := os.ReadFile(metadataPath)
	if err != nil {
		return err
	}
	var metadata webTypes
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		return fmt.Errorf("decode %s: %w", metadataPath, err)
	}
	if len(metadata.Contributions.HTML.Attributes) == 0 {
		return fmt.Errorf("%s declares no HTML attribute", metadataPath)
	}

	bundle, err := os.ReadFile(runtimePath)
	if err != nil {
		return err
	}
	served := string(bundle)
	servedVersion, err := runtimeVersion(served)
	if err != nil {
		return fmt.Errorf("%s: %w", runtimePath, err)
	}

	attributes := append([]webTypeAttribute(nil), metadata.Contributions.HTML.Attributes...)
	sort.Slice(attributes, func(i, j int) bool { return attributes[i].Name < attributes[j].Name })

	reads := newReader(served)
	var unread []string
	rows := make([]row, 0, len(attributes))
	for _, attribute := range attributes {
		label, found := reads.label(attribute.Name)
		if !found {
			unread = append(unread, attribute.Name)
			continue
		}
		rows = append(rows, row{
			Name:          label,
			Detail:        firstSentence(attribute.Description, attribute.Name),
			Documentation: flatten(firstParagraph(attribute.Description)),
			Deprecated:    attribute.Deprecated,
		})
	}
	if len(rows) == 0 {
		return fmt.Errorf("the runtime %s reads none of the %d attributes %s declares", runtimePath, len(attributes), metadataPath)
	}
	fmt.Fprintf(os.Stderr, "ypygen: %d attributes written; %d left out because %s does not read them: %s\n",
		len(rows), len(unread), runtimePath, strings.Join(unread, ", "))

	source, err := format.Source(render(metadata.Version, servedVersion, rows))
	if err != nil {
		return fmt.Errorf("format generated source: %w", err)
	}
	return os.WriteFile(outPath, source, 0o644)
}

type webTypes struct {
	Version       string `json:"version"`
	Contributions struct {
		HTML struct {
			Attributes []webTypeAttribute `json:"attributes"`
		} `json:"html"`
	} `json:"contributions"`
}

type webTypeAttribute struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Deprecated  bool   `json:"deprecated"`
}

type row struct {
	Name          string
	Detail        string
	Documentation string
	Deprecated    bool
}

// verbs are the request verbs the runtime keeps in one list and prefixes with
// `ypy-` when it reads them, so `ypy-get` never appears in the bundle as a
// string. Searching for the name alone would report the most used attributes
// as unread.
var verbs = []string{"get", "post", "put", "patch", "delete", "query"}

// reader answers what the served bundle reads.
type reader struct {
	bundle    string
	verbsList bool
}

func newReader(bundle string) reader {
	quoted := make([]string, len(verbs))
	for i, verb := range verbs {
		quoted[i] = `"` + verb + `"`
	}
	return reader{bundle: bundle, verbsList: strings.Contains(bundle, strings.Join(quoted, ","))}
}

// spelled reports whether the bundle holds text as a whole string, in any of
// the three quotes a script has.
func (r reader) spelled(text string) bool {
	for _, quote := range []string{`"`, `'`, "`"} {
		if strings.Contains(r.bundle, quote+text+quote) {
			return true
		}
	}
	return false
}

// label returns the name to offer for an attribute of the metadata, and whether
// the bundle reads it.
//
// A family the metadata writes with a star -- `ypy-status:*`, one attribute per
// status -- is offered by its prefix, which is what a person types before the
// part that varies. The other ways a name is read are the verbs, a name built
// from a prefix and a last word that the bundle spells apart (`ypy-error-` and
// `target`), and a name that appears only inside a selector, as the attributes
// without a value do.
func (r reader) label(name string) (string, bool) {
	if prefix, family := strings.CutSuffix(name, "*"); family {
		return prefix, r.spelled(prefix)
	}
	switch {
	case r.spelled(name):
		return name, true
	case r.verbsList && slices.Contains(verbs, strings.TrimPrefix(name, "ypy-")) && strings.HasPrefix(name, "ypy-"):
		return name, true
	case r.composed(name), r.selected(name):
		return name, true
	}
	return name, false
}

// composed reports whether the bundle spells the name in two parts: everything
// up to the last hyphen, hyphen included, and the word after it.
func (r reader) composed(name string) bool {
	at := strings.LastIndex(name, "-")
	if at <= len("ypy-")-1 {
		return false
	}
	return r.spelled(name[:at+1]) && r.spelled(name[at+1:])
}

// selected reports whether the name is written as an attribute selector, with
// or without a test on its value.
func (r reader) selected(name string) bool {
	for _, end := range []string{"]", "=", " ", ",", "~", "|", "^", "$", "*"} {
		if strings.Contains(r.bundle, "["+name+end) {
			return true
		}
	}
	return false
}

var versionPattern = regexp.MustCompile(`this\.version="([0-9][0-9A-Za-z.\-]*)"`)

// runtimeVersion reads the version the bundle reports about itself.
//
// It is recorded in the generated file so that a later reader can tell which
// runtime the table was checked against without having the bundle at hand.
func runtimeVersion(bundle string) (string, error) {
	match := versionPattern.FindStringSubmatch(bundle)
	if match == nil {
		return "", fmt.Errorf("no version found in the bundle")
	}
	return match[1], nil
}

var (
	markdownLink   = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	markdownAnchor = regexp.MustCompile(`\(@/[^)]*\)`)
	whitespaceRun  = regexp.MustCompile(`\s+`)
)

// flatten turns one markdown paragraph into the single line an editor shows.
//
// Links keep their text and lose their target, backticks go, and every run of
// whitespace becomes one space. A completion popup renders neither markdown nor
// newlines reliably, and the raw text carries both.
func flatten(text string) string {
	text = markdownLink.ReplaceAllString(text, "$1")
	text = markdownAnchor.ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "`", "")
	text = strings.ReplaceAll(text, "**", "")
	return strings.TrimSpace(whitespaceRun.ReplaceAllString(text, " "))
}

// firstParagraph is the text up to the first blank line, and never a fenced
// code block: the examples in this metadata start one paragraph in, and half an
// example is worse than none.
func firstParagraph(description string) string {
	paragraph := strings.SplitN(strings.TrimSpace(description), "\n\n", 2)[0]
	if at := strings.Index(paragraph, "```"); at >= 0 {
		paragraph = paragraph[:at]
	}
	return paragraph
}

const detailLimit = 96

// dropSelfReference removes the "The ypy-thing attribute" opening a description
// has, because the popup already shows the name on the same row.
//
// Every character spent repeating the label is one the summary does not get,
// and the row is drawn once without wrapping. The rewrite is a prefix match and
// a capital letter, nothing else: a description that does not open this way is
// left as it was written.
func dropSelfReference(sentence, name string) string {
	for _, prefix := range []string{
		"The " + name + " attribute ",
		"The " + name + "* attributes ",
		"The " + name + " attributes ",
	} {
		rest, found := strings.CutPrefix(sentence, prefix)
		if !found || rest == "" {
			continue
		}
		return strings.ToUpper(rest[:1]) + rest[1:]
	}
	return sentence
}

// firstSentence is the one-line summary shown beside the label.
//
// It ends at the first sentence break, and a sentence longer than the limit is
// cut on a word boundary: the detail is drawn on one row of a popup, so what
// does not fit is not shown at all rather than wrapped.
func firstSentence(description, name string) string {
	sentence := dropSelfReference(flatten(firstParagraph(description)), name)
	if at := strings.Index(sentence, ". "); at >= 0 {
		sentence = sentence[:at]
	}
	sentence = strings.TrimRight(sentence, ".:; ")
	if len([]rune(sentence)) <= detailLimit {
		return sentence
	}
	runes := []rune(sentence)[:detailLimit]
	if at := strings.LastIndex(string(runes), " "); at > 0 {
		return strings.TrimRight(string(runes)[:at], ",;: ") + "…"
	}
	return string(runes) + "…"
}

func render(metadataVersion, servedVersion string, rows []row) []byte {
	var out strings.Builder
	out.WriteString("// Code generated by internal/lsp/ypygen. DO NOT EDIT.\n\n")
	out.WriteString("package lsp\n\n")
	fmt.Fprintf(&out, `// ypyMetadataVersion is the YPY release whose published editor metadata
// these descriptions were read from.
const ypyMetadataVersion = %q

// ypyRuntimeVersion is the release of the bundle every name below was checked
// against.
//
// The two differ when the metadata read is newer than the bundle served, and
// that is the case this table is generated rather than typed: every name here
// was found in the served bundle, so a description written for a later release
// still describes an attribute that release understands.
const ypyRuntimeVersion = %q

// ypyAttributes is every attribute the served runtime reads, in name order.
var ypyAttributes = []ypyAttribute{
`, metadataVersion, servedVersion)
	for _, item := range rows {
		fmt.Fprintf(&out, "\t{name: %q, detail: %q, documentation: %q", item.Name, item.Detail, item.Documentation)
		if item.Deprecated {
			out.WriteString(", deprecated: true")
		}
		out.WriteString("},\n")
	}
	out.WriteString("}\n")
	return []byte(out.String())
}
