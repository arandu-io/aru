package gen

import (
	"bytes"
	"errors"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/arandu-io/aru/internal/skills"
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
		{filepath.Join("app", "Http", "Controllers", m.Entity()+"Controller.go"), controllerTemplate},
		{filepath.Join("app", "Models", m.Entity()+".go"), modelTemplate},
		{filepath.Join("app", "Policies", m.Entity()+"Policy.go"), policyTemplate},
		{filepath.Join("app", "Services", m.Entity()+"Service.go"), serviceTemplate + serviceBlocks},
		{filepath.Join("app", "Http", "Requests", m.Entity()+"Request.go"), requestTemplate + requestRulesTemplate},
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
		// The skill records where it came from the way every other skill in
		// a project does, so `aru skills:sync` and the doctor can tell it from
		// one a module handed over and leave it to the generator.
		if filepath.Base(t.path) == skills.File {
			if content, err = skills.Stamp(content, m.SkillSource()); err != nil {
				return nil, fmt.Errorf("%s: %w", t.path, err)
			}
		}
		out = append(out, File{Path: t.path, Content: content})
	}

	// The query file beside the entity is what `aru model:build` writes, from
	// the same renderer, so a module is complete -- and compiles -- before the
	// first build runs.
	query, err := renderModelQuery(m)
	if err != nil {
		return nil, err
	}
	out = append(out, query)

	// The migration goes through MigrationSpec, which is also what
	// `aru make:migration` and `aru make:model --migration` render: one shape of
	// migration file, whichever command asked for it.
	migration, err := RenderMigration(m.MigrationSpec())
	if err != nil {
		return nil, err
	}
	out = append(out, migration)

	// The test goes through RenderTest, which is what `aru make:test` renders
	// through: one shape of test file, whichever command asked for it.
	unit, err := RenderTest(m)
	if err != nil {
		return nil, err
	}
	out = append(out, unit)

	// The factory and the seeder, from the renderers `aru make:model --factory
	// --seed` uses, so the rows a module seeds are built the way the rows of an
	// entity written on its own are. The seeder goes through the factory: the
	// module writes it, and the policy whose actions the seeder names.
	factory, err := RenderFactory(m.FactorySpec())
	if err != nil {
		return nil, err
	}
	seeder, err := RenderSeeder(m.SeederSpec())
	if err != nil {
		return nil, err
	}
	out = append(out, factory, seeder)

	// A tenant-scoped table gets the feature test that proves it: rows stored
	// for one tenant, read with another's Grant, and nothing found. It is the
	// per-table half of the skeleton's catalogue check, which asks for the
	// claim this test is the evidence of.
	if m.Tenant {
		path := filepath.Join("tests", "Feature", m.Entity()+"TenantScope_test.go")
		content, err := render(filepath.Base(path), tenantScopeTestTemplate, m)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, File{Path: path, Content: content})
	}

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

// RenderTest produces the module's unit test.
//
// It takes the Module rather than a specification of its own, because the file
// is written entirely out of the module's names: the Model boundary it proves,
// the Service it constructs and the Policy it exercises. A struct carrying
// those again would be the same names with a second place to spell them
// differently. No field is read, so a caller holding the name and the project
// module path holds everything this needs.
//
// The file lands in tests/Unit rather than beside the controller. tests/Unit is
// for what is checked without booting and tests/Feature for what boots the
// application and makes a request. What this one checks -- that the service
// asks the Policy before it reaches the model, and that the policy denies an
// action nobody wrote a rule for -- needs neither a server nor a database.
//
// The name is <Entity>_test.go and not <Entity>Test.go. A file whose name does
// not end in _test.go is compiled into its package, so the test would ship
// inside the binary and its Test functions would run nowhere: no error, no
// warning, and a green build over a suite that was switched off.
func RenderTest(m Module) (File, error) {
	path := filepath.Join("tests", "Unit", m.Entity()+"_test.go")
	content, err := render(filepath.Base(path), testTemplate, m)
	if err != nil {
		return File{}, fmt.Errorf("%s: %w", path, err)
	}
	return File{Path: path, Content: content}, nil
}

// ModelParts is what `aru make:model` was asked to write besides the model.
//
// Each field is one file, and --all is every field set rather than a mode of its
// own: the command that writes four things and the command that writes one are
// the same code path with a different struct, so a part cannot behave
// differently depending on how it was asked for.
//
// Service has no flag of its own. A service needs the policy and the request
// it joins, so it is written only when both are -- which is what --all asks
// for -- and the controller is then built with it.
type ModelParts struct{ Migration, Factory, Seeder, Policy, Request, Controller, Service bool }

// Everything is ModelParts with every part set, which is what --all means.
//
// It is the entity and the path to it, and not the module: `aru make:module`
// writes the screens, the actions behind them and the module's own skill as
// well. The controller here is the resource controller whose seven actions
// answer 501 until they are written, built with the service.
func Everything() ModelParts {
	return ModelParts{Migration: true, Factory: true, Seeder: true, Policy: true, Request: true, Controller: true, Service: true}
}

// GenerateModel produces the model, and the parts the flags asked for.
//
// It renders the same templates Generate does: a model written by make:model and
// a model written by make:module are the same bytes, because they are the same
// file. A second template would be a second shape of one thing.
//
// What it never writes is a service. A model generated on its own is the data
// shape and its query entry point; the use-case path that validates, authorizes
// and spends the Grant belongs to `aru make:module`.
func GenerateModel(m Module, parts ModelParts) ([]File, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}

	content, err := render(m.Entity()+".go", modelTemplate, m)
	if err != nil {
		return nil, err
	}
	out := []File{{Path: filepath.Join("app", "Models", m.Entity()+".go"), Content: content}}
	query, err := renderModelQuery(m)
	if err != nil {
		return nil, err
	}
	out = append(out, query)

	if parts.Migration {
		f, err := RenderMigration(m.MigrationSpec())
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if parts.Factory {
		f, err := RenderFactory(m.FactorySpec())
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if parts.Seeder {
		// The seeder goes through the factory when this call also writes the
		// factory and the policy whose action it names; otherwise it is the
		// empty seeder, which compiles without either.
		f, err := RenderSeeder(SeederSpec{Entity: m.Entity(), ModulePath: m.ModulePath, Factory: parts.Factory && parts.Policy})
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}

	// The policy and the request are rendered from the same templates
	// `aru make:module` renders, for the reason the model is: a policy written
	// by one command and a policy written by the other would be two shapes of
	// one file, and the second one to change would be the one nobody noticed.
	if parts.Service && !(parts.Policy && parts.Request) {
		return nil, fmt.Errorf("a service joins the policy and the request, so it is written with both")
	}
	for _, t := range []struct {
		want bool
		path string
		tmpl string
	}{
		{parts.Policy, filepath.Join("app", "Policies", m.Entity()+"Policy.go"), policyTemplate},
		{parts.Request, filepath.Join("app", "Http", "Requests", m.Entity()+"Request.go"), requestTemplate + requestRulesTemplate},
		{parts.Service, filepath.Join("app", "Services", m.Entity()+"Service.go"), serviceTemplate + serviceBlocks},
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

	// The controller is the one `aru make:controller --resource` writes, from
	// the same template: seven actions answering 501, built with the service
	// when there is one.
	if parts.Controller {
		files, err := GenerateController(m.ControllerStub(parts.Service))
		if err != nil {
			return nil, err
		}
		out = append(out, files...)
	}
	return out, nil
}

// ControllerStub is the resource controller of the entity, as `aru make:model
// --controller` and `aru make:controller --resource` write it, built with the
// entity's service when withService is set.
func (m Module) ControllerStub(withService bool) Stub {
	s := Stub{
		Type: m.Controller(), ModulePath: m.ModulePath, Resource: m.Resource(), Entity: m.Entity(), Kind: KindResource,
	}
	if withService {
		s.Service = m.ServiceType()
	}
	return s
}

// FactorySpec is how a module describes its factory, for `aru make:module`
// and `aru make:model --factory` alike.
func (m Module) FactorySpec() FactorySpec {
	fields := make([]FactoryField, 0, len(m.Fields))
	for _, f := range m.Fields {
		fields = append(fields, f.Factory())
	}
	spec := FactorySpec{Entity: m.Entity(), Tenant: m.Tenant, Fields: fields, ModelsImport: m.ModelsImport()}
	if m.Parent != "" {
		spec.Parent = &FactoryParent{Entity: m.ParentEntity(), Field: m.ParentField(), Arg: m.ParentArg(), Human: m.ParentHuman()}
	}
	return spec
}

// SeederSpec is how a module describes its seeder: through the factory, and
// for a nested module under one parent the seeder loads or makes.
func (m Module) SeederSpec() SeederSpec {
	spec := SeederSpec{Entity: m.Entity(), ModulePath: m.ModulePath, Factory: true}
	if m.Parent != "" {
		spec.Parent = &SeederParent{Entity: m.ParentEntity(), Human: m.ParentHuman()}
	}
	return spec
}

// renderModelQuery is the module's app/Models/<Entity>Query.go.
func renderModelQuery(m Module) (File, error) {
	spec := m.QuerySpec()
	content, err := RenderQuery(spec)
	if err != nil {
		return File{}, err
	}
	return File{Path: filepath.Join("app", "Models", spec.Path()), Content: content}, nil
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

// Write writes the files, preserving custom blocks in the ones that already
// exist. It returns what it wrote and what it skipped.
//
// Without --force a file is created exclusively, in one operation, rather than
// looked for and then written. Looking first answers about the moment of the
// look: a second run of the same command reads the same directory, mints the
// same name, finds nothing there and replaces the first run's file -- reporting
// it as created, because from inside each run nothing was there. It is also what
// lets a symlink standing on the path swallow the write, since asking whether
// the file is there follows the link and answers about its target.
//
// With --force the existing file is read, merged and replaced, which is the
// whole of what --force means.
func Write(root string, files []File, force bool) (written, skipped []string, err error) {
	for _, f := range files {
		path := filepath.Join(root, f.Path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return written, skipped, err
		}

		if !force {
			created, err := createExclusively(path, f.Content)
			if err != nil {
				return written, skipped, err
			}
			if !created {
				skipped = append(skipped, f.Path)
				continue
			}
			written = append(written, f.Path)
			continue
		}

		content := f.Content
		if existing, readErr := os.ReadFile(path); readErr == nil {
			// The path is handed over with the two versions because the marker
			// is written in the syntax of the file: a view carries its custom
			// block in view comments, and a Go comment in one would be printed
			// to the reader of the page.
			content = Merge(f.Path, existing, f.Content)
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

// createExclusively writes the file only if the path is free, and reports
// whether it was. A path already taken is not an error: it is what --force is
// offered for, and the caller says so naming every file at once.
func createExclusively(path string, content []byte) (bool, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return false, nil
		}
		return false, err
	}
	if _, err := file.Write(content); err != nil {
		return false, errors.Join(err, file.Close())
	}
	return true, file.Close()
}

func numbered(s string) string {
	var b strings.Builder
	for i, line := range strings.Split(s, "\n") {
		fmt.Fprintf(&b, "%4d| %s\n", i+1, line)
	}
	return b.String()
}
