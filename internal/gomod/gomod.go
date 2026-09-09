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
	"strings"
)

// File is the part of a go.mod this server reads: what the tree is
// called, what it requires, and what it replaces.
type File struct {
	Path     string
	Versions map[string]string
	Replaced map[string]string
}

func Parse(source string) *File {
	module := &File{Versions: map[string]string{}, Replaced: map[string]string{}}
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
		if strings.HasPrefix(target, ".") || strings.HasPrefix(target, "/") || filepath.IsAbs(target) {
			m.Replaced[fields[0]] = target
		}
	}
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
