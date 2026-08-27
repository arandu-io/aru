package main

import (
	"crypto/rand"
	_ "embed"
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
// a project, not a library. Nobody depends on it, which is what
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

	// The variable is DATABASE_URL, and it is spelled out rather than named,
	// because the shape is the part nobody guesses.
	//
	// It used to say DB_CONNECTION. That is one of the variables that carried
	// the connection in parts, and those are refused at boot rather than
	// ignored -- so the last line of `aru new` told the reader to set the one
	// value that stops the application from starting, and the refusal they got
	// named the variable this command had just recommended.
	//
	// "And nothing else" is the whole claim, and it holds: the skeleton
	// registers the Postgres and SQLite connectors alike, so the engine changes
	// without a line of Go.
	fmt.Fprintf(stdout, `
%s created, module %s.

    cd %s
    aru migrate
    aru db:seed
    aru serve

It runs on SQLite, in a file under database/. Nothing to install. Moving to
Postgres is one line in .env and nothing else:

    DATABASE_URL=postgres://user:password@127.0.0.1:5432/dbname
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

	// The administrator the seeder creates.
	//
	// `aru new` prints `aru db:seed` as the third step, and the seeder refuses
	// without these two -- correctly, because an administrator with a default
	// password is an administrator everyone has. The refusal was right and the
	// instruction was wrong: nothing told the reader the variables existed, and
	// nothing wrote them anywhere.
	//
	// The password is generated per project rather than shipped as a constant,
	// for the same reason APP_KEY is. The file is 0600 and gitignored.
	password := make([]byte, 18)
	if _, err := rand.Read(password); err != nil {
		return fmt.Errorf("reading random bytes: %w", err)
	}
	env = strings.Replace(env, "ARANDU_ADMIN_EMAIL=", "ARANDU_ADMIN_EMAIL=admin@example.test", 1)
	env = strings.Replace(env, "ARANDU_ADMIN_PASSWORD=",
		"ARANDU_ADMIN_PASSWORD="+base64.RawURLEncoding.EncodeToString(password), 1)

	return os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o600)
}

// editorSettings is what a project's .vscode/settings.json needs so the editor
// stops reporting errors in files that are correct.
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
// It is settings, not an extension: settings need no manifest and therefore no
// package.json, which a project never carries. The grammar that would make the
// highlighting fine lives in aru/editors/vscode/ and is not published yet --
// see the README there for the decision that is open.
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
