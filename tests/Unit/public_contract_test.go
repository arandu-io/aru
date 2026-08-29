package unit_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/tests"
)

// TestThePublicModuleContractIsModelFirst keeps the three first-use surfaces on
// the architecture the generator actually emits. The README is what somebody
// reads before installing the CLI, help is what they read after installing it,
// and make:model is where the boundary between an entity and a full feature is
// taught. A Repository can still be written for a complex query, but none of
// these surfaces may promise that CRUD generation writes one.
func TestThePublicModuleContractIsModelFirst(t *testing.T) {
	root := tests.Root(t)

	t.Run("README", func(t *testing.T) {
		body, err := os.ReadFile(filepath.Join(root, "README.md"))
		if err != nil {
			t.Fatal(err)
		}
		text := oneLine(string(body))
		want := "it generates a full module — Model-backed entity, policy, service, request, controller, migration, screens"
		if !strings.Contains(text, want) {
			t.Errorf("the README does not say %q", want)
		}
		for _, stale := range []string{"entity, policy, repository", "with its policy, repository"} {
			if strings.Contains(strings.ToLower(text), stale) {
				t.Errorf("the README still promises the Repository-first module shape in %q", stale)
			}
		}
	})

	binary := buildPublicContractCLI(t, root)

	t.Run("help", func(t *testing.T) {
		out := runPublicContractCLI(t, root, binary, "help")
		line := lineContaining(out, "make:module")
		want := "generate a full module: Model-backed entity, policy, service, request, controller, migration, screens, tests"
		if !strings.Contains(oneLine(line), want) {
			t.Errorf("aru help does not say %q:\n%s", want, line)
		}
		if strings.Contains(strings.ToLower(line), "repository") {
			t.Errorf("aru help still lists a generated Repository:\n%s", line)
		}
	})

	t.Run("make model", func(t *testing.T) {
		project := t.TempDir()
		for name, body := range map[string]string{
			"go.mod":      "module example.test/project\n\ngo 1.26\n",
			"main.go":     "package main\n\nfunc main() {}\n",
			"arandu.toml": "name = \"project\"\n",
		} {
			if err := os.WriteFile(filepath.Join(project, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}

		out := runPublicContractCLI(t, project, binary,
			"make:model", "Invoice", "--fields", "reference:string")
		text := oneLine(out)
		for _, want := range []string{
			"A model here is data plus its Hesape Model entry point.",
			"Application use cases reach that persistence through a Service, after a Policy issues a security.Grant.",
			"a Model-backed entity, policy, service, request, controller, migration, four screens and test",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("make:model does not say %q:\n%s", want, out)
			}
		}
		for _, stale := range []string{
			"except a repository",
			"policy, repository",
			"already has a repository",
			"service, the repository",
		} {
			if strings.Contains(strings.ToLower(text), stale) {
				t.Errorf("make:model still teaches the Repository-first path in %q:\n%s", stale, out)
			}
		}
	})
}

func buildPublicContractCLI(t *testing.T, root string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "aru")
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building aru: %v\n%s", err, out)
	}
	return binary
}

func runPublicContractCLI(t *testing.T, dir, binary string, args ...string) string {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("aru %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func lineContaining(s, needle string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}
