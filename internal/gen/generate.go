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

	// The conventional tree, directory by directory. Each file lands where
	// somebody would look for it, and the file name is CamelCase for the same
	// reason -- it is what makes the project recognizable.
	var out []File

	for _, t := range []struct {
		path string
		tmpl string
	}{
		{filepath.Join("app", "Http", "Controllers", m.Entity()+"Controller.go"), controllerTemplate + controllerSessionTemplate},
		{filepath.Join("app", "Models", m.Entity()+".go"), modelTemplate},
		{filepath.Join("app", "Policies", m.Entity()+"Policy.go"), policyTemplate},
		{filepath.Join("app", "Repositories", m.Entity()+"Repository.go"), repositoryTemplate},
		{filepath.Join("app", "Services", m.Entity()+"Service.go"), serviceTemplate},
		{filepath.Join("app", "Http", "Requests", m.Entity()+"Request.go"), requestTemplate + requestRulesTemplate},
		// The test goes to tests/Unit, not beside the controller.
		//
		// tests/Unit is for what is checked without booting, tests/Feature for
		// what boots the application and makes a request. What this one checks
		// -- that every repository method demands its Grant, and that the policy
		// denies an action nobody defined -- needs neither a server nor a
		// database.
		//
		// The name is <Entity>_test.go, not <Entity>Test.go, and that is not a
		// convention: a file that does not end in _test.go is compiled into the
		// package, so the "test" would ship in the binary and its Test functions
		// would never run.
		{filepath.Join("tests", "Unit", m.Entity()+"_test.go"), testTemplate},
		// The skill an assistant reads when it meets this module.
		//
		// It is generated with the rest rather than written afterwards, and that
		// is the point: a description of a module written by hand stops being
		// true at the next field. This one is rendered from the same
		// specification the Go was rendered from, so the two cannot disagree,
		// and regenerating the module regenerates what says what it is.
		//
		// .agents/skills is the path the coding assistants read from -- Cursor,
		// Codex, Cline, Copilot, Gemini CLI and the rest all look there -- so
		// the file is read by whatever the project is being written with, and
		// there is one directory rather than a file per vendor.
		{filepath.Join(".agents", "skills", m.Resource(), "SKILL.md"), skillTemplate},
	} {
		content, err := render(filepath.Base(t.path), t.tmpl, m)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t.path, err)
		}
		out = append(out, File{Path: t.path, Content: content})
	}

	// The migration goes through MigrationSpec, which is also what
	// `aru make:migration` and `aru make:model --migration` render: one shape of
	// migration file, whichever command asked for it.
	migration, err := RenderMigration(m.MigrationSpec())
	if err != nil {
		return nil, err
	}
	out = append(out, migration)

	// The views, under resources/views/<plural>/, one per action that has a
	// screen.
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

// ModelParts is what `aru make:model` was asked to write besides the model.
//
// Each field is one file, and --all is every field set rather than a mode of its
// own: the command that writes four things and the command that writes one are
// the same code path with a different struct, so a part cannot behave
// differently depending on how it was asked for.
type ModelParts struct{ Migration, Factory, Seeder, Policy, Request bool }

// Everything is ModelParts with every part set, which is what --all means.
//
// It is the data side of a module and not the module: `aru make:module` writes
// the controller, the service, the repository, the views and the route wiring
// as well, and this writes what belongs to the entity itself.
func Everything() ModelParts {
	return ModelParts{Migration: true, Factory: true, Seeder: true, Policy: true, Request: true}
}

// GenerateModel produces the model, and the parts the flags asked for.
//
// It renders the same templates Generate does: a model written by make:model and
// a model written by make:module are the same bytes, because they are the same
// file. A second template would be a second shape of one thing.
//
// What it never writes is a repository. A repository pulls a policy with it --
// `aru doctor` reports repository-without-policy as an Error -- and the generated
// policy denies everything, which pulls a service to issue the Grant. A
// --repository flag would be `aru make:module` with an arbitrary subset missing,
// and the mandatory path (validate, Authorize, Grant, Repository) is indivisible
// by construction.
func GenerateModel(m Module, parts ModelParts) ([]File, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}

	content, err := render(m.Entity()+".go", modelTemplate, m)
	if err != nil {
		return nil, err
	}
	out := []File{{Path: filepath.Join("app", "Models", m.Entity()+".go"), Content: content}}

	if parts.Migration {
		f, err := RenderMigration(m.MigrationSpec())
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if parts.Factory {
		fields := make([]FactoryField, 0, len(m.Fields))
		for _, f := range m.Fields {
			fields = append(fields, f.Factory())
		}
		f, err := RenderFactory(FactorySpec{
			Entity:       m.Entity(),
			Tenant:       m.Tenant,
			Fields:       fields,
			ModelsImport: m.ModelsImport(),
		})
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if parts.Seeder {
		f, err := RenderSeeder(SeederSpec{Entity: m.Entity()})
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}

	// The policy and the request are rendered from the same templates
	// `aru make:module` renders, for the reason the model is: a policy written
	// by one command and a policy written by the other would be two shapes of
	// one file, and the second one to change would be the one nobody noticed.
	for _, t := range []struct {
		want bool
		path string
		tmpl string
	}{
		{parts.Policy, filepath.Join("app", "Policies", m.Entity()+"Policy.go"), policyTemplate},
		{parts.Request, filepath.Join("app", "Http", "Requests", m.Entity()+"Request.go"), requestTemplate + requestRulesTemplate},
	} {
		if !t.want {
			continue
		}
		content, err := render(filepath.Base(t.path), t.tmpl, m)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t.path, err)
		}
		out = append(out, File{Path: t.path, Content: content})
	}
	return out, nil
}

// errModulePath is returned when the project module path is missing, which is
// the one input the generator cannot infer.
var errModulePath = fmt.Errorf("the project module path is required")

// render turns a template into a file.
//
// One function, not two. A second renderer without the FuncMap would leave
// `quote` -- the second lock on anything from a specification that lands inside
// a Go string literal -- defined on a function nobody calls, with the first lock
// in the spec validator carrying it alone.
//
// The data is `any` rather than Module because the granular commands --
// make:controller, make:middleware, make:request, make:migration, make:factory,
// make:seeder, make:enum -- render their own specifications through this exact
// function. One renderer means one gofmt pass, one set of template functions and
// one error message when a template does not parse.
func render(name, tmpl string, data any) ([]byte, error) {
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
	if err := t.Execute(&buf, data); err != nil {
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
// This is the escape hatch: whatever is outside the standard shape is written in
// Go, inside the markers, and survives regeneration. Without it the
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
