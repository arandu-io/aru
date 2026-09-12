package pack

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// TestABuildWithNothingToBuildIsRefused keeps the toolchain from being started
// for a package that was never named.
func TestABuildWithNothingToBuildIsRefused(t *testing.T) {
	for name, options := range map[string]Options{
		"no package": {Target: "android"},
		"no target":  {Package: "./cmd/native"},
		"no platform this knows": {
			Package: "./cmd/native",
			Target:  "sailfish",
		},
	} {
		if err := (options).validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// TestAnUnknownPlatformSaysWhichAreKnown keeps a typo from reading as a
// platform that has not been implemented yet.
func TestAnUnknownPlatformSaysWhichAreKnown(t *testing.T) {
	err := Options{Package: ".", Target: "sailfish"}.validate()
	if err == nil {
		t.Fatal("an unknown platform was accepted")
	}
	for _, platform := range Targets() {
		if !strings.Contains(err.Error(), platform) {
			t.Errorf("the refusal does not offer %s", platform)
		}
	}
}

// TestHalfOfNotarizationIsRefused fixes the failure that otherwise arrives from
// Apple, after the upload.
//
// Two of the three fields produce a submission that is assembled, signed,
// uploaded and then rejected -- which is the slowest possible way to learn that
// a field was blank.
func TestHalfOfNotarizationIsRefused(t *testing.T) {
	base := Options{Package: ".", Target: "macos"}

	partial := []Options{
		{Package: base.Package, Target: base.Target, NotaryID: "a@b.c"},
		{Package: base.Package, Target: base.Target, NotaryID: "a@b.c", NotaryPass: "x"},
		{Package: base.Package, Target: base.Target, NotaryPass: "x", NotaryTeamID: "T"},
	}
	for index, options := range partial {
		if err := options.validate(); err == nil {
			t.Errorf("partial notarization %d was accepted", index+1)
		}
	}

	whole := base
	whole.NotaryID, whole.NotaryPass, whole.NotaryTeamID = "a@b.c", "x", "T"
	if err := whole.validate(); err != nil {
		t.Errorf("complete notarization was refused: %v", err)
	}
}

// TestASigningPasswordWithNoKeyIsRefused keeps a password from being carried to
// a build that signs nothing.
func TestASigningPasswordWithNoKeyIsRefused(t *testing.T) {
	if err := (Options{Package: ".", Target: "android", SignPass: "secret"}).validate(); err == nil {
		t.Error("a signing password with no key was accepted")
	}
}

// TestTheSettingsAreRestoredAfterABuild fixes the one hazard of keeping the
// packaging sources close to what they were.
//
// They read package-level variables, which is what makes a diff against the
// original readable. In a long-lived process that means a value left behind by
// one build is inherited by the next: an application signed with the previous
// caller's key, or named after the previous caller's project.
func TestTheSettingsAreRestoredAfterABuild(t *testing.T) {
	*appID = "left.over.id"
	*name = "Leftover"
	appArgs = []string{"--from-before"}

	restore := Options{
		Package: ".",
		Target:  "android",
		AppID:   "com.example.during",
		Name:    "During",
		Args:    []string{"--during"},
	}.apply(&bytes.Buffer{}, &bytes.Buffer{})

	if *appID != "com.example.during" || *name != "During" {
		t.Fatalf("the options did not reach the build: %s / %s", *appID, *name)
	}

	restore()

	if *appID != "left.over.id" || *name != "Leftover" {
		t.Errorf("a later build would inherit %s / %s", *appID, *name)
	}
	if len(appArgs) != 1 || appArgs[0] != "--from-before" {
		t.Errorf("the arguments were not put back: %v", appArgs)
	}
}

// TestTheDefaultsAreTheOnesAPlatformAccepts keeps a blank from reaching a tool
// that refuses it.
//
// A version of "" and a build mode of "" are what an Options literal carries
// when a caller filled in only what it cared about, and both are values the
// platform toolchains reject rather than default.
func TestTheDefaultsAreTheOnesAPlatformAccepts(t *testing.T) {
	restore := Options{Package: ".", Target: "android"}.apply(&bytes.Buffer{}, &bytes.Buffer{})
	defer restore()

	if *version == "" {
		t.Error("the version is blank, and every platform requires one")
	}
	if *buildMode != "exe" {
		t.Errorf("the build mode is %q; something that runs is the default", *buildMode)
	}
}

// TestOutputGoesWhereTheCallerWasGiven keeps the packaging sources from writing
// to the process's own streams.
//
// They printed to stdout and stderr directly, which in a library means a
// command that was handed somewhere else to write still prints past it -- and
// a test can then prove nothing about what was said.
func TestOutputGoesWhereTheCallerWasGiven(t *testing.T) {
	var out, errs bytes.Buffer

	restore := Options{Package: ".", Target: "android", PrintCommands: true}.apply(&out, &errs)
	defer restore()

	if output != &out || errput != &errs {
		t.Error("the packaging sources still write to the process's own streams")
	}
}

// TestAMacBundleDeclaresItselfAnApplication fixes the one key that decides
// whether macOS treats the artifact as a program or as a folder of files.
//
// BNDL is a generic bundle. With it the Finder shows a package, Launch Services
// does not register the application by name, and nothing about the failure
// looks like a failure -- the binary inside still runs when it is started
// directly, which is how it passes every check somebody thinks to make.
func TestAMacBundleDeclaresItselfAnApplication(t *testing.T) {
	manifest := macManifest(t)

	if !strings.Contains(manifest, "<string>APPL</string>") {
		t.Error("the bundle does not declare itself an application")
	}
	if strings.Contains(manifest, "<string>BNDL</string>") {
		t.Error("the bundle declares itself a generic bundle, which macOS does not register as a program")
	}
}

// TestTheBundleIsDescribedFromTheBuildAndNotFromDefaults keeps the one step
// between a build and its bundle honest.
//
// Every field of the property list above is checked against what was written
// into it; the description handed to the writer is not, and a field dropped on
// the way there produces a valid property list describing somebody else's
// application.
func TestTheBundleIsDescribedFromTheBuildAndNotFromDefaults(t *testing.T) {
	described := macManifestFor(&buildInfo{
		appID:   "dev.local.probe",
		version: Semver{Major: 9, Minor: 8, Patch: 7, VersionCode: 6},
		schemes: []string{"probe"},
	}, "Probe")

	if described.Name != "Probe" {
		t.Errorf("the bundle would be named %q", described.Name)
	}
	if described.Bundle != "dev.local.probe" {
		t.Errorf("the bundle would be filed under %q", described.Bundle)
	}
	if described.Version.Major != 9 || described.Version.VersionCode != 6 {
		t.Errorf("the bundle would declare version %s", described.Version)
	}
	if len(described.Schemes) != 1 || described.Schemes[0] != "probe" {
		t.Errorf("the bundle would answer the schemes %v", described.Schemes)
	}
}

// TestAMacBundleCarriesAName keeps the menu bar from showing the name of the
// executable file.
func TestAMacBundleCarriesAName(t *testing.T) {
	manifest := macManifest(t)

	for _, key := range []string{"CFBundleName", "CFBundleShortVersionString", "CFBundleVersion"} {
		if !strings.Contains(manifest, key) {
			t.Errorf("the bundle declares no %s", key)
		}
	}
}

// macManifest answers a macOS bundle's Info.plist, written.
//
// It used to answer the template instead, recovered from this directory's own
// source between "<?xml version" and "</plist>". That reads the shape of the
// file the checks are about and never the file: a substitution that fails, a
// field that never reaches the writer, and a writer that stops being called all
// leave the template exactly where it was.
func macManifest(t *testing.T) string {
	t.Helper()

	manifest, err := macInfoPlist(macManifestData{
		Name:    "Probe",
		Bundle:  "dev.local.probe",
		Version: Semver{Major: 1, Minor: 2, Patch: 3, VersionCode: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(manifest)
}

// TestTheDeviceFloorIsTheOneTheBackendNeeds fixes a number that disagreed with
// the code it described.
//
// The window backend is written against the scene API, which arrived in iOS 13.
// The floor said 10, and with warnings promoted to errors every use of a scene
// was an error -- so the whole target failed, after building the Go half. It
// had never produced an artifact.
//
// A constant pinned by a test is a weak guard, and it is the strongest one
// available from here: what requires the version lives in another module. What
// it buys is that lowering the number fails with the reason written next to it
// rather than after a full build.
func TestTheDeviceFloorIsTheOneTheBackendNeeds(t *testing.T) {
	const scenes = 13

	if minIOSVersion < scenes {
		t.Errorf("the device floor is %d and the window backend uses an API of %d; the target will not compile", minIOSVersion, scenes)
	}
	if minSimulatorVersion < scenes {
		t.Errorf("the simulator floor is %d and the window backend uses an API of %d", minSimulatorVersion, scenes)
	}
}

// TestTheCompilerAndTheManifestAreToldTheSameVersion keeps a binary compiled
// for one version from declaring another.
//
// They are two sites reading the same field, and a drift between them ships an
// application the system will load on a device whose libraries it was not built
// against -- which fails at the first call into one of them and nowhere
// earlier.
func TestTheCompilerAndTheManifestAreToldTheSameVersion(t *testing.T) {
	// Not the floor, and not the default of anything: a number this build could
	// only have got from the field below.
	const asked = 17

	build := &buildInfo{name: "probe", target: "ios", minsdk: asked}

	compiler := strings.Join(iosDeploymentFlags(build.minsdk), " ")
	if want := fmt.Sprintf("-miphoneos-version-min=%d.0", asked); !strings.Contains(compiler, want) {
		t.Errorf("the compiler is told %q, and the build asked for %d", compiler, asked)
	}

	manifest, err := iosInfoPlist(iosManifestFor(build))
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("<string>%d.0</string>", asked); !strings.Contains(string(manifest), want) {
		t.Errorf("the manifest does not declare %d, which is what the compiler was handed", asked)
	}
}

// TestTheBrowserTargetIsBuiltWithoutCgo fixes a platform that failed on an
// ordinary machine.
//
// The browser has no cgo. The environment a build inherits often says otherwise
// -- it is a common setting, and it is what this project's own commands use --
// and the toolchain honours the variable over the platform: the standard
// library's user lookup selects a path with no implementation for this pair,
// and the failure names five functions inside the standard library and nothing
// of the project's.
//
// The command that builds without packaging already decides this per platform.
// This is the same decision on the path that produces the artifact somebody
// ships, and it was missing there.
func TestTheBrowserTargetIsBuiltWithoutCgo(t *testing.T) {
	// The machine says the opposite, which is the situation this is about: it
	// is an ordinary setting, and it is what this project's own commands use.
	t.Setenv("CGO_ENABLED", "1")

	if got := effective("CGO_ENABLED", jsBuildEnv()); got != "0" {
		t.Errorf("the browser build is compiled with cgo %q, and this platform has none", got)
	}
}

// TestEveryPlatformThatNeedsCgoAsksForIt keeps a platform that draws through C
// from being built without it.
//
// The three that reach a window through a C library say so explicitly. A
// platform that inherited the answer would build a different program on a
// machine with the variable off -- one that compiles, links, and has no window
// backend in it.
func TestEveryPlatformThatNeedsCgoAsksForIt(t *testing.T) {
	// And the machine says otherwise, because a platform that inherited the
	// answer is exactly what this is about.
	t.Setenv("CGO_ENABLED", "0")

	for name, env := range map[string][]string{
		"macOS":          macBuildEnv("arm64"),
		"Android":        androidBuildEnv("arm64", "clang"),
		"iOS":            iosProgramEnv("arm64", "clang", "-arch arm64"),
		"an iOS archive": iosFrameworkEnv("arm64", "clang", "-arch arm64"),
	} {
		if got := effective("CGO_ENABLED", env); got != "1" {
			t.Errorf("%s is compiled with cgo %q, and its window comes from a C library", name, got)
		}
	}
}

// effective answers what a variable is set to in an environment.
//
// The last entry wins, which is the whole reason these platforms append to the
// machine's environment rather than prepending: os/exec keeps the last of a
// repeated name, so a decision written after what was inherited is a decision,
// and the same line written before it is a default.
func effective(name string, env []string) string {
	value := ""
	for _, entry := range env {
		if setting, found := strings.CutPrefix(entry, name+"="); found {
			value = setting
		}
	}
	return value
}
