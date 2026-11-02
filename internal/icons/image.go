package icons

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"

	xdraw "golang.org/x/image/draw"
)

// Image is a picture that can be drawn at any size.
type Image interface {
	// Bounds is the natural size, with its origin at zero.
	Bounds() image.Rectangle
	// Draw scales the part of the picture inside src into r of dst, over what
	// is already there.
	Draw(dst *image.NRGBA, r, src image.Rectangle)
}

// rasterImage is a bitmap.
type rasterImage struct{ img image.Image }

func (r rasterImage) Bounds() image.Rectangle {
	b := r.img.Bounds()
	return image.Rect(0, 0, b.Dx(), b.Dy())
}

func (r rasterImage) Draw(dst *image.NRGBA, to, src image.Rectangle) {
	off := r.img.Bounds().Min
	xdraw.CatmullRom.Scale(dst, to, r.img, src.Add(off), xdraw.Over, nil)
}

// Open reads a PNG, a JPEG or an SVG drawing.
func Open(path string) (Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Decode(data, filepath.Ext(path))
}

// Decode reads an image from its bytes. ext, with or without the dot, is the
// name's extension and breaks the tie when the content does not say.
func Decode(data []byte, ext string) (Image, error) {
	if strings.EqualFold(strings.TrimPrefix(ext, "."), "svg") || looksLikeSVG(data) {
		return parseSVG(data)
	}
	img, err := decodeRaster(data)
	if err != nil {
		return nil, err
	}
	return rasterImage{img}, nil
}

func decodeRaster(data []byte) (image.Image, error) {
	switch {
	case len(data) > 8 && string(data[1:4]) == "PNG":
		return png.Decode(bytes.NewReader(data))
	case len(data) > 2 && data[0] == 0xff && data[1] == 0xd8:
		return jpeg.Decode(bytes.NewReader(data))
	}
	return nil, errors.New("the icon is neither a PNG, a JPEG nor an SVG")
}

func looksLikeSVG(data []byte) bool {
	head := data
	if len(head) > 2048 {
		head = head[:2048]
	}
	return strings.Contains(string(head), "<svg")
}

// Mark is a picture prepared to sit in the middle of an icon: what is drawn in
// it, where, and how far from its centre it reaches.
type Mark struct {
	img Image
	// content is the smallest rectangle, in the picture's natural size, that
	// holds everything visible.
	content image.Rectangle
	// reach is how far, in the same unit, the farthest visible pixel is from
	// the centre of content.
	reach float64
	// mono is true when every visible pixel is a grey, so that inverting it for
	// a dark ground keeps the drawing what it was.
	mono bool
	// luma is the mean brightness of what is visible, 0 to 1.
	luma float64
	// ground is the colour a source with a full-bleed background was drawn on,
	// and hasGround says there was one.
	ground    color.NRGBA
	hasGround bool
}

// Prepare studies a picture.
//
// A picture that fills its square with one colour is a mark that was exported
// with its background. The colour is taken for the background and removed from
// the mark, so the mark is drawn on whatever ground a platform wants, and only
// the mark is measured. A picture with transparency is already a mark.
func Prepare(img Image) (*Mark, error) {
	b := img.Bounds()
	snap := image.NewNRGBA(b)
	img.Draw(snap, b, b)

	m := &Mark{img: img}
	if ground, ok := uniformGround(snap); ok {
		m.img = keyed{inner: img, ground: ground}
		m.ground, m.hasGround = ground, true
		snap = image.NewNRGBA(b)
		m.img.Draw(snap, b, b)
	}

	const visible = 12
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, b.Min.X-1, b.Min.Y-1
	opaque := true
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			p := snap.NRGBAAt(x, y)
			if p.A != 255 {
				opaque = false
			}
			if p.A <= visible {
				continue
			}
			minX, minY = min(minX, x), min(minY, y)
			maxX, maxY = max(maxX, x), max(maxY, y)
		}
	}
	if maxX < minX {
		return nil, errors.New("the icon has no visible pixels")
	}
	m.content = image.Rect(minX, minY, maxX+1, maxY+1)
	if opaque && !m.hasGround {
		// A picture with no background to take away is drawn whole.
		m.content = b
	}
	cx := float64(m.content.Min.X+m.content.Max.X) / 2
	cy := float64(m.content.Min.Y+m.content.Max.Y) / 2
	m.mono = true
	var lumaSum, weight float64
	for y := m.content.Min.Y; y < m.content.Max.Y; y++ {
		for x := m.content.Min.X; x < m.content.Max.X; x++ {
			p := snap.NRGBAAt(x, y)
			if p.A <= visible && !(opaque && !m.hasGround) {
				continue
			}
			// Distance to the far corner of the pixel.
			dx := math.Abs(float64(x)+0.5-cx) + 0.5
			dy := math.Abs(float64(y)+0.5-cy) + 0.5
			m.reach = math.Max(m.reach, math.Hypot(dx, dy))
			if absDiff(p.R, p.G) > 24 || absDiff(p.G, p.B) > 24 {
				m.mono = false
			}
			a := float64(p.A) / 255
			lumaSum += luminance(p) * a
			weight += a
		}
	}
	if weight > 0 {
		m.luma = lumaSum / weight
	}
	return m, nil
}

func absDiff(a, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

// luminance is the relative brightness of a colour, 0 to 1.
func luminance(c color.NRGBA) float64 {
	lin := func(v uint8) float64 {
		f := float64(v) / 255
		if f <= 0.03928 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

// uniformGround answers the colour a picture is drawn on when it has one: no
// transparent pixel and four corners of the same colour.
func uniformGround(img *image.NRGBA) (color.NRGBA, bool) {
	b := img.Bounds()
	corners := []color.NRGBA{
		img.NRGBAAt(b.Min.X, b.Min.Y), img.NRGBAAt(b.Max.X-1, b.Min.Y),
		img.NRGBAAt(b.Min.X, b.Max.Y-1), img.NRGBAAt(b.Max.X-1, b.Max.Y-1),
	}
	for _, c := range corners {
		if c.A != 255 {
			return color.NRGBA{}, false
		}
		if absDiff(c.R, corners[0].R) > 6 || absDiff(c.G, corners[0].G) > 6 || absDiff(c.B, corners[0].B) > 6 {
			return color.NRGBA{}, false
		}
	}
	// Transparency anywhere means the picture is already a mark on nothing.
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 255 {
			return color.NRGBA{}, false
		}
	}
	return corners[0], true
}

// keyed is a picture with its background colour taken out, so the mark stays
// and what was the ground is transparent. The edge pixels, which are a mix of
// the two, keep their share of the mark and lose the rest.
type keyed struct {
	inner  Image
	ground color.NRGBA
}

func (k keyed) Bounds() image.Rectangle { return k.inner.Bounds() }

func (k keyed) Draw(dst *image.NRGBA, r, src image.Rectangle) {
	tmp := image.NewNRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	k.inner.Draw(tmp, tmp.Bounds(), src)
	for i := 0; i < len(tmp.Pix); i += 4 {
		c := color.NRGBA{tmp.Pix[i], tmp.Pix[i+1], tmp.Pix[i+2], tmp.Pix[i+3]}
		c = colorToAlpha(c, k.ground)
		tmp.Pix[i], tmp.Pix[i+1], tmp.Pix[i+2], tmp.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	draw.Draw(dst, r, tmp, image.Point{}, draw.Over)
}

// colorToAlpha finds the least opaque colour that, laid over ground, gives c:
// the smallest alpha for which every channel is reachable.
func colorToAlpha(c, ground color.NRGBA) color.NRGBA {
	if c.A == 0 {
		return c
	}
	// The pixel is itself over the ground, by its own alpha.
	a := float64(c.A) / 255
	ch := func(v, g uint8) float64 { return float64(v)*a + float64(g)*(1-a) }
	vals := [3]float64{ch(c.R, ground.R), ch(c.G, ground.G), ch(c.B, ground.B)}
	grounds := [3]float64{float64(ground.R), float64(ground.G), float64(ground.B)}
	alpha := 0.0
	for i := range vals {
		var need float64
		switch {
		case vals[i] > grounds[i]:
			need = (vals[i] - grounds[i]) / (255 - grounds[i])
		case vals[i] < grounds[i]:
			need = (grounds[i] - vals[i]) / grounds[i]
		}
		alpha = math.Max(alpha, need)
	}
	if alpha <= 0 {
		return color.NRGBA{}
	}
	var out [3]uint8
	for i := range vals {
		v := grounds[i] + (vals[i]-grounds[i])/alpha
		out[i] = uint8(math.Round(math.Min(255, math.Max(0, v))))
	}
	return color.NRGBA{out[0], out[1], out[2], uint8(math.Round(alpha * 255))}
}

// inverted is a picture with its colours turned over, keeping the alpha.
type inverted struct{ inner Image }

func (v inverted) Bounds() image.Rectangle { return v.inner.Bounds() }

func (v inverted) Draw(dst *image.NRGBA, r, src image.Rectangle) {
	tmp := image.NewNRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	v.inner.Draw(tmp, tmp.Bounds(), src)
	for i := 0; i < len(tmp.Pix); i += 4 {
		tmp.Pix[i], tmp.Pix[i+1], tmp.Pix[i+2] = 255-tmp.Pix[i], 255-tmp.Pix[i+1], 255-tmp.Pix[i+2]
	}
	draw.Draw(dst, r, tmp, image.Point{}, draw.Over)
}

// Inverted answers the mark with its colours turned over.
func (m *Mark) inverted() *Mark {
	out := *m
	out.img = inverted{m.img}
	out.luma = 1 - m.luma
	return &out
}

// render draws the mark, centred, on a transparent square of side px, so that
// the circle that holds all of it is fraction of the side wide.
func (m *Mark) render(px int, fraction float64) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, px, px))
	scale := fraction * float64(px) / (2 * m.reach)
	w := float64(m.content.Dx()) * scale
	h := float64(m.content.Dy()) * scale
	x0 := (float64(px) - w) / 2
	y0 := (float64(px) - h) / 2
	r := image.Rect(int(math.Round(x0)), int(math.Round(y0)), int(math.Round(x0+w)), int(math.Round(y0+h)))
	if r.Empty() {
		return dst
	}
	m.img.Draw(dst, r, m.content)
	return dst
}

// silhouette is render with every visible pixel painted one colour: the shape
// of the mark and nothing of its colours.
func (m *Mark) silhouette(px int, fraction float64, c color.NRGBA) *image.NRGBA {
	dst := m.render(px, fraction)
	for i := 0; i < len(dst.Pix); i += 4 {
		dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2] = c.R, c.G, c.B
	}
	return dst
}
