package fonts_test

import (
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/fonts"
)

// TestAssetHashMatchesTheFramework.
//
// This is one half of a contract across a repository boundary. The framework
// serves an asset under the first twelve hex characters of the SHA-256 of its
// bytes, and compares the hash in the path against it -- a mismatch is served
// with Cache-Control: no-cache, deliberately, so a stale reference degrades into
// a slow page rather than a broken one.
//
// `aru font:add` writes that hash into the stylesheet it generates. If the two
// implementations drift, every vendored font is re-downloaded on every page
// view, silently, with nothing broken enough to notice. The framework's test
// pins the same input to the same answer.
func TestAssetHashMatchesTheFramework(t *testing.T) {
	const want = "06c6c3fc524a"

	if got := fonts.AssetHash([]byte("arandu")); got != want {
		t.Fatalf("AssetHash = %q, want %q.\n\nThe framework's view.AssetHash has the other half of this contract.", got, want)
	}
}

// TestTheStylesheetNamesTheFontsOwnHash: a relative url() reaches the file and
// is served uncached, which is the failure this exists to prevent.
func TestTheStylesheetNamesTheFontsOwnHash(t *testing.T) {
	css := fonts.Stylesheet([]fonts.Installed{{
		Role: fonts.Display, Family: "Young Serif", Weight: "400",
		FileList: []string{"young-serif-400-latin.woff2|U+0000-00FF|abcdef012345"},
		Metrics:  fonts.Metrics{Ascent: 104.6, Descent: 36.6},
	}})

	if !strings.Contains(css, `url("/_arandu/assets/abcdef012345/young-serif-400-latin.woff2")`) {
		t.Fatalf("the src does not carry the font's own hash:\n%s", css)
	}
	// The metric-matched fallback, and the token the templates read.
	if !strings.Contains(css, "ascent-override: 104.60%") {
		t.Error("the fallback is not metric matched: every heading reflows when the font lands")
	}
	if !strings.Contains(css, "--font-display:") {
		t.Error("nothing declares --font-display, so no template can reach the face")
	}
}

// TestAFaceWithNoMetricsGetsNoFallback: a made-up override shifts the layout in
// a direction nobody measured, which is worse than the reflow it replaces.
func TestAFaceWithNoMetricsGetsNoFallback(t *testing.T) {
	css := fonts.Stylesheet([]fonts.Installed{{
		Role: fonts.Display, Family: "Nameless", Weight: "400",
		FileList: []string{"nameless-400-latin.woff2||aaaaaaaaaaaa"},
	}})

	if strings.Contains(css, "ascent-override") {
		t.Fatal("an override was invented for a face whose metrics were never read")
	}
	if strings.Contains(css, "display-fallback") {
		t.Error("the token names a fallback face that was not declared")
	}
}

// TestPortugueseIsInsideLatin is the fact the default rests on.
//
// U+0000-00FF is Latin-1 Supplement, which carries every accented letter
// Portuguese and English use. latin-ext is Polish, Czech and Turkish, and
// defaulting to it would double the bytes for a language nobody asked about.
func TestPortugueseIsInsideLatin(t *testing.T) {
	for _, r := range "ãõçáéíóúâêôàü" {
		if r > 0x00FF {
			t.Errorf("%q is U+%04X, outside U+0000-00FF: the latin default would not draw it", r, r)
		}
	}
}
