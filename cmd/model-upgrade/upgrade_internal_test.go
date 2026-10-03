package main

import (
	"bytes"
	"flag"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/gen"
	"github.com/arandu-io/aru/tests"
)

// update rewrites the after trees: go test ./cmd/model-upgrade -update
var update = flag.Bool("update", false, "rewrite testdata/*/after from what the tool writes")

// The fixtures are trees before and after an upgrade: testdata/<case>/before
// is copied into a temporary module, the tool runs there, and every file of
// testdata/<case>/after has to be what it wrote, byte for byte. The query files
// model:build writes are not kept there -- they are the generator's, pinned by
// its own goldens -- and are checked for being present.

// TestTheSkeletonShapesAreRewritten is the project `aru new` creates: three
// entities, two of them in one file and two embedding nothing, a constructor
// that returns the model and one that assigns settings, two factories with
// declarations of their own outside the custom block, and callers that chain
// NewQuery, NewInstance and Query off a constructor and name the generic
// types. The rewritten tree has to compile against the model core.
func TestTheSkeletonShapesAreRewritten(t *testing.T) {
	core := modelCore(t)
	root := upgradeFixture(t, "skeleton", "Note", "TwoFactor", "RecoveryCode", "User")
	compiles(t, root, core)
}

// TestTheHyzShapesAreRewritten is an application grown by hand: a constructor
// whose name is not the plural (MediaLibrary, for a noun that is its own
// plural), keys the application writes, no timestamps, soft deletes, a page
// size from a constant, an event callback, a table shared by every tenant and
// one scoped by another column -- and callers that group a where with a
// closure over the generic builder.
func TestTheHyzShapesAreRewritten(t *testing.T) {
	core := modelCore(t)
	root := upgradeFixture(t, "hyz", "Media", "Entry", "Term", "Lead")
	compiles(t, root, core)
}

// TestWhatCannotBeReadStopsTheRunAndWritesNothing: a setting a table spec
// does not have, a constructor that decides at run time, the generic model
// embedded in a type that is not its entity or embedded by a type nothing
// constructs, NewQuery on a variable, NewInstance with attributes, and a
// generic factory helper with no typed equivalent. Each is named at its line,
// and the tree is left exactly as it was.
func TestWhatCannotBeReadStopsTheRunAndWritesNothing(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "aborts", "before"), root)
	before := snapshotTree(t, root)
	t.Chdir(root)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"./..."}, &stdout, &stderr); code != 1 {
		t.Fatalf("model-upgrade exited %d, want 1:\n%s%s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"app/Models/Account.go:15: ConnectionName is a setting model-upgrade does not translate into model.TableSpec",
		"app/Models/Account.go:29: a statement model-upgrade does not read in the constructor of Profile",
		"app/Models/Account.go:37: Orphan embeds model.Model[Orphan], and model-upgrade found no constructor of it to read",
		"app/Services/UserService.go:13: model.Model[models.User] outside the struct of the entity",
		"app/Services/UserService.go:18: NewQuery is called on m, which is not a call of a constructor",
		"app/Services/UserService.go:24: NewInstance with attributes or an existing row has no one-line equivalent",
		"database/factories/UserFactory.go:20: factory.ForParent is the generic factory",
		"wrote nothing",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("the refusal does not say %q:\n%s", want, stderr.String())
		}
	}
	if after := snapshotTree(t, root); after != before {
		t.Error("model-upgrade refused and changed the tree anyway")
	}
}

// snapshotTree is every file of root and its contents, as one string.
func snapshotTree(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		b.WriteString(path + "\n" + string(body) + "\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// upgradeFixture runs the tool over a copy of testdata/<name>/before and
// compares the result with testdata/<name>/after.
func upgradeFixture(t *testing.T, name string, entities ...string) string {
	t.Helper()
	root := t.TempDir()
	after := filepath.Join(testdataDir(t), name, "after")
	copyTree(t, filepath.Join("testdata", name, "before"), root)
	t.Chdir(root)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"./..."}, &stdout, &stderr); code != 0 {
		t.Fatalf("model-upgrade exited %d:\n%s%s", code, stdout.String(), stderr.String())
	}
	// The closing instruction names the release this module is built against,
	// not the oldest one with the model core.
	if want := "go get github.com/arandu-io/hesape@" + gen.HesapeRelease + "\n"; !strings.Contains(stdout.String(), want) {
		t.Errorf("the closing instruction does not say %q:\n%s", want, stdout.String())
	}

	for _, entity := range entities {
		query := filepath.Join(root, "app", "Models", entity+"Query.go")
		body, err := os.ReadFile(query)
		if err != nil {
			t.Errorf("model:build did not write the query of %s: %v", entity, err)
			continue
		}
		if _, ok := gen.GeneratedQuerySource(body); !ok {
			t.Errorf("%s does not carry the header model:build writes", query)
		}
	}

	if *update {
		if err := os.RemoveAll(after); err != nil {
			t.Fatal(err)
		}
		copyTree(t, root, after, func(rel string) bool { return !strings.HasSuffix(rel, "Query.go") })
		return root
	}
	err := filepath.WalkDir(after, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(after, path)
		want, _ := os.ReadFile(path)
		got, readErr := os.ReadFile(filepath.Join(root, rel))
		if readErr != nil {
			t.Errorf("%s: %v", rel, readErr)
			return nil
		}
		if !bytes.Equal(want, got) {
			t.Errorf("%s is not what testdata/%s/after holds; run go test ./cmd/model-upgrade -update and read the diff\n--- got\n%s",
				rel, name, got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// hesape is how an upgraded tree reaches the hesape this module is built
// with: the version to require and the working tree to replace it with, if any.
type hesape struct{ version, dir string }

// modelCore resolves it, from this module -- before a test moves into the
// tree it upgrades, where the go command would answer for that tree instead.
func modelCore(t *testing.T) hesape {
	t.Helper()
	if testing.Short() {
		return hesape{}
	}
	listed, listErr := exec.Command("go", tests.HesapeQuery...).Output()
	version, dir := tests.ModelCore(t, listed, listErr)
	return hesape{version: version, dir: dir}
}

// compiles builds and vets the upgraded tree against the hesape this module is
// built with, which has to be one with the model core.
func compiles(t *testing.T, root string, core hesape) {
	t.Helper()
	if testing.Short() {
		t.Skip("compiles the upgraded tree: skipped under -short")
	}
	version, dir := core.version, core.dir
	gomod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(gomod), "\n")
	for i, line := range lines {
		if strings.Contains(line, "github.com/arandu-io/hesape ") {
			lines[i] = "\tgithub.com/arandu-io/hesape " + version
		}
	}
	text := strings.Join(lines, "\n")
	if dir != "" {
		text += "\nreplace github.com/arandu-io/hesape => " + dir + "\n"
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{{"mod", "download", "all"}, {"build", "-trimpath", "./..."}, {"vet", "-trimpath", "./..."}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local", "GOSUMDB=off")
		if args[0] != "mod" {
			cmd.Env = append(cmd.Env, "GOPROXY=off")
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			if args[0] == "mod" {
				t.Skipf("the upgraded tree was not compiled: its modules could not be resolved: %v\n%s", err, out)
			}
			t.Fatalf("go %s refuses the upgraded tree:\n%s", strings.Join(args, " "), out)
		}
	}
}

// fixtures is testdata, made absolute while the working directory is still
// this package's: the tests move into the module they upgrade.
var fixtures, _ = filepath.Abs("testdata")

func testdataDir(t *testing.T) string {
	t.Helper()
	return fixtures
}

// copyTree copies the regular files of from into to, those keep accepts.
func copyTree(t *testing.T, from, to string, keep ...func(rel string) bool) {
	t.Helper()
	if !filepath.IsAbs(from) {
		from = filepath.Join(testdataDir(t), strings.TrimPrefix(filepath.ToSlash(from), "testdata/"))
	}
	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		for _, k := range keep {
			if !k(filepath.ToSlash(rel)) {
				return nil
			}
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, body, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}
