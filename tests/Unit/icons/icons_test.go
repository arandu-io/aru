package icons_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/arandu-io/aru/internal/icons"
)

// set is the test mark drawn on its own: a ring and a chevron, wider than tall
// and off-centre in its own picture, which is what a measured mark has to cope with.
func set(t *testing.T, o icons.Options) *icons.Set {
	t.Helper()
	s, err := icons.New(filepath.Join("testdata", "mark.svg"), o)
	if err != nil {
		t.Fatalf("the test mark was refused: %v", err)
	}
	return s
}

// cache keeps what was generated for one set of options, because drawing every
// platform at every density is the slow part of this suite and most tests read
// the same files.
var cache sync.Map

// generated is what one of the Set's methods writes for the options.
func generated(t *testing.T, o icons.Options, fn func(*icons.Set) ([]icons.File, error)) []icons.File {
	t.Helper()
	key := fmt.Sprintf("%p %v", fn, o)
	if files, ok := cache.Load(key); ok {
		return files.([]icons.File)
	}
	files, err := fn(set(t, o))
	if err != nil {
		t.Fatal(err)
	}
	cache.Store(key, files)
	return files
}

// byPath indexes generated files, and fails on one named twice.
func byPath(t *testing.T, files []icons.File) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, f := range files {
		if _, dup := out[f.Path]; dup {
			t.Errorf("%s is generated twice", f.Path)
		}
		out[f.Path] = f.Data
	}
	return out
}

func decode(t *testing.T, name string, data []byte) *image.NRGBA {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("%s is not a PNG: %v", name, err)
	}
	out := image.NewNRGBA(img.Bounds())
	draw.Draw(out, out.Bounds(), img, img.Bounds().Min, draw.Src)
	return out
}

// reach is the diameter of the smallest circle around the image's centre that
// holds every visible pixel, and the box of those pixels.
func reach(img *image.NRGBA) (float64, image.Rectangle) {
	b := img.Bounds()
	box := image.Rectangle{Min: b.Max, Max: b.Min}
	cx, cy := float64(b.Dx())/2, float64(b.Dy())/2
	far := 0.0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.NRGBAAt(x, y).A < 13 {
				continue
			}
			box.Min.X, box.Min.Y = min(box.Min.X, x), min(box.Min.Y, y)
			box.Max.X, box.Max.Y = max(box.Max.X, x+1), max(box.Max.Y, y+1)
			far = math.Max(far, math.Hypot(math.Abs(float64(x)+0.5-cx)+0.5, math.Abs(float64(y)+0.5-cy)+0.5))
		}
	}
	return 2 * far, box
}

// TestTheAdaptiveForegroundFitsTheSafeZone is the defect: the whole picture was
// both layers of the icon, so the mark filled the 108 units instead of the 66 a
// launcher mask is certain to leave visible, and it was neither centred nor the
// right size under a circle or a squircle.
func TestTheAdaptiveForegroundFitsTheSafeZone(t *testing.T) {
	files := generated(t, icons.Options{}, (*icons.Set).Android)
	got := byPath(t, files)
	for dir, side := range map[string]int{
		"mdpi": 108, "hdpi": 162, "xhdpi": 216, "xxhdpi": 324, "xxxhdpi": 432,
	} {
		img := decode(t, dir, got["mipmap-"+dir+"/ic_launcher_foreground.png"])
		if img.Bounds().Dx() != side || img.Bounds().Dy() != side {
			t.Errorf("%s foreground is %v, want %dx%d", dir, img.Bounds().Size(), side, side)
			continue
		}
		diameter, box := reach(img)
		share := diameter / float64(side)
		if share > 66.0/108 {
			t.Errorf("%s: the mark reaches %.1f%% of the layer, past the safe zone (61.1%%)", dir, share*100)
		}
		if share < 0.5-0.03 || share > 0.5+0.02 {
			t.Errorf("%s: the mark reaches %.1f%% of the layer, want about 50%% (54 of 108 units)", dir, share*100)
		}
		// Centred, to a pixel: the box of the mark is as far from one side as from the other.
		left, right := box.Min.X, side-box.Max.X
		top, bottom := box.Min.Y, side-box.Max.Y
		if math.Abs(float64(left-right)) > 1.5 || math.Abs(float64(top-bottom)) > 1.5 {
			t.Errorf("%s: the mark is off-centre: margins %d/%d and %d/%d", dir, left, right, top, bottom)
		}
	}
}

// TestTheAdaptiveIconHasThreeLayers: a background, a foreground and the
// monochrome layer the themed icon of Android 13 is drawn from.
func TestTheAdaptiveIconHasThreeLayers(t *testing.T) {
	files := generated(t, icons.Options{}, (*icons.Set).Android)
	got := byPath(t, files)
	for _, name := range []string{"ic_launcher.xml", "ic_launcher_round.xml"} {
		var doc struct {
			XMLName    xml.Name
			Background struct {
				Drawable string `xml:"drawable,attr"`
			} `xml:"background"`
			Foreground struct {
				Drawable string `xml:"drawable,attr"`
			} `xml:"foreground"`
			Monochrome struct {
				Drawable string `xml:"drawable,attr"`
			} `xml:"monochrome"`
		}
		if err := xml.Unmarshal(got["mipmap-anydpi-v26/"+name], &doc); err != nil {
			t.Fatalf("%s is not markup: %v", name, err)
		}
		if doc.XMLName.Local != "adaptive-icon" {
			t.Errorf("%s is a <%s>, want <adaptive-icon>", name, doc.XMLName.Local)
		}
		for layer, want := range map[string]string{
			doc.Background.Drawable: "@color/ic_launcher_background",
			doc.Foreground.Drawable: "@mipmap/ic_launcher_foreground",
			doc.Monochrome.Drawable: "@mipmap/ic_launcher_monochrome",
		} {
			if layer != want {
				t.Errorf("%s has the layer %q, want %q", name, layer, want)
			}
		}
	}
	for _, d := range []string{"mdpi", "hdpi", "xhdpi", "xxhdpi", "xxxhdpi"} {
		for _, n := range []string{"ic_launcher.png", "ic_launcher_round.png", "ic_launcher_foreground.png", "ic_launcher_monochrome.png"} {
			if got["mipmap-"+d+"/"+n] == nil {
				t.Errorf("mipmap-%s/%s is missing", d, n)
			}
		}
	}
}

// TestTheMonochromeLayerCarriesOnlyAlpha: the system tints it, so what colour
// it holds must not matter -- there is exactly one.
func TestTheMonochromeLayerCarriesOnlyAlpha(t *testing.T) {
	files := generated(t, icons.Options{}, (*icons.Set).Android)
	for _, name := range []string{"mipmap-xxxhdpi/ic_launcher_monochrome.png", "drawable-xxxhdpi/ic_stat.png"} {
		img := decode(t, name, byPath(t, files)[name])
		colours := map[[3]uint8]bool{}
		visible := 0
		for i := 0; i < len(img.Pix); i += 4 {
			if img.Pix[i+3] > 0 {
				visible++
				colours[[3]uint8{img.Pix[i], img.Pix[i+1], img.Pix[i+2]}] = true
			}
		}
		if visible == 0 {
			t.Errorf("%s draws nothing", name)
		}
		if len(colours) != 1 {
			t.Errorf("%s holds %d colours, want one: only the alpha is the drawing", name, len(colours))
		}
	}
	stat := decode(t, "ic_stat", byPath(t, files)["drawable-xxxhdpi/ic_stat.png"])
	if c := stat.NRGBAAt(48, 48); c.A > 0 && (c.R != 255 || c.G != 255 || c.B != 255) {
		t.Errorf("the notification icon is %v, want white", c)
	}
	if stat.Bounds().Dx() != 96 {
		t.Errorf("the xxxhdpi notification icon is %d wide, want 96", stat.Bounds().Dx())
	}
}

// TestTheLegacyIconsAreMaskedByTheirOwnShape: before Android 8 no launcher
// masks anything, so the round one has transparent corners.
func TestTheLegacyIconsAreMaskedByTheirOwnShape(t *testing.T) {
	files := generated(t, icons.Options{}, (*icons.Set).Android)
	got := byPath(t, files)
	round := decode(t, "round", got["mipmap-xxxhdpi/ic_launcher_round.png"])
	flat := decode(t, "flat", got["mipmap-xxxhdpi/ic_launcher.png"])
	if round.Bounds().Dx() != 192 || flat.Bounds().Dx() != 192 {
		t.Errorf("the xxxhdpi legacy icons are %d and %d wide, want 192", round.Bounds().Dx(), flat.Bounds().Dx())
	}
	if a := round.NRGBAAt(1, 1).A; a != 0 {
		t.Errorf("the corner of the round icon is %d opaque, want 0", a)
	}
	if c := flat.NRGBAAt(24, 96); c != (color.NRGBA{255, 255, 255, 255}) {
		t.Errorf("the flat icon's ground is %v, want white", c)
	}
}

// TestTheColoursAreResources: the launcher ground in one appearance, the splash
// ground in both, and the splash icon in both, from the directories the system
// picks between.
func TestTheColoursAreResources(t *testing.T) {
	files := generated(t, icons.Options{}, (*icons.Set).Android)
	got := byPath(t, files)
	read := func(name string) map[string]string {
		var doc struct {
			Color []struct {
				Name  string `xml:"name,attr"`
				Value string `xml:",chardata"`
			} `xml:"color"`
		}
		if err := xml.Unmarshal(got[name], &doc); err != nil {
			t.Fatalf("%s is not markup: %v", name, err)
		}
		out := map[string]string{}
		for _, c := range doc.Color {
			out[c.Name] = c.Value
		}
		return out
	}
	if c := read("values/ic_colors.xml"); c["ic_launcher_background"] != "#FFFFFF" || c["ic_splash_background"] != "#FFFFFF" {
		t.Errorf("the light colours are %v", c)
	}
	if c := read("values-night/ic_colors.xml"); c["ic_splash_background"] != "#000000" {
		t.Errorf("the dark colours are %v", c)
	}
	light := decode(t, "light", got["drawable-xxxhdpi/ic_splash.png"])
	dark := decode(t, "dark", got["drawable-night-xxxhdpi/ic_splash.png"])
	if light.Bounds().Dx() != 1152 {
		t.Errorf("the xxxhdpi splash icon is %d wide, want 1152 (288 units)", light.Bounds().Dx())
	}
	d, _ := reach(light)
	if share := d / 1152; share > 192.0/288+0.012 {
		t.Errorf("the splash mark reaches %.1f%% of its canvas, past the 66.7%% circle the system shows", share*100)
	}
	// The test mark is dark: on the dark ground it is turned over.
	if mean(light) > 40 || mean(dark) < 215 {
		t.Errorf("the splash marks are %.0f and %.0f bright, want dark on light and light on dark", mean(light), mean(dark))
	}
}

// mean is the average brightness of the visible pixels.
func mean(img *image.NRGBA) float64 {
	sum, n := 0.0, 0
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i+3] > 200 {
			sum += float64(img.Pix[i])
			n++
		}
	}
	if n == 0 {
		return -1
	}
	return sum / float64(n)
}

// TestIOSHasTheDefaultDarkAndTintedIcons: three pictures of 1024 and a
// Contents.json that names each by its appearance.
func TestIOSHasTheDefaultDarkAndTintedIcons(t *testing.T) {
	files := generated(t, icons.Options{}, (*icons.Set).IOS)
	got := byPath(t, files)

	var contents struct {
		Images []struct {
			Appearances []struct{ Appearance, Value string } `json:"appearances"`
			Filename    string                               `json:"filename"`
			Idiom       string                               `json:"idiom"`
			Platform    string                               `json:"platform"`
			Size        string                               `json:"size"`
		} `json:"images"`
		Info struct{ Version int } `json:"info"`
	}
	if err := json.Unmarshal(got["Contents.json"], &contents); err != nil {
		t.Fatalf("Contents.json is not JSON: %v", err)
	}
	named := map[string]string{}
	for _, im := range contents.Images {
		if im.Idiom != "universal" || im.Platform != "ios" || im.Size != "1024x1024" {
			t.Errorf("%s is %s/%s/%s, want universal/ios/1024x1024", im.Filename, im.Idiom, im.Platform, im.Size)
		}
		appearance := ""
		if len(im.Appearances) == 1 && im.Appearances[0].Appearance == "luminosity" {
			appearance = im.Appearances[0].Value
		}
		named[appearance] = im.Filename
		if got[im.Filename] == nil {
			t.Errorf("Contents.json names %s, which is not written", im.Filename)
		}
	}
	for appearance, file := range map[string]string{"": "AppIcon.png", "dark": "AppIcon-dark.png", "tinted": "AppIcon-tinted.png"} {
		if named[appearance] != file {
			t.Errorf("the %q icon is %q in Contents.json, want %q", appearance, named[appearance], file)
		}
	}

	def := decode(t, "default", got["AppIcon.png"])
	dark := decode(t, "dark", got["AppIcon-dark.png"])
	tint := decode(t, "tinted", got["AppIcon-tinted.png"])
	for name, img := range map[string]*image.NRGBA{"default": def, "dark": dark, "tinted": tint} {
		if img.Bounds().Dx() != 1024 || img.Bounds().Dy() != 1024 {
			t.Errorf("the %s icon is %v, want 1024x1024", name, img.Bounds().Size())
		}
	}
	// The App Store refuses transparency in the default icon; the dark one is
	// the mark alone, so the system puts its own ground behind it.
	for i := 3; i < len(def.Pix); i += 4 {
		if def.Pix[i] != 255 {
			t.Fatal("the default icon has a transparent pixel")
		}
	}
	if dark.NRGBAAt(2, 2).A != 0 {
		t.Error("the dark icon has a ground; it should be the mark on nothing")
	}
	for i := 0; i < len(tint.Pix); i += 4 {
		if tint.Pix[i] != tint.Pix[i+1] || tint.Pix[i+1] != tint.Pix[i+2] || tint.Pix[i+3] != 255 {
			t.Fatal("the tinted icon is not opaque grey")
		}
	}
}

// TestTheWebIcons: the sizes browsers and manifests ask for, a favicon that is
// a real icon file, and a manifest fragment that names what is written.
func TestTheWebIcons(t *testing.T) {
	files := generated(t, icons.Options{}, (*icons.Set).Web)
	got := byPath(t, files)
	for name, side := range map[string]int{
		"favicon-16x16.png": 16, "favicon-32x32.png": 32, "apple-touch-icon.png": 180,
		"icon-192.png": 192, "icon-512.png": 512, "icon-maskable-192.png": 192, "icon-maskable-512.png": 512,
	} {
		img := decode(t, name, got[name])
		if img.Bounds().Dx() != side || img.Bounds().Dy() != side {
			t.Errorf("%s is %v, want %dx%d", name, img.Bounds().Size(), side, side)
		}
		for i := 3; i < len(img.Pix); i += 4 {
			if img.Pix[i] != 255 {
				t.Errorf("%s has a transparent pixel", name)
				break
			}
		}
	}
	// A maskable icon keeps its mark inside the circle of 80% a launcher keeps.
	maskable := decode(t, "maskable", got["icon-maskable-512.png"])
	ground := maskable.NRGBAAt(0, 0)
	far := 0.0
	for y := 0; y < 512; y++ {
		for x := 0; x < 512; x++ {
			if maskable.NRGBAAt(x, y) != ground {
				far = math.Max(far, math.Hypot(float64(x)-256, float64(y)-256))
			}
		}
	}
	if far > 0.4*512 {
		t.Errorf("the maskable mark reaches %.0f pixels from the middle, past 205 (40%% of the side)", far)
	}

	checkICO(t, got["favicon.ico"], []int{16, 32, 48})

	var manifest struct {
		Icons []struct{ Src, Sizes, Type, Purpose string } `json:"icons"`
	}
	if err := json.Unmarshal(got["manifest-icons.json"], &manifest); err != nil {
		t.Fatalf("manifest-icons.json is not JSON: %v", err)
	}
	purposes := map[string]int{}
	for _, ic := range manifest.Icons {
		purposes[ic.Purpose]++
		if got[strings.TrimPrefix(ic.Src, "/")] == nil {
			t.Errorf("the manifest names %s, which is not written", ic.Src)
		}
	}
	if purposes["any"] != 2 || purposes["maskable"] != 2 {
		t.Errorf("the manifest has %v, want two any and two maskable", purposes)
	}
}

// checkICO reads the directory of an icon file and every image it points at.
func checkICO(t *testing.T, data []byte, sides []int) {
	t.Helper()
	if len(data) < 6 || binary.LittleEndian.Uint16(data[0:2]) != 0 || binary.LittleEndian.Uint16(data[2:4]) != 1 {
		t.Fatal("the icon file does not start with an ICONDIR")
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count != len(sides) {
		t.Fatalf("the icon file holds %d images, want %d", count, len(sides))
	}
	for i, side := range sides {
		entry := data[6+16*i:]
		w := int(entry[0])
		if w == 0 {
			w = 256
		}
		if w != side {
			t.Errorf("image %d is declared %d wide, want %d", i, w, side)
		}
		length := int(binary.LittleEndian.Uint32(entry[8:12]))
		offset := int(binary.LittleEndian.Uint32(entry[12:16]))
		if offset+length > len(data) {
			t.Fatalf("image %d runs past the end of the file", i)
		}
		img := decode(t, "ico image", data[offset:offset+length])
		if img.Bounds().Dx() != side {
			t.Errorf("image %d is %d wide, declared %d", i, img.Bounds().Dx(), side)
		}
	}
}

// TestTheDesktopIcons: an icon file Windows reads with every size it draws, and
// an icns whose entries add up to its own length and each hold a PNG of the
// size the four letters promise.
func TestTheDesktopIcons(t *testing.T) {
	files := generated(t, icons.Options{}, (*icons.Set).Desktop)
	got := byPath(t, files)
	checkICO(t, got["icon.ico"], []int{16, 24, 32, 48, 64, 128, 256})

	icns := got["icon.icns"]
	if string(icns[:4]) != "icns" || int(binary.BigEndian.Uint32(icns[4:8])) != len(icns) {
		t.Fatal("the icns header does not carry the file's length")
	}
	want := map[string]int{
		"icp4": 16, "icp5": 32, "icp6": 64, "ic07": 128, "ic08": 256, "ic09": 512, "ic10": 1024,
		"ic11": 32, "ic12": 64, "ic13": 256, "ic14": 512,
	}
	seen := map[string]bool{}
	for pos := 8; pos < len(icns); {
		kind := string(icns[pos : pos+4])
		size := int(binary.BigEndian.Uint32(icns[pos+4 : pos+8]))
		img := decode(t, kind, icns[pos+8:pos+size])
		if img.Bounds().Dx() != want[kind] {
			t.Errorf("%s is %d wide, want %d", kind, img.Bounds().Dx(), want[kind])
		}
		seen[kind] = true
		pos += size
	}
	if len(seen) != len(want) {
		t.Errorf("the icns holds %d kinds, want %d", len(seen), len(want))
	}
	// A Mac icon is inset on its canvas, the way the system's own are.
	mac := decode(t, "icon.png", got["icon.png"])
	if mac.NRGBAAt(20, 512).A != 0 || mac.NRGBAAt(512, 512).A != 255 {
		t.Error("the Mac icon is not a body inset on a transparent canvas")
	}
}

// TestADarkPictureIsUsedWhenGiven and the derived one when not.
func TestTheDarkAppearanceFollowsTheOptions(t *testing.T) {
	dir := t.TempDir()
	white := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><circle cx="5" cy="5" r="4" fill="#ff0000"/></svg>`
	if err := os.WriteFile(filepath.Join(dir, "dark.svg"), []byte(white), 0o600); err != nil {
		t.Fatal(err)
	}
	s := set(t, icons.Options{Dark: filepath.Join(dir, "dark.svg"), DarkBackground: "#101010"})
	files, err := s.IOS()
	if err != nil {
		t.Fatal(err)
	}
	dark := decode(t, "dark", byPath(t, files)["AppIcon-dark.png"])
	if c := dark.NRGBAAt(512, 512); c.R != 255 || c.G != 0 {
		t.Errorf("the dark icon's middle is %v, want the red of the dark picture", c)
	}
	android, err := s.Android()
	if err != nil {
		t.Fatal(err)
	}
	var night struct {
		Color []struct {
			Name  string `xml:"name,attr"`
			Value string `xml:",chardata"`
		} `xml:"color"`
	}
	if err := xml.Unmarshal(byPath(t, android)["values-night/ic_colors.xml"], &night); err != nil {
		t.Fatal(err)
	}
	if len(night.Color) != 1 || night.Color[0].Value != "#101010" {
		t.Errorf("the dark ground is %v, want #101010", night.Color)
	}
}

// TestASourceWithItsBackgroundIsTheSameIcon: the picture a designer exports
// with the ground in it gives the mark and the ground separately.
func TestASourceWithItsBackgroundIsTheSameIcon(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 512, 512))
	draw.Draw(src, src.Bounds(), image.NewUniform(color.NRGBA{0, 0, 0, 255}), image.Point{}, draw.Src)
	draw.Draw(src, image.Rect(100, 200, 412, 312), image.NewUniform(color.NRGBA{255, 255, 255, 255}), image.Point{}, draw.Src)
	var b bytes.Buffer
	if err := png.Encode(&b, src); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "icon.png")
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := icons.New(path, icons.Options{})
	if err != nil {
		t.Fatal(err)
	}
	files, err := s.Android()
	if err != nil {
		t.Fatal(err)
	}
	got := byPath(t, files)
	if !strings.Contains(string(got["values/ic_colors.xml"]), "#000000") {
		t.Error("the ground the picture was exported on was not taken for the launcher background")
	}
	fg := decode(t, "fg", got["mipmap-xxxhdpi/ic_launcher_foreground.png"])
	if fg.NRGBAAt(2, 2).A != 0 {
		t.Error("the foreground still has the ground in it")
	}
	if c := fg.NRGBAAt(216, 216); c.A != 255 || c.R != 255 {
		t.Errorf("the foreground's middle is %v, want the white bar", c)
	}
	// The white bar on black is already readable, so the dark icon is it unchanged.
	ios, _ := s.IOS()
	dark := decode(t, "dark", byPath(t, ios)["AppIcon-dark.png"])
	if c := dark.NRGBAAt(512, 512); c.R != 255 {
		t.Errorf("the dark icon's middle is %v, want white", c)
	}
}

func TestBadInputIsRefusedWithAReason(t *testing.T) {
	if _, err := icons.New(filepath.Join("testdata", "mark.svg"), icons.Options{Background: "blue"}); err == nil {
		t.Error("a colour that is not #rrggbb was accepted")
	}
	if _, err := icons.New(filepath.Join("testdata", "missing.png"), icons.Options{}); err == nil {
		t.Error("a missing picture was accepted")
	}
	bad := filepath.Join(t.TempDir(), "icon.png")
	if err := os.WriteFile(bad, []byte("not a picture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := icons.New(bad, icons.Options{}); err == nil {
		t.Error("a file that is not a picture was accepted")
	}
}

// TestGoldens holds the drawing: a change of a pixel in these is a change of
// what an application ships. UPDATE_GOLDEN=1 rewrites them.
func TestGoldens(t *testing.T) {
	o := icons.Options{}
	a, i := byPath(t, generated(t, o, (*icons.Set).Android)), byPath(t, generated(t, o, (*icons.Set).IOS))
	for name, data := range map[string][]byte{
		"android-foreground-xhdpi.png": a["mipmap-xhdpi/ic_launcher_foreground.png"],
		"android-round-xhdpi.png":      a["mipmap-xhdpi/ic_launcher_round.png"],
		"android-stat-xxhdpi.png":      a["drawable-xxhdpi/ic_stat.png"],
		"ios-dark-1024.png":            i["AppIcon-dark.png"],
	} {
		golden := filepath.Join("testdata", "golden", name)
		if os.Getenv("UPDATE_GOLDEN") != "" {
			if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(golden, data, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("%s: %v (UPDATE_GOLDEN=1 writes it)", name, err)
		}
		if !samePixels(t, name, want, data) {
			t.Errorf("%s differs from its golden; if the change is wanted, UPDATE_GOLDEN=1 go test ./tests/Unit/icons", name)
		}
	}
}

// goldenTolerance is how far a channel may stray from the golden. The drawing
// is floating point, and arm64 fuses a multiply and an add that amd64 rounds
// twice, so the same code lands a few channel steps apart on the machine that
// wrote a golden and on the one that checks it (18 bytes of 4194304, at most 3
// apart, in the dark iOS icon). A real change of the drawing moves far more
// pixels, and by far more than this.
const goldenTolerance = 4

func samePixels(t *testing.T, name string, want, got []byte) bool {
	t.Helper()
	a, b := decode(t, name+" golden", want), decode(t, name, got)
	if a.Bounds() != b.Bounds() || len(a.Pix) != len(b.Pix) {
		return false
	}
	for i := range a.Pix {
		d := int(a.Pix[i]) - int(b.Pix[i])
		if d < -goldenTolerance || d > goldenTolerance {
			return false
		}
	}
	return true
}
