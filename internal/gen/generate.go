package gen

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"
)

// File is one generated file.
type File struct {
	Path    string // relative to the project root
	Content []byte
}

// Generate produces every file of the module.
//
// It writes nothing: it returns the files, so the caller can show a diff, refuse
// to overwrite, or write them. A generator that writes as it goes cannot be
// tested against golden files, and cannot be run twice safely.
func Generate(m Module) ([]File, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}

	dir := filepath.Join("modules", m.Package())
	var out []File

	for _, t := range []struct {
		name string
		tmpl string
	}{
		{"module.go", moduleTemplate},
		{m.Name + ".entity.go", entityTemplate},
		{m.Name + ".policy.go", policyTemplate},
		{m.Name + ".repo.go", repoTemplate},
		{m.Name + ".service.go", serviceTemplate},
		{m.Name + ".request.go", requestTemplate},
		{"handlers.go", handlersTemplate},
		{m.Name + "_test.go", testTemplate},
	} {
		content, err := render(t.name, t.tmpl, m)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t.name, err)
		}
		out = append(out, File{Path: filepath.Join(dir, t.name), Content: content})
	}
	return out, nil
}

// errModulePath is returned when the project module path is missing, which is
// the one input the generator cannot infer.
var errModulePath = fmt.Errorf("the project module path is required")

// renderRaw renders without gofmt, for files that are not Go: a .templ has to
// reach templ as written.
func renderRaw(name, tmpl string, m Module) ([]byte, error) {
	t, err := template.New(name).Parse(tmpl)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, m); err != nil {
		return nil, err
	}
	if !strings.HasSuffix(name, ".go") {
		return buf.Bytes(), nil
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("%s does not parse -- bug in the template: %w", name, err)
	}
	return formatted, nil
}

func render(name, tmpl string, m Module) ([]byte, error) {
	t, err := template.New(name).Funcs(template.FuncMap{
		"lower": strings.ToLower,
		"join":  strings.Join,
	}).Parse(tmpl)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, m); err != nil {
		return nil, err
	}

	// gofmt the output rather than trusting the template's indentation. A
	// generator that emits unformatted Go makes every project fail its own CI on
	// the first run.
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("the generated code does not parse -- this is a bug in the template: %w\n%s", err, numbered(buf.String()))
	}
	return formatted, nil
}

// customBlock matches the region a regeneration must preserve.
//
// This is the escape hatch of doc 19: whatever is outside the standard shape is
// written in Go, inside the markers, and survives regeneration. Without it the
// generator is a one-time tool -- nobody regenerates a file that eats their work.
var customBlock = regexp.MustCompile(`(?s)// arandu:begin custom\n(.*?)// arandu:end custom`)

// Merge carries the custom blocks of the existing file into the newly generated
// one, in order.
//
// Blocks are matched by position, not by name, which is the honest limitation:
// reordering the generated file would shuffle them. That is why the marker
// appears once per file, at the end, where new blocks are appended rather than
// inserted.
func Merge(existing, generated []byte) []byte {
	old := customBlock.FindAllSubmatch(existing, -1)
	if len(old) == 0 {
		return generated
	}

	i := 0
	return customBlock.ReplaceAllFunc(generated, func(match []byte) []byte {
		if i >= len(old) {
			return match
		}
		body := old[i][1]
		i++
		return []byte("// arandu:begin custom\n" + string(body) + "// arandu:end custom")
	})
}

// Write writes the files, preserving custom blocks in the ones that already
// exist. It returns what it wrote and what it skipped.
func Write(root string, files []File, force bool) (written, skipped []string, err error) {
	for _, f := range files {
		path := filepath.Join(root, f.Path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return written, skipped, err
		}

		content := f.Content
		if existing, readErr := os.ReadFile(path); readErr == nil {
			if !force {
				skipped = append(skipped, f.Path)
				continue
			}
			content = Merge(existing, f.Content)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return written, skipped, err
		}
		written = append(written, f.Path)
	}
	sort.Strings(written)
	sort.Strings(skipped)
	return written, skipped, nil
}

func numbered(s string) string {
	var b strings.Builder
	for i, line := range strings.Split(s, "\n") {
		fmt.Fprintf(&b, "%4d| %s\n", i+1, line)
	}
	return b.String()
}
