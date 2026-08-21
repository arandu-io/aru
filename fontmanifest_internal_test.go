package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/fonts"
)

// TestTheManifestIsRewrittenAndNotAppendedTo.
//
// `aru font:add` writes a comment and the sections under it. A section-shaped
// removal takes the sections and leaves the comment, so every run appends a
// second copy: a project whose font was changed four times ends up with four
// copies of the same nine-line note in arandu.toml.
//
// The files themselves stay identical; only the manifest drifts, which is the
// kind of thing nobody notices until a diff is unreadable.
func TestTheManifestIsRewrittenAndNotAppendedTo(t *testing.T) {
	root := t.TempDir()
	writeManifestFile(t, root, `# The build tools this project downloads, pinned.

[arandu]
aru = "v0.27.0"

[tools]
tailwindcss = "v4.3.3"
`)

	installed := []fonts.Installed{{
		Role: fonts.Display, Family: "Montserrat", Weight: "400..800",
		Subsets: []string{"latin"}, Category: "Sans Serif", License: "ofl",
		FileList: []string{"montserrat-400-800-latin.woff2|U+0000-00FF|abcdef012345"},
		Metrics:  fonts.Metrics{Ascent: 96.8, Descent: 25.1},
	}}

	for range 4 {
		if err := writeManifest(root, installed); err != nil {
			t.Fatal(err)
		}
	}

	got := readManifestFile(t, root)
	if n := strings.Count(got, "The vendored faces."); n != 1 {
		t.Errorf("the generated comment appears %d times, want 1:\n%s", n, got)
	}
	if n := strings.Count(got, "[fonts.display]"); n != 1 {
		t.Errorf("the section appears %d times, want 1", n)
	}
	// What this function does not own has to survive every one of those runs.
	for _, keep := range []string{"# The build tools", "[tools]", `tailwindcss = "v4.3.3"`, "[arandu]"} {
		if !strings.Contains(got, keep) {
			t.Errorf("%q was dropped:\n%s", keep, got)
		}
	}
}

// TestTheGeneratedBlockRoundTripsThroughTheReader.
//
// Writing and reading are two halves of one format, and a format that only one
// of them understands is a manifest that loses a field on the second run.
func TestTheGeneratedBlockRoundTripsThroughTheReader(t *testing.T) {
	root := t.TempDir()
	writeManifestFile(t, root, "[tools]\ntailwindcss = \"v4.3.3\"\n")

	want := fonts.Installed{
		Role: fonts.Display, Family: "Montserrat", Weight: "400..800",
		Subsets: []string{"latin", "latin-ext"}, Category: "Sans Serif", License: "ofl",
		// A unicode-range carries commas, which is why the file list is joined
		// with semicolons: splitting this on commas tore every entry into
		// fifteen, and the round trip was broken from the first run.
		FileList: []string{"montserrat-400-800-latin.woff2|U+0000-00FF, U+0131, U+2000-206F|abcdef012345"},
		Metrics:  fonts.Metrics{Ascent: 96.8, Descent: 25.1, SizeAdjust: 101.5},
	}
	if err := writeManifest(root, []fonts.Installed{want}); err != nil {
		t.Fatal(err)
	}

	back, err := readInstalled(root)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := back[fonts.Display]
	if !ok {
		t.Fatal("the display face did not come back")
	}
	if got.Family != want.Family || got.Weight != want.Weight || got.Category != want.Category {
		t.Errorf("read back %+v", got)
	}
	if len(got.FileList) != 1 || got.FileList[0] != want.FileList[0] {
		t.Errorf("the file list came back as %q", got.FileList)
	}
	if got.Metrics.Ascent != want.Metrics.Ascent || got.Metrics.SizeAdjust != want.Metrics.SizeAdjust {
		t.Errorf("the metrics came back as %+v", got.Metrics)
	}
}

// TestRemovingTheLastFaceRemovesTheBlock: a manifest describing a font that is
// not there is a manifest that lies.
func TestRemovingTheLastFaceRemovesTheBlock(t *testing.T) {
	root := t.TempDir()
	writeManifestFile(t, root, "[tools]\ntailwindcss = \"v4.3.3\"\n")

	_ = writeManifest(root, []fonts.Installed{{
		Role: fonts.Display, Family: "Montserrat", Weight: "400",
		FileList: []string{"m.woff2||abc"},
	}})
	if err := writeManifest(root, nil); err != nil {
		t.Fatal(err)
	}

	got := readManifestFile(t, root)
	if strings.Contains(got, "[fonts.") || strings.Contains(got, "The vendored faces.") {
		t.Errorf("the block survived removal:\n%s", got)
	}
	if !strings.Contains(got, "[tools]") {
		t.Error("the rest of the file went with it")
	}
}

func writeManifestFile(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "arandu.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readManifestFile(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "arandu.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestABlockWrittenBeforeTheMarkerIsStillFound.
//
// The marker was added after the format shipped. Without recognising what the
// previous version wrote, the first run of a newer CLI leaves the old comment
// above the block it just replaced -- one orphan, permanently, which is the
// duplication this whole change exists to remove.
func TestABlockWrittenBeforeTheMarkerIsStillFound(t *testing.T) {
	root := t.TempDir()
	writeManifestFile(t, root, `[tools]
tailwindcss = "v4.3.3"

# The vendored faces. Written by `+"`aru font:add`"+`, and the files themselves are
# under assets/fonts/ and committed -- so this section is not what makes the
# build reproducible. The bytes are.

[fonts.display]
family = "Young Serif"
weight = "400"
files = "young-serif-400-latin.woff2||abc"
`)

	if err := writeManifest(root, []fonts.Installed{{
		Role: fonts.Display, Family: "Montserrat", Weight: "400..800",
		FileList: []string{"montserrat-400-800-latin.woff2||def"},
	}}); err != nil {
		t.Fatal(err)
	}

	got := readManifestFile(t, root)
	if n := strings.Count(got, "The vendored faces."); n != 1 {
		t.Errorf("the old comment was left behind: %d copies\n%s", n, got)
	}
	if strings.Contains(got, "Young Serif") {
		t.Error("the previous face survived being replaced")
	}
	if !strings.Contains(got, "[tools]") {
		t.Error("the rest of the file went with it")
	}
}
