package main

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/fonts"
)

// TestEachFontURLCarriesTheHashOfItsOwnFile vendors two different files, one
// per role, and checks every URL the stylesheet writes against the bytes it
// names.
//
// The framework serves /_arandu/assets/<hash>/<name> and compares the hash with
// the bytes it holds under that name; a mismatch is served with no caching, so
// a wrong hash is a font downloaded again on every page view with nothing
// broken enough to notice. The URL can only be right if every face is written
// under a name of its own -- two faces given one name leave the first face's
// URL pointing at the second face's bytes, and register one name twice.
//
// The cases differ in how much of the name the two files share. The last two
// share all of it: the same family at the same weight, which is how a display
// cut and a text cut of one typeface are installed.
func TestEachFontURLCarriesTheHashOfItsOwnFile(t *testing.T) {
	for _, tc := range []struct {
		name          string
		body, display []string // --family, --weight
		ext           string
	}{
		{name: "different families", body: []string{"Public Sans", "400"}, display: []string{"Young Serif", "400"}, ext: ".woff2"},
		{name: "one family at two weights", body: []string{"Montserrat", "400..600"}, display: []string{"Montserrat", "600..800"}, ext: ".woff2"},
		{name: "one family at one weight", body: []string{"Arandu", "400"}, display: []string{"Arandu", "400"}, ext: ".woff2"},
		{name: "one family at one weight, as TrueType", body: []string{"Arandu", "700"}, display: []string{"Arandu", "700"}, ext: ".ttf"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := fontProject(t)
			bodyFile := writeFontSource(t, root, "text"+tc.ext, "the text cut")
			displayFile := writeFontSource(t, root, "display"+tc.ext, "the display cut, drawn separately")

			for _, add := range [][]string{
				{"--file", bodyFile, "--family", tc.body[0], "--weight", tc.body[1], "--as", "body"},
				{"--file", displayFile, "--family", tc.display[0], "--weight", tc.display[1], "--as", "display"},
			} {
				if err := fontAdd(add, io.Discard, io.Discard); err != nil {
					t.Fatalf("aru font:add %s: %v", strings.Join(add, " "), err)
				}
			}

			css, err := os.ReadFile(filepath.Join(root, fontCSS))
			if err != nil {
				t.Fatal(err)
			}
			urls := regexp.MustCompile(`url\("/_arandu/assets/([0-9a-f]+)/([^"]+)"\)`).FindAllStringSubmatch(string(css), -1)
			if len(urls) != 2 {
				t.Fatalf("the stylesheet has %d font URLs, want one per role:\n%s", len(urls), css)
			}
			files := map[string]bool{}
			for _, u := range urls {
				hash, file := u[1], u[2]
				files[file] = true
				onDisk, err := os.ReadFile(filepath.Join(root, fontDir, file))
				if err != nil {
					t.Fatalf("the stylesheet names %s, which is not on disk: %v", file, err)
				}
				if got := fonts.AssetHash(onDisk); got != hash {
					t.Errorf("the URL of %s carries %s, and the file's own hash is %s", file, hash, got)
				}
			}
			if len(files) != 2 {
				t.Errorf("both roles point at one file %v, so one of the two fonts is gone", files)
			}

			// Each role keeps the bytes it was given.
			installed, err := readInstalled(root)
			if err != nil {
				t.Fatal(err)
			}
			for role, want := range map[fonts.Role]string{fonts.Body: "the text cut", fonts.Display: "the display cut, drawn separately"} {
				file, _, _ := strings.Cut(installed[role].FileList[0], "|")
				got, err := os.ReadFile(filepath.Join(root, fontDir, file))
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != want {
					t.Errorf("the %s face %s holds %q, want %q", role, file, got, want)
				}
			}

			// One registration per name, or the binary panics at init.
			generated, err := os.ReadFile(filepath.Join(root, fontGoGen))
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			for _, m := range regexp.MustCompile(`fonts\.Register\("([^"]+)"`).FindAllStringSubmatch(string(generated), -1) {
				if seen[m[1]] {
					t.Errorf("%s registers %s twice:\n%s", fontGoGen, m[1], generated)
				}
				seen[m[1]] = true
			}

			// Removing one role leaves the other's file where it was.
			if err := fontRemove([]string{"body"}, io.Discard, io.Discard); err != nil {
				t.Fatal(err)
			}
			file, _, _ := strings.Cut(installed[fonts.Display].FileList[0], "|")
			if _, err := os.Stat(filepath.Join(root, fontDir, file)); err != nil {
				t.Errorf("removing the body face removed the display face's file: %v", err)
			}
		})
	}
}

// TestFacesOfOneRequestThatShareANameAreMergedOrKeptApart pins the other way
// two faces arrive under one name: one catalogue request answering several faces
// for one subset. When the catalogue answers the same variable file for two
// weights the bytes are the same, so one file, one URL and one registration is
// right -- a second copy under a second name would be the same bytes twice in
// every binary. When the bytes differ, both are kept, under two names.
func TestFacesOfOneRequestThatShareANameAreMergedOrKeptApart(t *testing.T) {
	faces := []fonts.Face{
		{File: "inter-400-700-latin.woff2", UnicodeRange: "U+0000-00FF", Body: []byte("variable")},
		{File: "inter-400-700-latin.woff2", UnicodeRange: "U+0000-00FF", Body: []byte("variable")},
	}
	got := uniqueFaceFiles(faces, nil)
	if len(got) != 1 {
		t.Fatalf("got %d faces, want the duplicate dropped: %+v", len(got), got)
	}
	if got[0].File != "inter-400-700-latin.woff2" {
		t.Errorf("the face was renamed to %s", got[0].File)
	}

	// The same, when the other role already holds the name: the first copy is
	// renamed, and the repeat is still recognised as one.
	got = uniqueFaceFiles(faces, map[string]bool{"inter-400-700-latin.woff2": true})
	renamed := "inter-400-700-latin-" + fonts.AssetHash([]byte("variable")) + ".woff2"
	if len(got) != 1 || got[0].File != renamed {
		t.Errorf("got %+v, want one face named %s", got, renamed)
	}

	// A static family at a list of weights answers one file per weight under
	// one subset, so the names collide and the bytes do not: both stay, under
	// two names.
	static := []fonts.Face{
		{File: "lato-400-700-latin.woff2", UnicodeRange: "U+0000-00FF", Body: []byte("regular")},
		{File: "lato-400-700-latin.woff2", UnicodeRange: "U+0000-00FF", Body: []byte("bold")},
	}
	got = uniqueFaceFiles(static, nil)
	if len(got) != 2 {
		t.Fatalf("got %d faces, want both weights kept: %+v", len(got), got)
	}
	want := "lato-400-700-latin-" + fonts.AssetHash([]byte("bold")) + ".woff2"
	if got[0].File != "lato-400-700-latin.woff2" || got[1].File != want {
		t.Errorf("the faces are named %s and %s, want lato-400-700-latin.woff2 and %s", got[0].File, got[1].File, want)
	}
}

// TestAManifestThatSharesAFileBetweenRolesStillBuildsAndKeepsIt covers the
// projects that ran an older font:add: their manifest can list one file under
// both roles. Regenerating has to register it once, because the view layer
// panics on a second registration, and removing one role has to leave the file
// the other role still draws.
func TestAManifestThatSharesAFileBetweenRolesStillBuildsAndKeepsIt(t *testing.T) {
	root := fontProject(t)
	if err := os.MkdirAll(filepath.Join(root, fontDir), 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("one cut, two roles")
	if err := os.WriteFile(filepath.Join(root, fontDir, "arandu-400.woff2"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	note := "arandu-400.woff2||" + fonts.AssetHash(body)
	all := map[fonts.Role]fonts.Installed{
		fonts.Body:    {Role: fonts.Body, Family: "Arandu", Weight: "400", Subsets: []string{"custom"}, FileList: []string{note}},
		fonts.Display: {Role: fonts.Display, Family: "Arandu", Weight: "400", Subsets: []string{"custom"}, FileList: []string{note}},
	}
	if err := writeFontFiles(root, all); err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile(filepath.Join(root, fontGoGen))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(generated), `fonts.Register("arandu-400.woff2"`); n != 1 {
		t.Errorf("%s registers the shared file %d times, want 1:\n%s", fontGoGen, n, generated)
	}

	if err := fontRemove([]string{"body"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, fontDir, "arandu-400.woff2")); err != nil {
		t.Errorf("removing the body face removed the file the display face still draws: %v", err)
	}
}

// fontProject is an empty Arandu project, entered.
func fontProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":      "module example.test/fonts\n\ngo 1.26\n",
		"main.go":     "package main\n\nfunc main() {}\n",
		"arandu.toml": "[tools]\ntailwindcss = \"v4.3.3\"\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)
	return root
}

// writeFontSource writes a stand-in font file. The command reads no table out
// of a .woff2, and a .ttf it cannot parse only loses the metric overrides, so
// the bytes need to be distinct and nothing else.
func writeFontSource(t *testing.T, root, name, body string) string {
	t.Helper()
	path := filepath.Join(root, "sources", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
