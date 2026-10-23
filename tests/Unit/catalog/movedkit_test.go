package catalog_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/catalog"
)

// kitFramework writes a framework module, holding the test kit when withKit is
// set, and answers its directory.
func kitFramework(t *testing.T, withKit bool) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":                  "module github.com/arandu-io/framework\n\ngo 1.26\n",
		"http/doc.go":             "package http\n\nfunc Handle() {}\n",
		"test/doc.go":             "package test\n\ntype Server struct{}\n\nfunc Serve() *Server { return nil }\n",
		"test/browser/browser.go": "package browser\n\ntype Page struct{}\n",
	}
	for name, body := range files {
		if (!withKit && strings.HasPrefix(name, "test/")) {
			continue
		}
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestTheKitsOldHomeIsCataloguedUnderTheFramework: a framework that holds the
// kit catalogs hesape/arandutest and its packages as aliases of
// framework/test, so the fix moves an old import; one from before the move
// catalogs nothing there.
func TestTheKitsOldHomeIsCataloguedUnderTheFramework(t *testing.T) {
	c, err := catalog.Read(kitFramework(t, true))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct{ pkg, name, canonical string }{
		{catalog.MovedKit, "Serve", catalog.KitPath},
		{catalog.MovedKit, "Server", catalog.KitPath},
		{catalog.MovedKit + "/browser", "Page", catalog.KitPath + "/browser"},
	} {
		s, ok := c.Lookup(want.pkg, want.name)
		if !ok || !s.Moved() || s.Canonical != want.canonical || s.CanonicalName != want.name {
			t.Errorf("%s.%s = %+v, %v; want it moved to %s", want.pkg, want.name, s, ok, want.canonical)
		}
	}
	bridged := false
	for _, p := range c.Packages {
		if p.Path == catalog.MovedKit && p.Bridge == catalog.KitPath {
			bridged = true
		}
	}
	if !bridged {
		t.Errorf("%s is not catalogued as a bridge to %s", catalog.MovedKit, catalog.KitPath)
	}

	src := "package feature_test\n\nimport (\n\t\"testing\"\n\n\t\"github.com/arandu-io/hesape/arandutest\"\n)\n\nfunc TestX(t *testing.T) { _ = arandutest.Serve() }\n"
	out, renames, err := c.Rewrite("x_test.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"github.com/arandu-io/framework/test"`) || !strings.Contains(string(out), "test.Serve()") ||
		strings.Contains(string(out), "arandutest") || len(renames) == 0 {
		t.Errorf("the old import was not moved to the framework's kit:\n%s", out)
	}

	before, err := catalog.Read(kitFramework(t, false))
	if err != nil {
		t.Fatal(err)
	}
	if before.Covers(catalog.MovedKit) {
		t.Errorf("a framework without the kit catalogs %s, pointing at a package it does not have", catalog.MovedKit)
	}
}
