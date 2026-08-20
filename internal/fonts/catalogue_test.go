package fonts_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/fonts"
)

// catalogue is a handful of entries in the shape the real one has.
func catalogue() []fonts.Entry {
	return []fonts.Entry{
		{Family: "Space Grotesk", Category: "Sans Serif", Weights: []int{300, 700},
			Axes: []string{"wght"}, Popularity: 120, OpenSource: true, Subsets: []string{"latin", "latin-ext"}},
		{Family: "Young Serif", Category: "Serif", Weights: []int{400},
			Popularity: 804, OpenSource: true, Subsets: []string{"latin", "latin-ext"}},
		{Family: "Bricolage Grotesque", Category: "Sans Serif", Weights: []int{200, 800},
			Axes: []string{"opsz", "wght"}, Popularity: 400, OpenSource: true, Subsets: []string{"latin"}},
		{Family: "Roboto", Category: "Sans Serif", Weights: []int{100, 900},
			Axes: []string{"wght"}, Popularity: 1, OpenSource: true, Subsets: []string{"latin"}},
	}
}

// TestSearchAnswersInPopularityOrder.
//
// Alphabetical would answer a search for "sans" with families nobody has heard
// of, and a list nobody recognises is a list nobody picks from.
func TestSearchAnswersInPopularityOrder(t *testing.T) {
	found := fonts.Search(catalogue(), "", "sans-serif", false)

	if len(found) != 3 {
		t.Fatalf("%d matches, want 3", len(found))
	}
	if found[0].Family != "Roboto" {
		t.Errorf("the first answer is %s, not the most used one", found[0].Family)
	}
}

// TestSearchMatchesPartOfAName: "grot" is what somebody types, and it has to
// find both Space Grotesk and Bricolage Grotesque.
func TestSearchMatchesPartOfAName(t *testing.T) {
	found := fonts.Search(catalogue(), "grot", "", false)

	if len(found) != 2 {
		t.Fatalf("%d matches for \"grot\", want 2: %v", len(found), found)
	}
}

// TestTheCategoryIsSpeltThreeWaysAndMeansOne.
//
// The source writes "Sans Serif" and a person types "sans-serif". A filter that
// only accepted one of them would answer an empty list to a correct question.
func TestTheCategoryIsSpeltThreeWaysAndMeansOne(t *testing.T) {
	for _, spelling := range []string{"sans-serif", "Sans Serif", "sansserif", "SANS-SERIF"} {
		if got := len(fonts.Search(catalogue(), "", spelling, false)); got != 3 {
			t.Errorf("%q matched %d, want 3", spelling, got)
		}
	}
}

// TestVariableOnlyDropsTheStaticOnes.
func TestVariableOnlyDropsTheStaticOnes(t *testing.T) {
	found := fonts.Search(catalogue(), "", "", true)

	for _, e := range found {
		if !e.Variable() {
			t.Errorf("%s has no axes and was kept", e.Family)
		}
	}
	if len(found) != 3 {
		t.Fatalf("%d variable families, want 3", len(found))
	}
}

// TestWeightRangeSaysWhatWeightWouldTake: a variable family takes a range and a
// static one takes the instances, and offering the wrong one is a flag that
// fetches nothing.
func TestWeightRangeSaysWhatWeightWouldTake(t *testing.T) {
	all := catalogue()

	variable, _ := fonts.Find(all, "space grotesk")
	if got := variable.WeightRange(); got != "300..700" {
		t.Errorf("a variable family offers %q, want a range", got)
	}
	static, _ := fonts.Find(all, "Young Serif")
	if got := static.WeightRange(); got != "400" {
		t.Errorf("a static family offers %q", got)
	}
}

// TestNearestSuggestsSomethingForATypo.
//
// A refusal that only says "no such family" sends somebody to a browser, which
// is what installing from the terminal exists to avoid.
func TestNearestSuggestsSomethingForATypo(t *testing.T) {
	near := fonts.Nearest(catalogue(), "Yung Serif", 5)

	if len(near) == 0 {
		t.Fatal("a name off by one letter suggested nothing")
	}
	if near[0] != "Young Serif" {
		t.Errorf("the first suggestion is %q", near[0])
	}
}

// TestALocalFileNeedsAFamilyName.
//
// Deriving one from the path would produce a CSS family called
// Arandu-Regular-v3-final, which is what the file is called and not what
// the font is.
func TestALocalFileNeedsAFamilyName(t *testing.T) {
	path := write(t, "Own.ttf", []byte("not really a font"))

	if _, err := fonts.Local(path, "", "400", ""); err == nil {
		t.Fatal("a file with no --family was accepted")
	}
}

// TestALocalFileKeepsItsExtension.
//
// The extension decides the format() in the src:. A .ttf renamed .woff2 is a
// font every browser fetches and then refuses, with an error that names neither
// the file nor the declaration.
func TestALocalFileKeepsItsExtension(t *testing.T) {
	path := write(t, "Own.ttf", []byte("not really a font"))

	got, err := fonts.Local(path, "Arandu", "400", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got.Faces[0].File, ".ttf") {
		t.Errorf("the vendored file is %s", got.Faces[0].File)
	}
	if fonts.Format(got.Faces[0].File) != "truetype" {
		t.Error("the src: would declare woff2 for a TrueType")
	}
	// No unicode-range: nothing published one, and a range narrower than the
	// file is a glyph the browser refuses to ask for.
	if got.Faces[0].UnicodeRange != "" {
		t.Error("a unicode-range was invented for a file that published none")
	}
}

// TestAnUnsupportedExtensionIsRefused: a .zip named as a font is a 404 on every
// page, found at run time.
func TestAnUnsupportedExtensionIsRefused(t *testing.T) {
	path := write(t, "Own.zip", []byte("PK"))

	if _, err := fonts.Local(path, "Own", "400", ""); err == nil {
		t.Fatal(".zip was accepted as a font")
	}
}

// TestAdviceOnlyFiresForWhatIsNotAWoff2, and names the file it is about.
func TestAdviceOnlyFiresForWhatIsNotAWoff2(t *testing.T) {
	ttf := write(t, "Own.ttf", []byte("x"))
	got, _ := fonts.Local(ttf, "Own", "400", "")
	if advice := fonts.Advice(got, ttf); !strings.Contains(advice, ttf) {
		t.Errorf("the advice does not name the file: %s", advice)
	}

	woff2 := write(t, "Own.woff2", []byte("x"))
	got, _ = fonts.Local(woff2, "Own", "400", "")
	if advice := fonts.Advice(got, woff2); advice != "" {
		t.Errorf("a woff2 was told to convert itself: %s", advice)
	}
}

func write(t *testing.T, name string, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestTheFallbackFollowsTheCategoryAndNotTheRole.
//
// Following the role instead assumes a display face is a serif, so installing
// Montserrat -- a geometric sans -- as the display face produces a page that
// paints in Georgia and becomes a geometric sans when the font lands. The metric
// override cannot hide that: it matches the space the face occupies, not the
// shapes it draws.
func TestTheFallbackFollowsTheCategoryAndNotTheRole(t *testing.T) {
	sans := fonts.Stylesheet([]fonts.Installed{{
		Role: fonts.Display, Family: "Montserrat", Weight: "400 800",
		Category: "Sans Serif",
		FileList: []string{"montserrat-400-800-latin.woff2|U+0000-00FF|abc123abc123"},
		Metrics:  fonts.Metrics{Ascent: 96.8, Descent: 25.1},
	}})

	if strings.Contains(sans, "Georgia") {
		t.Error("a geometric sans falls back to a serif")
	}
	if !strings.Contains(sans, `local("Helvetica Neue")`) {
		t.Errorf("the metric-matched fallback is not a sans:\n%s", sans)
	}
	if !strings.Contains(sans, "sans-serif;") {
		t.Error("the last resort is not a sans either")
	}

	serif := fonts.Stylesheet([]fonts.Installed{{
		Role: fonts.Display, Family: "Young Serif", Weight: "400",
		Category: "Serif",
		FileList: []string{"young-serif-400-latin.woff2|U+0000-00FF|abc123abc123"},
		Metrics:  fonts.Metrics{Ascent: 104.6, Descent: 36.6},
	}})
	if !strings.Contains(serif, `local("Georgia")`) {
		t.Errorf("a serif does not fall back to one:\n%s", serif)
	}
}

// TestAnUnknownCategoryFallsBackToASans.
//
// Display and Handwriting are neither serif nor mono in any useful sense, and a
// sans is the least wrong thing to paint before one of them arrives.
func TestAnUnknownCategoryFallsBackToASans(t *testing.T) {
	for _, category := range []string{"Display", "Handwriting", "", "something new"} {
		css := fonts.Stylesheet([]fonts.Installed{{
			Role: fonts.Display, Family: "X", Weight: "400", Category: category,
			FileList: []string{"x-400.woff2||aaaaaaaaaaaa"},
			Metrics:  fonts.Metrics{Ascent: 100, Descent: 25},
		}})
		if strings.Contains(css, "Georgia") {
			t.Errorf("%q fell back to a serif", category)
		}
	}
}
