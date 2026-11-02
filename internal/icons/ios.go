package icons

import (
	"encoding/json"
	"image"
	"image/color"
)

// iosSize is the side of the one picture an iOS app icon is, from which the
// system draws every size it needs.
const iosSize = 1024

// IOS is the contents of an AppIcon.appiconset: the default icon, which is
// opaque because the App Store refuses transparency in it, the dark icon, which
// is the mark alone on nothing so the system puts its own ground behind it, and
// the tinted icon, a grey picture the system colours.
func (s *Set) IOS() ([]File, error) {
	var f files
	f.png("AppIcon.png", onGround(s.light.render(iosSize, fullBleed), s.bg))
	f.png("AppIcon-dark.png", s.dark.render(iosSize, fullBleed))
	f.png("AppIcon-tinted.png", s.tinted())
	if f.err != nil {
		return nil, f.err
	}

	type appearance struct {
		Appearance string `json:"appearance"`
		Value      string `json:"value"`
	}
	type entry struct {
		Appearances []appearance `json:"appearances,omitempty"`
		Filename    string       `json:"filename"`
		Idiom       string       `json:"idiom"`
		Platform    string       `json:"platform"`
		Size        string       `json:"size"`
	}
	entries := []entry{
		{Filename: "AppIcon.png"},
		{Filename: "AppIcon-dark.png", Appearances: []appearance{{"luminosity", "dark"}}},
		{Filename: "AppIcon-tinted.png", Appearances: []appearance{{"luminosity", "tinted"}}},
	}
	for i := range entries {
		entries[i].Idiom, entries[i].Platform, entries[i].Size = "universal", "ios", "1024x1024"
	}
	contents, err := json.MarshalIndent(map[string]any{
		"images": entries,
		"info":   map[string]any{"author": "aru", "version": 1},
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	f.list = append(f.list, File{Path: "Contents.json", Data: append(contents, '\n')})
	return f.list, nil
}

// tinted is the dark mark in greys, on black.
func (s *Set) tinted() *image.NRGBA {
	mark := s.dark.render(iosSize, fullBleed)
	for i := 0; i < len(mark.Pix); i += 4 {
		g := uint8(luma8(mark.Pix[i], mark.Pix[i+1], mark.Pix[i+2]) + 0.5)
		mark.Pix[i], mark.Pix[i+1], mark.Pix[i+2] = g, g, g
	}
	return onGround(mark, color.NRGBA{0, 0, 0, 255})
}

// luma8 is the brightness of an sRGB colour as the weighted sum video uses.
func luma8(r, g, b uint8) float64 {
	return 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
}
