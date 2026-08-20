package manifest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arandu-io/aru/internal/manifest"
)

func TestReadsWhatAModuleDeclares(t *testing.T) {
	dir := write(t, `# what this module does
name = "acme/crm"
framework = ">= 0.3"
profiles = ["conventional", "performance"]

[permissions]
network = true
filesystem = false
exec = false
migrations = true
`)

	m, err := manifest.Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if m.Name != "acme/crm" || m.Framework != ">= 0.3" {
		t.Fatalf("manifest = %+v", m)
	}
	if len(m.Profiles) != 2 || m.Profiles[0] != "conventional" {
		t.Fatalf("profiles = %v", m.Profiles)
	}
	if !m.Permissions.Network || m.Permissions.Filesystem || m.Permissions.Exec || !m.Permissions.Migrations {
		t.Fatalf("permissions = %+v", m.Permissions)
	}
}

// TestAMissingFileIsNotAnError: the doctor reports the absence with a message
// that says what to do, which is more useful than the reader refusing to run.
func TestAMissingFileIsNotAnError(t *testing.T) {
	m, err := manifest.Read(t.TempDir())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if m != nil {
		t.Fatalf("a module with no manifest returned %+v", m)
	}
}

// TestAMisspelledPermissionIsAnError: silently ignored, it reads as declared and
// is not -- which is exactly the failure this file exists to prevent.
func TestAMisspelledPermissionIsAnError(t *testing.T) {
	dir := write(t, "name = \"acme/crm\"\n\n[permissions]\nnetwrok = true\n")

	if _, err := manifest.Read(dir); err == nil {
		t.Fatal("a misspelled permission was accepted")
	}
}

func TestANonBooleanPermissionIsAnError(t *testing.T) {
	dir := write(t, "name = \"acme/crm\"\n\n[permissions]\nnetwork = \"yes\"\n")

	if _, err := manifest.Read(dir); err == nil {
		t.Fatal("a permission that is not a boolean was accepted")
	}
}

// TestTheNameIsRequired: without it the registry has nothing to list the module
// under, and the file is metadata about nothing.
func TestTheNameIsRequired(t *testing.T) {
	dir := write(t, "framework = \">= 0.3\"\n")

	if _, err := manifest.Read(dir); err == nil {
		t.Fatal("a manifest with no name was accepted")
	}
}

func write(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, manifest.Name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
