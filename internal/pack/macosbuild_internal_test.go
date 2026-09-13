package pack

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestAMacBundleWithoutAKeyIsStillSealed fixes a development artifact that
// opened locally but failed the platform's own verification.
//
// The Go linker signs the Mach-O file it writes. That is not a bundle
// signature: the Info.plist and resources remain outside its seal, so a strict
// verification refuses the .app. A build with no distribution identity still
// needs an ad hoc signature over the completed bundle.
func TestAMacBundleWithoutAKeyIsStillSealed(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("codesign is a macOS tool")
	}

	source := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":  "module example.test/macprobe\n",
		"main.go": "package main\n\nfunc main() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv("GOWORK", "off")
	t.Chdir(source)

	output := filepath.Join(t.TempDir(), "Probe.app")
	destination(t, output)
	if err := buildMac(t.TempDir(), &buildInfo{
		appID:   "dev.local.probe",
		archs:   []string{runtime.GOARCH},
		name:    "Probe",
		pkgPath: ".",
		version: Semver{Major: 1, Minor: 2, Patch: 3, VersionCode: 4},
	}); err != nil {
		t.Fatalf("packaging without a signing key: %v", err)
	}

	verify := exec.Command("codesign", "--verify", "--deep", "--strict", "--verbose=4", output)
	if result, err := verify.CombinedOutput(); err != nil {
		t.Fatalf("the ad hoc bundle did not pass strict verification: %v\n%s", err, result)
	}

	manifest := filepath.Join(output, "Contents", "Info.plist")
	file, err := os.OpenFile(manifest, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\n"); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	tampered := exec.Command("codesign", "--verify", "--deep", "--strict", "--verbose=4", output)
	if result, err := tampered.CombinedOutput(); err == nil {
		t.Fatalf("changing Info.plist did not break the bundle seal:\n%s", result)
	}
}
