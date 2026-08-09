// Package fonts vendors a web font into a project, once, from the command line.
//
// It exists because of the shape of the alternative. A font is either a CDN
// link -- which this framework cannot have, because the CSP is default-src
// 'self' and loosening it to look different is a bad trade -- or it is a file
// somebody downloads by hand, drops in a directory, and writes four @font-face
// rules for, guessing the unicode-range and getting the fallback metrics wrong.
//
// So it is a command. `aru font:add "Young Serif" --as display` fetches the
// family, writes the woff2 under resources/fonts/, generates the @font-face with
// the real unicode-range and the real metric overrides, and records what it took
// in arandu.toml. The bytes are committed: nothing is fetched at build time, and
// nothing at all at run time.
//
// It is the same shape as the Tailwind binary the toolchain package pins --
// downloaded once, verified, recorded, and then it is simply a file in the
// project. Having a build step is allowed; being Node is not (RULE 13).
//
// # What it deliberately does not do
//
// No font hosting, no self-serve subsetting beyond the ranges the source
// publishes, and no automatic weight discovery. A subsetter would need a font
// compiler in the toolchain; the published subsets are already per-script, which
// is the split that matters.
package fonts

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// The API this fetches from, and why the two user agents.
//
// css2 answers with @font-face blocks whose format depends on what the client
// says it is. A modern agent gets woff2, which is what ships. An old one gets
// ttf, which is uncompressed OpenType -- and that is the only way to read the
// font's metrics without a Brotli decoder, which would be a dependency for
// three numbers.
const (
	cssAPI    = "https://fonts.googleapis.com/css2"
	legacyAPI = "https://fonts.googleapis.com/css"
	metaAPI   = "https://fonts.google.com/metadata/fonts/"

	modernAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	// Anything old enough to predate woff2. The exact string does not matter,
	// only that it is not recognised as supporting the modern format.
	legacyAgent = "Mozilla/4.0"
)

// Face is one downloaded file: a family at a weight, covering one subset.
type Face struct {
	// Family is the name as the source spells it: "Young Serif".
	Family string
	// Weight is the CSS weight, or a range for a variable font: "400", "400 700".
	Weight string
	// Subset is the script coverage: "latin", "latin-ext".
	Subset string
	// UnicodeRange is what the @font-face declares, verbatim from the source.
	//
	// Copied rather than computed: the range and the file are two halves of one
	// decision made upstream, and a range narrower than the file wastes bytes
	// while a wider one asks the browser to download a file that cannot draw
	// the character it needed.
	UnicodeRange string
	// File is the name the font is written and served under.
	File string
	// Body is the woff2 itself.
	Body []byte
}

// Metrics are what a fallback face needs to occupy the same space.
//
// Without them, a font that arrives after first paint reflows everything under
// it -- the layout shift that `font-display: swap` is otherwise guaranteed to
// cause. With them, the fallback is stretched to the same ascent, descent and
// line gap, and the swap changes the shapes without moving anything.
//
// All three are percentages of the em, which is what the CSS properties take.
type Metrics struct {
	Ascent  float64
	Descent float64
	LineGap float64
	// SizeAdjust matches the x-height, so the fallback looks the same size and
	// not merely the same height. Zero means it could not be computed and the
	// property is left out.
	SizeAdjust float64
}

// Family is everything `aru font:add` learned about one request.
type Family struct {
	Name    string
	Weight  string
	Subsets []string
	// License is the identifier the source publishes: "ofl", "apache2".
	License string
	// LicenseText is the licence itself, to be written beside the files.
	//
	// The OFL requires it to travel with anything redistributed, and a binary
	// with the font embedded in it is a redistribution. A command that vendored
	// the bytes and left the licence behind would be a command that quietly put
	// every project using it in breach.
	LicenseText []byte
	Faces       []Face
	Metrics     Metrics
}

// Fetch downloads a family and reads its metrics.
//
// weight is a CSS weight ("400"), a list (";" separated, as the API takes them)
// or a range ("400..700") for a variable font. subsets are script names; empty
// means latin, which is the right default for English and for Portuguese --
// U+0000-00FF covers every accented letter both use, and latin-ext is Polish,
// Czech and Turkish.
func Fetch(client *http.Client, family, weight string, subsets []string) (Family, error) {
	if strings.TrimSpace(family) == "" {
		return Family{}, errors.New("no family name")
	}
	if weight == "" {
		weight = "400"
	}
	if len(subsets) == 0 {
		subsets = []string{"latin"}
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	out := Family{Name: family, Weight: weight, Subsets: subsets}

	css, err := get(client, cssURL(family, weight), modernAgent)
	if err != nil {
		return Family{}, err
	}

	wanted := map[string]bool{}
	for _, s := range subsets {
		wanted[s] = true
	}

	for _, block := range parse(string(css)) {
		if !wanted[block.subset] {
			continue
		}
		body, err := get(client, block.url, modernAgent)
		if err != nil {
			return Family{}, fmt.Errorf("downloading the %s subset: %w", block.subset, err)
		}
		out.Faces = append(out.Faces, Face{
			Family:       family,
			Weight:       weight,
			Subset:       block.subset,
			UnicodeRange: block.unicodeRange,
			File:         FileName(family, weight, block.subset),
			Body:         body,
		})
	}
	if len(out.Faces) == 0 {
		return Family{}, fmt.Errorf("%q has no %s subset -- the source published none of what was asked for",
			family, strings.Join(subsets, ", "))
	}

	// The metrics, from the TrueType the same API serves to an old client. It is
	// downloaded, read and thrown away: what ships is the woff2 above.
	if m, err := metrics(client, family, weight); err == nil {
		out.Metrics = m
	}
	// A failure here is not fatal. The stylesheet then omits the overrides and
	// the swap costs a reflow, which is worse than the alternative and much
	// better than refusing to add a font over three numbers.

	out.License, out.LicenseText = license(client, family)
	return out, nil
}

// FileName is what a face is written and served as.
//
// The weight and the subset are in the name because they are what makes two
// files of one family different, and a directory of young-serif.woff2,
// young-serif-2.woff2 is a directory nobody can review.
func FileName(family, weight, subset string) string {
	return fmt.Sprintf("%s-%s-%s.woff2", Slug(family), strings.NewReplacer(
		" ", "", ";", "-", "..", "-").Replace(weight), subset)
}

// AssetHash is the path segment the framework serves a file under.
//
// It is view.AssetHash, reimplemented, because the CLI is a separate module and
// cannot import the framework. Both sides carry a test pinning the same twelve
// characters for the same input; the framework's says so on the function.
//
// What silent drift looks like: every vendored font served with
// Cache-Control: no-cache and re-downloaded on every page view, with nothing
// broken enough for anybody to notice.
func AssetHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])[:12]
}

// Slug is the family name as a file name: "Young Serif" -> "young-serif".
func Slug(family string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(family)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '-', r == '_':
			b.WriteRune('-')
		}
	}
	return b.String()
}

// block is one @font-face in the answer.
type block struct {
	subset       string
	url          string
	unicodeRange string
}

var (
	commentPattern = regexp.MustCompile(`/\*\s*([a-z0-9-]+)\s*\*/`)
	urlPattern     = regexp.MustCompile(`url\((https://[^)]+)\)`)
	rangePattern   = regexp.MustCompile(`unicode-range:\s*([^;]+);`)
)

// parse reads the @font-face blocks, keeping the subset name from the comment
// above each one.
//
// The comment is how the subset is identified: the API emits `/* latin */`
// before the block it belongs to, and there is nothing inside the block that
// names it. Reading the unicode-range instead would mean maintaining a table of
// which range is which script, which is the source's job and changes when it
// adds a character.
func parse(css string) []block {
	// Walk the file once, remembering the last comment seen.
	var out []block
	var subset string
	var current *block
	for _, line := range strings.Split(css, "\n") {
		if m := commentPattern.FindStringSubmatch(line); m != nil {
			subset = m[1]
			continue
		}
		if strings.Contains(line, "@font-face") {
			out = append(out, block{subset: subset})
			current = &out[len(out)-1]
			continue
		}
		if current == nil {
			continue
		}
		if m := urlPattern.FindStringSubmatch(line); m != nil {
			current.url = m[1]
		}
		if m := rangePattern.FindStringSubmatch(line); m != nil {
			current.unicodeRange = strings.TrimSpace(m[1])
		}
	}

	kept := out[:0]
	for _, b := range out {
		if b.url != "" && b.subset != "" {
			kept = append(kept, b)
		}
	}
	return kept
}

func cssURL(family, weight string) string {
	q := strings.ReplaceAll(family, " ", "+")
	if weight != "" && weight != "400" {
		q += ":wght@" + weight
	}
	return cssAPI + "?family=" + q + "&display=swap"
}

// metrics downloads the TrueType and reads three tables out of it.
//
// A hand-written reader rather than a font library, and it is sixty lines: the
// table directory is a count and a list of (tag, offset, length), and the three
// numbers live at fixed offsets inside head, hhea and OS/2. A parser dependency
// would be a dependency in the CLI's graph -- which has exactly one node today
// -- for something the format publishes at a documented offset.
func metrics(client *http.Client, family, weight string) (Metrics, error) {
	css, err := get(client, legacyAPI+"?family="+strings.ReplaceAll(family, " ", "+"), legacyAgent)
	if err != nil {
		return Metrics{}, err
	}
	m := urlPattern.FindStringSubmatch(string(css))
	if m == nil {
		return Metrics{}, errors.New("no TrueType in the answer")
	}
	ttf, err := get(client, m[1], legacyAgent)
	if err != nil {
		return Metrics{}, err
	}
	return readMetrics(ttf)
}

// readMetrics parses the sfnt table directory and the three tables it needs.
func readMetrics(ttf []byte) (Metrics, error) {
	if len(ttf) < 12 {
		return Metrics{}, errors.New("not a font file")
	}
	count := int(binary.BigEndian.Uint16(ttf[4:6]))

	tables := map[string]int{}
	for i := range count {
		at := 12 + 16*i
		if at+16 > len(ttf) {
			return Metrics{}, errors.New("the table directory runs past the end of the file")
		}
		tables[string(ttf[at:at+4])] = int(binary.BigEndian.Uint32(ttf[at+8 : at+12]))
	}

	head, okHead := tables["head"]
	hhea, okHhea := tables["hhea"]
	os2, okOS2 := tables["OS/2"]
	if !okHead || !okHhea || !okOS2 {
		return Metrics{}, errors.New("the font is missing head, hhea or OS/2")
	}
	if head+20 > len(ttf) || hhea+10 > len(ttf) || os2+90 > len(ttf) {
		return Metrics{}, errors.New("a table runs past the end of the file")
	}

	// unitsPerEm is the denominator of everything below: the metrics are in font
	// units, and the CSS properties are percentages of the em.
	upm := float64(binary.BigEndian.Uint16(ttf[head+18 : head+20]))
	if upm == 0 {
		return Metrics{}, errors.New("unitsPerEm is zero")
	}

	ascent := float64(int16(binary.BigEndian.Uint16(ttf[hhea+4 : hhea+6])))
	descent := float64(int16(binary.BigEndian.Uint16(ttf[hhea+6 : hhea+8])))
	lineGap := float64(int16(binary.BigEndian.Uint16(ttf[hhea+8 : hhea+10])))
	// sxHeight, at offset 86 of OS/2 version 2 and later. Zero when the version
	// is older, and zero means the size-adjust is left out rather than guessed.
	xHeight := float64(int16(binary.BigEndian.Uint16(ttf[os2+86 : os2+88])))

	out := Metrics{
		Ascent:  100 * ascent / upm,
		Descent: 100 * abs(descent) / upm,
		LineGap: 100 * lineGap / upm,
	}
	if xHeight > 0 {
		// The fallback is scaled so its x-height matches this one. 0.52 em is
		// roughly where the system faces sit; matching exactly would need the
		// fallback's own metrics, which depend on the operating system and are
		// not knowable here.
		out.SizeAdjust = 100 * (xHeight / upm) / 0.52
	}
	return out, nil
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// license reads the identifier the source publishes, for the record written
// into arandu.toml.
//
// It is best-effort: an empty answer records nothing rather than guessing, and a
// licence nobody recorded is better than one recorded wrongly.
func license(client *http.Client, family string) (string, []byte) {
	body, err := get(client, metaAPI+strings.ReplaceAll(family, " ", "%20"), modernAgent)
	if err != nil {
		return "", nil
	}
	// The answer is JSON behind an anti-hijacking prefix. One field is wanted,
	// so it is read with a scan rather than by decoding a document whose shape
	// is not this package's business.
	const key = `"license": "`
	at := strings.Index(string(body), key)
	if at < 0 {
		return "", nil
	}
	rest := string(body)[at+len(key):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return "", nil
	}
	id := rest[:end]

	// The text itself, from where the source keeps it. A best-effort fetch: an
	// empty answer writes no file, and the identifier still reaches arandu.toml
	// so a person can find it. Recording a licence and inventing its text would
	// be worse than recording only the name.
	text, err := get(client, fmt.Sprintf(licenseURL, id, repoDir(family), licenseFile(id)), modernAgent)
	if err != nil {
		return id, nil
	}
	return id, text
}

// Where the source keeps the licence text, and what it is called there.
const licenseURL = "https://raw.githubusercontent.com/google/fonts/main/%s/%s/%s"

// repoDir is the family as the source's repository spells a directory:
// lowercased, with nothing between the words. Not the same as Slug, which keeps
// the hyphen because a file name with words run together is unreadable -- and
// using one for the other is a 404 on every licence fetch, which is how the
// first version of this shipped a font with no licence beside it.
func repoDir(family string) string {
	return strings.ReplaceAll(Slug(family), "-", "")
}

func licenseFile(id string) string {
	switch id {
	case "apache2":
		return "LICENSE.txt"
	case "ufl":
		return "UFL.txt"
	default:
		return "OFL.txt"
	}
}

// get fetches a URL as the given client.
func get(client *http.Client, url, agent string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", agent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", url, resp.Status)
	}
	// Bounded. A font is measured in tens of kilobytes and the largest family
	// with every subset is under two megabytes; anything past this is not a font.
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}
