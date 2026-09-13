package pack

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestTheWindowsResourceReachesTheProgramAndThenLeaves fixes both halves of one
// defect, and they can only be seen together.
//
// The resource section is handed to the linker by leaving a file in the
// package's own directory: go build links every *_windows_*.syso it finds
// there, without being asked. That makes the file an input with two failure
// modes. Written to the wrong directory it is never linked, and the program is
// built without its icon, manifest and version block -- silently, because
// nothing reports a resource file that was not found. Left behind afterwards it
// is linked into the next build of that package, which is the following
// architecture of the same packaging run and every ordinary build of the
// project after it.
//
// So the program is inspected for the manifest, which proves the file was in
// the directory the toolchain reads, and the directory is inspected afterwards,
// which proves it did not stay there.
func TestTheWindowsResourceReachesTheProgramAndThenLeaves(t *testing.T) {
	pkgDir := probeModule(t)
	out := filepath.Join(t.TempDir(), "probe.exe")
	destination(t, out)

	err := buildWindows(t.TempDir(), &buildInfo{
		appID:   "dev.local.probe",
		archs:   []string{"amd64"},
		minsdk:  10,
		name:    "probe",
		pkgDir:  pkgDir,
		pkgPath: "example.test/probe",
		version: Semver{Major: 1, Minor: 2, Patch: 3, VersionCode: 4},
	})
	if err != nil {
		t.Fatalf("packaging for Windows: %v", err)
	}

	program, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the program was not written: %v", err)
	}
	if !bytes.Contains(program, []byte(`<assemblyIdentity type="win32" name="probe"`)) {
		t.Error("the program carries no manifest, so the resource file was written somewhere the toolchain does not read")
	}

	if left := sysoIn(t, pkgDir); len(left) > 0 {
		t.Errorf("the resource files %v are still in the package directory, and the next build there would link them", left)
	}
}

// TestTheWindowsResourceLeavesAfterAFailedLink is the same removal on the path
// that skips it.
//
// A build that fails is exactly when a stray file is most expensive: whoever
// fixes the failure builds again in that directory, and the resource section of
// the attempt that failed is linked into the result.
func TestTheWindowsResourceLeavesAfterAFailedLink(t *testing.T) {
	pkgDir := probeModule(t)
	destination(t, filepath.Join(t.TempDir(), "probe.exe"))

	err := buildWindows(t.TempDir(), &buildInfo{
		appID:   "dev.local.probe",
		archs:   []string{"amd64"},
		minsdk:  10,
		name:    "probe",
		pkgDir:  pkgDir,
		pkgPath: "example.test/probe/there-is-no-such-package",
		version: Semver{Major: 1, VersionCode: 1},
	})
	if err == nil {
		t.Fatal("a package that does not exist was packaged")
	}

	if left := sysoIn(t, pkgDir); len(left) > 0 {
		t.Errorf("the resource files %v survived a failed build", left)
	}
}

// TestARequestedWindowsSignatureCannotBeIgnored fixes a successful unsigned
// artifact from a command that was explicitly given a signing key.
//
// The previous path never read the key. Even a path that did not exist was
// accepted, the executable was written, and the command reported success. A
// signing request is resolved before the build so failure leaves no artifact a
// release pipeline can mistake for the signed one.
func TestARequestedWindowsSignatureCannotBeIgnored(t *testing.T) {
	pkgDir := probeModule(t)
	output := filepath.Join(t.TempDir(), "probe.exe")
	destination(t, output)

	err := buildWindows(t.TempDir(), &buildInfo{
		archs:   []string{"amd64"},
		key:     filepath.Join(t.TempDir(), "missing.pfx"),
		name:    "probe",
		pkgDir:  pkgDir,
		pkgPath: "example.test/probe",
		version: Semver{Major: 1, VersionCode: 1},
	})
	if err == nil {
		t.Fatal("a Windows signing key was ignored")
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Errorf("an unsigned artifact was left at the requested destination: %v", statErr)
	}
}

// TestWindowsSigningNamesTheToolItNeeds keeps a missing external prerequisite
// from reading like a compilation failure.
func TestWindowsSigningNamesTheToolItNeeds(t *testing.T) {
	key := filepath.Join(t.TempDir(), "probe.pfx")
	if err := os.WriteFile(key, []byte("not read before signtool"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())

	_, err := windowsSignerFor(&buildInfo{key: key})
	if err == nil {
		t.Fatal("a signing request with no Authenticode tool was accepted")
	}
	for _, want := range []string{"signtool", "Windows SDK", "PATH"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s: %v", want, err)
		}
	}
}

// TestWindowsSigningUsesAndVerifiesAuthenticode keeps the key, the digest and
// the verification policy on the path that produces the executable.
func TestWindowsSigningUsesAndVerifiesAuthenticode(t *testing.T) {
	signerDir := t.TempDir()
	log := filepath.Join(t.TempDir(), "signtool.log")
	powershellLog := filepath.Join(t.TempDir(), "powershell.log")
	writeFakeSignTool(t, signerDir)
	writeFakePowerShell(t, signerDir)
	t.Setenv("ARANDU_TEST_SIGNTOOL_LOG", log)
	t.Setenv("ARANDU_TEST_POWERSHELL_LOG", powershellLog)
	t.Setenv("PATH", signerDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	pkgDir := probeModule(t)
	key := filepath.Join(t.TempDir(), "probe.pfx")
	if err := os.WriteFile(key, []byte("fake certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	var verbose bytes.Buffer
	restore := Options{Target: "windows", PrintCommands: true}.apply(&verbose, &bytes.Buffer{})
	defer restore()
	destination(t, filepath.Join(t.TempDir(), "probe.exe"))

	const password = "must-not-be-printed"
	err := buildWindows(t.TempDir(), &buildInfo{
		archs:    []string{"amd64"},
		key:      key,
		password: password,
		name:     "probe",
		pkgDir:   pkgDir,
		pkgPath:  "example.test/probe",
		version:  Semver{Major: 1, VersionCode: 1},
	})
	if err != nil {
		t.Fatalf("packaging with signtool: %v", err)
	}

	called, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(called)), "\n")
	if len(lines) != 2 {
		t.Fatalf("signtool was called %d times, want sign and verify:\n%s", len(lines), called)
	}
	for _, want := range []string{"sign", "/fd", "SHA256", "/s", "/sha1", "0123456789abcdef0123456789abcdef01234567"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("the signing command omitted %q: %s", want, lines[0])
		}
	}
	signFields := strings.Fields(lines[0])
	store := ""
	for index, field := range signFields {
		if field == "/s" && index+1 < len(signFields) {
			store = signFields[index+1]
			break
		}
	}
	storeParts := strings.Split(store, "-")
	if len(storeParts) != 4 || storeParts[0] != "Arandu" || storeParts[1] == "" || len(storeParts[2]) != 32 || storeParts[3] == "" {
		t.Errorf("the signing command did not use an isolated random certificate store: %s", lines[0])
	}
	storePrefix := strings.TrimSuffix(store, "-"+storeParts[len(storeParts)-1])
	for _, forbidden := range []string{"/p", password, key} {
		if strings.Contains(lines[0], forbidden) {
			t.Errorf("the signing command exposed %q: %s", forbidden, lines[0])
		}
	}
	for _, want := range []string{"verify", "/pa", "/v"} {
		if !strings.Contains(lines[1], want) {
			t.Errorf("the verification command omitted %q: %s", want, lines[1])
		}
	}
	if strings.Contains(verbose.String(), password) {
		t.Error("the signing password was printed in verbose output")
	}
	imported, err := os.ReadFile(powershellLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(imported), password) {
		t.Errorf("the certificate import exposed its password in argv:\n%s", imported)
	}
	if !strings.Contains(string(imported), "password-in-environment=true") {
		t.Errorf("the certificate import did not receive its password through the environment:\n%s", imported)
	}
	for _, cleanup := range []string{"catch", "-DeleteKey"} {
		if !strings.Contains(string(imported), cleanup) {
			t.Errorf("the certificate import does not guarantee %q cleanup after a partial failure:\n%s", cleanup, imported)
		}
	}
	if storePrefix != "" && !strings.Contains(string(imported), storePrefix) {
		t.Errorf("the isolated certificate store was not used consistently:\n%s", imported)
	}
	if strings.Contains(string(imported), `CurrentUser\My`) {
		t.Errorf("the certificate import mutated the shared CurrentUser\\My store:\n%s", imported)
	}
	if !strings.Contains(verbose.String(), "temporary CurrentUser certificate store "+storePrefix) {
		t.Errorf("verbose output does not show temporary certificate cleanup:\n%s", verbose.String())
	}
}

// TestUnexpectedCertificateImportOutputIsCleaned keeps a successful import
// with unusable output from bypassing the same private-key cleanup as a
// signing failure. This is defensive against wrapper/profile output changing
// what PowerShell writes even though the import itself succeeded.
func TestUnexpectedCertificateImportOutputIsCleaned(t *testing.T) {
	for _, test := range []struct {
		name string
		mode string
	}{
		{name: "empty output", mode: "empty"},
		{name: "invalid thumbprint", mode: "invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(t.TempDir(), "powershell.log")
			writeFakePowerShell(t, dir)
			t.Setenv("ARANDU_TEST_POWERSHELL_LOG", log)
			t.Setenv("ARANDU_TEST_POWERSHELL_OUTPUT", test.mode)

			_, err := importWindowsCertificate(filepath.Join(dir, executableName("powershell")), "probe.pfx", "secret")
			if err == nil {
				t.Fatal("an unusable certificate import response was accepted")
			}
			called, readErr := os.ReadFile(log)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if calls := strings.Count(string(called), "password-in-environment="); calls != 2 {
				t.Fatalf("PowerShell was called %d times, want import and cleanup:\n%s", calls, called)
			}
			if !strings.Contains(string(called), "-DeleteKey") {
				t.Errorf("cleanup did not remove the imported private key:\n%s", called)
			}
		})
	}
}

func executableName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// TestAFailedWindowsSignatureLeavesNoExecutable keeps a signing tool failure
// from leaving the unsigned build at the release destination.
func TestAFailedWindowsSignatureLeavesNoExecutable(t *testing.T) {
	signerDir := t.TempDir()
	writeFakeSignTool(t, signerDir)
	t.Setenv("ARANDU_TEST_SIGNTOOL_LOG", filepath.Join(t.TempDir(), "signtool.log"))
	t.Setenv("ARANDU_TEST_SIGNTOOL_FAIL", "sign")
	t.Setenv("PATH", signerDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	pkgDir := probeModule(t)
	key := filepath.Join(t.TempDir(), "probe.pfx")
	if err := os.WriteFile(key, []byte("fake certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "probe.exe")
	destination(t, output)

	err := buildWindows(t.TempDir(), &buildInfo{
		archs:   []string{"amd64"},
		key:     key,
		name:    "probe",
		pkgDir:  pkgDir,
		pkgPath: "example.test/probe",
		version: Semver{Major: 1, VersionCode: 1},
	})
	if err == nil {
		t.Fatal("a failed Authenticode signature was reported as successful")
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Errorf("the executable survived a failed signature: %v", statErr)
	}
}

// TestAFailedWindowsSignatureDoesNotReplaceThePreviousArtifact keeps signing
// transactional for repeated packaging into the same destination.
func TestAFailedWindowsSignatureDoesNotReplaceThePreviousArtifact(t *testing.T) {
	signerDir := t.TempDir()
	writeFakeSignTool(t, signerDir)
	t.Setenv("ARANDU_TEST_SIGNTOOL_LOG", filepath.Join(t.TempDir(), "signtool.log"))
	t.Setenv("ARANDU_TEST_SIGNTOOL_FAIL", "sign")
	t.Setenv("PATH", signerDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	pkgDir := probeModule(t)
	key := filepath.Join(t.TempDir(), "probe.pfx")
	if err := os.WriteFile(key, []byte("fake certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "probe.exe")
	const previous = "previous verified artifact"
	if err := os.WriteFile(output, []byte(previous), 0o600); err != nil {
		t.Fatal(err)
	}
	destination(t, output)

	err := buildWindows(t.TempDir(), &buildInfo{
		archs:   []string{"amd64"},
		key:     key,
		name:    "probe",
		pkgDir:  pkgDir,
		pkgPath: "example.test/probe",
		version: Semver{Major: 1, VersionCode: 1},
	})
	if err == nil {
		t.Fatal("a failed Authenticode signature was reported as successful")
	}
	got, readErr := os.ReadFile(output)
	if readErr != nil {
		t.Fatalf("the previous artifact was removed: %v", readErr)
	}
	if string(got) != previous {
		t.Errorf("the previous artifact was replaced after signing failed")
	}
}

// TestWindowsPublicationRestoresEveryPreviousArtifact keeps a failure while
// replacing one architecture from leaving a mixed release or deleting the
// last known-good executable.
func TestWindowsPublicationRestoresEveryPreviousArtifact(t *testing.T) {
	dir := t.TempDir()
	firstFinal := filepath.Join(dir, "probe_amd64.exe")
	secondFinal := filepath.Join(dir, "probe_arm64.exe")
	firstStaged := filepath.Join(dir, ".probe_amd64.new")
	secondStaged := filepath.Join(dir, ".probe_arm64.new")
	for path, contents := range map[string]string{
		firstFinal:   "previous amd64",
		secondFinal:  "previous arm64",
		firstStaged:  "new amd64",
		secondStaged: "new arm64",
	} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	builder := windowsBuilder{
		pending: []windowsProgram{
			{staged: firstStaged, final: firstFinal},
			{staged: secondStaged, final: secondFinal},
		},
		rename: func(oldPath, newPath string) error {
			if oldPath == secondStaged {
				return errors.New("simulated locked destination")
			}
			return os.Rename(oldPath, newPath)
		},
	}
	if err := builder.publishPrograms(); err == nil {
		t.Fatal("a failed executable replacement was reported as successful")
	}
	for path, want := range map[string]string{
		firstFinal:  "previous amd64",
		secondFinal: "previous arm64",
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("the previous artifact %s was not restored: %v", filepath.Base(path), err)
		}
		if string(got) != want {
			t.Errorf("artifact %s = %q, want %q", filepath.Base(path), got, want)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".previous") {
			t.Errorf("rollback left backup %s behind", entry.Name())
		}
	}
}

// writeFakeSignTool builds a process that records its arguments. It is a Go
// executable rather than a shell fixture so the test has the same runtime on
// every platform that can build the Windows target.
func writeFakeSignTool(t *testing.T, dir string) {
	t.Helper()

	source := filepath.Join(t.TempDir(), "main.go")
	body := `package main

import (
	"os"
	"strings"
)

func main() {
	file, err := os.OpenFile(os.Getenv("ARANDU_TEST_SIGNTOOL_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		panic(err)
	}
	if _, err := file.WriteString(strings.Join(os.Args[1:], " ") + "\n"); err != nil {
		panic(err)
	}
	if err := file.Close(); err != nil {
		panic(err)
	}
	if len(os.Args) > 1 && os.Getenv("ARANDU_TEST_SIGNTOOL_FAIL") == os.Args[1] {
		os.Exit(23)
	}
}
`
	if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	name := "signtool"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	command := exec.Command("go", "build", "-o", filepath.Join(dir, name), source)
	command.Env = append(os.Environ(), "GOWORK=off")
	if result, err := command.CombinedOutput(); err != nil {
		t.Fatalf("building fake signtool: %v\n%s", err, result)
	}
}

// writeFakePowerShell stands in for certificate import and cleanup on hosts
// that do not have a Windows certificate store. It records only argv and
// whether the secret arrived in the environment, never the secret itself.
func writeFakePowerShell(t *testing.T, dir string) {
	t.Helper()

	source := filepath.Join(t.TempDir(), "main.go")
	body := `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	file, err := os.OpenFile(os.Getenv("ARANDU_TEST_POWERSHELL_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		panic(err)
	}
	_, err = fmt.Fprintf(file, "args=%s password-in-environment=%t\n", strings.Join(os.Args[1:], " "), os.Getenv("ARANDU_SIGNPASS") != "")
	if err != nil {
		panic(err)
	}
	if err := file.Close(); err != nil {
		panic(err)
	}
	prefix := os.Args[len(os.Args)-1]
	switch os.Getenv("ARANDU_TEST_POWERSHELL_OUTPUT") {
	case "empty":
	case "invalid":
		fmt.Println(prefix + "-1")
		fmt.Println("not-a-thumbprint")
	default:
		fmt.Println(prefix + "-1")
		fmt.Println("0123456789ABCDEF0123456789ABCDEF01234567")
	}
}
`
	if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	name := executableName("powershell")
	command := exec.Command("go", "build", "-o", filepath.Join(dir, name), source)
	command.Env = append(os.Environ(), "GOWORK=off")
	if result, err := command.CombinedOutput(); err != nil {
		t.Fatalf("building fake PowerShell: %v\n%s", err, result)
	}
}

// probeModule writes the smallest module that can be built for Windows and
// makes it the working directory.
//
// A module of its own, and not a package of this repository: the resource file
// is written into the package's directory, and a test that proves it is removed
// has to be able to say the directory was empty to begin with.
func probeModule(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":  "module example.test/probe\n",
		"main.go": "package main\n\nfunc main() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// The go tool refuses a module that no workspace lists, and the suite may
	// be run from inside one.
	t.Setenv("GOWORK", "off")
	t.Chdir(dir)

	// t.TempDir answers a path under a symlinked directory on macOS, and the go
	// tool reports the resolved one. The directory is read back from the
	// process so the two halves of every assertion below are the same string.
	resolved, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// destination points the packaging sources at one output file for the length of
// a test.
func destination(t *testing.T, path string) {
	t.Helper()

	previous := *destPath
	*destPath = path
	t.Cleanup(func() { *destPath = previous })
}

// sysoIn answers the resource files left in a directory.
func sysoIn(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".syso") {
			left = append(left, entry.Name())
		}
	}
	return left
}
