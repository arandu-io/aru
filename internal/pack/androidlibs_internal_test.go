package pack

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A library manifest as an AAR carries one: a package, a minimum version, a
// permission, a service named relative to the package, a provider that uses a
// placeholder, an attribute meant for Gradle's merger, and a queries element.
const libraryManifest = `<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
	xmlns:tools="http://schemas.android.com/tools"
	package="com.example.messaging">
	<uses-sdk android:minSdkVersion="21" tools:overrideLibrary="x"/>
	<uses-permission android:name="android.permission.INTERNET"/>
	<uses-permission android:name="android.permission.WAKE_LOCK"/>
	<uses-feature android:name="android.hardware.faketouch" android:required="false"/>
	<permission android:name="${applicationId}.RECEIVER_PERMISSION" android:protectionLevel="signature"/>
	<queries>
		<intent><action android:name="com.example.BIND"/></intent>
	</queries>
	<application>
		<service android:name=".Relay" android:exported="false" tools:node="merge">
			<intent-filter>
				<action android:name="com.example.EVENT"/>
			</intent-filter>
		</service>
		<provider android:name="com.example.messaging.Init" android:authorities="${applicationId}.init" android:exported="false"/>
		<meta-data android:name="com.example.key" android:value="1"/>
	</application>
</manifest>`

func TestALibraryManifestIsMergedWithItsNamesQualified(t *testing.T) {
	merged, err := mergeManifests([]manifestSource{{name: "lib", pkg: "com.example.messaging", body: []byte(libraryManifest)}},
		map[string]string{"applicationId": "dev.local.probe"})
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"android.permission.INTERNET", "android.permission.WAKE_LOCK"}; !slices.Equal(merged.Permissions, want) {
		t.Errorf("permissions %v, want %v", merged.Permissions, want)
	}
	if want := []string{`name="android.hardware.faketouch"`}; !slices.Equal(merged.Features, want) {
		t.Errorf("features %v", merged.Features)
	}
	if len(merged.Components) != 3 {
		t.Fatalf("%d components: %v", len(merged.Components), merged.Components)
	}
	if !strings.Contains(merged.Components[0], `android:name="com.example.messaging.Relay"`) {
		t.Errorf("a name relative to the package was not qualified: %s", merged.Components[0])
	}
	if strings.Contains(merged.Components[0], "tools:") {
		t.Errorf("a tools attribute survived: %s", merged.Components[0])
	}
	if !strings.Contains(merged.Components[0], `<action android:name="com.example.EVENT"/>`) {
		t.Errorf("the service lost its intent filter: %s", merged.Components[0])
	}
	if !strings.Contains(merged.Components[1], `android:authorities="dev.local.probe.init"`) {
		t.Errorf("a placeholder was not resolved: %s", merged.Components[1])
	}
	if !strings.Contains(merged.Components[1], `android:name="com.example.messaging.Init"`) {
		t.Errorf("an absolute name was changed: %s", merged.Components[1])
	}
	if len(merged.TopLevel) != 1 || !strings.Contains(merged.TopLevel[0], `dev.local.probe.RECEIVER_PERMISSION`) {
		t.Errorf("top level %v", merged.TopLevel)
	}
	if len(merged.Queries) != 1 || !strings.Contains(merged.Queries[0], "com.example.BIND") {
		t.Errorf("queries %v", merged.Queries)
	}
}

func TestTwoManifestsDeclaringTheSameThingDeclareItOnce(t *testing.T) {
	one := manifestSource{name: "one", pkg: "a", body: []byte(`<manifest xmlns:android="http://schemas.android.com/apk/res/android" package="a">
<uses-permission android:name="android.permission.INTERNET"/>
<application><service android:name=".S"/><meta-data android:name="k" android:value="1"/></application></manifest>`)}
	two := manifestSource{name: "two", pkg: "a", body: one.body}
	merged, err := mergeManifests([]manifestSource{one, two}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Permissions) != 1 || len(merged.Components) != 2 {
		t.Errorf("permissions %v, components %v", merged.Permissions, merged.Components)
	}
}

// Firebase finds its parts through one service that every library declares with
// its own registrar in it. The service the system starts has to list them all.
func TestTheRegistrarsOfASharedServiceAreAddedTogether(t *testing.T) {
	declare := func(registrar string) manifestSource {
		return manifestSource{name: registrar, pkg: "com.google.firebase", body: []byte(`<manifest xmlns:android="http://schemas.android.com/apk/res/android" package="com.google.firebase">
<application>
<service android:name="com.google.firebase.components.ComponentDiscoveryService" android:exported="false" android:directBootAware="true">
<meta-data android:name="com.google.firebase.components:` + registrar + `" android:value="com.google.firebase.components.ComponentRegistrar"/>
</service>
</application></manifest>`)}
	}
	merged, err := mergeManifests([]manifestSource{declare("com.google.firebase.CommonKtx"), declare("com.google.firebase.messaging.Registrar"), declare("com.google.firebase.CommonKtx"), declare("com.google.firebase.installations.Registrar")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Components) != 1 {
		t.Fatalf("one service, got %d: %v", len(merged.Components), merged.Components)
	}
	service := merged.Components[0]
	for _, want := range []string{"CommonKtx", "messaging.Registrar", "installations.Registrar"} {
		if strings.Count(service, want) != 1 {
			t.Errorf("%s must be in the service once:\n%s", want, service)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(service), "</service>") {
		t.Errorf("the service is not closed:\n%s", service)
	}
}

// TestAMetaDataOfTheSameNameButAnotherValueIsKept: the first wins by name for
// components, but two settings that differ are two statements, and dropping the
// second silently is how a library ends up configured by another's value.
func TestAnUnresolvedPlaceholderIsAnError(t *testing.T) {
	src := manifestSource{name: "fragment", body: []byte(`<manifest xmlns:android="http://schemas.android.com/apk/res/android">
<application><meta-data android:name="channel" android:value="${pushChannelId}"/></application></manifest>`)}
	_, err := mergeManifests([]manifestSource{src}, map[string]string{"applicationId": "x"})
	if err == nil || !strings.Contains(err.Error(), "pushChannelId") || !strings.Contains(err.Error(), "-placeholder") {
		t.Errorf("an unresolved placeholder answered %v, want the name and how to set it", err)
	}
}

func TestAPlaceholderValueIsEscaped(t *testing.T) {
	src := manifestSource{name: "fragment", body: []byte(`<manifest xmlns:android="http://schemas.android.com/apk/res/android">
<application><meta-data android:name="channel" android:value="${name}"/></application></manifest>`)}
	merged, err := mergeManifests([]manifestSource{src}, map[string]string{"name": `A "quoted" & <odd>`})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(merged.Components[0], `"quoted"`) || strings.Contains(merged.Components[0], "<odd>") {
		t.Errorf("a value reached the manifest unescaped: %s", merged.Components[0])
	}
}

func TestAManifestThatIsNotXMLIsAnError(t *testing.T) {
	if _, err := mergeManifests([]manifestSource{{name: "bad", body: []byte(`<manifest><application></manifest>`)}}, nil); err == nil {
		t.Error("a broken manifest was merged")
	}
}

func TestTheNamesAFragmentAsksForAreTheDefaultsOfThePackager(t *testing.T) {
	for _, name := range []string{"pushChannelId", "pushChannelName"} {
		if manifestDefaults[name] == "" {
			t.Errorf("%s has no default, and the push package's manifest uses it", name)
		}
	}
}

// aar writes a library archive with what a test needs.
func aar(t *testing.T, dir, name string, files map[string]string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for entry, body := range files {
		w, err := zw.Create(entry)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAnArchiveIsUnpackedAndReadForWhatItCarries(t *testing.T) {
	src, work := t.TempDir(), t.TempDir()
	file := aar(t, src, "messaging.aar", map[string]string{
		"AndroidManifest.xml":          libraryManifest,
		"classes.jar":                  "jar",
		"libs/extra.jar":               "jar",
		"res/values/strings.xml":       "<resources/>",
		"jni/arm64-v8a/libnative.so":   "elf",
		"jni/armeabi-v7a/libnative.so": "elf",
		"assets/ignored.txt":           "x",
	})
	libs, err := openLibraries(work, []string{file})
	if err != nil || len(libs) != 1 {
		t.Fatalf("%v %v", libs, err)
	}
	lib := libs[0]
	if lib.pkg != "com.example.messaging" || lib.minSDK() != 21 {
		t.Errorf("package %q, minimum %d", lib.pkg, lib.minSDK())
	}
	if len(lib.jars) != 2 || filepath.Base(lib.jars[0]) != "classes.jar" || filepath.Base(lib.jars[1]) != "extra.jar" {
		t.Errorf("jars %v", lib.jars)
	}
	if lib.res == "" || len(libs.resourceDirs()) != 1 {
		t.Error("the resources were not found")
	}
	if len(lib.native) != 2 {
		t.Errorf("native libraries %v", lib.native)
	}
	if !slices.Equal(libs.packages(), []string{"com.example.messaging"}) {
		t.Errorf("packages %v", libs.packages())
	}
}

func TestADirectoryContributesItsLibrariesInNameOrder(t *testing.T) {
	src, work := t.TempDir(), t.TempDir()
	aar(t, src, "b.aar", map[string]string{"classes.jar": "x"})
	aar(t, src, "a.aar", map[string]string{"classes.jar": "x"})
	for name, body := range map[string]string{"c.jar": "jar", "notes.txt": "ignored"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	libs, err := openLibraries(work, []string{src})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, l := range libs {
		names = append(names, l.name)
	}
	if want := []string{"a.aar", "b.aar", "c.jar"}; !slices.Equal(names, want) {
		t.Errorf("libraries %v, want %v", names, want)
	}
}

// TestAnArchiveCannotWriteOutsideItsDirectory: an AAR is somebody else's file.
func TestAnArchiveCannotWriteOutsideItsDirectory(t *testing.T) {
	src, work := t.TempDir(), t.TempDir()
	file := aar(t, src, "evil.aar", map[string]string{"../../escaped.txt": "x"})
	if _, err := openLibraries(work, []string{file}); err == nil {
		t.Fatal("an archive wrote outside its directory")
	}
	if _, err := os.Stat(filepath.Join(work, "..", "escaped.txt")); err == nil {
		t.Error("the file was written")
	}
}

func TestALibraryThatIsNotThereIsAnError(t *testing.T) {
	if _, err := openLibraries(t.TempDir(), []string{"/nonexistent/library.aar"}); err == nil {
		t.Error("a missing library was accepted")
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "x.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := openLibraries(t.TempDir(), []string{filepath.Join(src, "x.txt")}); err == nil {
		t.Error("a file that is neither an aar nor a jar was accepted")
	}
}

const servicesFile = `{
  "project_info": {"project_number": "1234567890", "project_id": "probe-app", "storage_bucket": "probe-app.appspot.com", "firebase_url": "https://probe-app.firebaseio.com"},
  "client": [
    {"client_info": {"mobilesdk_app_id": "1:1234567890:android:aaaa", "android_client_info": {"package_name": "dev.local.other"}},
     "api_key": [{"current_key": "KEY-OTHER"}]},
    {"client_info": {"mobilesdk_app_id": "1:1234567890:android:bbbb", "android_client_info": {"package_name": "dev.local.probe"}},
     "oauth_client": [{"client_id": "web-client.apps.example", "client_type": 3}],
     "api_key": [{"current_key": "KEY-PROBE"}]}
  ]
}`

func TestGoogleServicesBecomeTheStringsTheLibrariesRead(t *testing.T) {
	got, err := googleServicesResources([]byte(servicesFile), "dev.local.probe")
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{
		`<string name="gcm_defaultSenderId" translatable="false">1234567890</string>`,
		`<string name="google_app_id" translatable="false">1:1234567890:android:bbbb</string>`,
		`<string name="google_api_key" translatable="false">KEY-PROBE</string>`,
		`<string name="project_id" translatable="false">probe-app</string>`,
		`<string name="default_web_client_id" translatable="false">web-client.apps.example</string>`,
		`<string name="google_storage_bucket" translatable="false">probe-app.appspot.com</string>`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %s in\n%s", want, text)
		}
	}
	if strings.Contains(text, "KEY-OTHER") || strings.Contains(text, "aaaa") {
		t.Error("the other client's values were used")
	}
}

func TestGoogleServicesOfAnotherPackageSayWhichTheyHave(t *testing.T) {
	_, err := googleServicesResources([]byte(servicesFile), "dev.local.missing")
	if err == nil || !strings.Contains(err.Error(), "dev.local.probe") || !strings.Contains(err.Error(), "-appid") {
		t.Errorf("answered %v, want the packages the file has and how to choose one", err)
	}
	if _, err := googleServicesResources([]byte("{"), "x"); err == nil {
		t.Error("broken JSON was accepted")
	}
	if _, err := googleServicesResources([]byte(`{"client": []}`), "x"); err == nil {
		t.Error("a file with no project was accepted")
	}
}

func TestAResourceValueIsEscapedForAapt(t *testing.T) {
	for in, want := range map[string]string{"it's": `it\&#39;s`, "@ref": `\@ref`, "a&b": "a&amp;b", `say "hi"`: `say \&#34;hi\&#34;`, `a\b`: `a\\b`} {
		if got := xmlResourceText(in); got != want {
			t.Errorf("%q became %q, want %q", in, got, want)
		}
	}
}

func TestAnEntitlementTheProfileLacksIsNamed(t *testing.T) {
	has := "<dict><key>application-identifier</key><string>X</string><key>aps-environment</key><string>development</string></dict>"
	lacks := "<dict><key>application-identifier</key><string>X</string></dict>"
	if got := missingEntitlement(has, []string{apsEnvironment}); got != "" {
		t.Errorf("a profile with the entitlement was refused for %q", got)
	}
	if got := missingEntitlement(lacks, []string{apsEnvironment}); got != apsEnvironment {
		t.Errorf("a profile without it answered %q", got)
	}
	if got := missingEntitlement(lacks, nil); got != "" {
		t.Errorf("nothing required, and %q was missing", got)
	}
}
