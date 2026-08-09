package toolchain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestChecksumParsing covers the disagreement that actually bit: the filename in
// a checksums file comes bare, as "./name", or as "*name". A parser that only
// handles one of them fails on first use, on someone else's machine.
func TestChecksumParsing(t *testing.T) {
	sums := `55fd0b241214eff3de1e8ee4f227966662f2d2e7a49bcfca7477cfd0bac398195  ./tailwindcss-linux-arm64
71ea4be79c9de982754568' 2df3e0400 53fb535d37c71ed2cfdedf9385a0868e0  ./tailwindcss-linux-arm64-musl
dc61b3ac6b8c9ca874c0cc4c57b24097 91a64c554040 4ca5f5367360babc313a  ./tailwindcss-linux-x64
aaaa  tailwindcss-macos-arm64
bbbb *tailwindcss-windows-x64.exe`

	for _, c := range []struct {
		asset string
		want  string
	}{
		{"tailwindcss-linux-arm64", "55fd0b241214eff3de1e8ee4f227966662f2d2e7a49bcfca7477cfd0bac398195"},
		{"tailwindcss-macos-arm64", "aaaa"},
		{"tailwindcss-windows-x64.exe", "bbbb"},
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
		libc          string
		want          string
		wantPublished bool
	}{
		{Tailwind(""), "darwin", "arm64", "gnu", "tailwindcss-macos-arm64", true},
		{Tailwind(""), "darwin", "amd64", "gnu", "tailwindcss-macos-x64", true},
		{Tailwind(""), "linux", "arm64", "gnu", "tailwindcss-linux-arm64", true},
		{Tailwind(""), "linux", "amd64", "gnu", "tailwindcss-linux-x64", true},

		// The musl builds. Asking for the glibc one on Alpine fails with "no
		// such file or directory" naming the file that is right there, because
		// what is missing is the loader -- and Alpine is what the Dockerfile
		// builds in, so this is the case CI runs and a laptop never does.
		{Tailwind(""), "linux", "amd64", "musl", "tailwindcss-linux-x64-musl", true},
		{Tailwind(""), "linux", "arm64", "musl", "tailwindcss-linux-arm64-musl", true},

		{Tailwind(""), "windows", "amd64", "gnu", "tailwindcss-windows-x64.exe", true},
		{Tailwind(""), "darwin", "386", "gnu", "", false},
		{Tailwind(""), "plan9", "amd64", "gnu", "", false},
	} {
		got, published := c.tool.asset(c.goos, c.goarch, c.libc)
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
	first, err := Tailwind("v4.0.0").Path()
	if err != nil {
		t.Fatal(err)
	}
	second, err := Tailwind("v4.1.0").Path()
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
	if pins.Tailwind != "" {
		t.Fatalf("pins = %+v, want empty so the defaults apply", pins)
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
tailwindcss = "v4.0.0"
`)

	pins, err := ReadPins(root)
	if err != nil {
		t.Fatalf("ReadPins: %v", err)
	}
	if pins.Tailwind != "v4.0.0" {
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

// TestTheErrorNeverOffersTempl: the message that lists what a tool key may be
// has to name only tools that exist. Offering `templ` sent readers to install a
// binary that nothing has downloaded since ADR 0020.
//
// This replaces TestATemplPinIsRefused, which asserted the opposite and was
// right about the goal and wrong about the mechanism: refusing the key broke
// every project that had it, which was all of them. Retirement with a warning
// is in TestARetiredPinDoesNotBreakTheBuild.
func TestTheErrorNeverOffersTempl(t *testing.T) {
	root := t.TempDir()
	writePins(t, root, "[tools]\ntailwindcs = \"v4.1.11\"\n")

	_, err := ReadPins(root)
	if err == nil {
		t.Fatal("a misspelled tool was accepted")
	}
	if strings.Contains(err.Error(), "templ") {
		t.Errorf("the error still offers templ as an option: %v", err)
	}
}

func writePins(t *testing.T, root, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "arandu.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestARetiredPinDoesNotBreakTheBuild is the regression guard for a break that
// reached every project at once.
//
// Refusing the `templ` key was defensible in isolation -- a pin that is read and
// never used is worse than one that is refused -- and catastrophic in practice:
// the published skeleton still carries the line, so `aru new` produced a project
// whose first `aru view:build` exited 1, and every project already on disk broke
// on upgrade. No deprecation window, in a build tool, which is the worst place
// for one: what the reader sees is a project that stopped compiling for a reason
// they did not cause.
//
// Retired is not the same as unknown. A key that was never a tool stays an
// error, because that one is a typo.
func TestARetiredPinDoesNotBreakTheBuild(t *testing.T) {
	root := t.TempDir()
	writePins(t, root, "[tools]\ntempl = \"v0.3.1020\"\ntailwindcss = \"v4.1.11\"\n")

	pins, err := ReadPins(root)
	if err != nil {
		t.Fatalf("the published skeleton's arandu.toml was refused: %v", err)
	}
	if pins.Tailwind != "v4.1.11" {
		t.Errorf("the retired key swallowed the one that still counts: Tailwind = %q", pins.Tailwind)
	}

	// Ignoring it in silence is the other half of the mistake: the line would
	// sit there forever and the next reader would assume templ is still in play.
	var warning strings.Builder
	pins.Warn(&warning)
	for _, want := range []string{"arandu.toml:2", "templ", "kyse", "Remove the line"} {
		if !strings.Contains(warning.String(), want) {
			t.Errorf("the warning does not mention %q: %s", want, warning.String())
		}
	}
}

// TestATypoIsStillRefused: the deprecation window is for keys that were real,
// not for keys nobody ever defined.
func TestATypoIsStillRefused(t *testing.T) {
	root := t.TempDir()
	writePins(t, root, "[tools]\ntailwindcs = \"v4.1.11\"\n")

	if _, err := ReadPins(root); err == nil {
		t.Fatal("a misspelled tool was accepted")
	}
}
