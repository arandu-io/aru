package icons

import (
	"image"
)

// The body of a macOS icon is 824 of the 1024 units of its canvas, and its
// corners are a little over a fifth of the body's side.
const (
	macInset  = (1 - 824.0/1024) / 2
	macRadius = 0.2237
)

// windowsSizes are the images of a Windows icon: the system picks the nearest.
var windowsSizes = []int{16, 24, 32, 48, 64, 128, 256}

// macSizes are the images of a macOS icns, each the pixel side of an entry.
var macEntries = []struct {
	kind string
	px   int
}{
	{"icp4", 16}, {"icp5", 32}, {"icp6", 64}, {"ic07", 128}, {"ic08", 256},
	{"ic09", 512}, {"ic10", 1024}, {"ic11", 32}, {"ic12", 64}, {"ic13", 256}, {"ic14", 512},
}

// Desktop is the icons of the Windows and macOS applications: icon.ico, with
// the sizes Windows draws, and icon.icns, with the sizes of the Mac. A Windows
// icon fills its square with rounded corners; a Mac icon is inset on its
// canvas, the way the system's own are.
func (s *Set) Desktop() ([]File, error) {
	ico, err := s.ICO()
	if err != nil {
		return nil, err
	}
	icns, err := s.ICNS()
	if err != nil {
		return nil, err
	}
	var f files
	f.png("icon.png", s.MacImage(1024))
	if f.err != nil {
		return nil, f.err
	}
	return append(f.list, File{Path: "icon.ico", Data: ico}, File{Path: "icon.icns", Data: icns}), nil
}

// WindowsImage is the icon of a Windows application at one size.
func (s *Set) WindowsImage(px int) *image.NRGBA {
	return s.body(px, 0, 0.18, desktop)
}

// MacImage is the icon of a Mac application at one size.
func (s *Set) MacImage(px int) *image.NRGBA {
	return s.body(px, macInset, macRadius, desktop)
}

// ICO is the Windows icon file.
func (s *Set) ICO() ([]byte, error) {
	images := make([]*image.NRGBA, len(windowsSizes))
	for i, px := range windowsSizes {
		images[i] = s.WindowsImage(px)
	}
	return encodeICO(images)
}

// ICNS is the macOS icon file.
func (s *Set) ICNS() ([]byte, error) {
	var entries []icnsEntry
	cache := map[int][]byte{}
	for _, e := range macEntries {
		data, ok := cache[e.px]
		if !ok {
			var err error
			if data, err = encode(s.MacImage(e.px)); err != nil {
				return nil, err
			}
			cache[e.px] = data
		}
		entries = append(entries, icnsEntry{kind: e.kind, data: data})
	}
	return encodeICNS(entries), nil
}
