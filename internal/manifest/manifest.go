// Package manifest reads arandu.mod.toml, the metadata every module declares.
//
// It exists from phase 2 rather than from the phase that needs it, and the
// reason is narrow: a module published without the file cannot gain it later
// without breaking whoever already installed it. The registry that consumes this
// is phase 4; the contract cannot wait for it.
//
// What the file declares is capability, not intent: a module that says it makes
// no outbound network calls and then imports an HTTP client is rejected by
// `aru doctor`. That check is what makes the declaration worth reading.
package manifest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Name is the file every module carries.
const Name = "arandu.mod.toml"

// Module is what a module declares about itself.
type Module struct {
	// Name identifies the module for the registry: "acme/crm".
	Name string
	// Framework is the version range this module supports: ">= 0.3".
	Framework string
	// Profiles lists the deployment profiles it works on: conventional,
	// performance. See doc 12.
	Profiles []string
	// Permissions is what it does beyond compute and its own database rows.
	Permissions Permissions
	// Path is where the file was read from, for reporting.
	Path string
}

// Permissions is the capability declaration.
//
// Four booleans rather than a free-form list, because a capability model people
// extend is a capability model nobody can audit. Anything outside these four is
// ordinary Go and needs no declaration.
type Permissions struct {
	// Network is true when the module makes outbound calls: an HTTP client, a
	// mail server, anything that leaves the process.
	Network bool
	// Filesystem is true when it reads or writes files outside the database.
	Filesystem bool
	// Exec is true when it runs another program.
	Exec bool
	// Migrations is true when it owns tables.
	Migrations bool
}

// Read loads the manifest of one module directory.
//
// A missing file is not an error here: the caller decides whether the absence
// matters, and `aru doctor` reports it as a warning rather than refusing to run.
func Read(dir string) (*Module, error) {
	path := filepath.Join(dir, Name)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	m := &Module{Path: path}
	var section string
	for i, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.Trim(line, "[]")
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected key = value", path, i+1)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		switch section {
		case "":
			switch key {
			case "name":
				m.Name = unquote(value)
			case "framework":
				m.Framework = unquote(value)
			case "profiles":
				m.Profiles = list(value)
			default:
				return nil, fmt.Errorf("%s:%d: unknown key %q", path, i+1, key)
			}
		case "permissions":
			flag, err := boolean(value)
			if err != nil {
				return nil, fmt.Errorf("%s:%d: %s must be true or false", path, i+1, key)
			}
			switch key {
			case "network":
				m.Permissions.Network = flag
			case "filesystem":
				m.Permissions.Filesystem = flag
			case "exec":
				m.Permissions.Exec = flag
			case "migrations":
				m.Permissions.Migrations = flag
			default:
				// A misspelled permission that is ignored reads as declared and
				// is not, which is the failure this file exists to prevent.
				return nil, fmt.Errorf("%s:%d: unknown permission %q, want network, filesystem, exec or migrations", path, i+1, key)
			}
		default:
			return nil, fmt.Errorf("%s:%d: unknown section [%s]", path, i+1, section)
		}
	}

	if m.Name == "" {
		return nil, fmt.Errorf("%s: name is required", path)
	}
	return m, nil
}

// ReadAll loads the manifest of every module under root/modules.
//
// The key is the module directory name, which is how the doctor's findings refer
// to a module everywhere else.
func ReadAll(root string) (map[string]*Module, error) {
	out := map[string]*Module{}

	entries, err := os.ReadDir(filepath.Join(root, "modules"))
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := Read(filepath.Join(root, "modules", e.Name()))
		if err != nil {
			return nil, err
		}
		if m != nil {
			out[e.Name()] = m
		}
	}
	return out, nil
}

func unquote(v string) string {
	return strings.Trim(v, `"`)
}

func list(v string) []string {
	v = strings.Trim(strings.TrimSpace(v), "[]")
	var out []string
	for _, item := range strings.Split(v, ",") {
		if item = unquote(strings.TrimSpace(item)); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func boolean(v string) (bool, error) {
	switch strings.TrimSpace(v) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, fmt.Errorf("not a boolean")
}
