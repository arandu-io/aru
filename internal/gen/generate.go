package gen

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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

	// The Laravel tree, directory by directory. A developer arriving from there
	// finds each file where they expect it, and the file name is CamelCase for
	// the same reason -- it is what makes the project recognizable (ADR 0019).
	var out []File

	for _, t := range []struct {
		path string
		tmpl string
	}{
		{filepath.Join("app", "Http", "Controllers", m.Entity()+"Controller.go"), controllerTemplate},
		{filepath.Join("app", "Models", m.Entity()+".go"), modelTemplate},
		{filepath.Join("app", "Policies", m.Entity()+"Policy.go"), policyTemplate},
		{filepath.Join("app", "Repositories", m.Entity()+"Repository.go"), repositoryTemplate},
		{filepath.Join("app", "Services", m.Entity()+"Service.go"), serviceTemplate},
		{filepath.Join("app", "Http", "Requests", m.Entity()+"Request.go"), requestTemplate},
		{filepath.Join("database", "migrations", m.MigrationID()+".go"), migrationTemplate},
		{filepath.Join("app", "Http", "Controllers", m.Entity()+"Controller_test.go"), testTemplate},
	} {
		content, err := render(filepath.Base(t.path), t.tmpl, m)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t.path, err)
		}
		out = append(out, File{Path: t.path, Content: content})
	}

	// The views. Laravel puts them under resources/views/<plural>/, one per
	// action that has a screen.
	views := filepath.Join("resources", "views", m.Resource())
	for _, v := range []struct {
		name string
		tmpl string
	}{
		{"index", viewIndexTemplate},
		{"show", viewShowTemplate},
		// The two forms share their fields, and share them at generation time
		// rather than through @include: the create screen and the edit screen
		// take different data, and an included view receives the page's data
		// unchanged -- so one partial would assert one type and fail on the other.
		{"create", viewCreateTemplate + viewFieldsTemplate},
		{"edit", viewEditTemplate + viewFieldsTemplate},
	} {
		content, err := render(v.name+".kyse.go", v.tmpl, m)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", v.name, err)
		}
		out = append(out, File{Path: filepath.Join(views, v.name+".kyse.go"), Content: content})
	}

	return out, nil
}

// errModulePath is returned when the project module path is missing, which is
// the one input the generator cannot infer.
var errModulePath = fmt.Errorf("the project module path is required")

// render turns a template into a file.
//
// One function, not two. There used to be a render with the FuncMap and a
// renderRaw without it, and every caller used renderRaw -- so `quote`, the
// second lock on anything from a specification that lands inside a Go string
// literal, was defined on a function nobody called. Found by audit; the first
// lock in the spec validator was carrying it alone.
func render(name, tmpl string, m Module) ([]byte, error) {
	t := template.New(name).Funcs(template.FuncMap{
		"lower": strings.ToLower,
		"join":  strings.Join,
		// quote is the second lock. The spec validator is the first, and it
		// says why; this one holds even if a future field forgets to validate.
		"quote": strconv.Quote,
	})

	// A view template is rendered with <% %> instead of {{ }}.
	//
	// The view it produces is kyse, and kyse interpolates with {{ }} -- the same
	// delimiters text/template uses. Without the swap, `{{ .Name }}` in the
	// generated view is read as an action of the generator, and generation fails
	// on markup that is correct.
	view := strings.HasSuffix(name, ".kyse.go")
	if view {
		t = t.Delims("<%", "%>")
	}

	t, err := t.Parse(tmpl)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, m); err != nil {
		return nil, err
	}

	// Only real Go is formatted. A .kyse.go is a view: it ends in .go so the
	// build tag can exclude it, and everything below the package clause is
	// markup that gofmt would refuse.
	if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, ".kyse.go") {
		return buf.Bytes(), nil
	}

	// gofmt the output rather than trusting the template's indentation. A
	// generator that emits unformatted Go makes every project fail its own CI on
	// the first run.
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("%s does not parse -- this is a bug in the template: %w\n%s", name, err, numbered(buf.String()))
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
