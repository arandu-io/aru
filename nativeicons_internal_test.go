package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const iconTestMark = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 60"><circle cx="30" cy="30" r="25" fill="#111111"/><rect x="62" y="10" width="30" height="40" fill="#111111"/></svg>`

// TestNativeIconsWritesEveryPlatformFromOneImage runs the command the way a
// person does and reads back what it left.
func TestNativeIconsWritesEveryPlatformFromOneImage(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "mark.svg")
	if err := os.WriteFile(source, []byte(iconTestMark), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "icons")

	code, stdout, stderr := exercise(t, "native:icons", source, "-output", out, "-background", "#eeeeee", "-dark-background", "#101010")
	if code != 0 {
		t.Fatalf("aru native:icons exited %d: %s", code, stderr)
	}
	for _, want := range []string{
		"android/res/mipmap-anydpi-v26/ic_launcher.xml",
		"android/res/mipmap-xxxhdpi/ic_launcher_foreground.png",
		"android/res/drawable-xxxhdpi/ic_stat.png",
		"android/res/values-night/ic_colors.xml",
		"ios/AppIcon.appiconset/Contents.json",
		"ios/AppIcon.appiconset/AppIcon-dark.png",
		"web/favicon.ico",
		"web/icon-maskable-512.png",
		"desktop/icon.ico",
		"desktop/icon.icns",
	} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(want))); err != nil {
			t.Errorf("%s was not written: %v", want, err)
		}
	}
	for _, platform := range []string{"android", "ios", "web", "desktop"} {
		if !strings.Contains(stdout, filepath.Join(out, platform)) {
			t.Errorf("the summary does not say what went into %s: %q", platform, stdout)
		}
	}
	colors, err := os.ReadFile(filepath.Join(out, "android", "res", "values-night", "ic_colors.xml"))
	if err != nil || !strings.Contains(string(colors), "#101010") {
		t.Errorf("the dark background did not reach the Android colours: %s %v", colors, err)
	}
}

// TestNativeIconsRefusesWhatItCannotDraw: no image, a missing one, a colour that
// is not one.
func TestNativeIconsRefusesWhatItCannotDraw(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "mark.svg")
	if err := os.WriteFile(source, []byte(iconTestMark), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"no image":       {"native:icons"},
		"missing image":  {"native:icons", filepath.Join(dir, "nope.png"), "-output", filepath.Join(dir, "o")},
		"bad background": {"native:icons", source, "-output", filepath.Join(dir, "o"), "-background", "blue"},
	} {
		code, _, stderr := exercise(t, args...)
		if code == 0 {
			t.Errorf("%s: exited 0", name)
		}
		if stderr == "" {
			t.Errorf("%s: said nothing", name)
		}
	}
}

// TestNativeBuildHelpNamesTheIconOptions keeps the options of the icon from
// existing only in the parser.
func TestNativeBuildHelpNamesTheIconOptions(t *testing.T) {
	_, stdout, _ := exercise(t, "native:build", "--help")
	for _, name := range []string{"icon-background", "icon-dark", "icon-dark-background"} {
		if !strings.Contains(stdout, "-"+name) {
			t.Errorf("aru native:build --help does not name -%s", name)
		}
	}
}
