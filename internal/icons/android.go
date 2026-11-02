package icons

import (
	"fmt"
	"image/color"
	"strings"
)

// density is one of the screen densities an Android resource directory is named for.
type density struct {
	name  string
	scale float64
}

var densities = []density{
	{"mdpi", 1}, {"hdpi", 1.5}, {"xhdpi", 2}, {"xxhdpi", 3}, {"xxxhdpi", 4},
}

func (d density) px(dp float64) int { return int(dp*d.scale + 0.5) }

// colour names the resources the splash screen and the launcher background
// are read from. The Android build writes a theme that points at them.
const (
	// AndroidSplashBackground is the colour resource of the splash screen's ground.
	AndroidSplashBackground = "ic_splash_background"
	// AndroidSplashIcon is the drawable of the splash screen's icon.
	AndroidSplashIcon = "ic_splash"
	// AndroidNotificationIcon is the drawable of the status bar icon.
	AndroidNotificationIcon = "ic_stat"
)

// Android is the resources of an Android application, relative to its res
// directory.
//
// The launcher icon is adaptive, from Android 8: a background, a foreground
// drawn inside the 66 units of its 108 that every mask leaves visible, and from
// Android 13 a monochrome layer the system tints for a themed icon. Releases
// before 8 get a flat icon, square with rounded corners and round. The status
// bar icon is white on transparent, and the splash screen of Android 12 gets its
// icon and its ground in the light and the dark appearance.
func (s *Set) Android() ([]File, error) {
	var f files
	black := color.NRGBA{0, 0, 0, 255}
	white := color.NRGBA{255, 255, 255, 255}

	for _, d := range densities {
		dir := "mipmap-" + d.name + "/"
		f.png(dir+"ic_launcher_foreground.png", s.light.render(d.px(108), androidMark))
		f.png(dir+"ic_launcher_monochrome.png", s.light.silhouette(d.px(108), androidMark, black))

		legacy := onGround(s.light.render(d.px(48), androidLegacy), s.bg)
		f.png(dir+"ic_launcher.png", round(legacy, 0, 0.18))
		f.png(dir+"ic_launcher_round.png", round(legacy, 0, 0.5))

		f.png("drawable-"+d.name+"/"+AndroidNotificationIcon+".png",
			s.light.silhouette(d.px(24), androidNotification, white))
	}
	// The splash icon is 288 units, which is 1152 pixels at the densest screens;
	// the three densest are drawn and a lighter screen scales one of them down.
	for _, d := range densities[2:] {
		f.png("drawable-"+d.name+"/"+AndroidSplashIcon+".png", s.light.render(d.px(288), androidSplash))
		f.png("drawable-night-"+d.name+"/"+AndroidSplashIcon+".png", s.dark.render(d.px(288), androidSplash))
	}
	if f.err != nil {
		return nil, f.err
	}

	adaptive := `<?xml version="1.0" encoding="utf-8"?>
<adaptive-icon xmlns:android="http://schemas.android.com/apk/res/android">
    <background android:drawable="@color/ic_launcher_background" />
    <foreground android:drawable="@mipmap/ic_launcher_foreground" />
    <monochrome android:drawable="@mipmap/ic_launcher_monochrome" />
</adaptive-icon>
`
	f.text("mipmap-anydpi-v26/ic_launcher.xml", adaptive)
	f.text("mipmap-anydpi-v26/ic_launcher_round.xml", adaptive)
	f.text("values/ic_colors.xml", colorsXML(
		"ic_launcher_background", s.bg,
		AndroidSplashBackground, s.bg,
	))
	f.text("values-night/ic_colors.xml", colorsXML(
		AndroidSplashBackground, s.darkBg,
	))
	return f.list, nil
}

// colorsXML is a resource file of colours, written as name and colour pairs.
func colorsXML(pairs ...any) string {
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<resources>\n")
	for i := 0; i+1 < len(pairs); i += 2 {
		fmt.Fprintf(&b, "    <color name=%q>%s</color>\n", pairs[i], hex(pairs[i+1].(color.NRGBA)))
	}
	b.WriteString("</resources>\n")
	return b.String()
}

func hex(c color.NRGBA) string { return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B) }
