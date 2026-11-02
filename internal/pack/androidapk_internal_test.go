package pack

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// This builds a real APK of an application that imports the push package, and
// reads the artifact back. It is the only test that runs the whole Android
// pipeline -- the Java of the package compiled with the runtime's, a library
// merged in, the resources linked, the dex written, the library signed -- and
// it needs what that pipeline needs: the Android SDK and NDK, a JDK, and the
// source of the runtime to build against, which is not in this repository.
//
//	ARANDU_AYRA_SRC=/path/to/ayra go test ./internal/pack -run APK
//
// Without them it is skipped, and says what was missing.

// stubSources stand in for the push service's libraries. The package's Java
// compiles against these and not against the real ones, which is what lets this
// test run with no network and no licence to accept: what it proves is that the
// packager wires a library in, and the real libraries are the project's to
// bring.
var stubSources = map[string]string{
	"com/google/android/gms/tasks/OnCompleteListener.java": `package com.google.android.gms.tasks;
public interface OnCompleteListener<T> { void onComplete(Task<T> task); }`,
	"com/google/android/gms/tasks/Task.java": `package com.google.android.gms.tasks;
public abstract class Task<T> {
	public abstract boolean isSuccessful();
	public abstract T getResult();
	public abstract Task<T> addOnCompleteListener(OnCompleteListener<T> l);
}`,
	"com/google/firebase/messaging/FirebaseMessaging.java": `package com.google.firebase.messaging;
import com.google.android.gms.tasks.Task;
public class FirebaseMessaging {
	public static FirebaseMessaging getInstance() { throw new IllegalStateException("stub"); }
	public Task<String> getToken() { return null; }
}`,
	"com/google/firebase/messaging/RemoteMessage.java": `package com.google.firebase.messaging;
import java.util.Map;
public class RemoteMessage {
	public static class Notification { public String getTitle() { return ""; } public String getBody() { return ""; } }
	public String getMessageId() { return ""; }
	public Notification getNotification() { return null; }
	public Map<String, String> getData() { return null; }
	public long getSentTime() { return 0; }
}`,
	"com/google/firebase/messaging/FirebaseMessagingService.java": `package com.google.firebase.messaging;
public class FirebaseMessagingService extends android.app.Service {
	public void onNewToken(String token) {}
	public void onMessageReceived(RemoteMessage message) {}
	@Override public android.os.IBinder onBind(android.content.Intent intent) { return null; }
}`,
}

const stubManifest = `<manifest xmlns:android="http://schemas.android.com/apk/res/android" package="com.google.firebase.stub">
	<uses-sdk android:minSdkVersion="21"/>
	<uses-permission android:name="android.permission.INTERNET"/>
	<application>
		<service android:name="com.google.firebase.messaging.FirebaseMessagingService" android:exported="false">
			<intent-filter android:priority="-500"><action android:name="com.google.firebase.MESSAGING_EVENT"/></intent-filter>
		</service>
	</application>
</manifest>`

func TestAnAPKOfAPushApplicationDeclaresTheServiceAndThePermission(t *testing.T) {
	buildPushAPK(t, "")
}

// TestAnAPKWithTheRealPushLibraries is the same build against the libraries the
// push service really ships, which a project downloads and this repository
// does not carry. ARANDU_ANDROID_LIBS names a directory holding the .aar and
// .jar files of firebase-messaging and what it depends on; the build is then
// given a google-services.json too, and the resources made from it are read
// back.
func TestAnAPKWithTheRealPushLibraries(t *testing.T) {
	libs := os.Getenv("ARANDU_ANDROID_LIBS")
	if libs == "" {
		t.Skip("set ARANDU_ANDROID_LIBS to a directory of firebase-messaging and its dependencies")
	}
	buildPushAPK(t, libs)
}

// buildPushAPK builds the example application and checks the package. With no
// directory of libraries it builds against stubs.
func buildPushAPK(t *testing.T, realLibs string) {
	t.Helper()
	src := os.Getenv("ARANDU_AYRA_SRC")
	sdk := os.Getenv("ANDROID_HOME")
	if src == "" || sdk == "" {
		t.Skip("set ARANDU_AYRA_SRC to the runtime's source and ANDROID_HOME to the SDK to build an APK")
	}
	javac, err := findJavaC()
	if err != nil {
		t.Skipf("no JDK: %v", err)
	}
	platform, err := latestPlatform(sdk)
	if err != nil {
		t.Skipf("no platform in the SDK: %v", err)
	}

	work := t.TempDir()
	library := realLibs
	if library == "" {
		library = buildStubLibrary(t, work, javac, filepath.Join(platform, "android.jar"))
	}

	app := filepath.Join(work, "app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(app, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.test/pushapp\n\ngo 1.26.10\n\nrequire github.com/arandu-io/ayra v0.0.0\n\nreplace github.com/arandu-io/ayra => "+src+"\n")
	if sum, err := os.ReadFile(filepath.Join(src, "go.sum")); err == nil {
		write("go.sum", string(sum))
	}
	write("main.go", `package main

import (
	"context"

	"github.com/arandu-io/ayra"
	"github.com/arandu-io/ayra/live"
	"github.com/arandu-io/ayra/push"
	"github.com/arandu-io/ayra/shell"
)

func main() {
	shell.Exit(shell.Run(shell.Config{Title: "Probe"}, func(c ayra.Context) ayra.Dimensions {
		_ = push.Token()
		_ = live.NewPlayer(live.PlayerConfig{})
		go push.Request(context.Background())
		return ayra.Dimensions{}
	}))
}
`)
	// The application's go.mod names only the runtime; what the runtime needs is
	// filled in from the module cache, with no network.
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = app
	tidy.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOFLAGS=-mod=mod")
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Skipf("the example application's dependencies are not in the module cache: %v\n%s", err, out)
	}
	googleServicesFile := ""
	if realLibs != "" {
		googleServicesFile = filepath.Join(work, "google-services.json")
		if err := os.WriteFile(googleServicesFile, []byte(`{"project_info":{"project_number":"1234567890","project_id":"probe-app"},
"client":[{"client_info":{"mobilesdk_app_id":"1:1234567890:android:abcdef","android_client_info":{"package_name":"dev.local.pushprobe"}},"api_key":[{"current_key":"AIzaProbeKey"}]}]}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(app)

	apk := filepath.Join(work, "probe.apk")
	var out bytes.Buffer
	err = Build(Options{
		Package: ".", Target: "android", Arch: []string{"arm64"}, Output: apk,
		AppID: "dev.local.pushprobe", Name: "Probe", Version: "1.0.0.1",
		Libraries: []string{library}, GoogleServices: googleServicesFile,
		Placeholders: map[string]string{"pushChannelId": "alerts", "pushChannelName": "Alerts"},
		Tags:         "", PrintCommands: false,
	}, &out, &out)
	if err != nil {
		t.Fatalf("the APK did not build: %v\n%s", err, out.String())
	}

	tree := manifestTree(t, sdk, apk)
	wants := []string{
		"android.permission.POST_NOTIFICATIONS",
		"android.permission.INTERNET",
		"io.arandu.ayra.push.AyraMessagingService",
		"com.google.firebase.MESSAGING_EVENT",
		"com.google.firebase.messaging.default_notification_channel_id",
		"alerts",
		"io.arandu.ayra.AyraActivity",
	}
	if realLibs != "" {
		wants = append(wants, "com.google.firebase.provider.FirebaseInitProvider", "com.google.firebase.iid.FirebaseInstanceIdReceiver", "dev.local.pushprobe.firebaseinitprovider")
	}
	for _, want := range wants {
		if !strings.Contains(tree, want) {
			t.Errorf("the manifest of the APK does not say %q:\n%s", want, tree)
		}
	}

	files := readAPK(t, apk)
	if dex := files["classes.dex"]; !bytes.Contains(dex, []byte("Lio/arandu/ayra/push/AyraMessagingService;")) ||
		!bytes.Contains(dex, []byte("Lio/arandu/ayra/push/AyraPush;")) ||
		!bytes.Contains(dex, []byte("Lio/arandu/ayra/live/AyraDecoder;")) ||
		!bytes.Contains(dex, []byte("Lcom/google/firebase/messaging/FirebaseMessagingService;")) {
		t.Error("the dex does not carry the package's classes and the library's")
	}
	if realLibs != "" {
		if len(files["classes2.dex"]) == 0 && len(files["classes.dex"]) < 200_000 {
			t.Errorf("the real libraries are in the dex? classes.dex is %d bytes", len(files["classes.dex"]))
		}
		resources := manifestStrings(t, sdk, apk)
		if !resources.has("google_app_id", "1:1234567890:android:abcdef") || !resources.has("gcm_defaultSenderId", "1234567890") {
			t.Errorf("the resources made from google-services.json are not in the package")
		}
	}
	so := files["lib/arm64-v8a/libayra.so"]
	for _, symbol := range []string{
		"Java_io_arandu_ayra_push_AyraPush_onToken",
		"Java_io_arandu_ayra_push_AyraPush_onMessage",
		"Java_io_arandu_ayra_push_AyraPush_onPermission",
		"Java_io_arandu_ayra_live_AyraDecoder_onFrame",
	} {
		if !bytes.Contains(so, []byte(symbol)) {
			t.Errorf("libayra.so does not export %s, so the Java half cannot reach the Go one", symbol)
		}
	}
}

// buildStubLibrary compiles the stub sources and packs them as an archive with
// a manifest.
func buildStubLibrary(t *testing.T, work, javac, androidJar string) string {
	t.Helper()
	srcDir, classes := filepath.Join(work, "stub-src"), filepath.Join(work, "stub-classes")
	var files []string
	for name, body := range stubSources {
		path := filepath.Join(srcDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, path)
	}
	if err := os.MkdirAll(classes, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(javac, append([]string{"-source", "8", "-target", "8", "-bootclasspath", androidJar, "-d", classes}, files...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the stub library did not compile: %v\n%s", err, out)
	}
	jar := filepath.Join(work, "classes.jar")
	if err := writeJar(jar, classes); err != nil {
		t.Fatal(err)
	}
	jarBytes, err := os.ReadFile(jar)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string][]byte{"AndroidManifest.xml": []byte(stubManifest), "classes.jar": jarBytes} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(work, "stub-messaging.aar")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// manifestTree reads the manifest back out of the built package.
func manifestTree(t *testing.T, sdk, apk string) string {
	t.Helper()
	tools, err := latestTools(sdk)
	if err != nil {
		t.Skipf("no build tools: %v", err)
	}
	out, err := exec.Command(filepath.Join(tools, "aapt2"), "dump", "xmltree", "--file", "AndroidManifest.xml", apk).CombinedOutput()
	if err != nil {
		t.Fatalf("aapt2 could not read the manifest back: %v\n%s", err, out)
	}
	return string(out)
}

// readAPK answers the files of the package by name.
func readAPK(t *testing.T, apk string) map[string][]byte {
	t.Helper()
	zr, err := zip.OpenReader(apk)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	files := map[string][]byte{}
	for _, f := range zr.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r)
		if cerr := r.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = body
	}
	return files
}

// stringResources is what aapt2 prints of the package's resources.
type stringResources struct{ raw string }

// has reports whether the package has a string resource of this name with this
// value.
func (s stringResources) has(name, value string) bool {
	lines := strings.Split(s.raw, "\n")
	for i, line := range lines {
		if strings.Contains(line, "string/"+name) {
			for _, next := range lines[i+1 : min(i+4, len(lines))] {
				if strings.Contains(next, `"`+value+`"`) {
					return true
				}
			}
		}
	}
	return false
}

// manifestStrings reads the resources of the package.
func manifestStrings(t *testing.T, sdk, apk string) stringResources {
	t.Helper()
	tools, err := latestTools(sdk)
	if err != nil {
		t.Skipf("no build tools: %v", err)
	}
	out, err := exec.Command(filepath.Join(tools, "aapt2"), "dump", "resources", apk).CombinedOutput()
	if err != nil {
		t.Fatalf("aapt2 could not read the resources back: %v\n%s", err, out)
	}
	return stringResources{string(out)}
}

// TestAnIOSSimulatorAppOfAPushApplicationDeclaresTheBackgroundMode builds the
// same application for the iOS simulator and reads the property list back: the
// remote-notification mode is there because the push package is in the graph.
// The aps-environment entitlement is the signing step's, and a simulator build
// is not signed, so that half is covered by the check on the profile.
func TestAnIOSSimulatorAppOfAPushApplicationDeclaresTheBackgroundMode(t *testing.T) {
	src := os.Getenv("ARANDU_AYRA_SRC")
	if src == "" {
		t.Skip("set ARANDU_AYRA_SRC to the runtime's source")
	}
	if _, err := exec.LookPath("xcrun"); err != nil {
		t.Skip("building for iOS needs Xcode")
	}
	if out, err := exec.Command("xcrun", "--sdk", "iphonesimulator", "--show-sdk-path").Output(); err != nil || len(out) == 0 {
		t.Skip("no iOS simulator SDK")
	}

	work := t.TempDir()
	app := filepath.Join(work, "app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	mod := "module example.test/pushapp\n\ngo 1.26.10\n\nrequire github.com/arandu-io/ayra v0.0.0\n\nreplace github.com/arandu-io/ayra => " + src + "\n"
	if err := os.WriteFile(filepath.Join(app, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}
	if sum, err := os.ReadFile(filepath.Join(src, "go.sum")); err == nil {
		if err := os.WriteFile(filepath.Join(app, "go.sum"), sum, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	main := `package main

import (
	"github.com/arandu-io/ayra"
	"github.com/arandu-io/ayra/live"
	"github.com/arandu-io/ayra/push"
	"github.com/arandu-io/ayra/shell"
)

func main() {
	shell.Exit(shell.Run(shell.Config{Title: "Probe"}, func(c ayra.Context) ayra.Dimensions {
		_ = push.Token()
		_ = live.NewPlayer(live.PlayerConfig{})
		return ayra.Dimensions{}
	}))
}
`
	if err := os.WriteFile(filepath.Join(app, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = app
	tidy.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOFLAGS=-mod=mod")
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Skipf("the example application's dependencies are not in the module cache: %v\n%s", err, out)
	}
	t.Chdir(app)

	out := filepath.Join(work, "probe.app")
	if keep := os.Getenv("ARANDU_KEEP_APP"); keep != "" {
		out = keep
	}
	var log bytes.Buffer
	if err := Build(Options{
		Package: ".", Target: "ios", Arch: []string{"simarm64"}, Output: out,
		AppID: "dev.local.pushprobe", Name: "Probe", Version: "1.0.0.1",
	}, &log, &log); err != nil {
		t.Fatalf("the simulator app did not build: %v\n%s", err, log.String())
	}

	plist, err := exec.Command("plutil", "-p", filepath.Join(out, "Info.plist")).CombinedOutput()
	if err != nil {
		t.Fatalf("the property list cannot be read: %v\n%s", err, plist)
	}
	if !strings.Contains(string(plist), "UIBackgroundModes") || !strings.Contains(string(plist), "remote-notification") {
		t.Errorf("the application does not declare the remote-notification mode:\n%s", plist)
	}
}
