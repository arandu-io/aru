package main

import (
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// skeletonRepo is what `aru new` clones. It is the equivalent of
// laravel/laravel: a project, not a library. Nobody depends on it, which is what
// lets the framework evolve without fighting the directory layout of older
// projects.
const skeletonRepo = "https://github.com/arandu-io/arandu.git"

// newProject creates a project from the skeleton.
//
// It clones, drops the skeleton's git history, rewrites the module path, and
// writes a .env with a fresh key. What it does NOT do is run `go mod tidy` or
// start anything: a command that reaches the network twice and starts a server
// is a command that fails in three different ways.
func newProject(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	fs.SetOutput(stderr)
	modulePath := fs.String("module", "", "the Go module path (default: the project name)")

	var name string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("new: %w", err)
	}
	if name == "" {
		return fmt.Errorf("usage: aru new <name> [--module github.com/you/name]")
	}
	if strings.ContainsAny(name, `/\ `) {
		return fmt.Errorf("the project name %q cannot contain a path separator or a space", name)
	}

	if _, err := os.Stat(name); err == nil {
		return fmt.Errorf("%s already exists", name)
	}
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git was not found in PATH, and aru needs it to fetch the skeleton")
	}

	path := *modulePath
	if path == "" {
		path = name
	}

	fmt.Fprintf(stdout, "fetching the skeleton\n")
	clone := exec.Command("git", "clone", "--depth", "1", "--quiet", skeletonRepo, name)
	clone.Stderr = stderr
	if err := clone.Run(); err != nil {
		return fmt.Errorf("cloning the skeleton: %w", err)
	}

	// The skeleton's history is not the project's history.
	if err := os.RemoveAll(filepath.Join(name, ".git")); err != nil {
		return fmt.Errorf("removing the skeleton history: %w", err)
	}

	if err := rewriteModulePath(name, path); err != nil {
		return err
	}
	if err := writeEnv(name); err != nil {
		return err
	}

	fmt.Fprintf(stdout, `
%s created, module %s.

    cd %s
    aru migrate
    aru db:seed
    aru serve

It runs on SQLite, in a file under database/. Nothing to install. Moving to
Postgres is DB_CONNECTION in .env and nothing else.
`, name, path, name)
	return nil
}

// rewriteModulePath replaces the skeleton's module path with the project's, in
// go.mod and in every import that referenced it.
func rewriteModulePath(dir, newPath string) error {
	const oldPath = "github.com/arandu-io/arandu"
	if newPath == oldPath {
		return nil
	}

	return filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if ext := filepath.Ext(p); ext != ".go" && d.Name() != "go.mod" {
			return nil
		}

		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if !strings.Contains(string(content), oldPath) {
			return nil
		}
		updated := strings.ReplaceAll(string(content), oldPath, newPath)
		return os.WriteFile(p, []byte(updated), 0o644)
	})
}

// writeEnv copies .env.example to .env with a fresh APP_KEY.
//
// Generating the key here rather than telling the user to run another command is
// the difference between four steps and five -- and the key is per project, so
// there is no reason to make it a decision.
func writeEnv(dir string) error {
	example, err := os.ReadFile(filepath.Join(dir, ".env.example"))
	if err != nil {
		return fmt.Errorf("reading .env.example: %w", err)
	}

	key := make([]byte, appKeyLen)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("reading random bytes: %w", err)
	}

	env := strings.Replace(string(example), "APP_KEY=",
		"APP_KEY=base64:"+base64.StdEncoding.EncodeToString(key), 1)
	return os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o600)
}
