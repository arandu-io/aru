package lsp

import (
	"io"
	"sort"
	"strings"
)

// The doctor's findings as diagnostics of the protocol.
//
// They are published only to a client that asks for them in its
// initializationOptions. A client that does not ask already draws the
// findings from the map's diagnostic nodes, and publishing them as well would
// show each finding twice.

// doctorDiagnosticSource is what a client shows as the origin of a finding.
const doctorDiagnosticSource = "aru doctor"

// codeDescription points a diagnostic at the documentation of its rule.
type codeDescription struct {
	Href string `json:"href"`
}

// initializationOptions are the settings a client may hand the server when it
// starts it.
type initializationOptions struct {
	// DoctorDiagnostics asks for the doctor's findings to be published as
	// diagnostics of the documents they are about.
	DoctorDiagnostics bool `json:"doctorDiagnostics"`
}

// doctorState is what the server has published of the doctor's findings, by
// document, so a finding that went away is cleared rather than left behind.
type doctorState struct {
	enabled   bool
	published map[string][]diagnostic
}

// findingsByDocument answers the findings of the tree as diagnostics, by the
// URI of the document each is about, with the range of the text of the line
// it reports, the rule as the code, and the rule's documentation as the code
// description.
func (p *project) findingsByDocument() map[string][]diagnostic {
	out := map[string][]diagnostic{}
	m := p.projectMap()
	if m == nil {
		return out
	}
	lines := newSourceLines(p.root)
	for _, node := range m.Nodes {
		if node.Kind != "diagnostic" || node.File == "" {
			continue
		}
		location, ok := nodeLocation(lines, node)
		if !ok {
			continue
		}
		severity := 2
		if node.Severity == "error" {
			severity = 1
		}
		message := node.Label
		if node.Detail != "" {
			message += "\n" + node.Detail
		}
		finding := diagnostic{
			Range: location.Range, Severity: severity, Source: doctorDiagnosticSource,
			Code: node.Rule, Message: message,
		}
		if href := ruleDocURI(lines, node.RuleDoc); href != "" {
			finding.CodeDescription = &codeDescription{Href: href}
		}
		out[location.URI] = append(out[location.URI], finding)
	}
	for uri := range out {
		sort.SliceStable(out[uri], func(i, j int) bool {
			a, b := out[uri][i].Range.Start, out[uri][j].Range.Start
			if a.Line != b.Line {
				return a.Line < b.Line
			}
			return a.Character < b.Character
		})
	}
	return out
}

// ruleDocURI turns where a rule is documented into an address a client
// opens: a path inside the project becomes its file URI, line fragment kept.
func ruleDocURI(lines *sourceLines, doc string) string {
	if doc == "" || strings.Contains(doc, "://") {
		return doc
	}
	rel, fragment, _ := strings.Cut(doc, "#")
	uri, err := lines.uri(rel)
	if err != nil {
		return ""
	}
	if fragment != "" {
		uri += "#" + fragment
	}
	return uri
}

// refresh publishes the findings of the tree as it is on disk, and clears the
// documents whose findings went away. A document open in the editor keeps the
// view compiler's diagnostics beside the doctor's.
func (d *doctorState) refresh(out io.Writer, workspace *project, documents map[string]string) error {
	if !d.enabled || workspace == nil {
		return nil
	}
	current := workspace.findingsByDocument()
	uris := make([]string, 0, len(current)+len(d.published))
	for uri := range current {
		uris = append(uris, uri)
	}
	for uri := range d.published {
		if _, still := current[uri]; !still {
			uris = append(uris, uri)
		}
	}
	sort.Strings(uris)
	d.published = current
	for _, uri := range uris {
		source, open := documents[uri]
		var compiler []diagnostic
		if open {
			compiler = diagnosticsFor(uri, source)
		}
		if err := writeDiagnostics(out, uri, append(compiler, current[uri]...)); err != nil {
			return err
		}
	}
	return nil
}

// forDocument answers the doctor's diagnostics last published for a document.
func (d *doctorState) forDocument(uri string) []diagnostic {
	if !d.enabled {
		return nil
	}
	return d.published[uri]
}
