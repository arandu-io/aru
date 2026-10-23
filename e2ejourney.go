package main

import (
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/arandu-io/aru/internal/gen"
)

// The import paths of the test kit, as a journey written here spells them.
// They are data: the strings a generated file imports.
const (
	testKitImport     = "github.com/arandu-io/framework/test"
	browserKitImport  = testKitImport + "/browser"
	cdpKitImport      = browserKitImport + "/cdp"
	openAPIKitImport  = testKitImport + "/openapi"
	journeyPackage    = "e2e_test"
	contractPackage   = "contract_test"
	contractTestsDir  = "tests/Contract"
	journeyHelperDocs = "tests/TestCase.go"
)

// journey is one end-to-end test to write: its name, and the Go statements
// that drive the page.
type journey struct {
	// Name is the journey, exported: Checkout is TestCheckout in
	// tests/E2E/Checkout_test.go.
	Name string
	// Smoke names the test TestSmoke<Name>, which aru test --smoke runs.
	Smoke      bool
	ModulePath string
	// Doc is the comment above the test, without the slashes.
	Doc []string
	// Steps are the statements after the page is open, one per line.
	Steps []string
	// Custom wraps the steps in the custom block a regeneration keeps.
	Custom bool
}

// testName is the function the journey is.
func (j journey) testName() string {
	if j.Smoke {
		return smokePrefix + j.Name
	}
	return "Test" + j.Name
}

// path is where the journey is written.
func (j journey) path() string {
	return filepath.ToSlash(filepath.Join(e2eTestsDir, j.Name+"_test.go"))
}

// renderJourney writes the journey as a Go test file behind the e2e tag,
// formatted. It imports the browser package only when a step names it, and
// the project's tests package for Serve and Open.
func renderJourney(j journey) (gen.File, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "//go:build %s\n\npackage %s\n\nimport (\n\t\"testing\"\n\n", e2eTag, journeyPackage)
	body := strings.Join(j.Steps, "\n")
	if strings.Contains(body, "browser.") {
		fmt.Fprintf(&b, "\t%q\n\n", browserKitImport)
	}
	fmt.Fprintf(&b, "\t%q\n)\n\n", j.ModulePath+"/tests")
	for _, line := range j.Doc {
		if line == "" {
			b.WriteString("//\n")
			continue
		}
		b.WriteString("// " + line + "\n")
	}
	fmt.Fprintf(&b, "func %s(t *testing.T) {\n\tserver := tests.Serve(t)\n\tpage := tests.Open(t, server)\n\n", j.testName())
	if j.Custom {
		b.WriteString("\t// arandu:begin custom\n")
	}
	for _, s := range j.Steps {
		b.WriteString("\t" + s + "\n")
	}
	if j.Custom {
		b.WriteString("\t// arandu:end custom\n")
	}
	b.WriteString("}\n")
	src, err := format.Source([]byte(b.String()))
	if err != nil {
		return gen.File{}, fmt.Errorf("the journey did not format: %w\n%s", err, b.String())
	}
	return gen.File{Path: j.path(), Content: src}, nil
}

// journeyHelpers is what tests/TestCase.go declares for a journey to open a
// page, printed when it does not: the wiring is a person's to paste.
const journeyHelpers = `Add the two helpers a journey opens its page with to ` + journeyHelperDocs + `,
beside the boot and the migrations your tests already use:

    import (
        "fmt"

        "` + testKitImport + `"
        "` + browserKitImport + `"
        "` + cdpKitImport + `"
    )

    // Drivers are the browser drivers the journeys in tests/E2E run with;
    // ARANDU_E2E_DRIVER picks one.
    var Drivers = browser.Registry{"cdp": cdp.Launch}

    // Serve boots the application on a free port over a database of the
    // test's own.
    func Serve(t *testing.T) *test.Server {
        t.Helper()
        return test.Serve(t, Boot, test.Migrate(Migrate))
    }

    // Open opens a browser page on the server, resolving route names.
    func Open(t *testing.T, server *test.Server) *browser.Page {
        t.Helper()
        routes := func(name string, params ...any) (string, error) {
            values := make([]string, len(params))
            for i, p := range params {
                values[i] = fmt.Sprint(p)
            }
            return server.Route(name, values...), nil
        }
        return browser.Open(t, Drivers, browser.BaseURL(server.URL), browser.Routes(routes))
    }

Boot and Migrate are the functions your tests boot the application and run
its migrations with.`

// missingHelpers answers which of the functions a generated test calls on the
// project's tests package it does not declare. The package is the Go files
// directly under tests/, the test files left out.
func missingHelpers(root string, names ...string) []string {
	entries, err := os.ReadDir(filepath.Join(root, "tests"))
	var source strings.Builder
	if err == nil {
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			if body, err := os.ReadFile(filepath.Join(root, "tests", name)); err == nil {
				source.Write(body)
			}
		}
	}
	var missing []string
	for _, n := range names {
		if !strings.Contains(source.String(), "func "+n+"(") {
			missing = append(missing, n)
		}
	}
	return missing
}

// goString quotes s as a Go string literal.
func goString(s string) string { return strconv.Quote(s) }

// journeyName turns what a person typed -- checkout, sign-up flow,
// invoices.index -- into the exported name of a journey.
func journeyName(s string) string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	})
	var b strings.Builder
	for _, f := range fields {
		b.WriteString(strings.ToUpper(f[:1]) + f[1:])
	}
	name := b.String()
	if len(name) > 4 && strings.HasPrefix(name, "Test") && name[4] >= 'A' && name[4] <= 'Z' {
		name = name[4:]
	}
	if name == "" || name[0] >= '0' && name[0] <= '9' {
		name = "Journey" + name
	}
	return name
}
