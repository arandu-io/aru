package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/arandu-io/aru/internal/fonts"
)

// The font commands.
//
//	aru font:add "Young Serif" --as display
//	aru font:add "Public Sans" --as body --weight 400..700 --subset latin,latin-ext
//	aru font:list
//	aru font:remove display
//
// What `add` does, in order: fetch the family, write each subset under
// resources/fonts/, generate resources/css/fonts.css with the real
// unicode-range and the real metric overrides, generate assets/fonts.go so the
// files reach the served assets, and record what it took in arandu.toml.
//
// Everything it writes is committed. Nothing is fetched at build time and
// nothing at all at run time -- see the note on the fonts package.

// fontDir and the two generated files. They are constants because a project
// that put them somewhere else would be a project where `aru font:list` reports
// on a directory nobody else reads.
const (
	// Under assets/ and not under resources/, and the reason is a hard Go rule
	// rather than taste: //go:embed cannot reference a parent directory, so a
	// font in resources/fonts/ could not be embedded from assets/fonts.go at
	// all. It also turns out to be the honest place -- resources/ is what a
	// person edits, and nobody edits a woff2.
	fontDir   = "assets/fonts"
	fontCSS   = "resources/css/fonts.css"
	fontGoGen = "assets/fonts.go"
)

func fontAdd(args []string, stdout, stderr io.Writer) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}

	var family, role, weight, subsets, file, metricsFrom string
	var rest []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--as", "-a":
			role, i = next(args, i)
		case "--weight", "-w":
			weight, i = next(args, i)
		case "--subset", "-s":
			subsets, i = next(args, i)
		case "--file", "-f":
			file, i = next(args, i)
		case "--family":
			family, i = next(args, i)
		case "--metrics-from":
			metricsFrom, i = next(args, i)
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unknown flag %q", args[i])
			}
			rest = append(rest, args[i])
		}
	}
	// A positional name and --family are the same field. The positional form is
	// what a catalogue install reads like; --family is what a file install
	// needs, because the family and the path are two different strings.
	if positional := strings.Join(rest, " "); positional != "" {
		family = positional
	}

	if family == "" && file == "" {
		return fmt.Errorf(`which family?

    aru font:add "Young Serif" --as display
    aru font:add "Public Sans" --as body --weight 400..700

Anything in the catalogue, by the name it is published under. To find one:

    aru font:search grotesk
    aru font:search --category serif --variable
    aru font:info "Young Serif"

Or a file of your own:

    aru font:add --file ./HyzisGrotesk.woff2 --family "Hyzis Grotesk" --as display`)
	}
	if !fonts.Role(role).Valid() {
		return fmt.Errorf(`--as must be display or body, and %q is neither.

display is headings and the masthead; body is running text. There are two,
deliberately: a third would be a third file in every binary, and the answer to
"what about captions" is a weight rather than a family.`, role)
	}

	var subsetList []string
	for _, s := range strings.Split(subsets, ",") {
		if s = strings.TrimSpace(s); s != "" {
			subsetList = append(subsetList, s)
		}
	}
	// Only for a catalogue install. A file of your own covers whatever it
	// covers, and announcing a subset choice nobody made would be announcing
	// something untrue.
	if len(subsetList) == 0 && file == "" {
		subsetList = []string{"latin"}
		fmt.Fprintln(stderr, "subset: latin (English and Portuguese are both inside U+0000-00FF;")
		fmt.Fprintln(stderr, "        --subset latin,latin-ext adds Polish, Czech and Turkish)")
	}

	var got fonts.Family
	if file != "" {
		// A file of your own: no download, no catalogue, and no licence to
		// fetch. Whoever drew it owns those questions.
		if got, err = fonts.Local(file, family, weight, metricsFrom); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "vendoring %s as %s\n", file, got.Name)
	} else {
		fmt.Fprintf(stdout, "fetching %s...\n", family)
		if got, err = fonts.Fetch(nil, family, weight, subsetList); err != nil {
			// A name that matched nothing is the common mistake, and a refusal
			// that only says so sends somebody to a browser.
			if all, cErr := fonts.Catalogue(nil); cErr == nil {
				if near := fonts.Nearest(all, family, 6); len(near) > 0 {
					return fmt.Errorf("%s: %w\n\n  did you mean: %s\n  or search: aru font:search %s",
						family, err, strings.Join(near, ", "), firstWord(family))
				}
			}
			return fmt.Errorf("%s: %w", family, err)
		}
	}

	// Written before anything is recorded, so a failure halfway leaves files
	// that the next run overwrites rather than a manifest naming files that are
	// not there.
	if err := os.MkdirAll(filepath.Join(root, fontDir), 0o755); err != nil {
		return err
	}

	total := 0
	var notes []string
	for _, face := range got.Faces {
		path := filepath.Join(root, fontDir, face.File)
		if err := os.WriteFile(path, face.Body, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
		total += len(face.Body)
		notes = append(notes, face.File+"|"+face.UnicodeRange+"|"+fonts.AssetHash(face.Body))
		fmt.Fprintf(stdout, "  %s  %s\n", face.File, size(len(face.Body)))
	}

	// The licence, beside the files it covers. The OFL requires it to travel
	// with anything redistributed, and a binary with the font embedded in it is
	// a redistribution.
	if len(got.LicenseText) > 0 {
		name := strings.ToUpper(got.License) + "-" + fonts.Slug(got.Name) + ".txt"
		if err := os.WriteFile(filepath.Join(root, fontDir, name), got.LicenseText, 0o644); err != nil {
			return fmt.Errorf("writing the licence: %w", err)
		}
		fmt.Fprintf(stdout, "  %s  %s\n", name, size(len(got.LicenseText)))
	} else if got.License != "" {
		fmt.Fprintf(stderr, "  the %s licence text could not be fetched -- add it to %s by hand before redistributing\n",
			strings.ToUpper(got.License), fontDir)
	}

	installed := fonts.Installed{
		Role: fonts.Role(role), Family: got.Name, Weight: got.Weight,
		Subsets: got.Subsets, License: got.License, Metrics: got.Metrics,
		FileList: notes,
	}

	all, err := readInstalled(root)
	if err != nil {
		return err
	}
	all[fonts.Role(role)] = installed

	if err := writeFontFiles(root, all); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "\n%s is the %s face, %s in the binary\n", got.Name, role, size(total))
	if advice := fonts.Advice(got, file); advice != "" {
		fmt.Fprintf(stderr, "\n  %s\n", advice)
	}
	if got.Metrics.Ascent > 0 {
		fmt.Fprintf(stdout, "  metric-matched fallback: ascent %.1f%%, descent %.1f%%\n",
			got.Metrics.Ascent, got.Metrics.Descent)
	} else {
		fmt.Fprintln(stderr, "  the metrics could not be read: the swap will reflow when the font lands")
	}
	if got.License != "" {
		fmt.Fprintf(stdout, "  licence: %s -- keep it with the files you redistribute\n", strings.ToUpper(got.License))
	}
	fmt.Fprintf(stdout, "\nUse it: font-family: var(--font-%s) -- or the Tailwind utility for that token.\n", fontToken(role))
	fmt.Fprintln(stdout, "Then: aru view:build")
	return nil
}

func fontList(_ []string, stdout, _ io.Writer) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	all, err := readInstalled(root)
	if err != nil {
		return err
	}
	if len(all) == 0 {
		fmt.Fprintln(stdout, "no fonts vendored -- the system stack is what draws every page")
		fmt.Fprintln(stdout, `  aru font:add "Young Serif" --as display`)
		return nil
	}

	for _, role := range fonts.Roles {
		in, ok := all[role]
		if !ok {
			continue
		}
		bytes := 0
		for _, note := range in.FileList {
			file, _, _ := strings.Cut(note, "|")
			if st, err := os.Stat(filepath.Join(root, fontDir, file)); err == nil {
				bytes += int(st.Size())
			}
		}
		fmt.Fprintf(stdout, "%-8s %s  weight %s  %s  %s  %s\n",
			role, in.Family, in.Weight, strings.Join(in.Subsets, "+"),
			size(bytes), strings.ToUpper(in.License))
	}
	return nil
}

func fontRemove(args []string, stdout, _ io.Writer) error {
	if len(args) != 1 || !fonts.Role(args[0]).Valid() {
		return fmt.Errorf("aru font:remove <display|body>")
	}
	root, err := projectRoot()
	if err != nil {
		return err
	}

	all, err := readInstalled(root)
	if err != nil {
		return err
	}
	in, ok := all[fonts.Role(args[0])]
	if !ok {
		return fmt.Errorf("no %s font is vendored", args[0])
	}

	for _, note := range in.FileList {
		file, _, _ := strings.Cut(note, "|")
		path := filepath.Join(root, fontDir, file)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		fmt.Fprintf(stdout, "removed %s\n", file)
	}
	delete(all, fonts.Role(args[0]))

	if err := writeFontFiles(root, all); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s falls back to the system stack\n", args[0])
	return nil
}

// writeFontFiles regenerates everything derived from the manifest.
//
// One function for all three, because they have to agree: a fonts.css naming a
// file that assets/fonts.go does not register is a 404 on every page, and a
// registered file nothing references is bytes in the binary nobody draws.
func writeFontFiles(root string, all map[fonts.Role]fonts.Installed) error {
	list := make([]fonts.Installed, 0, len(all))
	for _, in := range all {
		list = append(list, in)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Role < list[j].Role })

	if err := os.MkdirAll(filepath.Join(root, "resources", "css"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, fontCSS), []byte(fonts.Stylesheet(list)), 0o644); err != nil {
		return err
	}
	if err := writeFontGo(root, list); err != nil {
		return err
	}
	return writeManifest(root, list)
}

// writeFontGo generates the file that hands the bytes to the view layer.
//
// go:embed and one call per file, which is the same shape assets/app.css
// already has. Deleting the import in bootstrap is what turns it off.
func writeFontGo(root string, list []fonts.Installed) error {
	path := filepath.Join(root, fontGoGen)
	if len(list) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	var b strings.Builder
	b.WriteString(`// Code generated by ` + "`aru font:add`" + `. DO NOT EDIT.
//
// The vendored faces, embedded and handed to kyse, which serves them
// content-addressed under /_arandu/assets/<hash>/. resources/css/fonts.css
// references each by that exact URL -- its own content hash, not the
// stylesheet's, because a mismatch is served uncached on purpose.
//
// It is committed for the same reason assets/app.css is: ` + "`go build`" + ` has to
// work on a fresh clone, before any tool has run.

package assets

import (
	_ "embed"

	"github.com/arandu-io/kyse/fonts"
)

`)
	var inits []string
	for _, in := range list {
		for i, note := range in.FileList {
			file, _, _ := strings.Cut(note, "|")
			name := fmt.Sprintf("%s%d", in.Role, i)
			fmt.Fprintf(&b, "//go:embed fonts/%s\nvar %s []byte\n\n", file, name)
			inits = append(inits, fmt.Sprintf("\tfonts.Register(%q, %s)", file, name))
		}
	}
	b.WriteString("func init() {\n" + strings.Join(inits, "\n") + "\n}\n")

	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func fontToken(role string) string {
	if role == string(fonts.Body) {
		return "sans"
	}
	return "display"
}

// firstWord is what to suggest searching for: the whole name matched nothing, so
// half of it is the better query.
func firstWord(s string) string {
	if i := strings.IndexByte(s, ' '); i > 0 {
		return s[:i]
	}
	return s
}

func size(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f KB", float64(n)/1024)
}

func next(args []string, i int) (string, int) {
	if i+1 < len(args) {
		return args[i+1], i + 1
	}
	return "", i
}
