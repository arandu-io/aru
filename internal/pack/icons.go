package pack

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/arandu-io/aru/internal/icons"
)

// iconSet is the application's icons, drawn from the picture it named or from
// appicon.png, or appicon.svg, beside its package. It answers nil, with no error, for an
// application that has no icon: each platform then ships its own placeholder.
func (bi *buildInfo) iconSet() (*icons.Set, error) {
	if _, err := os.Stat(bi.iconPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return icons.New(bi.iconPath, bi.iconOptions)
}

// writeIcons writes generated files under dir.
func writeIcons(dir string, files []icons.File) error {
	for _, f := range files {
		path := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, f.Data, 0o600); err != nil {
			return err
		}
	}
	return nil
}
