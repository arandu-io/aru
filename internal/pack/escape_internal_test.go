package pack

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

// A golden file records what a writer produced, and says nothing about whether
// what it produced was valid.
//
// That is not a hypothetical about this corpus: the ampersand case was recorded
// green for a day with `<string>Faturas & Cobranças</string>` inside it, which
// is not a property list an installer can read. The recording was faithful and
// the output was broken, and nothing in a comparison against a recording can
// tell those apart.
//
// So these tests do not compare. They parse. Four of the five documents are
// XML, and an XML parser is the same judge the tools on the other end use; the
// fifth is a page, and what matters there is that markup handed in as a name
// arrives as text rather than as markup.

// hostileNames are the values a project is allowed to choose and a writer must
// survive. Each one breaks a different escape.
var hostileNames = map[string]string{
	"ampersand and an accent": "Faturas & Cobranças",
	"a closing tag":           "Probe</string><key>Injected</key><string>yes",
	"an attribute quote":      `Probe" oninstall="anything`,
	"angle brackets":          "<script>alert(1)</script>",
	"a newline and a tab":     "Two\nLines\tApart",
}

// parses reports whether every token of the document reads back, which is what
// the installer on the other end will do to it.
func parses(t *testing.T, label string, document []byte) {
	t.Helper()

	decoder := xml.NewDecoder(strings.NewReader(string(document)))
	for {
		_, err := decoder.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Errorf("%s is not a document an installer can read: %v", label, err)
			return
		}
	}
}

func TestEveryMarkupDocumentSurvivesAHostileName(t *testing.T) {
	for label, name := range hostileNames {
		t.Run(label, func(t *testing.T) {
			android := androidCorpus()["plain"]
			android.AppName = name
			android.Schemes = []string{name}
			document, err := androidManifest(android)
			if err != nil {
				t.Fatal(err)
			}
			parses(t, "the Android manifest", document)

			archive, err := androidArchiveManifest(android)
			if err != nil {
				t.Fatal(err)
			}
			parses(t, "the Android archive manifest", archive)

			ios := iosCorpus()["plain"]
			ios.AppName = name
			ios.Schemes = []string{name}
			document, err = iosInfoPlist(ios)
			if err != nil {
				t.Fatal(err)
			}
			parses(t, "the iOS property list", document)

			mac := macCorpus()["plain"]
			mac.Name = name
			mac.Schemes = []string{name}
			document, err = macInfoPlist(mac)
			if err != nil {
				t.Fatal(err)
			}
			parses(t, "the macOS property list", document)

			win := windowsCorpus()["plain"]
			win.Name = name
			document, err = windowsManifestXML(win)
			if err != nil {
				t.Fatal(err)
			}
			parses(t, "the Windows manifest", document)
		})
	}
}

// TestTheBrowserPageKeepsAHostileNameAsText is the fifth document, and it is
// judged differently because it is not XML.
//
// What has to hold is that a name given as markup reaches the page as text: a
// project called "<script>alert(1)</script>" produces a title that says so and
// a page that does not run it.
func TestTheBrowserPageKeepsAHostileNameAsText(t *testing.T) {
	for label, name := range hostileNames {
		t.Run(label, func(t *testing.T) {
			page, err := jsIndexHTML(jsIndexData{Name: name})
			if err != nil {
				t.Fatal(err)
			}

			// The raw value must not appear: if it does, whatever markup it
			// carries is markup in the page.
			if strings.Contains(string(page), name) && strings.ContainsAny(name, `<>&"'`) {
				t.Errorf("the page carries the name as written, so the markup in it is the page's: %q", name)
			}

			// And the escaped form must, or the name was dropped instead of
			// escaped -- which passes the check above for the wrong reason.
			escaped := htmlEscapeForTest(name)
			if !strings.Contains(string(page), escaped) {
				t.Errorf("the page does not carry the name at all; expected the escaped form %q", escaped)
			}
		})
	}
}

// htmlEscapeForTest is the escape the page is expected to have applied, written
// out here rather than imported, so that the test fails if the writer changes
// which escape it uses instead of agreeing with it silently.
func htmlEscapeForTest(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&#34;",
		"'", "&#39;",
	)
	return replacer.Replace(value)
}
