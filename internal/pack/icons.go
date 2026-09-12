package pack

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"

	"golang.org/x/image/draw"
	"golang.org/x/sync/errgroup"
)

type iconVariant struct {
	path string
	size int
	fill bool
}

func buildIcons(baseDir, icon string, variants []iconVariant) error {
	f, err := os.Open(icon)
	if err != nil {
		return err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return err
	}
	var resizes errgroup.Group
	for _, v := range variants {
		v := v
		resizes.Go(func() (err error) {
			path := filepath.Join(baseDir, v.path)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			f, err := os.Create(path)
			if err != nil {
				return err
			}
			defer func() {
				if cerr := f.Close(); err == nil {
					err = cerr
				}
			}()
			return png.Encode(f, resizeIcon(v, img))
		})
	}
	return resizes.Wait()
}

func resizeIcon(v iconVariant, img image.Image) *image.NRGBA {
	scaled := image.NewNRGBA(image.Rectangle{Max: image.Point{X: v.size, Y: v.size}})
	op := draw.Src
	if v.fill {
		op = draw.Over
		draw.Draw(scaled, scaled.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
	}
	draw.CatmullRom.Scale(scaled, scaled.Bounds(), img, img.Bounds(), op, nil)

	return scaled
}
