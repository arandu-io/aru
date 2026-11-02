package icons

import (
	"encoding/json"
	"image"
)

// Web is the icons of a web page and of the manifest of a web application: the
// favicon in the two forms browsers ask for, the touch icon, and the icons of
// the manifest, with the maskable ones drawn inside the safe zone so a launcher
// can cut them to any shape.
func (s *Set) Web() ([]File, error) {
	var f files
	flat := func(px int, fraction float64) *image.NRGBA {
		return onGround(s.light.render(px, fraction), s.bg)
	}
	f.png("favicon-16x16.png", flat(16, favicon))
	f.png("favicon-32x32.png", flat(32, favicon))
	f.png("apple-touch-icon.png", flat(180, webAny))
	f.png("icon-192.png", flat(192, webAny))
	f.png("icon-512.png", flat(512, webAny))
	f.png("icon-maskable-192.png", flat(192, fullBleed))
	f.png("icon-maskable-512.png", flat(512, fullBleed))
	if f.err != nil {
		return nil, f.err
	}

	ico, err := encodeICO([]*image.NRGBA{flat(16, favicon), flat(32, favicon), flat(48, favicon)})
	if err != nil {
		return nil, err
	}
	f.list = append(f.list, File{Path: "favicon.ico", Data: ico})

	type icon struct {
		Src     string `json:"src"`
		Sizes   string `json:"sizes"`
		Type    string `json:"type"`
		Purpose string `json:"purpose,omitempty"`
	}
	manifest, err := json.MarshalIndent(map[string]any{"icons": []icon{
		{"/icon-192.png", "192x192", "image/png", "any"},
		{"/icon-512.png", "512x512", "image/png", "any"},
		{"/icon-maskable-192.png", "192x192", "image/png", "maskable"},
		{"/icon-maskable-512.png", "512x512", "image/png", "maskable"},
	}}, "", "  ")
	if err != nil {
		return nil, err
	}
	f.list = append(f.list, File{Path: "manifest-icons.json", Data: append(manifest, '\n')})
	return f.list, nil
}
