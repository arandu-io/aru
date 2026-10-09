package lsp

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/arandu-io/aru/internal/doctor"
	"github.com/arandu-io/aru/internal/kyse"
)

// Options is what the command that starts the server knows and this package
// cannot: the catalogue of the command line it is part of.
type Options struct {
	// Commands is the command line's catalogue, in the order its help lists
	// it. The server answers it to arandu/catalog and keeps no list of its own.
	Commands []Command
}

// Command is one entry of the command line's catalogue.
type Command struct {
	Name        string   `json:"name"`
	Usage       string   `json:"usage"`
	Description string   `json:"description"`
	Flags       []string `json:"flags"`
}

// projectGraphParams is what a client may say when it asks for the map.
//
// A client that says nothing, or sends no object at all, is a client built
// against the first schema and receives exactly that.
type projectGraphParams struct {
	SchemaVersion *int `json:"schemaVersion"`
}

// requestedSchema reads the schema a client asked for: 1 for anything that is
// not an object naming one, and an error for an object naming one this server
// does not answer.
func requestedSchema(raw json.RawMessage) (int, error) {
	var params projectGraphParams
	if !decodeObjectParams(raw, &params) || params.SchemaVersion == nil {
		return 1, nil
	}
	switch *params.SchemaVersion {
	case 1, doctor.MapSchemaVersion:
		return *params.SchemaVersion, nil
	}
	return 0, fmt.Errorf("unsupported schemaVersion %d, want 1 or %d", *params.SchemaVersion, doctor.MapSchemaVersion)
}

// analysisCache is the last analysis of the tree and what the tree looked
// like when it was made.
type analysisCache struct {
	stamp    string
	analysis doctor.Analysis
}

// analysis answers the analysis of the tree with the second schema of the map,
// against the profile the project declares, reusing the last one while no file
// the analysis reads has changed.
//
// It reads the disk and never a buffer an editor holds: what the doctor
// reports is about the files a build would compile, and an unsaved edit is
// not one of them yet.
func (p *project) analysis() (doctor.Analysis, error) {
	stamp := p.treeStamp()
	if p.analyzed != nil && p.analyzed.stamp == stamp {
		return p.analyzed.analysis, nil
	}
	profile, err := doctor.DeclaredProfile(p.root)
	if err != nil {
		return doctor.Analysis{}, err
	}
	analysis, err := doctor.AnalyzeWithMap(p.root, profile)
	if err != nil {
		return doctor.Analysis{}, err
	}
	p.analyzed = &analysisCache{stamp: stamp, analysis: analysis}
	return analysis, nil
}

// treeStamp is the name, size and time of every file an analysis reads, in
// one string: two equal stamps are two reads of the same tree.
func (p *project) treeStamp() string {
	var entries []string
	_ = filepath.WalkDir(p.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", "testdata", "bin":
				if path != p.root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") && name != "arandu.mod.toml" && name != "go.mod" &&
			name != ".env.example" && name != "SKILL.md" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		entries = append(entries, path+":"+strconv.FormatInt(info.Size(), 10)+":"+strconv.FormatInt(info.ModTime().UnixNano(), 10))
		return nil
	})
	sort.Strings(entries)
	return strings.Join(entries, "\n")
}

// sourceLines reads the lines of a project file once per conversion.
type sourceLines struct {
	root  string
	files map[string][]string
}

func newSourceLines(root string) *sourceLines {
	return &sourceLines{root: root, files: map[string][]string{}}
}

func (s *sourceLines) line(rel string, line int) string {
	lines, read := s.files[rel]
	if !read {
		if body, err := os.ReadFile(s.absolute(rel)); err == nil {
			lines = strings.Split(string(body), "\n")
		}
		s.files[rel] = lines
	}
	if line < 1 || line > len(lines) {
		return ""
	}
	return strings.TrimSuffix(lines[line-1], "\r")
}

func (s *sourceLines) absolute(rel string) string {
	path := filepath.FromSlash(rel)
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(s.root, path)
}

// position turns a one-based line and byte column into the protocol's
// zero-based line and UTF-16 character.
func (s *sourceLines) position(rel string, line, column int) position {
	text := s.line(rel, line)
	character := max(column-1, 0)
	if character > len(text) {
		character = len(text)
	}
	return position{Line: max(line-1, 0), Character: utf16Length(text[:character])}
}

func (s *sourceLines) uri(rel string) (string, error) {
	return fileURIFromPath(s.absolute(rel), nativeFilePathStyle())
}

// mapForProtocol answers the second schema in the protocol's terms: every
// file a URI, every position zero-based and counted in UTF-16, and a rule's
// documentation inside the project a URI with its line fragment.
func mapForProtocol(root string, m doctor.ProjectMap) (doctor.ProjectMap, error) {
	lines := newSourceLines(root)
	out := m
	out.Nodes = make([]doctor.MapNode, len(m.Nodes))
	for i, node := range m.Nodes {
		if node.File != "" {
			start := lines.position(node.File, node.Line, node.Column)
			end := lines.position(node.File, node.EndLine, node.EndColumn)
			uri, err := lines.uri(node.File)
			if err != nil {
				return doctor.ProjectMap{}, err
			}
			node.File = uri
			node.Line, node.Column, node.EndLine, node.EndColumn = start.Line, start.Character, end.Line, end.Character
		}
		if node.RuleDoc != "" && !strings.Contains(node.RuleDoc, "://") {
			rel, fragment, _ := strings.Cut(node.RuleDoc, "#")
			uri, err := lines.uri(rel)
			if err != nil {
				return doctor.ProjectMap{}, err
			}
			if fragment != "" {
				uri += "#" + fragment
			}
			node.RuleDoc = uri
		}
		out.Nodes[i] = node
	}
	out.Edges = make([]doctor.MapEdge, len(m.Edges))
	for i, edge := range m.Edges {
		if edge.At != nil {
			at := *edge.At
			start := lines.position(at.File, at.Line, at.Column)
			end := lines.position(at.File, at.EndLine, at.EndColumn)
			uri, err := lines.uri(at.File)
			if err != nil {
				return doctor.ProjectMap{}, err
			}
			edge.At = &doctor.MapLocation{
				File: uri, Line: start.Line, Column: start.Character, EndLine: end.Line, EndColumn: end.Character,
			}
		}
		out.Edges[i] = edge
	}
	return out, nil
}

// catalogResult is the answer to arandu/catalog.
type catalogResult struct {
	Directives []catalogDirective `json:"directives"`
	Commands   []Command          `json:"commands"`
}

// catalogDirective is one directive the view compiler knows. Kind is block
// for one that opens a block, end for the one that closes it, and inline for
// the rest.
type catalogDirective struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	ClosedBy string `json:"closedBy,omitempty"`
	Closes   string `json:"closes,omitempty"`
}

// catalog answers the directives the compiler of this binary knows and the
// commands of the command line it was started by.
func catalog(options Options) catalogResult {
	ends := kyse.BlockEnds()
	opens := map[string]string{}
	for open, end := range ends {
		opens[end] = open
	}
	names := kyse.Directives()
	directives := make([]catalogDirective, 0, len(names))
	for _, name := range names {
		entry := catalogDirective{Name: name, Kind: "inline"}
		if end, block := ends[name]; block {
			entry.Kind, entry.ClosedBy = "block", end
		} else if open, closes := opens[name]; closes {
			entry.Kind, entry.Closes = "end", open
		}
		directives = append(directives, entry)
	}
	commands := make([]Command, len(options.Commands))
	for i, command := range options.Commands {
		command.Flags = append([]string{}, command.Flags...)
		commands[i] = command
	}
	return catalogResult{Directives: directives, Commands: commands}
}
