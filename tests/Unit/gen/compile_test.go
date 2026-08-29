package gen_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/gen"
	"github.com/arandu-io/aru/internal/kyse"
	"github.com/arandu-io/aru/tests"
)

// published is what the generated code is compiled against: the tags a project
// created today resolves to, and nothing else.
//
// They are written down here rather than discovered, and there is deliberately
// no replace directive anywhere below. A replace would point the build at the
// working tree beside this one, and then a green run would say the generator
// agrees with code nobody can `go get` -- which is the opposite of what this
// file exists to prove. What is being checked is that the emitted source
// compiles against the libraries a person actually receives.
//
// The cost is that these three lines go stale the day the skeleton moves, and
// that is what TestThePinnedTagsMatchTheSkeleton answers.
var published = map[string]string{
	"github.com/arandu-io/framework": "v0.41.0",
	"github.com/arandu-io/hesape":    "v0.19.1",
	"github.com/arandu-io/kyse":      "v0.13.0",
}

// generatedModulePath is the module path the fixtures already generate imports
// for, so the emitted `example.test/project/app/Models` resolves to the package
// written beside it.
const generatedModulePath = "example.test/project"

// TestTheGeneratedModuleCompiles hands every generator's output to the Go
// compiler.
//
// Parsing is not compiling. A file can parse and still name a function that
// does not exist, pass the wrong number of arguments, assign a string to an
// int64 or import a package whose symbol was renamed two releases ago -- and
// every one of those reaches the person who ran the generator as a broken
// project rather than as a failing test here. The other tests in this suite
// read the emitted bytes; this one builds them.
//
// It builds them in a module of its own, with no replace, so what the generated
// imports resolve to is the published library and not the working tree.
func TestTheGeneratedModuleCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a generated project: skipped under -short")
	}

	tool := goTool(t)
	root := t.TempDir()
	writeProjectSkeleton(t, root)

	// Where every file came from, so a compiler error names the command that
	// wrote the file rather than only the file.
	from := map[string]string{}
	emitted := map[string][]byte{}
	emit := func(command string, generate func() ([]gen.File, error)) {
		t.Helper()
		files, err := generate()
		if err != nil {
			t.Fatalf("%s: %v", command, err)
		}
		for _, f := range files {
			// Two commands may write one path, and only when they write the same
			// bytes: that is the claim `aru make:test` makes about the file
			// `aru make:module` already emits, and it is checked here rather
			// than assumed. Different bytes mean the corpus is smaller than it
			// looks -- the second write hides whatever the first produced, and
			// the compiler never sees it.
			if previous, clash := from[f.Path]; clash {
				if !bytes.Equal(emitted[f.Path], f.Content) {
					t.Fatalf("%s and %s both write %s and disagree about its contents, "+
						"so the corpus is smaller than it looks", previous, command, f.Path)
				}
				from[f.Path] = previous + " and " + command
				continue
			}
			from[f.Path] = command
			emitted[f.Path] = f.Content
			writeInto(t, filepath.Join(root, f.Path), f.Content)
		}
	}
	one := func(command string, generate func() (gen.File, error)) {
		t.Helper()
		emit(command, func() ([]gen.File, error) {
			f, err := generate()
			return []gen.File{f}, err
		})
	}

	// A tenant module and a global one. The two differ in the scope every Model
	// query applies and in the keys the migration composes, so building one of
	// them proves nothing about the other.
	tenantModule := compiled("purchase_order", true)
	globalModule := compiled("stock_item", false)
	emit("aru make:module purchase_order --tenant", func() ([]gen.File, error) { return gen.Generate(tenantModule) })
	emit("aru make:module stock_item", func() ([]gen.File, error) { return gen.Generate(globalModule) })

	// make:test, over both modules. It writes the file make:module already
	// wrote, so what reaches the compiler is one file and what is proved is two
	// things: that the bytes are the same, checked by emit above, and that they
	// build. It cannot be given a module of its own -- the test names a service,
	// a policy and a model, and only a whole module has all three.
	one("aru make:test PurchaseOrder", func() (gen.File, error) { return gen.RenderTest(tenantModule) })
	one("aru make:test StockItem", func() (gen.File, error) { return gen.RenderTest(globalModule) })

	// make:model --all, which is the data half of a module: the model, the
	// migration, the factory, the seeder, the policy and the request. It is a
	// separate path through the same templates and it emits two files no module
	// emits at all.
	model := compiled("warehouse", true)
	emit("aru make:model warehouse --all --tenant", func() ([]gen.File, error) {
		return gen.GenerateModel(model, gen.Everything())
	})

	// The two shapes `aru make:migration` renders. There is no third: an empty
	// migration is refused rather than written, because an Up that applies
	// nothing is still recorded as applied and the id is immutable, so it would
	// never run again.
	one("aru make:migration create_shipments_table --create=shipments", func() (gen.File, error) {
		return gen.RenderMigration(gen.MigrationSpec{
			ID:     "2026_07_31_000004_create_shipments_table",
			Type:   "CreateShipmentsTable",
			Table:  "shipments",
			Tenant: true,
			Create: true,
			Fields: []gen.Field{
				{Name: "tracking_code", Type: gen.TypeString, Required: true, Unique: true},
				{Name: "dispatched_at", Type: gen.TypeTimestamp},
			},
		})
	})
	one("aru make:migration add_status_to_purchase_orders --table=purchase_orders", func() (gen.File, error) {
		return gen.RenderMigration(gen.MigrationSpec{
			ID:     "2026_07_31_000005_add_status_to_purchase_orders",
			Type:   "AddStatusToPurchaseOrders",
			Table:  "purchase_orders",
			Tenant: true,
			Fields: []gen.Field{
				{Name: "status", Type: gen.TypeString, Unique: true},
				{Name: "settled_at", Type: gen.TypeTimestamp},
			},
		})
	})

	// The views are compiled the way the CLI compiles them, because the
	// controller imports the package they compile into: a module whose views
	// were left as markup does not build, and the failure would be about a
	// missing import rather than about anything the generator wrote.
	for path, out := range buildGeneratedViews(t, root, from) {
		from[path] = "aru view:build"
		writeInto(t, filepath.Join(root, path), out)
	}

	// Before the build, and with the network still allowed: the corpus is
	// nothing without the libraries it names, and a machine that cannot reach
	// them has to say so rather than report a pass.
	ensureModules(t, tool, root)

	// `go build` and `go vet`, and both are load-bearing. `go build ./...`
	// silently skips a directory holding only _test.go files, which is exactly
	// where the generated test lands -- measured: a generated test with a type
	// error builds clean and fails vet.
	for _, stage := range []struct {
		what string
		args []string
	}{
		{"go build", []string{"build", "./..."}},
		{"go vet", []string{"vet", "./..."}},
	} {
		out, err := runGo(tool, root, stage.args...)
		if err == nil {
			continue
		}
		t.Errorf("%s refuses the generated project:\n\n%s\n%s",
			stage.what, blame(out, from), out)
	}
}

// TestThePinnedTagsMatchTheSkeleton is the alarm on the constants above.
//
// The versions cannot be read out of the skeleton at test time: the machine
// that runs this in CI has this repository and nothing else, and a test that
// looked for a sibling checkout would skip there -- in the one place the answer
// matters. So they are written down, and checked here against the skeleton
// whenever it happens to be beside this repository, which is every developer's
// machine and none of the build agents.
func TestThePinnedTagsMatchTheSkeleton(t *testing.T) {
	path := filepath.Join(filepath.Dir(tests.Root(t)), "arandu", "go.mod")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no project skeleton at %s: the pinned tags cannot be checked from here", path)
	}

	for module, want := range published {
		got := requiredVersion(string(body), module)
		if got == "" {
			t.Errorf("%s does not require %s, and the compile harness pins it at %s", path, module, want)
			continue
		}
		if got != want {
			t.Errorf("the compile harness builds against %s %s and a new project gets %s: update `published`",
				module, want, got)
		}
	}
}

// compiled is the specification the harness generates from.
//
// Every accepted field type appears, because the type is what decides the Go
// type, the column, the parse in the controller and the format in the view --
// so a corpus missing one leaves that whole column of the templates unbuilt.
// The date and the sequence are fixed for the reason the golden fixtures fix
// them: a generated identifier that moves with the calendar is one this test
// cannot name.
func compiled(name string, tenant bool) gen.Module {
	return gen.Module{
		Name: name,
		Fields: []gen.Field{
			{Name: "reference", Type: gen.TypeString, Required: true, Unique: true},
			{Name: "supplier_email", Type: gen.TypeEmail, Required: true},
			{Name: "total", Type: gen.TypeMoney, Required: true},
			{Name: "weight", Type: gen.TypeDecimal},
			{Name: "quantity", Type: gen.TypeInt, Required: true},
			{Name: "external_id", Type: gen.TypeUUID},
			{Name: "approved", Type: gen.TypeBool},
			{Name: "notes", Type: gen.TypeText},
			{Name: "delivery_date", Type: gen.TypeDate},
			{Name: "received_at", Type: gen.TypeTimestamp},
		},
		Tenant:     tenant,
		ModulePath: generatedModulePath,
		Date:       "2026_07_31",
	}
}

// buildGeneratedViews turns the emitted markup into the Go the controller
// imports, and answers with the compiled files by their path in the project.
//
// It runs the same compiler the CLI runs and derives the name and the data type
// the same way, because a view compiled under another name is a view the
// controller cannot render -- and that is a failure no compiler reports.
func buildGeneratedViews(t *testing.T, root string, from map[string]string) map[string][]byte {
	t.Helper()

	viewsDir := filepath.Join(root, "resources", "views")
	out := map[string][]byte{}

	sources := make([]string, 0, len(from))
	for path := range from {
		if strings.HasSuffix(path, ".kyse.go") {
			sources = append(sources, path)
		}
	}
	sort.Strings(sources)

	for _, path := range sources {
		source := filepath.Join(root, path)
		body, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}

		file, err := kyse.Parse(path, string(body))
		if err != nil {
			t.Fatalf("%s does not parse as a view: %v", path, err)
		}
		// A generated page always declares the struct it draws. An empty answer
		// here would compile into a file asserting nothing, so it stops the test
		// instead of reaching the compiler as a puzzle.
		dataType := kyse.RenderType(file)
		if dataType == "" {
			t.Fatalf("%s declares no data type, so the compiled view would assert nothing", path)
		}

		target, err := filepath.Rel(root, kyse.OutputPath(source))
		if err != nil {
			t.Fatal(err)
		}
		generated, err := kyse.Generate(file, kyse.Name(viewsDir, source), dataType, target)
		if err != nil {
			t.Fatalf("%s does not compile as a view: %v", path, err)
		}
		out[target] = generated
	}
	return out
}

// writeProjectSkeleton lays out the part of a project the generator does not
// write: the module file, and the two contracts the emitted code declares
// against.
//
// They are the skeleton's, reduced to the surface the generated files touch.
// Anything more would be this test asserting things about a project layout it
// does not own; anything less and the corpus would not build for a reason that
// has nothing to do with the generator.
func writeProjectSkeleton(t *testing.T, root string) {
	t.Helper()

	modules := make([]string, 0, len(published))
	for path, version := range published {
		modules = append(modules, "\t"+path+" "+version)
	}
	sort.Strings(modules)

	writeInto(t, filepath.Join(root, "go.mod"), []byte("module "+generatedModulePath+`

go 1.26

require (
`+strings.Join(modules, "\n")+`
)
`))

	// The base type every generated controller embeds. It answers a failed
	// validation and reports whether one passed, which is what the generated
	// Store and Update call.
	writeInto(t, filepath.Join(root, "app", "Http", "Controllers", "Controller.go"), []byte(`package controllers

import (
	"net/http"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/validation"
)

type Controller struct{}

func (Controller) Invalid(ctx *fhttp.Context, view string, data any) error {
	return ctx.Fragment(http.StatusUnprocessableEntity, view, data)
}

func (Controller) Validated(errs validation.Errors) bool { return len(errs) == 0 }
`))

	// The contract a generated seeder is written against: the interface it
	// proves it satisfies, and the dependencies its signature takes.
	writeInto(t, filepath.Join(root, "database", "seeders", "seeders.go"), []byte(`package seeders

import "context"

type Deps struct {
	Tenant string
}

type Seeder interface {
	Name() string
	Run(ctx context.Context, d Deps) error
}
`))
}

// ensureModules fetches what the generated imports name, before the build that
// needs them.
//
// This is the one step allowed to reach the network, and it is separate from
// the build for that reason: the build below runs with lookups disabled, so a
// green result cannot have come from resolving something outside the complete
// dependency graph rooted at these three lines.
//
// A machine that can neither reach the proxy nor find the tags already
// extracted has not tested anything, and says so. The skip names the version,
// because "it did not build" and "it was never built" are different answers and
// only one of them is about the generator.
//
// On a build agent it is not a skip at all. A skipped test reports ok without
// -v, so on the one machine whose green run is what anybody trusts, a tag that
// was withdrawn or misspelled would read exactly like a generator that works.
// A laptop on a train is a reason to stop; a build agent is not.
func ensureModules(t *testing.T, tool, root string) {
	t.Helper()

	args := []string{"mod", "download", "all"}

	out, err := runGo(tool, root, args...)
	if err == nil {
		return
	}
	say := t.Skipf
	if os.Getenv("CI") != "" {
		say = t.Fatalf
	}
	say("the generated project was not compiled, so nothing here was proved: "+
		"the dependency graph rooted at %s could not be resolved from the module cache or the proxy.\n%v\n%s",
		publishedModules(), err, out)
}

func publishedModules() string {
	modules := make([]string, 0, len(published))
	for path, version := range published {
		modules = append(modules, path+"@"+version)
	}
	sort.Strings(modules)
	return strings.Join(modules, " ")
}

// runGo runs one go command inside the generated project.
//
// GOWORK is off so a workspace on the developer's machine cannot substitute a
// working tree for the tag; GOPROXY is off so the build resolves only what
// ensureModules already fetched; GOTOOLCHAIN is local so the go directive
// cannot start a download of its own. GOSUMDB is off because the sums are
// computed from what is already on this machine, and consulting the database
// would be the network the previous line just closed.
func runGo(tool, root string, args ...string) (string, error) {
	cmd := exec.Command(tool, args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"GOWORK=off", "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local", "GOSUMDB=off")
	if args[0] != "mod" {
		cmd.Env = append(cmd.Env, "GOPROXY=off")
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// blame maps the files a compiler named back to the commands that wrote them.
//
// The compiler reports a path and a line, which is enough to open the file and
// not enough to know what produced it -- and the whole point of the corpus is
// that six commands wrote into one tree. Without this the reader of a failure
// has to reconstruct that mapping by hand.
func blame(output string, from map[string]string) string {
	named := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		for path := range from {
			if strings.HasPrefix(line, path+":") {
				named[path] = true
			}
		}
	}
	if len(named) == 0 {
		return "no generated file is named in the output below, so the failure is in the project layout around it"
	}

	paths := make([]string, 0, len(named))
	for path := range named {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var b strings.Builder
	b.WriteString("the generated files the compiler named, and what wrote each:\n")
	for _, path := range paths {
		fmt.Fprintf(&b, "  %s\n      written by %s\n", path, from[path])
	}
	return b.String()
}

// requiredVersion reads the version a go.mod requires for one module path.
func requiredVersion(body, module string) string {
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "require "))
		if len(fields) >= 2 && fields[0] == module {
			return fields[1]
		}
	}
	return ""
}

func goTool(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH, so nothing was compiled")
	}
	return path
}

func writeInto(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}
