package catalog_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/arandu-io/aru/internal/catalog"
	"github.com/arandu-io/aru/tests"
)

const (
	security   = "github.com/arandu-io/framework/security"
	httpBridge = "github.com/arandu-io/framework/http"
	kernel     = "github.com/arandu-io/framework/kernel"
	foundation = "github.com/arandu-io/framework/foundation"
	hesapeAuth = "github.com/arandu-io/hesape/auth"
)

// read is the catalog of the fixture module, which declares every shape a
// bridge can take, one name each.
func read(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Read(tests.Fixture(t, "catalog", "framework"))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return c
}

// TestEachDeclarationIsDecidedFromItsShape pins the decision for every shape
// the fixture declares: what the framework only points at goes to the target,
// and what it declares, envelops or translates stays.
func TestEachDeclarationIsDecidedFromItsShape(t *testing.T) {
	c := read(t)
	for _, want := range []struct {
		pkg, name          string
		how                catalog.How
		canonical, as, why string
	}{
		{security, "Grant", catalog.Alias, hesapeAuth, "Grant", "a type alias"},
		{security, "Policy", catalog.Alias, hesapeAuth, "Policy", "a generic alias whose parameters pass through"},
		{security, "StringPolicy", catalog.Declared, security, "StringPolicy", "an alias of an instantiation names no single symbol"},
		{security, "GrantPointer", catalog.Declared, security, "GrantPointer", "an alias of a pointer names no single symbol"},
		{security, "Role", catalog.Declared, security, "Role", "a defined type is the framework's own"},
		{security, "ErrForbidden", catalog.Alias, hesapeAuth, "ErrForbidden", "a value that only points"},
		{security, "MaxSignInFailures", catalog.Alias, hesapeAuth, "MaxSignInFailures", "a constant that only points"},
		{security, "ErrLocal", catalog.Declared, security, "ErrLocal", "a value computed here"},
		{security, "ErrTyped", catalog.Declared, security, "ErrTyped", "a value with a declared type of its own"},
		{security, "First", catalog.Declared, security, "First", "an iota"},
		{security, "Second", catalog.Declared, security, "Second", "an implicit repetition of an iota"},
		{security, "Authorize", catalog.Forward, hesapeAuth, "Authorize", "a generic forward with its type arguments spelled"},
		{security, "HashPassword", catalog.Forward, "github.com/arandu-io/hesape/hashing", "Make", "a forward under another name"},
		{security, "Forget", catalog.Forward, hesapeAuth, "Forget", "a forward with no result"},
		{security, "Any", catalog.Forward, hesapeAuth, "Any", "a variadic forward"},
		{security, "First1", catalog.Declared, security, "First1", "a variadic parameter passed without its ellipsis"},
		{security, "Swap", catalog.Declared, security, "Swap", "arguments reordered"},
		{security, "Background", catalog.Declared, security, "Background", "an argument added"},
		{security, "Twice", catalog.Declared, security, "Twice", "a body of two statements"},
		{security, "Wrap", catalog.Declared, security, "Wrap", "a signature naming a type the framework declares"},
		{security, "Reveal", catalog.Declared, security, "Reveal", "a forward into an internal package"},
		{security, "Hidden", catalog.Declared, security, "Hidden", "a forward to an unexported function"},
		{security, "Allowed", catalog.Forward, hesapeAuth, "Forget", "a forward to a forward of the same package"},
		{security, "SessionStore", catalog.Declared, security, "SessionStore", "an envelope"},
		{security, "Exposed", catalog.Declared, security, "Exposed", "an alias of an unexported name"},
		{httpBridge, "Context", catalog.Alias, "github.com/arandu-io/hesape/http", "Context", "an alias in a bridge that is mostly an envelope"},
		{httpBridge, "Router", catalog.Declared, httpBridge, "Router", "the envelope beside it"},
		{httpBridge, "NewRouter", catalog.Declared, httpBridge, "NewRouter", "a constructor of the envelope"},
		{kernel, "Kernel", catalog.Alias, foundation, "Application", "a chain that ends at a type another framework package declares"},
		{kernel, "Bootable", catalog.Alias, "github.com/arandu-io/hesape/foundation", "Bootable", "a chain followed through the framework"},
		{kernel, "New", catalog.Forward, foundation, "New", "a forward inside the framework"},
	} {
		got, ok := c.Lookup(want.pkg, want.name)
		if !ok {
			t.Errorf("%s.%s (%s) is not in the catalog", want.pkg, want.name, want.why)
			continue
		}
		if got.How != want.how || got.Canonical != want.canonical || got.CanonicalName != want.as {
			t.Errorf("%s.%s (%s) = %s %s.%s, want %s %s.%s",
				want.pkg, want.name, want.why, got.How, got.Canonical, got.CanonicalName, want.how, want.canonical, want.as)
		}
		if got.Moved() != (want.canonical != want.pkg || want.as != want.name) {
			t.Errorf("%s.%s: Moved() = %t disagrees with its canonical path", want.pkg, want.name, got.Moved())
		}
	}
}

// TestWhatAProjectCannotImportIsLeftOut keeps the catalog to what a selector
// in a project can reach.
func TestWhatAProjectCannotImportIsLeftOut(t *testing.T) {
	c := read(t)
	for _, absent := range []struct{ pkg, name, why string }{
		{security, "TestOnly", "declared in a test file"},
		{security, "Skipped", "excluded by a build constraint"},
		{security, "Load", "a method, which belongs to its type"},
		{security, "unexported", "unexported"},
		{"github.com/arandu-io/framework/security/testdata", "Ignored", "under testdata"},
		{"github.com/arandu-io/framework/internal/secret", "Reveal", "in an internal package"},
		{"github.com/arandu-io/framework/cmd/tool", "Command", "in a command"},
		{"github.com/arandu-io/framework/plugin", "Nested", "in a module of its own"},
	} {
		if _, ok := c.Lookup(absent.pkg, absent.name); ok {
			t.Errorf("%s.%s is in the catalog, and it is %s", absent.pkg, absent.name, absent.why)
		}
	}
	if c.Covers("github.com/arandu-io/framework/internal/secret") {
		t.Error("the catalog covers an internal package")
	}
}

// TestABridgeIsReadFromItsOwnDocumentation reads the sentence a bridge
// carries, and nothing from a package that does not carry it.
func TestABridgeIsReadFromItsOwnDocumentation(t *testing.T) {
	c := read(t)
	bridges := map[string]string{}
	for _, pkg := range c.Packages {
		bridges[pkg.Path] = pkg.Bridge
	}
	for pkg, want := range map[string]string{
		security:   hesapeAuth,
		httpBridge: "github.com/arandu-io/hesape/http",
		kernel:     foundation,
		foundation: "",
	} {
		if got := bridges[pkg]; got != want {
			t.Errorf("%s is a bridge to %q, want %q", pkg, got, want)
		}
	}
}

// TestTheProjectDecidesWhichSourceIsRead runs ForProject over the three places
// a required framework can sit and the two answers that are not a catalog.
func TestTheProjectDecidesWhichSourceIsRead(t *testing.T) {
	stub := tests.Fixture(t, "catalog", "framework")

	t.Run("a directory replace is read, and is no version", func(t *testing.T) {
		root := project(t, "require github.com/arandu-io/framework v0.50.2\n\nreplace github.com/arandu-io/framework => "+stub+"\n")
		c, err := catalog.ForProject(root)
		if err != nil {
			t.Fatalf("ForProject: %v", err)
		}
		if c.Version != "" {
			t.Errorf("a working tree was reported as version %s", c.Version)
		}
		if _, ok := c.Lookup(security, "Grant"); !ok {
			t.Error("the replaced directory was not the one read")
		}
	})

	t.Run("a vendored framework is read without a go.mod", func(t *testing.T) {
		root := project(t, "require github.com/arandu-io/framework v0.50.2\n")
		vendored := filepath.Join(root, "vendor", "github.com", "arandu-io", "framework", "http")
		if err := os.MkdirAll(vendored, 0o755); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(filepath.Join(stub, "http", "http.go"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(vendored, "http.go"), body, 0o644); err != nil {
			t.Fatal(err)
		}
		c, err := catalog.ForProject(root)
		if err != nil {
			t.Fatalf("ForProject: %v", err)
		}
		if c.Version != "v0.50.2" || !c.Covers(httpBridge) || c.Covers(security) {
			t.Errorf("version %q, http covered %t, security covered %t: want the vendored http alone at v0.50.2",
				c.Version, c.Covers(httpBridge), c.Covers(security))
		}
	})

	t.Run("a version on no disk says so", func(t *testing.T) {
		t.Setenv("GOMODCACHE", t.TempDir())
		root := project(t, "require github.com/arandu-io/framework v0.50.2\n")
		_, err := catalog.ForProject(root)
		var missing *catalog.NotOnDisk
		if !errors.As(err, &missing) || missing.Version != "v0.50.2" {
			t.Errorf("ForProject = %v, want NotOnDisk for v0.50.2", err)
		}
	})

	t.Run("a replace naming a missing directory is not a download", func(t *testing.T) {
		root := project(t, "require github.com/arandu-io/framework v0.50.2\n\nreplace github.com/arandu-io/framework => ../nowhere\n")
		_, err := catalog.ForProject(root)
		var missing *catalog.NotOnDisk
		if err == nil || errors.As(err, &missing) {
			t.Errorf("ForProject = %v, want an error that names the replace", err)
		}
	})

	t.Run("a project without the framework has nothing to read", func(t *testing.T) {
		root := project(t, "")
		if _, err := catalog.ForProject(root); !errors.Is(err, catalog.ErrNotRequired) {
			t.Errorf("ForProject = %v, want ErrNotRequired", err)
		}
	})
}

// TestTheFrameworkCheckedOutBesideThisRepository pins the symbols the decision
// was taken over, against the framework itself: the request context and the
// Grant are the component's, the router and the session store the framework's.
func TestTheFrameworkCheckedOutBesideThisRepository(t *testing.T) {
	root, err := filepath.EvalSymlinks(filepath.Join(tests.Root(t), "..", "framework"))
	if err != nil {
		t.Skip("framework is not checked out next to this repository")
	}
	c, err := catalog.Read(root)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	for _, want := range []struct{ pkg, name, canonical string }{
		{httpBridge, "Context", "github.com/arandu-io/hesape/http"},
		{httpBridge, "Router", httpBridge},
		{security, "Grant", hesapeAuth},
		{security, "Authorize", hesapeAuth},
		{security, "SessionStore", security},
	} {
		got, ok := c.Lookup(want.pkg, want.name)
		if !ok {
			t.Errorf("%s.%s is not in the framework's catalog", want.pkg, want.name)
			continue
		}
		if got.Canonical != want.canonical {
			t.Errorf("%s.%s is %s's, want %s's", want.pkg, want.name, got.Canonical, want.canonical)
		}
	}
}

func project(t *testing.T, requires string) string {
	t.Helper()
	root := t.TempDir()
	body := "module example.test/project\n\ngo 1.26\n"
	if requires != "" {
		body += "\n" + requires
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}
