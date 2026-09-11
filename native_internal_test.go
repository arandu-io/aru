package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// nativeProject writes the three files that make a directory an Arandu
// project, optionally gives it a native target, and moves into it.
func nativeProject(t *testing.T, withNativeTarget bool) string {
	t.Helper()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(root, "arandu.toml"), "name = \"app\"\n")

	if withNativeTarget {
		writeFile(t, filepath.Join(root, nativeDir, "main.go"), "package main\n\nfunc main() {}\n")
	}

	chdir(t, root)

	// The temporary directory is behind a symlink on some systems, and the
	// project root is resolved rather than assembled -- so comparing what this
	// wrote against what the command answers needs the resolved form.
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// TestAProjectWithNoNativeTargetIsToldHowToGetOne fixes which of two failures
// a person gets.
//
// Without the check, the compiler answers that a package does not exist, and
// that reads as a broken checkout. The actual state is a project that has not
// published a native target yet, and the fix is one command.
func TestAProjectWithNoNativeTargetIsToldHowToGetOne(t *testing.T) {
	nativeProject(t, false)

	_, err := nativeRoot()
	if err == nil {
		t.Fatal("a project with no native target was accepted")
	}
	for _, expected := range []string{nativeDir, "vendor:publish"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the refusal does not mention %q: %v", expected, err)
		}
	}
}

// TestAProjectWithANativeTargetIsAccepted keeps the check above from refusing
// the case it exists for.
func TestAProjectWithANativeTargetIsAccepted(t *testing.T) {
	root := nativeProject(t, true)

	got, err := nativeRoot()
	if err != nil {
		t.Fatalf("a project with a native target was refused: %v", err)
	}
	if got != root {
		t.Errorf("the root is %q, want %q", got, root)
	}
}

// TestNotBeingInAProjectIsSaidBeforeAnythingAboutPlatforms fixes the order of
// two refusals that are true at the same time.
//
// Somebody who runs this in the wrong directory and asks for a platform that
// needs a toolchain is wrong about the directory. Answering about the Android
// SDK sends them to install one.
func TestNotBeingInAProjectIsSaidBeforeAnythingAboutPlatforms(t *testing.T) {
	nativeProject(t, false)

	var out, errs bytes.Buffer
	err := nativeBuild([]string{"-target", "android/arm64"}, &out, &errs)
	if err == nil {
		t.Fatal("a project with no native target built something")
	}
	if strings.Contains(err.Error(), "Android") {
		t.Errorf("the refusal is about the platform, not the project: %v", err)
	}
	if !strings.Contains(err.Error(), nativeDir) {
		t.Errorf("the refusal does not name what is missing: %v", err)
	}
}

// TestAPlatformThatNeedsAToolchainSaysSoInTheListing keeps somebody from
// discovering the cost by starting a build.
//
// The build itself is attempted now rather than refused: the packager drives
// the platform toolchains, and when one is missing its own message names what
// is missing better than a guess here would. What this fixes is that the price
// is readable before anybody types the command.
//
// The words are written out here rather than read from the table the listing
// is built from. Comparing a message against its own source passes whatever
// the table says, including "x" -- which is the shape this test had first, and
// it survived a mutation that emptied every explanation in the product.
func TestAPlatformThatNeedsAToolchainSaysSoInTheListing(t *testing.T) {
	var out bytes.Buffer
	if err := listNativeTargets(&out); err != nil {
		t.Fatal(err)
	}
	listing := out.String()

	for platform, expected := range map[string][]string{
		"android/arm64": {"Android SDK", "NDK", "JDK"},
		"ios/arm64":     {"Xcode", "provisioning"},
	} {
		target, known := nativeTargets[platform]
		if !known {
			t.Fatalf("%s is not a platform", platform)
		}
		for _, word := range expected {
			if !strings.Contains(target.needs, word) {
				t.Errorf("%s does not say it needs %q", platform, word)
			}
		}
		if !strings.Contains(listing, platform) {
			t.Errorf("%s is not in the listing", platform)
		}
	}
}

// TestThePackagedPlatformsAreTheOnesThatProduceAnArtifact keeps a target from
// claiming a package it cannot write.
func TestThePackagedPlatformsAreTheOnesThatProduceAnArtifact(t *testing.T) {
	for name, target := range nativeTargets {
		if target.installable && target.packages == "" {
			t.Errorf("%s has no bare binary anybody can install and names no packager", name)
		}
	}
}

// TestAnUnknownPlatformIsRefusedWithWhereToLook keeps a typo from reaching the
// compiler as a build for a platform nobody supports.
func TestAnUnknownPlatformIsRefusedWithWhereToLook(t *testing.T) {
	nativeProject(t, true)

	var out, errs bytes.Buffer
	err := nativeBuild([]string{"-target", "solaris/sparc"}, &out, &errs)
	if err == nil {
		t.Fatal("an unsupported platform was accepted")
	}
	if !strings.Contains(err.Error(), "-list") {
		t.Errorf("the refusal does not say where the platforms are listed: %v", err)
	}
}

// TestEveryPlatformIsListedWithItsPrice keeps the list from being a list of
// only the easy ones.
//
// A platform that is absent reads as one nobody thought about. A platform with
// its cost written next to it is a decision somebody can make.
func TestEveryPlatformIsListedWithItsPrice(t *testing.T) {
	var out bytes.Buffer
	if err := listNativeTargets(&out); err != nil {
		t.Fatal(err)
	}
	listing := out.String()

	for name, target := range nativeTargets {
		if !strings.Contains(listing, name) {
			t.Errorf("%s is not listed", name)
		}
		if target.note == "" {
			t.Errorf("%s is listed with nothing said about it", name)
		}
	}

	if !strings.Contains(listing, "needs a toolchain") {
		t.Error("the listing does not explain its own mark")
	}
}

// TestTheBrowserIsTheOnlyTargetBuiltWithoutCgo fixes the one thing that decides
// whether a platform can draw at all.
//
// The window, the input and the GPU are reached through the operating system's
// own libraries; there is no pure-Go path to any of them. A target built
// without cgo compiles and opens nothing. The browser is the exception because
// there the browser is the window.
func TestTheBrowserIsTheOnlyTargetBuiltWithoutCgo(t *testing.T) {
	for name := range nativeTargets {
		goos, _, _ := strings.Cut(name, "/")
		wantCgo := goos != "js"

		if got := cgoFor(goos); got != wantCgo {
			t.Errorf("%s builds with cgo=%v, want %v", name, got, wantCgo)
		}
	}
}

// TestEveryWindowsArtifactIsNamedLikeOne keeps a build for Windows from
// producing a file that machine will not run.
func TestEveryWindowsArtifactIsNamedLikeOne(t *testing.T) {
	for name, target := range nativeTargets {
		goos, _, _ := strings.Cut(name, "/")

		switch goos {
		case "windows":
			if target.extension != ".exe" {
				t.Errorf("%s produces %q, and Windows will not run it", name, target.extension)
			}
		case "js":
			if target.extension != ".wasm" {
				t.Errorf("%s produces %q, and a browser will not load it", name, target.extension)
			}
		default:
			if target.extension != "" {
				t.Errorf("%s produces %q, and nothing on that platform expects a suffix", name, target.extension)
			}
		}
	}
}
