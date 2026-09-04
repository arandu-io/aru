// Command htmxgen writes the HTMX attribute table the language server offers.
//
// The table was typed by hand once and covered thirteen of the thirty-five
// attributes, which is the failure mode a hand-maintained list has: it is
// correct on the day it is written and silently short forever after. This reads
// the editor metadata HTMX publishes for its own tooling and emits the same
// facts as Go.
//
// It refuses to emit an attribute the served runtime does not understand.
// Completion that offers an attribute the bundle in the page ignores is worse
// than no completion, because nothing reports it: the markup is valid HTML, the
// build is green, and the element simply does nothing. The check is textual --
// every `hx-` name the bundle mentions is collected, and an attribute is either
// one of those or one of the five built from the verb list -- so it answers
// about the file that is actually served rather than about a version number
// written down somewhere.
//
// Usage:
//
//	go run ./internal/lsp/htmxgen -metadata <web-types.json> -runtime <htmx.min.js> -out <file.go>
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
	"sort"
	"strings"
)

func main() {
	metadata := flag.String("metadata", "", "path to the HTMX web-types JSON")
	runtime := flag.String("runtime", "", "path to the htmx.min.js the project serves")
	out := flag.String("out", "", "path of the Go file to write")
	flag.Parse()

	if *metadata == "" || *runtime == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: htmxgen -metadata <web-types.json> -runtime <htmx.min.js> -out <file.go>")
		os.Exit(2)
	}
	if err := run(*metadata, *runtime, *out); err != nil {
		fmt.Fprintln(os.Stderr, "htmxgen:", err)
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

	names := servedNames(served)
	var unknown []string
	rows := make([]row, 0, len(attributes))
	for _, attribute := range attributes {
		if !servedUnderstands(served, names, attribute.Name) {
			unknown = append(unknown, attribute.Name)
			continue
		}
		rows = append(rows, row{
			Name:          attribute.Name,
			Detail:        firstSentence(attribute.Description, attribute.Name),
			Documentation: documentation(attribute),
			Deprecated:    attribute.Deprecated,
		})
	}
	if len(unknown) > 0 {
		return fmt.Errorf("the served runtime does not understand %s, so offering them would suggest markup that does nothing",
			strings.Join(unknown, ", "))
	}

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
	DocURL      string `json:"doc-url"`
	Deprecated  bool   `json:"deprecated"`
}

type row struct {
	Name          string
	Detail        string
	Documentation string
	Deprecated    bool
}

// verbAttributes are the five names the runtime builds rather than spells.
//
// htmx keeps its request verbs in one list and prefixes each with `hx-` at
// load, so `hx-get` never appears in the bundle as a string. Searching for the
// name alone would report the five most used attributes as unsupported.
var verbAttributes = map[string]bool{
	"hx-get": true, "hx-post": true, "hx-put": true, "hx-delete": true, "hx-patch": true,
}

var (
	verbList     = regexp.MustCompile(`"get","post","put","delete","patch"`)
	attributeRef = regexp.MustCompile(`hx-[a-z]+(?:-[a-z]+)*`)
)

// servedNames collects every `hx-` name the bundle mentions.
//
// A name is read out of whatever encloses it rather than matched as a whole
// quoted string, because the bundle spells several of them inside a selector --
// `[hx-preserve]`, `[hx-history-elt]` -- where the quotes are around the
// brackets. Asking for the quoted form reported four supported attributes as
// unknown.
func servedNames(bundle string) map[string]bool {
	names := map[string]bool{}
	for _, match := range attributeRef.FindAllString(bundle, -1) {
		names[match] = true
	}
	return names
}

// servedUnderstands reports whether the bundle acts on the attribute.
func servedUnderstands(bundle string, names map[string]bool, name string) bool {
	if verbAttributes[name] {
		return verbList.MatchString(bundle)
	}
	return names[name]
}

var versionPattern = regexp.MustCompile(`version:"([0-9][0-9A-Za-z.\-]*)"`)

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

// dropSelfReference removes the "The hx-thing attribute" opening a description
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

// documentation is the paragraph plus the page it came from, so a reader who
// needs the whole rule has the address of it.
func documentation(attribute webTypeAttribute) string {
	text := flatten(firstParagraph(attribute.Description))
	if attribute.DocURL != "" {
		text += "\n\n" + attribute.DocURL
	}
	return text
}

func render(metadataVersion, servedVersion string, rows []row) []byte {
	var out strings.Builder
	out.WriteString("// Code generated by internal/lsp/htmxgen. DO NOT EDIT.\n\n")
	out.WriteString("package lsp\n\n")
	fmt.Fprintf(&out, `// htmxMetadataVersion is the HTMX release whose published editor metadata
// these descriptions were read from.
const htmxMetadataVersion = %q

// htmxRuntimeVersion is the release of the bundle every name below was checked
// against.
//
// The two differ when the metadata read is newer than the bundle served, and
// that is the case this table is generated rather than typed: every name here
// was found in the served bundle, so a description written for a later release
// still describes an attribute that release understands.
const htmxRuntimeVersion = %q

// htmxAttributes is every attribute HTMX declares, in name order.
var htmxAttributes = []htmxAttribute{
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
