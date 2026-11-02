package pack

import (
	"encoding/xml"
	"strings"
	"testing"
)

// What an Android application looks like in the first frames, before Ayra has
// drawn anything, is written by the packager and read by the system. Three
// things decide it: whether the activity survives the system changing between
// the light and the dark mode, what the window shows until the first frame, and
// what the bars are painted with. Each is a string in a generated file, and each
// was wrong -- the activity was recreated on the change, the window was white
// in the dark, and the bars were a fixed translucent black -- so each is read
// back from the file the packager writes.

// TestTheActivityHandlesTheSystemChangingScheme is the manifest declaring uiMode
// among the configuration changes the activity handles itself. Without it the
// system destroys and recreates the activity on every switch between light and
// dark, which restarts the application in the middle of whatever it was doing.
func TestTheActivityHandlesTheSystemChangingScheme(t *testing.T) {
	for name, data := range androidCorpus() {
		manifest, err := androidManifest(data)
		if err != nil {
			t.Fatalf("%s: the manifest was not written: %v", name, err)
		}
		changes := configChanges(t, manifest)
		for _, want := range []string{"uiMode", "orientation", "screenSize", "keyboardHidden"} {
			if !strings.Contains("|"+changes+"|", "|"+want+"|") {
				t.Errorf("%s: configChanges is %q, want %s among them", name, changes, want)
			}
		}
	}
}

// configChanges reads android:configChanges of the activity out of a manifest.
func configChanges(t *testing.T, manifest []byte) string {
	t.Helper()
	var doc struct {
		Activity []struct {
			Name    string `xml:"name,attr"`
			Changes string `xml:"configChanges,attr"`
		} `xml:"application>activity"`
	}
	if err := xml.Unmarshal(manifest, &doc); err != nil {
		t.Fatalf("the manifest is not markup: %v", err)
	}
	for _, a := range doc.Activity {
		if a.Name == "io.arandu.ayra.AyraActivity" {
			return a.Changes
		}
	}
	t.Fatal("the manifest declares no AyraActivity")
	return ""
}

// style reads Theme.AyraApp out of a theme resource.
func style(t *testing.T, resource string) map[string]string {
	t.Helper()
	var doc struct {
		Style []struct {
			Name string `xml:"name,attr"`
			Item []struct {
				Name  string `xml:"name,attr"`
				Value string `xml:",chardata"`
			} `xml:"item"`
		} `xml:"style"`
	}
	if err := xml.Unmarshal([]byte(resource), &doc); err != nil {
		t.Fatalf("a theme resource is not markup: %v", err)
	}
	items := map[string]string{}
	for _, s := range doc.Style {
		if s.Name != "Theme.AyraApp" {
			continue
		}
		for _, item := range s.Item {
			items[item.Name] = strings.TrimSpace(item.Value)
		}
	}
	if len(items) == 0 {
		t.Fatal("a theme resource has no Theme.AyraApp")
	}
	return items
}

// TestTheWindowShowsTheGroundOfTheSystemSchemeBeforeTheFirstFrame fixes the
// ground: white for a light device and the dark palette's for a night one, in
// every directory the system might pick -- because a resource is taken whole
// from the one directory that matches, so a directory that forgot it shows the
// system's default.
func TestTheWindowShowsTheGroundOfTheSystemSchemeBeforeTheFirstFrame(t *testing.T) {
	seen := map[string]bool{}
	for _, theme := range androidThemes() {
		seen[theme.dir] = true
		items := style(t, theme.xml)
		want := androidLightGround
		if strings.Contains(theme.dir, "night") {
			want = androidDarkGround
		}
		if got := items["android:windowBackground"]; got != want {
			t.Errorf("%s: the window background is %q, want %q", theme.dir, got, want)
		}
	}
	for _, dir := range []string{"values", "values-v21", "values-night", "values-night-v21"} {
		if !seen[dir] {
			t.Errorf("there is no theme in %s", dir)
		}
	}
	if androidDarkGround == androidLightGround {
		t.Error("the dark and the light ground are the same, which is the white flash in the dark")
	}
}

// TestTheBarsAreTransparentAndTheApplicationPaintsThem is the bars: no fixed
// translucent black, which was a grey band over a white page, and none of the
// dark ground's own colour either, since the shell paints the bars at run time.
func TestTheBarsAreTransparentAndTheApplicationPaintsThem(t *testing.T) {
	for _, theme := range androidThemes() {
		if strings.Contains(theme.xml, "#40000000") {
			t.Errorf("%s: the bars are still the fixed translucent black", theme.dir)
		}
		items := style(t, theme.xml)
		if theme.dir == "values" || theme.dir == "values-night" {
			// The releases before 21 have no bar colours to set.
			continue
		}
		for _, bar := range []string{"android:statusBarColor", "android:navigationBarColor"} {
			if got := items[bar]; got != "@android:color/transparent" {
				t.Errorf("%s: %s is %q, want transparent", theme.dir, bar, got)
			}
		}
		if got := items["android:windowDrawsSystemBarBackgrounds"]; got != "true" {
			t.Errorf("%s: the window does not draw the system bar backgrounds", theme.dir)
		}
	}
}

// TestALightDeviceGetsDarkStatusIconsFromTheFirstFrame is the one flag the
// theme can set for the icons: dark on a light ground. A night device has the
// light icons by default, and must not ask for the dark ones.
func TestALightDeviceGetsDarkStatusIconsFromTheFirstFrame(t *testing.T) {
	for _, theme := range androidThemes() {
		got := style(t, theme.xml)["android:windowLightStatusBar"]
		switch {
		case strings.Contains(theme.dir, "night") && got != "":
			t.Errorf("%s asks for dark status icons on a dark ground", theme.dir)
		case theme.dir == "values-v23" && got != "true":
			t.Errorf("%s: windowLightStatusBar is %q, want true", theme.dir, got)
		}
	}
}

// TestTheSplashScreenOfAndroid12PointsAtTheIconAndItsGround: the two items that
// replace the system's own splash, in the light and the dark directory, over
// the same bars as the themes they stand in for.
func TestTheSplashScreenOfAndroid12PointsAtTheIconAndItsGround(t *testing.T) {
	themes := androidSplashThemes()
	if len(themes) != 2 {
		t.Fatalf("there are %d splash themes, want one light and one night", len(themes))
	}
	for _, theme := range themes {
		items := style(t, theme.xml)
		if got := items["android:windowSplashScreenBackground"]; got != "@color/ic_splash_background" {
			t.Errorf("%s: the splash ground is %q", theme.dir, got)
		}
		if got := items["android:windowSplashScreenAnimatedIcon"]; got != "@drawable/ic_splash" {
			t.Errorf("%s: the splash icon is %q", theme.dir, got)
		}
		if got := items["android:statusBarColor"]; got != "@android:color/transparent" {
			t.Errorf("%s: the bars lost their transparency: %q", theme.dir, got)
		}
		wantWindow := androidLightGround
		wantLightStatus := "true"
		if strings.Contains(theme.dir, "night") {
			wantWindow, wantLightStatus = androidDarkGround, ""
		}
		if got := items["android:windowBackground"]; got != wantWindow {
			t.Errorf("%s: the window ground is %q, want %q", theme.dir, got, wantWindow)
		}
		if got := items["android:windowLightStatusBar"]; got != wantLightStatus {
			t.Errorf("%s: windowLightStatusBar is %q, want %q", theme.dir, got, wantLightStatus)
		}
	}
	// The themes every application has do not name resources only an icon makes.
	for _, theme := range androidThemes() {
		if strings.Contains(theme.xml, "ic_splash") {
			t.Errorf("%s names the splash icon, which an application without an icon does not have", theme.dir)
		}
	}
}

// TestTheManifestNamesTheIconsItGot: the round icon beside the launcher icon,
// and the drawable a notification is marked with.
func TestTheManifestNamesTheIconsItGot(t *testing.T) {
	manifest, err := androidManifest(manifestData{
		AppID:      "sh.example.app",
		MinSDK:     21,
		TargetSDK:  35,
		AppName:    "Example",
		IconSnip:   `android:icon="@mipmap/ic_launcher" android:roundIcon="@mipmap/ic_launcher_round"`,
		Components: []string{notificationIconElement},
	})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Application struct {
			Icon      string `xml:"icon,attr"`
			RoundIcon string `xml:"roundIcon,attr"`
			Meta      []struct {
				Name     string `xml:"name,attr"`
				Resource string `xml:"resource,attr"`
			} `xml:"meta-data"`
		} `xml:"application"`
	}
	if err := xml.Unmarshal(manifest, &doc); err != nil {
		t.Fatalf("the manifest is not markup: %v", err)
	}
	if doc.Application.Icon != "@mipmap/ic_launcher" || doc.Application.RoundIcon != "@mipmap/ic_launcher_round" {
		t.Errorf("the icons are %q and %q", doc.Application.Icon, doc.Application.RoundIcon)
	}
	found := false
	for _, m := range doc.Application.Meta {
		if m.Name == "com.google.firebase.messaging.default_notification_icon" && m.Resource == "@drawable/ic_stat" {
			found = true
		}
	}
	if !found {
		t.Errorf("the manifest does not name the notification icon: %+v", doc.Application.Meta)
	}
}
