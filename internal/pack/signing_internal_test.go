package pack

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testPassword = "correct-horse-battery-staple"

// fakeTool writes an executable at dir/name that records its arguments and
// the password it found under secretEnv into dir/name.log, prints out, and
// exits with code.
func fakeTool(t *testing.T, dir, name, out string, code int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake tools are shell scripts")
	}
	path := filepath.Join(dir, name)
	script := "#!/bin/sh\n" +
		"log=\"$0.log\"\n" +
		"for a in \"$@\"; do printf '%s\\n' \"$a\" >> \"$log\"; done\n" +
		"printf 'secret=%s\\n' \"$" + secretEnv + "\" >> \"$log\"\n" +
		"printf '%s' '" + out + "'\n" +
		"exit " + string(rune('0'+code)) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// logOf answers what the fake tool at dir/name recorded.
func logOf(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name+".log"))
	if err != nil {
		t.Fatalf("%s never ran: %v", name, err)
	}
	return string(b)
}

// assertHandedOverByEnvironment checks that a tool received the password and
// that its command line did not carry it.
func assertHandedOverByEnvironment(t *testing.T, log, tool, flag string) {
	t.Helper()
	args, secret, _ := strings.Cut(log, "secret=")
	if strings.Contains(args, testPassword) {
		t.Errorf("%s was given the password on its command line, which every process on the machine can read:\n%s", tool, args)
	}
	if !strings.Contains(args, flag) {
		t.Errorf("%s was not told to read the password from %s:\n%s", tool, flag, args)
	}
	if strings.TrimSpace(secret) != testPassword {
		t.Errorf("%s did not find the password in its environment: %q", tool, secret)
	}
}

// An APK is signed with the password in apksigner's environment, never on its
// command line.
func TestAnAPKIsSignedWithThePasswordOutOfTheCommandLine(t *testing.T) {
	buildtools := t.TempDir()
	fakeTool(t, buildtools, "zipalign", "", 0)
	fakeTool(t, buildtools, "apksigner", "", 0)

	bi := &buildInfo{key: filepath.Join(t.TempDir(), "release.keystore"), password: testPassword}
	if err := signAPK(t.TempDir(), filepath.Join(t.TempDir(), "app.apk"), &androidTools{buildtools: buildtools}, bi); err != nil {
		t.Fatalf("signAPK: %v", err)
	}
	assertHandedOverByEnvironment(t, logOf(t, buildtools, "apksigner"), "apksigner", "env:"+secretEnv)
}

// An AAB is listed and signed with the password in the environment of keytool
// and jarsigner, never on their command lines.
func TestAnAABIsSignedWithThePasswordOutOfTheCommandLine(t *testing.T) {
	buildtools := t.TempDir()
	fakeTool(t, buildtools, "zipalign", "", 0)
	if err := os.WriteFile(filepath.Join(buildtools, "bundletool-all.jar"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	fakeTool(t, bin, "java", "", 0)
	fakeTool(t, bin, "keytool", "Alias name: release\n", 0)
	fakeTool(t, bin, "jarsigner", "", 0)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	bi := &buildInfo{key: filepath.Join(t.TempDir(), "release.keystore"), password: testPassword}
	if err := signAAB(t.TempDir(), filepath.Join(t.TempDir(), "app.aab"), &androidTools{buildtools: buildtools}, bi); err != nil {
		t.Fatalf("signAAB: %v", err)
	}
	assertHandedOverByEnvironment(t, logOf(t, bin, "keytool"), "keytool", "-keypass:env\n"+secretEnv)
	assertHandedOverByEnvironment(t, logOf(t, bin, "jarsigner"), "jarsigner", "-storepass:env\n"+secretEnv)
}

// The keystore generated when there is none is created with its password in
// keytool's environment as well.
func TestTheGeneratedKeystoreTakesThePasswordOutOfTheCommandLine(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("JAVA_HOME", "")
	bin := t.TempDir()
	fakeTool(t, bin, "keytool", "", 0)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	bi := &buildInfo{password: testPassword}
	if err := defaultAndroidKeystore(t.TempDir(), bi); err != nil {
		t.Fatalf("defaultAndroidKeystore: %v", err)
	}
	// The password is the well-known debug one, which the command also writes as
	// the alias, so what is checked is the flag that would carry it.
	log := logOf(t, bin, "keytool")
	if strings.Contains(log, "-storepass\n") {
		t.Errorf("keytool -genkey was given the password on its command line:\n%s", log)
	}
	if !strings.HasSuffix(strings.TrimSpace(log), "secret="+bi.password) {
		t.Errorf("keytool -genkey did not find the password in its environment:\n%s", log)
	}
	if !strings.Contains(log, "-storepass:env\n"+secretEnv) {
		t.Errorf("keytool -genkey was not told to read the password from the environment:\n%s", log)
	}
}

// A failing tool's error and the command line --verbose prints carry no
// password, even when the tool echoes what it was given.
func TestAFailedCommandRedactsItsPasswords(t *testing.T) {
	dir := t.TempDir()
	tool := fakeTool(t, dir, "notarytool", "rejected "+testPassword+" and other-secret", 3)

	var printed bytes.Buffer
	defer Options{PrintCommands: true}.apply(&printed, &printed)()

	cmd := exec.Command(tool, "submit", "--password", "other-secret")
	cmd.Env = secretEnviron(testPassword)
	_, err := runCmdRaw(cmd)
	if err == nil {
		t.Fatal("a command that exited 3 was reported as a success")
	}
	for _, secret := range []string{testPassword, "other-secret"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("the error carries %q: %v", secret, err)
		}
		if strings.Contains(printed.String(), secret) {
			t.Errorf("--verbose printed %q: %s", secret, printed.String())
		}
	}
	if !strings.Contains(err.Error(), "[redacted]") || !strings.Contains(printed.String(), "--password [redacted]") {
		t.Errorf("the redaction does not show where the password was:\nerror: %v\nprinted: %s", err, printed.String())
	}
}
