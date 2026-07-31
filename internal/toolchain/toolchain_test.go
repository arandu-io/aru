package toolchain

import (
	"os"
	"path/filepath"
	"testing"
)

// TestChecksumParsing covers the disagreement that actually bit: templ writes
// the filename bare, Tailwind writes "./name". A parser that only handles one of
// them fails on first use, on someone else's machine.
func TestChecksumParsing(t *testing.T) {
	sums := `55fd0b241214eff3de1e8ee4f227966662f2d2e7a49bcfca7477cfd0bac398195  ./tailwindcss-linux-arm64
71ea4be79c9de982754568' 2df3e0400 53fb535d37c71ed2cfdedf9385a0868e0  ./tailwindcss-linux-arm64-musl
dc61b3ac6b8c9ca874c0cc4c57b24097 91a64c554040 4ca5f5367360babc313a  ./tailwindcss-linux-x64
aaaa  templ_Darwin_arm64.tar.gz
bbbb *templ_Linux_x86_64.tar.gz`

	for _, c := range []struct {
		asset string
		want  string
	}{
		{"tailwindcss-linux-arm64", "55fd0b241214eff3de1e8ee4f227966662f2d2e7a49bcfca7477cfd0bac398195"},
		{"templ_Darwin_arm64.tar.gz", "aaaa"},
		{"templ_Linux_x86_64.tar.gz", "bbbb"},
	} {
		got, err := checksumFor(sums, c.asset)
		if err != nil {
			t.Errorf("%s: %v", c.asset, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.asset, got, c.want)
		}
	}

	if _, err := checksumFor(sums, "tailwindcss-plan9-riscv"); err == nil {
		t.Error("an asset that is not listed was accepted")
	}
}

// TestAssetNames guards the platform mapping. Getting it wrong produces a 404 on
// first run, which reads as "the release is broken" rather than "we asked for a
// file that was never published".
func TestAssetNames(t *testing.T) {
	for _, c := range []struct {
		tool          Tool
		goos, goarch  string
		want          string
		wantPublished bool
	}{
		{Templ(""), "darwin", "arm64", "templ_Darwin_arm64.tar.gz", true},
		{Templ(""), "linux", "amd64", "templ_Linux_x86_64.tar.gz", true},
		{Templ(""), "windows", "amd64", "templ_Windows_x86_64.tar.gz", true},
		{Templ(""), "plan9", "amd64", "", false},
		{Tailwind(""), "darwin", "arm64", "tailwindcss-macos-arm64", true},
		{Tailwind(""), "linux", "amd64", "tailwindcss-linux-x64", true},
		{Tailwind(""), "windows", "amd64", "tailwindcss-windows-x64.exe", true},
		{Tailwind(""), "darwin", "386", "", false},
	} {
		got, published := c.tool.asset(c.goos, c.goarch)
		if published != c.wantPublished {
			t.Errorf("%s %s/%s published = %v", c.tool.Name, c.goos, c.goarch, published)
		}
		if got != c.want {
			t.Errorf("%s %s/%s = %q, want %q", c.tool.Name, c.goos, c.goarch, got, c.want)
		}
	}
}

// TestTheVersionIsInTheCachedPath: two projects pinning different versions must
// not overwrite each other's binary, and upgrading must not silently reuse the
// old one.
func TestTheVersionIsInTheCachedPath(t *testing.T) {
	first, err := Templ("v0.1.0").Path()
	if err != nil {
		t.Fatal(err)
	}
	second, err := Templ("v0.2.0").Path()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("both versions cache to %s", first)
	}
}

func TestPinsDefaultToTheBuiltInVersions(t *testing.T) {
	pins, err := ReadPins(t.TempDir())
	if err != nil {
		t.Fatalf("ReadPins on a project with no arandu.toml: %v", err)
	}
	if pins.Templ != "" || pins.Tailwind != "" {
		t.Fatalf("pins = %+v, want empty so the defaults apply", pins)
	}
	if Templ(pins.Templ).Version != DefaultTemplVersion {
		t.Error("the default templ version did not apply")
	}
	if Tailwind(pins.Tailwind).Version != DefaultTailwindVersion {
		t.Error("the default tailwind version did not apply")
	}
}

func TestPinsAreRead(t *testing.T) {
	root := t.TempDir()
	writePins(t, root, `# the versions this project builds with
[project]
name = "ignored"

[tools]
templ = "v0.3.999"
tailwindcss = "v4.0.0"
`)

	pins, err := ReadPins(root)
	if err != nil {
		t.Fatalf("ReadPins: %v", err)
	}
	if pins.Templ != "v0.3.999" || pins.Tailwind != "v4.0.0" {
		t.Fatalf("pins = %+v", pins)
	}
}

// TestAnUnknownToolIsAnError: a typo that is ignored is a pinned version that
// silently does not apply, and the build differs between two machines for a
// reason nobody can see in the file.
func TestAnUnknownToolIsAnError(t *testing.T) {
	root := t.TempDir()
	writePins(t, root, "[tools]\ntemp = \"v0.3.999\"\n")

	if _, err := ReadPins(root); err == nil {
		t.Fatal("a misspelled tool name was accepted")
	}
}

func writePins(t *testing.T, root, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "arandu.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
