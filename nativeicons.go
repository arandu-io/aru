package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/arandu-io/aru/internal/icons"
)

// nativeIconsUsage is the one line the dispatch table prints and the refusal repeats.
const nativeIconsUsage = "aru native:icons <image> [-background #rrggbb] [-dark-background #rrggbb] [-dark image] [-output dir]"

const nativeIconsHelp = `Draws every icon an application needs from one picture: a PNG, a JPEG or an
SVG, with its background or without. Nothing outside aru is involved.

The mark is measured, not trusted: its margins are cut away and it is centred
and scaled until the circle that holds all of it is as wide as the platform
wants, so the same picture comes out the same size on every one of them.

It writes, under the output directory (default: icons):

  android/res/              launcher icons for every density, round and flat for
                            releases before Android 8; adaptive ones with a
                            foreground well inside the 66 of 108 units every mask leaves
                            visible, a background colour and a monochrome layer for
                            the themed icon of Android 13; the status bar icon
                            (ic_stat), white on transparent; the splash screen's
                            icon in the light and the dark appearance and its colours
  ios/AppIcon.appiconset/   the default, dark (on transparent) and tinted (grey)
                            icons of iOS 18, and the Contents.json that names them
  web/                      favicon.ico and PNGs, the touch icon, the icons of a web
                            manifest including the maskable ones
  desktop/                  icon.ico for Windows and icon.icns for macOS

native:build does the same by itself from -icon, so this command is for the
files that are wanted on their own: a store listing, a project built by another
tool, a look before a build.

The background is the colour the picture was exported on, or white. The dark
background is black. The dark appearance draws the same mark, turned over when it
is grey and would be lost on the dark ground; -dark names a picture of its own.`

// nativeIcons writes every icon of every platform from one picture.
func nativeIcons(args []string, stdout, stderr io.Writer) error {
	source, args := takeName(args)
	flags := flag.NewFlagSet("native:icons", flag.ContinueOnError)
	flags.SetOutput(stderr)
	background := flags.String("background", "", "the colour the mark sits on, as #rrggbb (default: the picture's own, or white)")
	darkBackground := flags.String("dark-background", "", "the colour the mark sits on in the dark appearance, as #rrggbb (default: black)")
	dark := flags.String("dark", "", "a picture for the dark appearance (default: the same mark, turned over when it is grey)")
	output := flags.String("output", "icons", "the directory the icons are written to")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("native:icons: %w", err)
	}
	if source == "" && flags.NArg() > 0 {
		source = flags.Arg(0)
	}
	if source == "" {
		return fmt.Errorf("usage: %s", nativeIconsUsage)
	}

	set, err := icons.New(source, icons.Options{Dark: *dark, Background: *background, DarkBackground: *darkBackground})
	if err != nil {
		return fmt.Errorf("native:icons: %w", err)
	}
	files, err := set.Files()
	if err != nil {
		return fmt.Errorf("native:icons: %w", err)
	}
	if err := os.MkdirAll(*output, 0o755); err != nil {
		return fmt.Errorf("native:icons: %w", err)
	}
	if entries, err := os.ReadDir(*output); err == nil && len(entries) > 0 {
		fmt.Fprintf(stderr, "native:icons: %s already has files; the ones with the names below are replaced\n", *output)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("native:icons: %w", err)
	}

	count := map[string]int{}
	for _, f := range files {
		path := filepath.Join(*output, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("native:icons: %w", err)
		}
		if err := os.WriteFile(path, f.Data, 0o644); err != nil {
			return fmt.Errorf("native:icons: %w", err)
		}
		count[firstDir(f.Path)]++
	}
	platforms := make([]string, 0, len(count))
	for p := range count {
		platforms = append(platforms, p)
	}
	sort.Strings(platforms)
	for _, p := range platforms {
		fmt.Fprintf(stdout, "%s  %d files\n", filepath.Join(*output, p), count[p])
	}
	return nil
}

func firstDir(p string) string {
	for i := 0; i < len(p); i++ {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return p
}
