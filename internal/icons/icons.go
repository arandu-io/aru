// Package icons draws every icon an application needs from one picture.
//
// A mark -- a PNG, a JPEG or an SVG, with or without its background -- goes in,
// and what each platform asks for comes out: the adaptive, legacy, themed and
// notification icons of Android, its splash screen, the default, dark and
// tinted icons of iOS, the favicons and manifest icons of the web, and the
// icons of Windows and macOS. Nothing outside the Go toolchain is involved: the
// SVG is drawn by a reader of the subset a mark uses, and the bitmaps are
// scaled with the standard library's image packages.
//
// The mark is measured, not trusted. Its margins are cut away and what is left
// is centred and scaled until the circle that holds all of it has the width a
// platform wants -- the safe zone of an Android adaptive icon is 66 of its 108
// units -- so a source exported with a margin of its own, or with none, comes
// out the same size.
package icons

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"path"
	"strings"
)

// The width of the circle that holds the mark, as a share of the icon it sits
// in. Each has the reason that is written beside it.
const (
	// androidMark is the width of the circle that holds the mark in the
	// foreground of an adaptive icon: 54 of its 108 units. The safe zone, the 66
	// units every launcher mask leaves visible whatever its shape, is the limit,
	// and a mark that reaches it touches the edge of a circular mask; this stays
	// well inside it, so a wide mark has a margin of its own.
	androidMark = 54.0 / 108

	// androidLegacy fills the icon of a launcher that has no mask of its own.
	androidLegacy = 0.72

	// androidNotification is the 20 units of the 24 a status bar icon draws in.
	androidNotification = 20.0 / 24

	// androidSplash is the circle of an Android 12 splash screen: 192 units of
	// the 288 the icon is drawn on.
	androidSplash = 192.0 / 288

	// fullBleed is the mark on an icon the platform masks itself, which is iOS,
	// and on the maskable icon of a web application.
	fullBleed = 0.62

	// favicon is the mark on an icon that is read at 16 pixels.
	favicon = 0.80

	// webAny is the mark on a web application's icon that is shown unmasked.
	webAny = 0.70

	// desktop is the mark on the body of a Windows or macOS icon.
	desktop = 0.66
)

// A File is one generated file, named relative to the directory it is written in.
type File struct {
	Path string
	Data []byte
}

// Options are what a build says about the icon besides the picture itself.
type Options struct {
	// Dark is a picture for the dark appearance: iOS, and the splash screen.
	// Empty draws the same mark, turned over when it is a grey one that would
	// otherwise vanish on the dark ground.
	Dark string

	// Background and DarkBackground are the colours the mark sits on, as
	// #rgb or #rrggbb. Empty takes the colour the picture was exported on, or
	// white for the light one, and black for the dark one.
	Background     string
	DarkBackground string
}

// Set is one icon, ready to be drawn for every platform.
type Set struct {
	light, dark *Mark
	bg, darkBg  color.NRGBA
}

// New reads the icon at path and the options around it.
func New(path string, o Options) (*Set, error) {
	img, err := Open(path)
	if err != nil {
		return nil, fmt.Errorf("the icon %s: %w", path, err)
	}
	var dark Image
	if o.Dark != "" {
		if dark, err = Open(o.Dark); err != nil {
			return nil, fmt.Errorf("the dark icon %s: %w", o.Dark, err)
		}
	}
	return NewFrom(img, dark, o.Background, o.DarkBackground)
}

// NewFrom is New for pictures already read. dark may be nil.
func NewFrom(img, dark Image, background, darkBackground string) (*Set, error) {
	light, err := Prepare(img)
	if err != nil {
		return nil, err
	}
	s := &Set{light: light, bg: color.NRGBA{255, 255, 255, 255}, darkBg: color.NRGBA{0, 0, 0, 255}}
	if light.hasGround {
		s.bg = light.ground
	}
	if background != "" {
		if s.bg, err = ParseColor(background); err != nil {
			return nil, fmt.Errorf("the background: %w", err)
		}
	}
	if darkBackground != "" {
		if s.darkBg, err = ParseColor(darkBackground); err != nil {
			return nil, fmt.Errorf("the dark background: %w", err)
		}
	}
	s.dark = light
	if dark != nil {
		if s.dark, err = Prepare(dark); err != nil {
			return nil, fmt.Errorf("the dark icon: %w", err)
		}
	}
	s.light = readable(s.light, s.bg)
	s.dark = readable(s.dark, s.darkBg)
	return s, nil
}

// ParseColor reads #rgb or #rrggbb.
func ParseColor(v string) (color.NRGBA, error) {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "#") || (len(v) != 4 && len(v) != 7) {
		return color.NRGBA{}, fmt.Errorf("%q is not a colour: write it as #rrggbb", v)
	}
	c, ok := parseColor(v)
	if !ok {
		return color.NRGBA{}, fmt.Errorf("%q is not a colour: write it as #rrggbb", v)
	}
	c.A = 255
	return c, nil
}

// readable turns a grey mark over when it would be lost on its ground.
func readable(m *Mark, ground color.NRGBA) *Mark {
	if m.mono && contrast(m.luma, luminance(ground)) < 2 {
		return m.inverted()
	}
	return m
}

func contrast(a, b float64) float64 {
	hi, lo := math.Max(a, b), math.Min(a, b)
	return (hi + 0.05) / (lo + 0.05)
}

// Files is everything, each platform under a directory of its own:
//
//	android/res/   the resources of an Android application
//	ios/           AppIcon.appiconset
//	web/           favicons and the icons of a web manifest
//	desktop/       the Windows and macOS icons
func (s *Set) Files() ([]File, error) {
	var out []File
	for _, part := range []struct {
		dir string
		fn  func() ([]File, error)
	}{
		{"android/res", s.Android},
		{"ios/AppIcon.appiconset", s.IOS},
		{"web", s.Web},
		{"desktop", s.Desktop},
	} {
		files, err := part.fn()
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			out = append(out, File{Path: path.Join(part.dir, f.Path), Data: f.Data})
		}
	}
	return out, nil
}

// encode is a PNG.
func encode(img image.Image) ([]byte, error) {
	var b bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := enc.Encode(&b, img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// files collects PNGs and the first error met while making them.
type files struct {
	list []File
	err  error
}

func (f *files) png(name string, img image.Image) {
	if f.err != nil {
		return
	}
	data, err := encode(img)
	if err != nil {
		f.err = err
		return
	}
	f.list = append(f.list, File{Path: name, Data: data})
}

func (f *files) text(name, body string) {
	f.list = append(f.list, File{Path: name, Data: []byte(body)})
}

// onGround is the mark on a square of one colour, with nothing transparent.
func onGround(mark *image.NRGBA, ground color.NRGBA) *image.NRGBA {
	out := image.NewNRGBA(mark.Bounds())
	draw.Draw(out, out.Bounds(), image.NewUniform(ground), image.Point{}, draw.Src)
	draw.Draw(out, out.Bounds(), mark, image.Point{}, draw.Over)
	return out
}

// round cuts an image to a rounded square inset from its edges. radius is a
// share of the shape's own side; 0.5 is a circle.
func round(img *image.NRGBA, inset, radius float64) *image.NRGBA {
	n := float64(img.Bounds().Dx())
	side := n - 2*inset*n
	r := radius * side
	out := image.NewNRGBA(img.Bounds())
	half := side / 2
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			// The signed distance to the rounded square: negative inside.
			px, py := math.Abs(float64(x)+0.5-n/2), math.Abs(float64(y)+0.5-n/2)
			qx, qy := px-(half-r), py-(half-r)
			d := math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - r
			cover := math.Min(1, math.Max(0, 0.5-d))
			if cover == 0 {
				continue
			}
			c := img.NRGBAAt(x, y)
			c.A = uint8(math.Round(float64(c.A) * cover))
			out.SetNRGBA(x, y, c)
		}
	}
	return out
}

// body is an icon drawn the way a desktop expects it: a rounded square of the
// ground colour, inset from the canvas, with the mark in it.
func (s *Set) body(px int, inset, radius, fraction float64) *image.NRGBA {
	inner := 1 - 2*inset
	square := onGround(s.light.render(px, fraction*inner), s.bg)
	return round(square, inset, radius)
}
