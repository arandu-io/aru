package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func assetFixture(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAssetBuildBundlesLocalModulesWithoutExternalTools(t *testing.T) {
	root := t.TempDir()
	assetFixture(t, root, "resources/js/app.js", "import {value} from './parts/value.js'; window.answer = value; // remove this comment\n")
	assetFixture(t, root, "resources/js/parts/value.js", "export const value = 42;")
	assetFixture(t, root, "resources/js/theme.js", "window.themeReady = true;")
	t.Setenv("PATH", "")
	if err := buildScripts(root, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "assets", "bundles_gen.go")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"app.min.js", "theme.min.js", "RegisterAsset", "window.answer=42", "window.themeReady=!0"} {
		if !bytes.Contains(before, []byte(want)) {
			t.Errorf("missing %s in generated assets", want)
		}
	}
	if bytes.Contains(before, []byte("remove this comment")) {
		t.Fatal("JavaScript was not minified")
	}
	info, _ := os.Stat(file)
	if err := buildScripts(root, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(file)
	infoAfter, _ := os.Stat(file)
	if !bytes.Equal(before, after) || !info.ModTime().Equal(infoAfter.ModTime()) {
		t.Fatal("unchanged build rewrote output")
	}
}

func TestAssetBuildRejectsRemotePackageAndEscapingImports(t *testing.T) {
	for _, target := range []string{"https://cdn.example/app.js", "//cdn.example/app.js", "some-package", "node:fs", "../../../outside.js", "./missing.js"} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			assetFixture(t, root, "resources/js/app.js", "import '"+target+"';")
			assetFixture(t, root, "assets/bundles_gen.go", "previous good build")
			if err := buildScripts(root, &bytes.Buffer{}); err == nil {
				t.Fatal("unsafe or missing import accepted")
			}
			got, _ := os.ReadFile(filepath.Join(root, "assets/bundles_gen.go"))
			if string(got) != "previous good build" {
				t.Fatal("failed build replaced the last good output")
			}
		})
	}
}

func TestAssetBuildRejectsSymlinkEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	assetFixture(t, outside, "outside.js", "window.leaked = true;")
	assetFixture(t, root, "resources/js/app.js", "import './escaped.js';")
	if err := os.Symlink(filepath.Join(outside, "outside.js"), filepath.Join(root, "resources/js/escaped.js")); err != nil {
		t.Skip(err)
	}
	if err := buildScripts(root, &bytes.Buffer{}); err == nil {
		t.Fatal("import escaped project through symlink")
	}
}

func TestAssetBuildPrunesOnlyItsOwnRegistration(t *testing.T) {
	root := t.TempDir()
	assetFixture(t, root, "resources/js/app.js", "window.answer = 42;")
	if err := buildScripts(root, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "resources/js/app.js")); err != nil {
		t.Fatal(err)
	}
	if err := buildScripts(root, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "assets/bundles_gen.go")); !os.IsNotExist(err) {
		t.Fatal("stale bundle survived removal of its entry")
	}
	assetFixture(t, root, "assets/bundles_gen.go", "package assets // hand-written\n")
	if err := buildScripts(root, &bytes.Buffer{}); err == nil {
		t.Fatal("hand-written output was not protected")
	}
}

func TestAssetInputsAreWatchedButGeneratedOutputIsNot(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"resources/js/app.js", "resources/js/parts/value.mjs", "resources/js/parts/value.ts", "assets/bundles_gen.go", "go.mod", "go.sum"} {
		assetFixture(t, root, name, "input")
	}
	state := snapshot(root)
	for _, name := range []string{"resources/js/app.js", "resources/js/parts/value.mjs", "resources/js/parts/value.ts", "go.mod", "go.sum"} {
		path := filepath.Join(root, name)
		if _, ok := state[path]; !ok || !isViewInput(path) {
			t.Errorf("%s does not trigger asset rebuild", name)
		}
	}
	if _, ok := state[filepath.Join(root, "assets/bundles_gen.go")]; ok {
		t.Fatal("generated bundle triggers its own rebuild")
	}
}

func TestAssetCSSMinificationPreservesLocalReferencesAndLicense(t *testing.T) {
	source := "/*! Copyright Example, MIT */\n.a { color: red; background: url('/_arandu/assets/abcdef123456/icon.svg'); }"
	got, err := minifyStylesheet([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Copyright Example", "/_arandu/assets/abcdef123456/icon.svg"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("lost %s", want)
		}
	}
	if len(got) >= len(source) {
		t.Fatal("stylesheet was not minified")
	}
}

func TestAssetCSSRejectsRemoteAndUnresolvedReferences(t *testing.T) {
	for _, source := range []string{`@import "https://cdn.example/styles.css";`, `.a{background:url(//cdn.example/a.png)}`, `.a{background:url(./unregistered.svg)}`} {
		if _, err := minifyStylesheet([]byte(source)); err == nil {
			t.Errorf("accepted %s", source)
		}
	}
}

func TestAssetBuildUsesConsumerNativeVersionAndGeneratedGoCompiles(t *testing.T) {
	root, native := t.TempDir(), t.TempDir()
	t.Setenv("GOWORK", "off")
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	assetFixture(t, root, "go.mod", "module example.test/app\n\ngo 1.26\n\nrequire github.com/arandu-io/hesape v0.0.0\nreplace github.com/arandu-io/hesape => "+filepath.ToSlash(native)+"\n")
	assetFixture(t, native, "go.mod", "module github.com/arandu-io/hesape\n\ngo 1.26\n")
	assetFixture(t, native, "view/view.go", "package view\nfunc RegisterAsset(name, kind string, body []byte) {}\n")
	assetFixture(t, native, "view/assets/htmx.min.js", "var htmx = {version: 'consumer-version'};")
	assetFixture(t, native, "view/assets/ui.js", "window.sequence = window.htmx.version;")
	assetFixture(t, native, "view/assets/basecoat.bundle.js", "/* Copyright Fixture, MIT */\nwindow.fixture = true;")
	assetFixture(t, root, "resources/js/app.js", "import 'arandu:htmx.min.js'; import 'arandu:ui.js'; import 'arandu:basecoat.bundle.js';")
	if err := buildScripts(root, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile(filepath.Join(root, bundleOutput))
	if err != nil {
		t.Fatal(err)
	}
	code := string(generated)
	for _, want := range []string{"consumer-version", "window.htmx=", "Copyright Fixture, MIT"} {
		if !strings.Contains(code, want) {
			t.Errorf("lost native contract %s", want)
		}
	}
	if strings.Index(code, "window.htmx=") > strings.Index(code, "window.sequence=") {
		t.Fatal("native dependency executed after its consumer")
	}
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated registration does not compile: %v\n%s", err, out)
	}
}

func TestAssetSyntaxFailurePreservesLastGoodBuild(t *testing.T) {
	root := t.TempDir()
	assetFixture(t, root, "resources/js/app.js", "window.answer = 42;")
	if err := buildScripts(root, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(root, bundleOutput))
	assetFixture(t, root, "resources/js/app.js", "function broken( {")
	if err := buildScripts(root, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "app.js") {
		t.Fatalf("syntax failure does not name the source: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(root, bundleOutput))
	if !bytes.Equal(before, after) {
		t.Fatal("failed build changed the served bundle")
	}
}
