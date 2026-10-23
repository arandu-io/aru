package testlayout_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/testlayout"
)

// inventoryProject is a module with a test in every kind of place the
// inventory has to answer about, and in a few it has to ignore.
var inventoryProject = map[string]string{
	"go.mod":  "module example.test/shop\n\ngo 1.22\n",
	"main.go": "package main\n\nfunc main() {}\n",

	"tests/Unit/invoice_test.go": `package unit

import "testing"

func TestInvoice(t *testing.T) {
	t.Run("adds tax", func(t *testing.T) {
		t.Run("rounds half up", func(t *testing.T) {})
	})
	t.Run("adds tax", func(t *testing.T) {})
	for _, name := range []string{"a", "b"} {
		t.Run(name, func(t *testing.T) {
			t.Run("unknowable", func(t *testing.T) {})
		})
	}
	t.Run("", func(t *testing.T) {})
}

func Testlower(t *testing.T) {}

func TestMain(m *testing.M) { m.Run() }

type suite struct{}

func (suite) TestMethod(t *testing.T) {}

// TestInAComment(t *testing.T) is not a declaration.
var source = "func TestInAString(t *testing.T) {}"
`,
	"tests/Feature/orders_test.go": `//go:build pg || mysql

package feature

import (
	. "testing"
)

func TestOrders(t *T) {
	t.Run("lists", nil)
}
`,
	"tests/E2E/checkout_test.go": `//go:build e2e

package e2e_test

import "testing"

func TestSmokeCheckout(t *testing.T) {
	t.Run("pays", func(t *testing.T) {})
}

func TestCheckout(t *testing.T) {}
`,
	"tests/Conformance/kit_test.go": `//go:build !race && linux && go1.22

package conformance

import tt "testing"

func TestKit(t *tt.T) {}
`,
	"app/Models/money_internal_test.go": `package models

//go:build notaconstraint

import "testing"

func TestMoney(_ *testing.T) {}
`,
	"tests/Unit/broken_test.go":     "package unit\n\nfunc TestBroken(t *testing.T) {\n",
	"tests/Unit/testdata/x_test.go": "package x\n\nimport \"testing\"\n\nfunc TestFixture(t *testing.T) {}\n",
	"tests/Unit/_skip/x_test.go":    "package x\n\nimport \"testing\"\n\nfunc TestUnderscore(t *testing.T) {}\n",
	"tools/go.mod":                  "module example.test/tools\n",
	"tools/tool_test.go":            "package tools\n\nimport \"testing\"\n\nfunc TestOtherModule(t *testing.T) {}\n",
	"tests/Unit/helpers.go":         "package unit\n\nimport \"testing\"\n\nfunc TestNotInATestFile(t *testing.T) {}\n",
	"tests/Unit/signatures_test.go": "package unit\n\nimport \"testing\"\n\n" +
		"func TestHelper(tb testing.TB) {}\n\nfunc TestResult(t *testing.T) error { return nil }\n\n" +
		"func TestGeneric[T any](t *testing.T) {}\n\nfunc TestTwo(t, u *testing.T) {}\n",
	"tests/Contract/notesting_test.go":  "package contract\n\ntype T struct{}\n\nfunc TestNoImport(t *T) {}\n",
	"tests/E2E/support/support_test.go": "//go:build e2e\n\npackage support\n",
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestTheInventoryListsEveryTestWithItsLayerTagsAndSubtests reads the whole
// answer at once, because each field is a claim an editor acts on: the layer
// decides where the test is shown, the tags decide whether it compiles, and a
// subtest left out is one that cannot be run on its own.
func TestTheInventoryListsEveryTestWithItsLayerTagsAndSubtests(t *testing.T) {
	root := writeTree(t, inventoryProject)

	inv, err := testlayout.List(root)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Version != 1 || inv.Module != "example.test/shop" {
		t.Errorf("version %d, module %q; want 1 and example.test/shop", inv.Version, inv.Module)
	}

	want := []testlayout.Test{
		{
			Layer: "Unit", Package: "example.test/shop/tests/Unit", File: "tests/Unit/invoice_test.go", Line: 5,
			Name: "TestInvoice", Tags: []string{},
			Subtests: []testlayout.Subtest{
				{Name: "TestInvoice/adds_tax", Line: 6},
				{Name: "TestInvoice/adds_tax/rounds_half_up", Line: 7},
				{Name: "TestInvoice/adds_tax#01", Line: 9},
				{Name: "TestInvoice/#00", Line: 15},
			},
		},
		{
			Layer: "Feature", Package: "example.test/shop/tests/Feature", File: "tests/Feature/orders_test.go", Line: 9,
			Name: "TestOrders", Constraint: "pg || mysql", Tags: []string{"pg", "mysql"},
			Subtests: []testlayout.Subtest{{Name: "TestOrders/lists", Line: 10}},
		},
		{
			Layer: "E2E", Package: "example.test/shop/tests/E2E", File: "tests/E2E/checkout_test.go", Line: 7,
			Name: "TestSmokeCheckout", Constraint: "e2e", Tags: []string{"e2e"},
			Subtests: []testlayout.Subtest{{Name: "TestSmokeCheckout/pays", Line: 8}},
		},
		{
			Layer: "E2E", Package: "example.test/shop/tests/E2E", File: "tests/E2E/checkout_test.go", Line: 11,
			Name: "TestCheckout", Constraint: "e2e", Tags: []string{"e2e"}, Subtests: []testlayout.Subtest{},
		},
		{
			Layer: "Conformance", Package: "example.test/shop/tests/Conformance", File: "tests/Conformance/kit_test.go", Line: 7,
			Name: "TestKit", Constraint: "!race && linux && go1.22", Tags: []string{}, Subtests: []testlayout.Subtest{},
		},
		{
			Layer: "Other", Package: "example.test/shop/app/Models", File: "app/Models/money_internal_test.go", Line: 7,
			Name: "TestMoney", Tags: []string{}, Subtests: []testlayout.Subtest{},
		},
	}
	if !reflect.DeepEqual(inv.Tests, want) {
		t.Errorf("the inventory is wrong.\n got: %+v\nwant: %+v", inv.Tests, want)
	}

	if len(inv.Unreadable) != 1 || inv.Unreadable[0].File != "tests/Unit/broken_test.go" || inv.Unreadable[0].Error == "" {
		t.Errorf("the file that does not parse is not reported once, with what the parser said: %+v", inv.Unreadable)
	}

	var layers []string
	for _, l := range inv.Layers {
		layers = append(layers, l.Name+":"+l.Dir+":"+l.Tag)
	}
	wantLayers := []string{"Unit:tests/Unit:", "Feature:tests/Feature:", "Contract:tests/Contract:",
		"E2E:tests/E2E:e2e", "Conformance:tests/Conformance:", "Other::"}
	if !reflect.DeepEqual(layers, wantLayers) {
		t.Errorf("layers = %q; want %q", layers, wantLayers)
	}
}

// TestTheLayersAreTheCategories keeps the inventory and the commands that
// write and run a layer on one list: a sixth category added there appears
// here without a second edit.
func TestTheLayersAreTheCategories(t *testing.T) {
	inv, err := testlayout.List(writeTree(t, map[string]string{"go.mod": "module example.test/empty\n"}))
	if err != nil {
		t.Fatal(err)
	}
	cats := testlayout.Categories()
	if len(inv.Layers) != len(cats)+1 || inv.Layers[len(cats)].Name != testlayout.OtherLayer {
		t.Fatalf("layers %+v are not the categories followed by %s", inv.Layers, testlayout.OtherLayer)
	}
	for i, c := range cats {
		if inv.Layers[i] != (testlayout.Layer{Name: c.Name(), Dir: c.Dir, Tag: c.Tag}) {
			t.Errorf("layer %d is %+v; the category is %+v", i, inv.Layers[i], c)
		}
	}
	if inv.Tests == nil || inv.Unreadable == nil {
		t.Error("an empty inventory carries a nil slice, which JSON writes as null")
	}
}

// TestTheNamesAndTagsAreTheOnesGoTestUses runs the fixture's readable
// packages with go test and compares what it ran with what the inventory
// said, so a subtest name spelled differently from the toolchain, or a tag set
// that does not compile the file, fails here and not in an editor.
func TestTheNamesAndTagsAreTheOnesGoTestUses(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH")
	}
	files := map[string]string{}
	for name, body := range inventoryProject {
		switch name {
		case "tests/Unit/broken_test.go", "tests/Unit/signatures_test.go":
			// go test refuses a package holding either, which is the point of
			// listing from the sources.
			continue
		}
		if !strings.HasPrefix(name, "tests/Feature/") {
			files[name] = body
		}
	}
	root := writeTree(t, files)
	inv, err := testlayout.List(root)
	if err != nil {
		t.Fatal(err)
	}

	for _, pkg := range []string{"tests/Unit", "tests/E2E"} {
		var listed, tags []string
		for _, test := range inv.Tests {
			if !strings.HasPrefix(test.File, pkg+"/") {
				continue
			}
			listed = append(listed, test.Name)
			for _, s := range test.Subtests {
				listed = append(listed, s.Name)
			}
			tags = test.Tags
		}
		// vet is off because the fixture holds Testlower on purpose, which vet
		// reports and go test, rightly, does not run.
		args := []string{"test", "-vet=off", "-count=1", "-v", "-run", "."}
		if len(tags) > 0 {
			args = append(args, "-tags", strings.Join(tags, ","))
		}
		cmd := exec.Command("go", append(args, "./"+pkg+"/")...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go test %s with the listed tags failed: %v\n%s", pkg, err, out)
		}
		ran := map[string]bool{}
		for _, line := range strings.Split(string(out), "\n") {
			if name, ok := strings.CutPrefix(line, "=== RUN   "); ok {
				ran[strings.TrimSpace(name)] = true
			}
		}
		for _, name := range listed {
			if !ran[name] {
				t.Errorf("%s lists %s, and go test did not run a test by that name:\n%s", pkg, name, out)
			}
			delete(ran, name)
		}
		var unlisted []string
		for name := range ran {
			if !tableCase(name) {
				unlisted = append(unlisted, name)
			}
		}
		sort.Strings(unlisted)
		if len(unlisted) > 0 {
			t.Errorf("go test ran %q in %s, which the inventory left out", unlisted, pkg)
		}
	}
}

// tableCase reports whether go test ran name for the table in TestInvoice,
// whose names are values and which the inventory leaves out on purpose.
func tableCase(name string) bool {
	for _, c := range []string{"TestInvoice/a", "TestInvoice/b"} {
		if name == c || strings.HasPrefix(name, c+"/") {
			return true
		}
	}
	return false
}
