// Package gomod reads the part of a go.mod the rest of this CLI needs, and
// resolves a module to where it sits on disk.
//
// It exists because two callers need the same answers -- the language server,
// to find the package behind an import, and the project graph, to find the
// manifest of a module the project depends on -- and two parsers of the same
// file disagree the first time one of them learns about a replace directive.
//
// The parse is deliberately small. What is needed is a module path, a version
// per requirement and the local replacements, and a full go.mod reader would be
// a dependency this module does not take.
package gomod

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// File is the part of a go.mod this server reads: what the tree is
// called, what it requires, and what it replaces.
type File struct {
	Path string
	// Go is the version the go directive names, or empty without one.
	Go       string
	Versions map[string]string
	// Replaced maps a module to the directory that replaces it.
	Replaced map[string]string
	// ReplacedVersions maps a module to the version of the module that
	// replaces it, for a replace whose target is not a directory.
	ReplacedVersions map[string]string
}

// Parse reads the module line, the requirements and the replacements of a
// go.mod.
func Parse(source string) *File {
	module := &File{Versions: map[string]string{}, Replaced: map[string]string{}, ReplacedVersions: map[string]string{}}
	block := ""
	for _, line := range strings.Split(source, "\n") {
		if at := strings.Index(line, "//"); at >= 0 {
			line = line[:at]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == ")" {
			block = ""
			continue
		}
		if block == "" {
			keyword, rest, found := strings.Cut(line, " ")
			if !found {
				continue
			}
			rest = strings.TrimSpace(rest)
			if rest == "(" {
				block = keyword
				continue
			}
			module.record(keyword, rest)
			continue
		}
		module.record(block, line)
	}
	return module
}

func (m *File) record(keyword, rest string) {
	fields := strings.Fields(rest)
	switch keyword {
	case "module":
		if len(fields) >= 1 {
			m.Path = fields[0]
		}
	case "go":
		if len(fields) >= 1 {
			m.Go = fields[0]
		}
	case "require":
		if len(fields) >= 2 {
			m.Versions[fields[0]] = fields[1]
		}
	case "replace":
		// `old => new` and `old v1 => new v2` both end with the replacement,
		// and only a replacement that is a directory is usable here: a module
		// swapped for another module is still found through the cache.
		at := -1
		for i, field := range fields {
			if field == "=>" {
				at = i
			}
		}
		if at < 0 || at+1 >= len(fields) || len(fields) == 0 {
			return
		}
		target := fields[at+1]
		switch {
		case strings.HasPrefix(target, ".") || strings.HasPrefix(target, "/") || filepath.IsAbs(target):
			m.Replaced[fields[0]] = target
		case at+2 < len(fields):
			m.ReplacedVersions[fields[0]] = fields[at+2]
		}
	}
}

// Pinned answers the version the build resolves module to, after a replace
// that names a version. A module replaced by a directory, or not required,
// has no version, and answers false.
func (m *File) Pinned(module string) (string, bool) {
	if _, local := m.Replaced[module]; local {
		return "", false
	}
	if v, ok := m.ReplacedVersions[module]; ok {
		return v, true
	}
	v, ok := m.Versions[module]
	return v, ok
}

// Less reports whether version a sorts before b: by the three numbers, then a
// pre-release -- which a pseudo-version is -- before the release it precedes.
//
// A version that does not parse is not less than anything, so a caller that
// refuses below a floor does not refuse for a string it could not read.
func Less(a, b string) bool {
	x, okA := parseVersion(a)
	y, okB := parseVersion(b)
	if !okA || !okB {
		return false
	}
	for i := range x.nums {
		if x.nums[i] != y.nums[i] {
			return x.nums[i] < y.nums[i]
		}
	}
	switch {
	case x.pre == y.pre, x.pre == "":
		return false
	case y.pre == "":
		return true
	}
	return x.pre < y.pre
}

// IsVersion reports whether v is a semantic version Less can order.
func IsVersion(v string) bool {
	_, ok := parseVersion(v)
	return ok
}

type version struct {
	nums [3]int
	pre  string
}

func parseVersion(v string) (version, bool) {
	v, ok := strings.CutPrefix(v, "v")
	if !ok {
		return version{}, false
	}
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	var out version
	core := v
	if i := strings.IndexByte(v, '-'); i >= 0 {
		core, out.pre = v[:i], v[i+1:]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" {
			return version{}, false
		}
		out.nums[i] = n
	}
	return out, true
}

// Under reports whether the import path is the prefix or lies inside
// it, and returns what is left over.
//
// Comparing on the slash boundary is what keeps `example.com/kyseless` from
// resolving through a requirement on `example.com/kyse`.
func Under(importPath, prefix string) (string, bool) {
	if importPath == prefix {
		return "", true
	}
	if rest, found := strings.CutPrefix(importPath, prefix+"/"); found {
		return rest, true
	}
	return "", false
}

// Dir answers the directory that holds module's source for the tree rooted at
// root, looked for in the order the toolchain resolves it: a replace naming a
// directory, the tree's vendor directory, then the module cache at the version
// the build selects. It answers false when the module is not required, or is
// required and on none of them -- not yet downloaded, typically.
//
// It reads the disk and never starts the toolchain, so a caller that may run
// `go mod download` does that itself and asks again.
func (m *File) Dir(root, module string) (string, bool) {
	if target, local := m.Replaced[module]; local {
		dir := target
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(root, filepath.FromSlash(dir))
		}
		return dir, isDir(dir)
	}
	if dir := filepath.Join(root, "vendor", filepath.FromSlash(module)); isDir(dir) {
		return dir, true
	}
	version, required := m.Pinned(module)
	cache := Cache()
	if !required || cache == "" {
		return "", false
	}
	dir := filepath.Join(cache, filepath.FromSlash(EscapePath(module)+"@"+version))
	return dir, isDir(dir)
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// Cache answers the module cache directory from the environment, the way the
// toolchain does when nothing was written with `go env -w`: GOMODCACHE, then
// pkg/mod under the first GOPATH entry, then under the default GOPATH.
func Cache() string {
	if cache := os.Getenv("GOMODCACHE"); cache != "" {
		return cache
	}
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		return filepath.Join(strings.Split(gopath, string(os.PathListSeparator))[0], "pkg", "mod")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "go", "pkg", "mod")
}

// EscapePath is the module cache's spelling of a path: an upper-case
// letter becomes an exclamation mark and its lower-case form.
//
// The cache has to name modules on a filesystem that does not distinguish case,
// so `Sirupsen` and `sirupsen` would be one directory without it.
func EscapePath(importPath string) string {
	var out strings.Builder
	for _, r := range importPath {
		if r >= 'A' && r <= 'Z' {
			out.WriteByte('!')
			out.WriteRune(r - 'A' + 'a')
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}
