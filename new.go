package main

import (
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// skeletonRepo is what `aru new` clones. It is the equivalent of
// a project, not a library. Nobody depends on it, which is what
// lets the framework evolve without fighting the directory layout of older
// projects.
const (
	skeletonRepo    = "https://github.com/arandu-io/arandu.git"
	skeletonVersion = "v0.27.1"
)

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
	clone := exec.Command("git", "clone", "--branch", skeletonVersion, "--single-branch", "--depth", "1", "--quiet", skeletonRepo, name)
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
	if err := dropRetractions(name); err != nil {
		return err
	}
	if err := writeEnv(name); err != nil {
		return err
	}
	if err := writeEditorSettings(name); err != nil {
		return err
	}

	// The views are compiled here rather than left for the first command the
	// person runs. The skeleton's controllers import the package this writes,
	// so a project handed over without it does not build -- and "go build
	// ./..." is the first thing most people type after cd.
	//
	// A failure here is reported and not returned: the project on disk is
	// complete and correct, and what is missing is a build step the person can
	// run again once whatever stopped it -- usually the network, the first time
	// the stylesheet compiler is fetched -- is out of the way.
	if err := buildViews(name, io.Discard, stderr); err != nil {
		fmt.Fprintf(stderr, "\nThe project was created, but its views were not compiled: %v\nRun `aru view:build` inside %s before building.\n", err, name)
	}

	fmt.Fprint(stdout, createdMessage(name, path))
	return nil
}

// createdMessage is what `aru new` prints last: how to run the project, and
// what moving it to Postgres takes.
//
// The variable is DATABASE_URL, and it is spelled out rather than named,
// because the shape is the part nobody guesses. It used to say DB_CONNECTION.
// That is one of the variables that carried the connection in parts, and those
// are refused at boot rather than ignored -- so the last line of `aru new` told
// the reader to set the one value that stops the application from starting.
//
// The move is three lines and not one, because the skeleton links only the
// SQLite connector. An engine reaches the binary by importing its connector, so
// pointing DATABASE_URL at Postgres in a project that links none stops the
// boot. The message used to promise "one line in .env and nothing else", which
// held only while the skeleton linked every connector it might be asked for.
// The first two lines are the ones the refusal prints, word for word, so
// whoever skips them here reads them again there.
func createdMessage(name, modulePath string) string {
	return fmt.Sprintf(`
%s created, module %s.

    cd %s
    aru migrate
    aru db:seed
    aru serve

It runs on SQLite, in a file under database/. Nothing to install. Moving to
Postgres links its connector into the binary:

    go get github.com/arandu-io/hesape/database/connectors/pgx

blank-imports it in bootstrap/app.go, next to the SQLite one:

    _ "github.com/arandu-io/hesape/database/connectors/pgx"

and points DATABASE_URL at the server, in .env:

    DATABASE_URL=postgres://user:password@127.0.0.1:5432/dbname
`, name, modulePath, name)
}

// rewriteModulePath replaces the skeleton's module path with the project's, in
// go.mod and in every import that referenced it.
//
// Every Go file it changes is formatted again afterwards. The skeleton's
// imports are sorted with its own path among the other github.com paths, and a
// project path sorts wherever its first letter puts it -- so a textual rename
// leaves import groups out of order, and the project fails its own gofmt gate
// before anybody has edited a line. See formatRenamed for what is left alone.
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
		updated := []byte(strings.ReplaceAll(string(content), oldPath, newPath))
		if rel, err := filepath.Rel(dir, p); err == nil {
			updated = formatRenamed(filepath.ToSlash(rel), updated)
		}
		return os.WriteFile(p, updated, 0o644)
	})
}

// formatRenamed formats a renamed Go file the way gofmt would, imports sorted.
//
// Two kinds of file are returned as they are. A view source ends in .go and is
// not Go until `aru view:build` compiles it, so a formatter would refuse it at
// the first directive. A file under testdata/ is a fixture, and a fixture may be
// malformed on purpose -- reformatting it would change what it tests.
//
// A file that does not parse is returned as it is too. The rename is still
// right, and refusing to create the project over it would leave half a project
// on disk; gofmt in that project then names the file, which is the report it
// needs.
func formatRenamed(rel string, content []byte) []byte {
	if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, ".kyse.go") {
		return content
	}
	for _, segment := range strings.Split(rel, "/") {
		if segment == "testdata" {
			return content
		}
	}
	formatted, err := format.Source(content)
	if err != nil {
		return content
	}
	return formatted
}

// dropRetractions removes the skeleton's retract directives from the new project's go.mod.
//
// A retraction is what a module says about its own published versions. The
// project has published none, so a retraction copied from the skeleton is a claim
// about versions the project never had, and every new project would carry it.
func dropRetractions(dir string) error {
	path := filepath.Join(dir, "go.mod")
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var kept []string
	inBlock := false
	for _, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case inBlock:
			inBlock = trimmed != ")"
			continue
		case strings.HasPrefix(trimmed, "retract ("), strings.HasPrefix(trimmed, "retract("):
			inBlock = !strings.HasSuffix(trimmed, ")")
			continue
		case trimmed == "retract", strings.HasPrefix(trimmed, "retract "), strings.HasPrefix(trimmed, "retract\t"):
			continue
		}
		kept = append(kept, line)
	}
	updated := strings.Join(kept, "\n")
	for strings.Contains(updated, "\n\n\n") {
		updated = strings.ReplaceAll(updated, "\n\n\n", "\n\n")
	}
	if updated == string(content) {
		return nil
	}
	return os.WriteFile(path, []byte(updated), 0o644)
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

// editorSettings is the project-local configuration that lets an installed
// editor adapter recognize Kyse sources without treating generated views as
// editable files.
//
// It is embedded rather than copied from a template file, because it has to
// reach a project created on a machine that has only the `aru` binary.
//
//go:embed editors/vscode/settings.json
var editorSettings []byte

// writeEditorSettings drops the editor configuration into the new project.
//
// A `.kyse.go` is not Go. Without the association, gopls parses it, marks every
// directive as a syntax error, and the file is red while being correct -- which
// is the first impression somebody gets of the view layer.
//
// A generated project receives only these settings. The installable adapter,
// including its grammar, language configuration and `aru lsp` client, lives in
// its own repository so editor packaging is not a dependency of this binary or
// of the applications it creates.
func writeEditorSettings(dir string) error {
	vscode := filepath.Join(dir, ".vscode")
	if err := os.MkdirAll(vscode, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", vscode, err)
	}
	path := filepath.Join(vscode, "settings.json")
	if err := os.WriteFile(path, editorSettings, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
