package icons

import (
	"bytes"
	"encoding/binary"
	"image"
)

// encodeICO writes the images, each a PNG, as an icon file. Windows reads PNG
// entries from Vista on, and an entry of 256 pixels is written as 0.
func encodeICO(images []*image.NRGBA) ([]byte, error) {
	pngs := make([][]byte, len(images))
	for i, img := range images {
		data, err := encode(img)
		if err != nil {
			return nil, err
		}
		pngs[i] = data
	}
	var b bytes.Buffer
	le := binary.LittleEndian
	_ = binary.Write(&b, le, [3]uint16{0, 1, uint16(len(images))})
	offset := 6 + 16*len(images)
	for i, img := range images {
		side := func(n int) byte {
			if n >= 256 {
				return 0
			}
			return byte(n)
		}
		b.WriteByte(side(img.Bounds().Dx()))
		b.WriteByte(side(img.Bounds().Dy()))
		b.WriteByte(0) // colours in the palette
		b.WriteByte(0)
		_ = binary.Write(&b, le, [2]uint16{1, 32}) // planes, bits per pixel
		_ = binary.Write(&b, le, uint32(len(pngs[i])))
		_ = binary.Write(&b, le, uint32(offset))
		offset += len(pngs[i])
	}
	for _, data := range pngs {
		b.Write(data)
	}
	return b.Bytes(), nil
}

// icnsEntry is one image of a macOS icon file, by the four letters that say
// which size it is.
type icnsEntry struct {
	kind string
	data []byte
}

// encodeICNS writes entries as an icns file: a header, then each entry's four
// letters and its length counted with them, all big-endian.
func encodeICNS(entries []icnsEntry) []byte {
	total := 8
	for _, e := range entries {
		total += 8 + len(e.data)
	}
	var b bytes.Buffer
	b.WriteString("icns")
	_ = binary.Write(&b, binary.BigEndian, uint32(total))
	for _, e := range entries {
		b.WriteString(e.kind)
		_ = binary.Write(&b, binary.BigEndian, uint32(8+len(e.data)))
		b.Write(e.data)
	}
	return b.Bytes()
}
