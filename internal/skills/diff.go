package skills

import (
	"fmt"
	"strings"
)

// context is how many unchanged lines a hunk shows around a change.
const context = 3

// Diff is the unified diff from before to after, with path in both headers,
// or empty when the two are the same. A file that does not exist yet is the
// empty before, and every line of it is an addition.
//
// It is a longest-common-subsequence diff over lines, which is quadratic in
// the length of the file and fine for a skill: a few hundred lines, compared
// once per file per run.
func Diff(path string, before, after []byte) string {
	a, b := lines(normalize(before)), lines(normalize(after))
	ops := script(a, b)

	var out strings.Builder
	from := "a/" + path
	if len(before) == 0 {
		from = "/dev/null"
	}
	header := false
	for start := 0; start < len(ops); {
		// Find the next change, and open a hunk context lines before it.
		first := start
		for first < len(ops) && ops[first].kind == ' ' {
			first++
		}
		if first == len(ops) {
			break
		}
		lo := max(first-context, start)
		// Extend the hunk while the next change is within two contexts.
		hi, gap := first, 0
		for i := first; i < len(ops); i++ {
			if ops[i].kind == ' ' {
				gap++
				if gap > 2*context {
					break
				}
				continue
			}
			gap = 0
			hi = i
		}
		end := min(hi+1+context, len(ops))

		if !header {
			fmt.Fprintf(&out, "--- %s\n+++ b/%s\n", from, path)
			header = true
		}
		aStart, aCount, bStart, bCount := ops[lo].a, 0, ops[lo].b, 0
		for _, op := range ops[lo:end] {
			if op.kind != '+' {
				aCount++
			}
			if op.kind != '-' {
				bCount++
			}
		}
		fmt.Fprintf(&out, "@@ -%s +%s @@\n", span(aStart, aCount), span(bStart, bCount))
		for _, op := range ops[lo:end] {
			out.WriteByte(op.kind)
			out.WriteString(op.text)
			out.WriteByte('\n')
		}
		start = end
	}
	return out.String()
}

// op is one line of an edit script: kept (' '), removed ('-') or added ('+'),
// with the zero-based line it starts at on each side.
type op struct {
	kind byte
	text string
	a, b int
}

// script is the shortest edit script from a to b.
func script(a, b []string) []op {
	// common[i][j] is the length of the longest common subsequence of a[i:]
	// and b[j:].
	common := make([][]int, len(a)+1)
	for i := range common {
		common[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				common[i][j] = common[i+1][j+1] + 1
			} else {
				common[i][j] = max(common[i+1][j], common[i][j+1])
			}
		}
	}

	var out []op
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			out = append(out, op{' ', a[i], i, j})
			i++
			j++
		// A removal goes before the addition that replaces it, as every
		// unified diff writes them.
		case i < len(a) && (j == len(b) || common[i+1][j] >= common[i][j+1]):
			out = append(out, op{'-', a[i], i, j})
			i++
		default:
			out = append(out, op{'+', b[j], i, j})
			j++
		}
	}
	return out
}

// span is a hunk range in the unified format: the one-based first line and
// the count, where an empty range names the line before it.
func span(start, count int) string {
	if count == 0 {
		return fmt.Sprintf("%d,0", start)
	}
	if count == 1 {
		return fmt.Sprintf("%d", start+1)
	}
	return fmt.Sprintf("%d,%d", start+1, count)
}

// lines splits text into lines without their line feeds. A final line feed
// ends the last line rather than opening an empty one.
func lines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}
