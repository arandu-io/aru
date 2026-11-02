package icons

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
)

func solid(w, h int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
	return img
}

// TestAMarkExportedWithItsBackgroundIsKeyedOut: white on black, with the black
// taken for a ground and the white kept as the mark.
func TestAMarkExportedWithItsBackgroundIsKeyedOut(t *testing.T) {
	src := solid(100, 100, color.NRGBA{0, 0, 0, 255})
	draw.Draw(src, image.Rect(30, 40, 70, 60), image.NewUniform(color.NRGBA{255, 255, 255, 255}), image.Point{}, draw.Src)
	m, err := Prepare(rasterImage{src})
	if err != nil {
		t.Fatal(err)
	}
	if !m.hasGround || m.ground != (color.NRGBA{0, 0, 0, 255}) {
		t.Fatalf("the ground is %v (%v), want black", m.ground, m.hasGround)
	}
	if m.content != image.Rect(30, 40, 70, 60) {
		t.Errorf("the content is %v, want the white bar", m.content)
	}
	if !m.mono {
		t.Error("a white mark is not taken for a grey one")
	}
	out := m.render(200, 0.5)
	if a := out.NRGBAAt(2, 2).A; a != 0 {
		t.Errorf("the corner is %d opaque after keying, want 0", a)
	}
	if c := out.NRGBAAt(100, 100); c.A != 255 || c.R != 255 {
		t.Errorf("the middle is %v, want opaque white", c)
	}
}

// TestColorToAlphaKeepsTheMarkAndLosesTheGround covers the edge pixels, which
// are a mix: half white over black keeps half of the white.
func TestColorToAlphaKeepsTheMarkAndLosesTheGround(t *testing.T) {
	black := color.NRGBA{0, 0, 0, 255}
	got := colorToAlpha(color.NRGBA{128, 128, 128, 255}, black)
	if got.R != 255 || got.A < 127 || got.A > 129 {
		t.Errorf("mid grey over black is %v, want white at half alpha", got)
	}
	got = colorToAlpha(color.NRGBA{255, 255, 255, 255}, color.NRGBA{255, 255, 255, 255})
	if got.A != 0 {
		t.Errorf("the ground over itself is %v, want nothing", got)
	}
	got = colorToAlpha(color.NRGBA{0, 0, 0, 255}, color.NRGBA{255, 255, 255, 255})
	if got.A != 255 || got.R != 0 {
		t.Errorf("black over white is %v, want opaque black", got)
	}
}

// TestTheMarkIsMeasuredNotTrusted: the same mark with margins or without comes
// out the same size.
func TestTheMarkIsMeasuredNotTrusted(t *testing.T) {
	black := image.NewUniform(color.NRGBA{0, 0, 0, 255})
	cross := func(img *image.NRGBA, x, y int) {
		draw.Draw(img, image.Rect(x+15, y, x+25, y+40), black, image.Point{}, draw.Src)
		draw.Draw(img, image.Rect(x, y+15, x+40, y+25), black, image.Point{}, draw.Src)
	}
	tight := image.NewNRGBA(image.Rect(0, 0, 40, 40))
	cross(tight, 0, 0)
	loose := image.NewNRGBA(image.Rect(0, 0, 400, 400))
	cross(loose, 130, 90)

	var sizes [2]int
	for i, src := range []*image.NRGBA{tight, loose} {
		m, err := Prepare(rasterImage{src})
		if err != nil {
			t.Fatal(err)
		}
		b := bounds(m.render(300, 0.5))
		sizes[i] = b.Dx()
	}
	if d := sizes[0] - sizes[1]; d < -1 || d > 1 {
		t.Errorf("the mark is %d wide with no margin and %d with one: the margin was trusted", sizes[0], sizes[1])
	}
}

func bounds(img *image.NRGBA) image.Rectangle {
	r := image.Rectangle{Min: img.Bounds().Max, Max: img.Bounds().Min}
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			if img.NRGBAAt(x, y).A > 12 {
				r.Min.X, r.Min.Y = min(r.Min.X, x), min(r.Min.Y, y)
				r.Max.X, r.Max.Y = max(r.Max.X, x+1), max(r.Max.Y, y+1)
			}
		}
	}
	return r
}

func TestAnEmptyPictureIsRefused(t *testing.T) {
	if _, err := Prepare(rasterImage{image.NewNRGBA(image.Rect(0, 0, 10, 10))}); err == nil {
		t.Error("a transparent picture was accepted")
	}
}

// TestAGreyMarkIsTurnedOverWhenItWouldVanish: black on a black ground is
// nothing; the mark is inverted, and a coloured one is left alone.
func TestAGreyMarkIsTurnedOverWhenItWouldVanish(t *testing.T) {
	black := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	draw.Draw(black, image.Rect(5, 5, 15, 15), image.NewUniform(color.NRGBA{0, 0, 0, 255}), image.Point{}, draw.Src)
	set, err := NewFrom(rasterImage{black}, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if c := set.dark.render(40, 0.5).NRGBAAt(20, 20); c.R != 255 {
		t.Errorf("the dark mark's middle is %v, want turned to white on black", c)
	}
	if c := set.light.render(40, 0.5).NRGBAAt(20, 20); c.R != 0 {
		t.Errorf("the light mark's middle is %v, want it as drawn", c)
	}

	red := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	draw.Draw(red, image.Rect(5, 5, 15, 15), image.NewUniform(color.NRGBA{200, 0, 0, 255}), image.Point{}, draw.Src)
	set, err = NewFrom(rasterImage{red}, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if c := set.dark.render(40, 0.5).NRGBAAt(20, 20); c.R != 200 {
		t.Errorf("a red mark became %v on the dark ground", c)
	}
}
