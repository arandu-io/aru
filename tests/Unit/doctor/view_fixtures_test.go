package doctor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	nativeViewImport = `"github.com/arandu-io/hesape/view"`
	viewBridgeImport = `"github.com/arandu-io/framework/view"`
)

// TestViewFixturesUseTheNativeRuntime keeps the projects the Doctor reads on
// the same import boundary as a project compiled today. The explicit file list
// prevents a scan with no relevant imports from passing vacuously; the walk
// keeps a stale generated copy elsewhere in the fixture from hiding.
func TestViewFixturesUseTheNativeRuntime(t *testing.T) {
	expected := []struct {
		name  string
		files []string
	}{
		{name: "clean", files: []string{
			"main.go",
			"resources/views/invoices/index.go",
			"resources/views/invoices/row.go",
			"resources/views/layouts/app.go",
			"storage/framework/views/invoices/index.go",
			"storage/framework/views/invoices/row.go",
			"storage/framework/views/layouts/app.go",
		}},
		{name: "gaps", files: []string{
			"bootstrap/app.go",
			"resources/views/reports/index.go",
			"storage/framework/views/reports/index.go",
		}},
		{name: "violations", files: []string{
			"resources/views/billing/index.go",
			"storage/framework/views/billing/index.go",
		}},
	}

	for _, project := range expected {
		t.Run(project.name, func(t *testing.T) {
			root := fixture(t, project.name)
			for _, rel := range project.files {
				body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(body), nativeViewImport) {
					t.Errorf("%s does not import the native view runtime", rel)
				}
			}

			err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
				if err != nil || entry.IsDir() {
					return err
				}
				body, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				rel, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				if strings.Contains(string(body), viewBridgeImport) {
					t.Errorf("%s still imports the view bridge", filepath.ToSlash(rel))
				}
				if strings.Contains(string(body), "UnsafeText") {
					t.Errorf("%s still calls the bridge-only UnsafeText", filepath.ToSlash(rel))
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
