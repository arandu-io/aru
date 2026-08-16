package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// build compiles the application into one static binary.
//
// One artifact: no runtime, no vendor directory, no node_modules, no web server
// required in front. This is the command that produces it -- CGO off, assets
// already embedded, templates already compiled.
//
// The version and the commit go in through -ldflags rather than a constant
// somebody edits, because a binary that reports the wrong version sends people
// to the wrong changelog.
func build(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("build", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "", "where to write the binary (default: bin/<project>)")
	version := flags.String("version", "", "the version to stamp in (default: the git tag or commit)")
	docker := flags.Bool("docker", false, "build a container image instead of a binary")
	tag := flags.String("tag", "", "the image tag (default: <project>:<version>)")
	skipViews := flags.Bool("skip-views", false, "do not run view:build first")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("build: %w", err)
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}
	name := filepath.Base(root)

	stamp := *version
	if stamp == "" {
		stamp = describe(root)
	}

	// Views first. A binary built from Go generated before the last edit to a
	// `.kyse.go` is a binary that serves yesterday's page, and finding that out
	// in production is expensive for something a build step prevents.
	if !*skipViews {
		if err := buildViews(root, stdout, stderr); err != nil {
			return err
		}
	}

	if *docker {
		return buildImage(root, name, stamp, *tag, stdout, stderr)
	}
	return buildBinary(root, name, stamp, *output, stdout, stderr)
}

func buildBinary(root, name, version, output string, stdout, stderr io.Writer) error {
	if output == "" {
		output = filepath.Join("bin", name)
	}
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(output)), 0o755); err != nil {
		return err
	}

	// -s -w drops the symbol table and DWARF: about 25% off the binary, and
	// nothing production needs -- a panic still carries file and line, which is
	// what the error page reads.
	ldflags := fmt.Sprintf("-s -w -X main.version=%s -X main.commit=%s", version, commit(root))

	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", output, appPackage)
	cmd.Dir = root
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// CGO off is what makes the binary static, and what lets a distroless image
	// run it. The SQLite driver in the skeleton is pure Go for this reason.
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build failed")
	}

	sum, size, err := checksum(filepath.Join(root, output))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, output+".sha256"),
		[]byte(sum+"  "+filepath.Base(output)+"\n"), 0o644); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "%s  %s  %.1f MB\n%s\n", output, version, float64(size)/(1<<20), sum)
	return nil
}

func buildImage(root, name, version, tag string, stdout, stderr io.Writer) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker was not found in PATH")
	}
	if _, err := os.Stat(filepath.Join(root, "Dockerfile")); err != nil {
		return fmt.Errorf("this project has no Dockerfile.\n" +
			"`aru new` writes one; copy it from github.com/arandu-io/arandu")
	}
	if tag == "" {
		tag = name + ":" + version
	}

	cmd := exec.Command("docker", "build",
		"--build-arg", "VERSION="+version,
		"--build-arg", "COMMIT="+commit(root),
		"-t", tag, ".")
	cmd.Dir = root
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build failed")
	}
	fmt.Fprintf(stdout, "\n%s\n", tag)
	return nil
}

// describe returns the version to stamp: the git tag when the tree is on one,
// otherwise the short commit with a -dirty suffix when there are changes.
//
// A binary built from a dirty tree that reports a clean tag is how a bug gets
// investigated against the wrong source.
func describe(root string) string {
	if out, err := git(root, "describe", "--tags", "--always", "--dirty"); err == nil && out != "" {
		return out
	}
	return "dev"
}

func commit(root string) string {
	if out, err := git(root, "rev-parse", "--short", "HEAD"); err == nil {
		return out
	}
	return "unknown"
}

func git(root string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func checksum(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), size, nil
}
