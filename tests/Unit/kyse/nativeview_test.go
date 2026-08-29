package kyse_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestViewBuildProducesAProjectThatImportsNativeViewDirectly exercises the
// command rather than Generate alone. It compiles a real view into storage and
// then hands the whole temporary project to the Go toolchain, which is the
// boundary where an import written inside generated code becomes observable.
func TestViewBuildProducesAProjectThatImportsNativeViewDirectly(t *testing.T) {
	tool := goTool(t)
	repository, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "aru")

	buildAru := exec.Command(tool, "build", "-o", binary, ".")
	buildAru.Dir = repository
	buildAru.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOTOOLCHAIN=local")
	if out, err := buildAru.CombinedOutput(); err != nil {
		t.Fatalf("building aru from the task checkout: %v\n%s", err, out)
	}

	root := t.TempDir()
	writeStubModule(t, root)
	writeFile(t, filepath.Join(root, "resources", "views", "home.kyse.go"), explicitlyImportedNativeView)

	viewBuild := exec.Command(binary, "view:build")
	viewBuild.Dir = root
	viewBuild.Env = append(os.Environ(), "GOWORK=off")
	if out, err := viewBuild.CombinedOutput(); err != nil {
		t.Fatalf("aru view:build: %v\n%s", err, out)
	}

	generatedPath := filepath.Join(root, "storage", "framework", "views", "home.go")
	generated, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), `kyse__view "github.com/arandu-io/hesape/view"`) {
		t.Fatalf("the real view build did not import the native view package:\n%s", generated)
	}
	if strings.Contains(string(generated), `"github.com/arandu-io/framework/view"`) {
		t.Fatalf("the real view build still emitted the Framework bridge:\n%s", generated)
	}

	buildProject := exec.Command(tool, "build", "./...")
	buildProject.Dir = root
	buildProject.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off", "GOTOOLCHAIN=local")
	if out, err := buildProject.CombinedOutput(); err != nil {
		t.Fatalf("the project produced by the real view build does not compile: %v\n%s", err, out)
	}
}
