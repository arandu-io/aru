package fonts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A font of your own, from a file rather than from the catalogue.
//
// It exists for the case the catalogue cannot serve: a family being drawn for
// the project, a licensed face bought from a foundry, a subset produced by a
// tool somebody already runs. The vendoring is otherwise identical -- same
// directory, same generated @font-face, same manifest -- because the point of
// the command is the wiring, and the wiring does not care where the bytes came
// from.

// Formats a local file may be in, and what each is served as.
//
// WOFF2 is what ships. TrueType and OpenType are accepted because a font in
// development is a .ttf long before it is a .woff2, and refusing them would mean
// the command is unusable during exactly the work it was asked for -- but they
// are two to four times the bytes, and the command says so every time.
var formats = map[string]string{
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
}

// Local reads a font from disk and returns it in the shape Fetch would have.
//
// family is what the CSS will call it, and it is required: a file name is not a
// family name, and deriving one from "Arandu-Regular-v3-final.ttf" would
// produce a family called exactly that.
//
// metricsFrom is an optional TrueType or OpenType to read the fallback metrics
// out of. It exists because a .woff2 is Brotli-compressed and its tables cannot
// be reached without a decompressor -- so when the vendored file is a .woff2,
// the metrics come from the .ttf it was built from, which whoever built it has.
// Without either, no override is written: an invented number shifts the layout
// in a direction nobody measured.
func Local(path, family, weight, metricsFrom string) (Family, error) {
	if strings.TrimSpace(family) == "" {
		return Family{}, fmt.Errorf(`--family is required with --file.

A file name is not a family name: --file Arandu-Regular-v3.ttf would
produce a CSS family called Arandu-Regular-v3.

    aru font:add --file ./Arandu.woff2 --family "Arandu" --as display`)
	}

	ext := strings.ToLower(filepath.Ext(path))
	if _, ok := formats[ext]; !ok {
		return Family{}, fmt.Errorf("%s is not a font this can vendor: want .woff2, .ttf or .otf", ext)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		return Family{}, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(body) == 0 {
		return Family{}, fmt.Errorf("%s is empty", path)
	}

	if weight == "" {
		weight = "400"
	}

	out := Family{
		Name: family, Weight: weight, Subsets: []string{"custom"},
		Faces: []Face{{
			Family: family, Weight: weight, Subset: "custom",
			// No unicode-range. The source published none, and inventing one is
			// how a font stops drawing a character it can draw: a range narrower
			// than the file is a glyph the browser refuses to ask for.
			File: localFileName(family, weight, ext),
			Body: body,
		}},
	}

	// The metrics, from whichever file can be read. A .ttf or .otf carries its
	// tables in the clear; a .woff2 does not, which is what metricsFrom is for.
	source := path
	if metricsFrom != "" {
		source = metricsFrom
	}
	if strings.ToLower(filepath.Ext(source)) != ".woff2" {
		raw := body
		if source != path {
			if raw, err = os.ReadFile(source); err != nil {
				return Family{}, fmt.Errorf("reading %s for the metrics: %w", source, err)
			}
		}
		if m, err := readMetrics(raw); err == nil {
			out.Metrics = m
		}
	}

	return out, nil
}

// localFileName is what a vendored local face is written as.
//
// The extension is kept, because it decides the content type and because a .ttf
// renamed .woff2 is a font every browser refuses with an error that names
// neither.
func localFileName(family, weight, ext string) string {
	return fmt.Sprintf("%s-%s%s", Slug(family),
		strings.NewReplacer(" ", "", ";", "-", "..", "-").Replace(weight), ext)
}

// Advice is what to say about a file that is not a woff2.
//
// Empty for a woff2. Otherwise the size it would save and the one command that
// does it -- a warning that does not say what to do instead is a warning people
// learn to scroll past.
func Advice(f Family, path string) string {
	if len(f.Faces) == 0 || strings.HasSuffix(f.Faces[0].File, ".woff2") {
		return ""
	}
	size := len(f.Faces[0].Body)
	return fmt.Sprintf(`this is not a .woff2, so it ships uncompressed: %.1f KB, roughly two to
  four times what the same font costs as woff2, on every first visit.

  Converting needs a font compiler, which is not in this toolchain and will not
  be -- it is a build dependency for a step that runs once per font. Whatever
  drew the file can export it, and so can:

      pip install fonttools brotli && fonttools ttLib.woff2 compress %s

  Then run this again, pointing --file at the .woff2 it writes and
  --metrics-from at this file -- a woff2 is compressed and its metrics cannot
  be read out of it.`, float64(size)/1024, path)
}
