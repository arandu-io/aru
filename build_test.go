package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildProducesABinaryAndItsChecksum: the artifact of doc 17 is one static
// binary with a checksum beside it, and a version that came from the tag rather
// than from a constant somebody edits.
func TestBuildProducesABinaryAndItsChecksum(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a project: skipped under -short")
	}

	framework, _ := siblingCheckouts(t)
	project := t.TempDir()

	writeFile(t, filepath.Join(project, "go.mod"), `module example.test/project

go 1.25

require github.com/arandu-io/framework v0.8.0

replace github.com/arandu-io/framework => `+framework+`
`)
	writeFile(t, filepath.Join(project, "main.go"), `package main

import "fmt"

// version is stamped by the build with -ldflags.
var version = "dev"

func main() { fmt.Println(version) }
`)
	writeFile(t, filepath.Join(project, "arandu.toml"), "name = \"test\"\n")

	chdir(t, project)
	var out, errOut strings.Builder
	if err := build([]string{"--version", "v9.9.9"}, &out, &errOut); err != nil {
		t.Fatalf("build: %v\n%s", err, errOut.String())
	}

	binary := filepath.Join(project, "bin", filepath.Base(project))
	info, err := os.Stat(binary)
	if err != nil {
		t.Fatalf("no binary at %s: %v", binary, err)
	}
	if info.Size() < 100_000 {
		t.Errorf("the binary is %d bytes, which is not a Go program", info.Size())
	}

	// The checksum is what a deploy verifies, and it has to be beside the
	// artifact rather than printed and lost.
	sumFile := binary + ".sha256"
	sum, err := os.ReadFile(sumFile)
	if err != nil {
		t.Fatalf("no checksum at %s: %v", sumFile, err)
	}
	if len(strings.Fields(string(sum))) != 2 {
		t.Errorf("the checksum file is not in shasum format: %q", sum)
	}
	if !strings.Contains(out.String(), "v9.9.9") {
		t.Errorf("the build did not report the version:\n%s", out.String())
	}

	// The version reaches the binary. A binary that reports the wrong version
	// sends people to the wrong changelog.
	reported := runBinary(t, binary)
	if strings.TrimSpace(reported) != "v9.9.9" {
		t.Errorf("the binary reports %q, want the stamped version", strings.TrimSpace(reported))
	}
}

// TestBuildingAnImageWithoutADockerfileSaysWhere: the error names what is
// missing and where to get it, rather than whatever docker prints about a
// context.
func TestBuildingAnImageWithoutADockerfileSaysWhere(t *testing.T) {
	project := t.TempDir()
	writeFile(t, filepath.Join(project, "go.mod"), "module example.test/project\n\ngo 1.25\n")
	writeFile(t, filepath.Join(project, "main.go"), "package main\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(project, "arandu.toml"), "name = \"test\"\n")

	chdir(t, project)
	var out, errOut strings.Builder
	err := build([]string{"--docker", "--skip-views"}, &out, &errOut)
	if err == nil {
		t.Fatal("an image was built without a Dockerfile")
	}
	// Either docker is missing on this machine, or the Dockerfile is -- both are
	// worth naming, and neither should be a stack trace.
	message := err.Error()
	if !strings.Contains(message, "Dockerfile") && !strings.Contains(message, "docker was not found") {
		t.Errorf("the error explains neither: %v", err)
	}
}

func runBinary(t *testing.T, path string) string {
	t.Helper()
	out, err := exec.Command(path).CombinedOutput()
	if err != nil {
		t.Fatalf("running the binary: %v\n%s", err, out)
	}
	return string(out)
}
