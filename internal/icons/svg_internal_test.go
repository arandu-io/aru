package icons

import (
	"image"
	"image/color"
	"strings"
	"testing"
)

// render draws a drawing at its natural size.
func render(t *testing.T, svg string) *image.NRGBA {
	t.Helper()
	img, err := parseSVG([]byte(svg))
	if err != nil {
		t.Fatalf("the drawing was refused: %v", err)
	}
	out := image.NewNRGBA(img.Bounds())
	img.Draw(out, img.Bounds(), img.Bounds())
	return out
}

func alphaAt(img *image.NRGBA, fx, fy float64) uint8 {
	b := img.Bounds()
	return img.NRGBAAt(int(fx*float64(b.Dx())), int(fy*float64(b.Dy()))).A
}

// TestAnEvenOddPathLeavesTheHole is the ring of every mark: two contours drawn
// in the same direction, which a plain winding rule fills solid.
func TestAnEvenOddPathLeavesTheHole(t *testing.T) {
	img := render(t, `<svg viewBox="0 0 100 100"><path fill-rule="evenodd"
		d="M10 10H90V90H10Z M30 30H70V70H30Z"/></svg>`)
	if a := alphaAt(img, 0.2, 0.5); a != 255 {
		t.Errorf("the ring is %d opaque, want 255", a)
	}
	if a := alphaAt(img, 0.5, 0.5); a != 0 {
		t.Errorf("the hole is %d opaque, want 0", a)
	}
	if a := alphaAt(img, 0.05, 0.05); a != 0 {
		t.Errorf("outside the ring is %d opaque, want 0", a)
	}
}

// TestANonZeroPathFillsContoursWoundTheSameWay is the other rule: the same two
// squares in one direction are one solid square, and in opposite directions a ring.
func TestANonZeroPathFillsContoursWoundTheSameWay(t *testing.T) {
	same := render(t, `<svg viewBox="0 0 100 100"><path d="M10 10H90V90H10Z M30 30H70V70H30Z"/></svg>`)
	if a := alphaAt(same, 0.5, 0.5); a != 255 {
		t.Errorf("the inner square is %d opaque, want 255: same direction fills", a)
	}
	opposite := render(t, `<svg viewBox="0 0 100 100"><path d="M10 10H90V90H10Z M30 30V70H70V30Z"/></svg>`)
	if a := alphaAt(opposite, 0.5, 0.5); a != 0 {
		t.Errorf("the inner square is %d opaque, want 0: opposite direction cuts", a)
	}
}

// TestShapesTransformsAndStyles covers what a mark is drawn with besides paths.
func TestShapesTransformsAndStyles(t *testing.T) {
	img := render(t, `<svg viewBox="0 0 100 100">
		<g transform="translate(50 50)" fill="#ff0000" style="fill-opacity:0.5">
			<circle r="20"/>
		</g>
		<rect x="0" y="0" width="10" height="10" fill="#00ff00" rx="3"/>
		<line x1="0" y1="90" x2="100" y2="90" stroke="#0000ff" stroke-width="10"/>
	</svg>`)
	b := img.Bounds()
	at := func(fx, fy float64) color.NRGBA {
		return img.NRGBAAt(int(fx*float64(b.Dx())), int(fy*float64(b.Dy())))
	}
	if c := at(0.5, 0.5); c.R != 255 || c.A < 126 || c.A > 129 {
		t.Errorf("the translated circle is %v, want half-opaque red", c)
	}
	if c := at(0.05, 0.05); c.G != 255 || c.A != 255 {
		t.Errorf("the rectangle is %v, want opaque green", c)
	}
	if c := at(0.5, 0.9); c.B != 255 || c.A != 255 {
		t.Errorf("the stroked line is %v, want opaque blue", c)
	}
	if c := at(0.5, 0.78); c.A != 0 {
		t.Errorf("above the line is %v, want nothing", c)
	}
}

// TestTheMiterOfAStrokeReachesItsPoint checks the join of a sharp turn: the
// tip of the chevron is beyond the stroke's own half width.
func TestTheMiterOfAStrokeReachesItsPoint(t *testing.T) {
	img := render(t, `<svg viewBox="0 0 100 100"><polyline points="10,90 50,20 90,90"
		fill="none" stroke="#000" stroke-width="8" stroke-miterlimit="10"/></svg>`)
	// The vertex is at y=20; a bevel would stop short of y=16 there.
	if a := alphaAt(img, 0.5, 0.14); a == 0 {
		t.Error("the miter does not reach its point")
	}
}

// TestArcsAndCurves checks the commands that are not lines.
func TestArcsAndCurves(t *testing.T) {
	img := render(t, `<svg viewBox="0 0 100 100">
		<path d="M10 50 A40 40 0 0 1 90 50 A40 40 0 0 1 10 50Z"/>
		<path d="M0 0 Q 0 10 10 10 T 20 20" fill="none" stroke="#000"/>
		<path d="M0 100 C 0 90 10 90 10 100 S 20 110 20 100 Z"/>
	</svg>`)
	if a := alphaAt(img, 0.5, 0.5); a != 255 {
		t.Errorf("the disc made of two arcs is %d opaque in the middle, want 255", a)
	}
	if a := alphaAt(img, 0.5, 0.05); a != 0 {
		t.Errorf("above the disc is %d opaque, want 0", a)
	}
}

// TestNumbersThatTouch is the path data minifiers write: "1.5.5" and "1-2".
func TestNumbersThatTouch(t *testing.T) {
	nums, err := numbers("1.5.5-2 3e1,4")
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{1.5, 0.5, -2, 30, 4}
	if len(nums) != len(want) {
		t.Fatalf("read %v, want %v", nums, want)
	}
	for i := range want {
		if nums[i] != want[i] {
			t.Errorf("number %d is %v, want %v", i, nums[i], want[i])
		}
	}
	// Arc flags are single digits that may touch the number after them.
	paths, err := parsePath("M0 0a1 1 0 11 2 0")
	if err != nil || len(paths) != 1 {
		t.Fatalf("the arc with touching flags was not read: %v %v", paths, err)
	}
}

// TestWhatCannotBeDrawnIsRefusedByName: a gradient drawn as nothing, or as a
// flat colour, is a different picture, and the author is told before it ships.
func TestWhatCannotBeDrawnIsRefusedByName(t *testing.T) {
	for _, tag := range []string{"linearGradient", "filter", "clipPath", "mask", "text", "image", "use"} {
		_, err := parseSVG([]byte(`<svg viewBox="0 0 10 10"><` + tag + `/><rect width="5" height="5"/></svg>`))
		if err == nil || !strings.Contains(err.Error(), tag) {
			t.Errorf("<%s> was not refused by name: %v", tag, err)
		}
	}
	if _, err := parseSVG([]byte(`<svg viewBox="0 0 10 10"><rect width="5" height="5" fill="url(#g)"/></svg>`)); err == nil {
		t.Error("a fill that is a reference was accepted")
	}
	if _, err := parseSVG([]byte(`<html></html>`)); err == nil {
		t.Error("a file that is not an SVG was accepted")
	}
	if _, err := parseSVG([]byte(`<svg viewBox="0 0 10 10"></svg>`)); err == nil {
		t.Error("a drawing of nothing was accepted")
	}
}

func TestColors(t *testing.T) {
	for in, want := range map[string]color.NRGBA{
		"#fff":              {255, 255, 255, 255},
		"#102030":           {16, 32, 48, 255},
		"#10203080":         {16, 32, 48, 128},
		"rgb(1, 2, 3)":      {1, 2, 3, 255},
		"rgba(255,0,0,0.5)": {255, 0, 0, 128},
		"black":             {0, 0, 0, 255},
	} {
		got, ok := parseColor(in)
		if !ok || got != want {
			t.Errorf("%s is %v (%v), want %v", in, got, ok, want)
		}
	}
	if _, ok := parseColor("#12"); ok {
		t.Error("#12 was taken for a colour")
	}
}
