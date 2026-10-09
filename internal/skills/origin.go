package skills

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/arandu-io/aru/internal/gomod"
)

// SkeletonModule is the module `aru new` creates a project from, and the first
// origin of a project's skills.
const SkeletonModule = "github.com/arandu-io/arandu"

// SkeletonVersion is the skeleton release this build creates projects from
// and keeps their skills in step with.
//
// It is pinned rather than read from the newest tag: a project created today
// has to be the one this build's generators and checks were written against.
const SkeletonVersion = "v0.34.1"

// ModuleOwner is the path prefix of the modules whose skills an application
// receives when its go.mod requires them.
const ModuleOwner = "github.com/hyz-is/"

// Origin is one module, at one version, that a project receives skills from.
type Origin struct {
	// Module is the module path.
	Module string
	// Version is the version the project pins it at.
	Version string
	// Replaced is the directory go.mod replaces the module with, made absolute,
	// or empty when it is not replaced by a directory.
	Replaced string
}

// Label is how a stamped skill names the origin: the module path without its
// host, then the version.
func (o Origin) Label() string {
	return o.repository() + "@" + o.Version
}

// Names reports whether a source written in a skill's header is this origin's,
// at any version.
func (o Origin) Names(source string) bool {
	repository, _, found := strings.Cut(source, "@")
	return found && repository == o.repository()
}

func (o Origin) repository() string {
	return strings.TrimPrefix(o.Module, "github.com/")
}

// Distributes reports whether the skill named name, with header h, is one the
// origin hands to an application.
//
// The skeleton hands over its arandu- skills: they are the framework's
// procedures. A skill there named after something else describes the
// skeleton's example resource, which a project deletes with the resource, and
// handing it back would undo the deletion. A module hands over what it marks
// `audience: app` under metadata: its other skills are for whoever maintains
// the module, and an application that received the module's release procedure
// would follow it.
func (o Origin) Distributes(name string, h Header) bool {
	if o.Module == SkeletonModule {
		return strings.HasPrefix(name, "arandu-")
	}
	return h.Audience == "app"
}

// Skeleton is the skeleton at the version this build pins.
func Skeleton() Origin {
	return Origin{Module: SkeletonModule, Version: SkeletonVersion}
}

// Origins answers where the project rooted at root receives skills from: the
// skeleton first, then every module under ModuleOwner its go.mod requires, by
// path.
func Origins(root string) ([]Origin, error) {
	body, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}
	mod := gomod.Parse(string(body))

	out := []Origin{Skeleton()}
	var modules []string
	for module := range mod.Versions {
		if strings.HasPrefix(module, ModuleOwner) {
			modules = append(modules, module)
		}
	}
	sort.Strings(modules)
	for _, module := range modules {
		o := Origin{Module: module, Version: mod.Versions[module]}
		if pinned, ok := mod.Pinned(module); ok {
			o.Version = pinned
		}
		if target, replaced := mod.Replaced[module]; replaced {
			if !filepath.IsAbs(target) {
				target = filepath.Join(root, filepath.FromSlash(target))
			}
			o.Replaced = target
		}
		out = append(out, o)
	}
	return out, nil
}

// Dir answers the directory holding the origin's source, looked for without
// starting anything: the directory go.mod replaces it with, then the module
// cache at the pinned version. It answers false when neither is on disk.
//
// A vendor directory is not looked in. The go command vendors packages, and a
// skill is not part of one.
func (o Origin) Dir() (string, bool) {
	if o.Replaced != "" {
		return o.Replaced, isDir(o.Replaced)
	}
	cache := gomod.Cache()
	if cache == "" {
		return "", false
	}
	dir := filepath.Join(cache, filepath.FromSlash(gomod.EscapePath(o.Module)+"@"+o.Version))
	return dir, isDir(dir)
}

// Skill is one SKILL.md.
type Skill struct {
	// Name is the directory that holds it.
	Name    string
	Content []byte
}

// Path is where the skill sits in a project, slash-separated and relative to
// the root.
func (s Skill) Path() string {
	return Dir + "/" + s.Name + "/" + File
}

// Read answers the skills the origin hands to an application, from its source
// rooted at dir, ordered by name. A source with no skills directory hands over
// nothing.
func (o Origin) Read(dir string) ([]Skill, error) {
	entries, err := os.ReadDir(filepath.Join(dir, filepath.FromSlash(Dir)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(Dir), entry.Name(), File))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", o.Label(), err)
		}
		if o.Distributes(entry.Name(), ParseHeader(content)) {
			out = append(out, Skill{Name: entry.Name(), Content: content})
		}
	}
	return out, nil
}

// Local answers the skills the project rooted at root carries, by name.
func Local(root string) (map[string][]byte, error) {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(Dir)))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string][]byte{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(Dir), entry.Name(), File))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[entry.Name()] = content
	}
	return out, nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
