package pack

import (
	"bytes"
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
