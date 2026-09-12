package pack

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// update rewrites the golden files: go test ./internal/pack -update
var update = flag.Bool("update", false, "rewrite the golden files")

// Every platform is described to its installer by one file of markup, and the
// only checks any of them had read this directory's own Go looking for string
// literals. That kind of check cannot be wrong about behaviour, because it says
// nothing about behaviour: one of them matched "MinVersion:      bi.minsdk,"
// with the alignment gofmt happened to give it inside the quotes, so moving the
// field one column would have failed it and writing the wrong number there
// would not.
//
// What is recorded here is bytes out for a description in. The corpus is chosen
// for where these files are known to differ quietly: with an icon and without,
// with no URL scheme and with two, at the floor of the version range and past
// its ceiling, and with a name carrying an ampersand and an accent -- which is
// the case that separates a document whose writer escapes markup from one whose
// writer does not.

// androidCorpus is the manifest a store reads, in the shapes it comes in.
func androidCorpus() map[string]manifestData {
	return map[string]manifestData{
		"plain": {
			AppID:       "dev.local.probe",
			Version:     Semver{Major: 1, Minor: 2, Patch: 3, VersionCode: 4},
			MinSDK:      16,
			TargetSDK:   35,
			Permissions: []string{"android.permission.INTERNET"},
			Features:    []string{`glEsVersion="0x00020000"`},
			AppName:     "Probe",
		},
		"with-icon": {
			AppID:     "dev.local.probe",
			Version:   Semver{Major: 1, Minor: 2, Patch: 3, VersionCode: 4},
			MinSDK:    16,
			TargetSDK: 35,
			IconSnip:  `android:icon="@mipmap/ic_launcher"`,
			AppName:   "Probe",
		},
		"two-schemes": {
			AppID:     "dev.local.probe",
			Version:   Semver{Major: 1, Minor: 2, Patch: 3, VersionCode: 4},
			MinSDK:    16,
			TargetSDK: 35,
			AppName:   "Probe",
			Schemes:   []string{"probe", "probe-dev"},
		},
		"queries": {
			AppID:          "dev.local.probe",
			Version:        Semver{Major: 1, Minor: 2, Patch: 3, VersionCode: 4},
			MinSDK:         16,
			TargetSDK:      35,
			AppName:        "Probe",
			PackageQueries: []string{"com.example.other"},
		},
		// The floor the packager itself applies, and a floor above the ceiling
		// -- which is a real state rather than an invalid one, because a
		// minimum higher than the target raises the target to meet it.
		"sdk-at-the-floor": {
			AppID:     "dev.local.probe",
			Version:   Semver{Major: 1, VersionCode: 1},
			MinSDK:    16,
			TargetSDK: 35,
			AppName:   "Probe",
		},
		"sdk-above-the-ceiling": {
			AppID:     "dev.local.probe",
			Version:   Semver{Major: 1, VersionCode: 1},
			MinSDK:    36,
			TargetSDK: 36,
			AppName:   "Probe",
		},
		"name-with-markup-in-it": {
			AppID:     "dev.local.probe",
			Version:   Semver{Major: 1, VersionCode: 1},
			MinSDK:    16,
			TargetSDK: 35,
			AppName:   "Faturas & Cobranças",
		},
	}
}

// androidArchiveCorpus is the shorter manifest an archive another build embeds
// carries.
func androidArchiveCorpus() map[string]manifestData {
	return map[string]manifestData{
		"plain": {
			AppID:  "dev.local.probe",
			MinSDK: 16,
		},
		"permissions-and-features": {
			AppID:       "dev.local.probe",
			MinSDK:      16,
			Permissions: []string{"android.permission.INTERNET", "android.permission.CAMERA"},
			Features:    []string{`name="android.hardware.camera"`},
		},
		"sdk-above-the-ceiling": {
			AppID:  "dev.local.probe",
			MinSDK: 36,
		},
	}
}

// iosCorpus is the property list an iOS application is described by.
func iosCorpus() map[string]iosManifestData {
	base := iosManifestData{
		AppName:         "Probe",
		AppID:           "dev.local.probe",
		Version:         "1.2.3",
		VersionCode:     4,
		Platform:        "iphoneos",
		MinVersion:      13,
		SupportPlatform: "iPhoneOS",
	}

	twoSchemes := base
	twoSchemes.Schemes = []string{"probe", "probe-dev"}

	aboveTheFloor := base
	aboveTheFloor.MinVersion = 26

	television := base
	television.Platform, television.SupportPlatform, television.MinVersion = "appletvos", "AppleTVOS", 11

	markup := base
	markup.AppName = "Faturas & Cobranças"

	return map[string]iosManifestData{
		"plain":                  base,
		"two-schemes":            twoSchemes,
		"above-the-floor":        aboveTheFloor,
		"the-other-platform":     television,
		"name-with-markup-in-it": markup,
	}
}

// macCorpus is the property list a macOS bundle is recognised through.
func macCorpus() map[string]macManifestData {
	return map[string]macManifestData{
		"plain": {
			Name:    "Probe",
			Bundle:  "dev.local.probe",
			Version: Semver{Major: 1, Minor: 2, Patch: 3, VersionCode: 4},
		},
		"two-schemes": {
			Name:    "Probe",
			Bundle:  "dev.local.probe",
			Version: Semver{Major: 1, Minor: 2, Patch: 3, VersionCode: 4},
			Schemes: []string{"probe", "probe-dev"},
		},
		"name-with-markup-in-it": {
			Name:    "Faturas & Cobranças",
			Bundle:  "dev.local.probe",
			Version: Semver{Major: 1, VersionCode: 1},
		},
	}
}

// windowsCorpus is the manifest that decides which versions of the system will
// run the program.
func windowsCorpus() map[string]windowsManifest {
	return map[string]windowsManifest{
		"the-current-version": {
			Version:        "1.2.3.4",
			WindowsVersion: 10,
			Name:           "probe",
		},
		// Every version the program declares support for sits behind a test on
		// this number, so the floor is where the manifest is at its longest.
		"the-oldest-version": {
			Version:        "1.2.3.4",
			WindowsVersion: 6,
			Name:           "probe",
		},
		"name-with-markup-in-it": {
			Version:        "1.0.0.1",
			WindowsVersion: 10,
			Name:           "Faturas & Cobranças",
		},
	}
}

// browserCorpus is the page a browser opens the compiled program through.
func browserCorpus() map[string]jsIndexData {
	return map[string]jsIndexData{
		"plain":                  {Name: "Probe"},
		"with-icon":              {Name: "Probe", Icon: "appicon.png"},
		"no-name":                {},
		"name-with-markup-in-it": {Name: "Faturas & Cobranças"},
	}
}

func TestTheAndroidManifestIsWhatItWas(t *testing.T) {
	for name, data := range androidCorpus() {
		t.Run(name, func(t *testing.T) {
			got, err := androidManifest(data)
			if err != nil {
				t.Fatalf("writing the manifest: %v", err)
			}
			golden(t, "android", name, got)
		})
	}
}

func TestTheAndroidArchiveManifestIsWhatItWas(t *testing.T) {
	for name, data := range androidArchiveCorpus() {
		t.Run(name, func(t *testing.T) {
			got, err := androidArchiveManifest(data)
			if err != nil {
				t.Fatalf("writing the manifest: %v", err)
			}
			golden(t, "android-archive", name, got)
		})
	}
}

func TestTheIOSPropertyListIsWhatItWas(t *testing.T) {
	for name, data := range iosCorpus() {
		t.Run(name, func(t *testing.T) {
			got, err := iosInfoPlist(data)
			if err != nil {
				t.Fatalf("writing the property list: %v", err)
			}
			golden(t, "ios", name, got)
		})
	}
}

func TestTheMacPropertyListIsWhatItWas(t *testing.T) {
	for name, data := range macCorpus() {
		t.Run(name, func(t *testing.T) {
			got, err := macInfoPlist(data)
			if err != nil {
				t.Fatalf("writing the property list: %v", err)
			}
			golden(t, "macos", name, got)
		})
	}
}

func TestTheWindowsManifestIsWhatItWas(t *testing.T) {
	for name, data := range windowsCorpus() {
		t.Run(name, func(t *testing.T) {
			got, err := windowsManifestXML(data)
			if err != nil {
				t.Fatalf("writing the manifest: %v", err)
			}
			golden(t, "windows", name, got)
		})
	}
}

func TestTheBrowserPageIsWhatItWas(t *testing.T) {
	for name, data := range browserCorpus() {
		t.Run(name, func(t *testing.T) {
			got, err := jsIndexHTML(data)
			if err != nil {
				t.Fatalf("writing the page: %v", err)
			}
			golden(t, "browser", name, got)
		})
	}
}

// TestEveryGoldenIsStillWritten is the direction the six tests above cannot
// reach.
//
// Renaming a case and running -update writes the new file and leaves the old
// one where it is: never read, never compared, and never failing. Nothing above
// notices, because each of them starts from the corpus and the orphan is no
// longer in it.
func TestEveryGoldenIsStillWritten(t *testing.T) {
	written := map[string]bool{}
	for platform, cases := range map[string][]string{
		"android":         names(androidCorpus()),
		"android-archive": names(androidArchiveCorpus()),
		"ios":             names(iosCorpus()),
		"macos":           names(macCorpus()),
		"windows":         names(windowsCorpus()),
		"browser":         names(browserCorpus()),
	} {
		for _, name := range cases {
			written[goldenPath(platform, name)] = true
		}
	}

	found := 0
	err := filepath.WalkDir(filepath.Join("testdata", "manifest"), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		found++
		if !written[path] {
			t.Errorf("%s is a golden file nothing writes any more: delete it, or the case that wrote it is gone and nobody noticed", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found != len(written) {
		t.Errorf("%d golden files are on disk and the corpus has %d cases", found, len(written))
	}
}

// names answers the keys of a corpus.
func names[T any](corpus map[string]T) []string {
	keys := make([]string, 0, len(corpus))
	for name := range corpus {
		keys = append(keys, name)
	}
	return keys
}

// goldenPath is where one case's recorded bytes live.
func goldenPath(platform, name string) string {
	return filepath.Join("testdata", "manifest", platform, name+".golden")
}

// golden compares bytes against the file recording what they were, and writes
// that file instead when -update was passed.
func golden(t *testing.T, platform, name string, got []byte) {
	t.Helper()

	path := goldenPath(platform, name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v -- run: go test ./internal/pack -update", path, err)
	}
	if !bytes.Equal(want, got) {
		t.Errorf("%s differs from what was recorded.\nRun `go test ./internal/pack -update` and read the diff before keeping it.", path)
	}
}
