package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/arandu-io/aru/internal/kyse"
	"github.com/arandu-io/aru/internal/toolchain"
)

// stylesheetSource is where a project keeps the stylesheet it compiles, in the
// place somebody looks for it first.
const stylesheetSource = "resources/css/app.css"

// stylesheetOutput sits next to the code that embeds it, so `go:embed` finds it
// and the deploy stays one binary -- no asset publishing step, no CDN.
const stylesheetOutput = "assets/app.css"

// componentLibrary is the module whose Go files carry class names that no view
// of the project spells out.
//
// One name and not a list: kyse is the component library -- imported, never
// copied -- and a second one would be a second way to draw a button.
const componentLibrary = "github.com/arandu-io/kyse"

// viewBuild turns every `.kyse.go` into Go and, when the project has its own
// stylesheet, compiles the CSS.
//
// The view compiler is kyse, which is part of this CLI; the CSS is the Tailwind
// standalone binary aru downloads and verifies. Neither is Node, and neither is
// optional at deploy time: what they produce is committed, so the server only
// ever runs `go build`.
func viewBuild(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("view:build", flag.ContinueOnError)
	flags.SetOutput(stderr)
	// There is deliberately no --watch here. `aru dev` is the loop, and a second
	// watch implementation is the second way to do one thing.
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("view:build: %w", err)
	}

	// A module root, not necessarily a project: a component library has views
	// and no main.go. See viewsIn.
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	return buildViews(root, stdout, stderr)
}

func buildViews(root string, stdout, stderr io.Writer) error {
	pins, err := toolchain.ReadPins(root)
	if err != nil {
		return err
	}
	// On stderr, so `aru view:build > log` keeps the warning where a person sees
	// it and out of what the command produced.
	pins.Warn(stderr)

	// The warning above asks the reader to remove the line; this removes the
	// bytes that line used to download. Nothing else does: the cached name
	// carries the version, so a tool nobody pins any more is never overwritten.
	//
	// Reported and not fatal. A stale binary that will not delete is untidy, and
	// refusing to build the project over it would trade a real failure for a
	// cosmetic one.
	if err := toolchain.Sweep(stderr); err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
	}

	// Before anything is compiled. An old CLI refuses correct views one message
	// per line, and none of those messages says the CLI is what is old -- so
	// this has to be the first thing the person reads, not the sixty-first.
	if err := toolchain.CheckVersion(Version(), pins.Aru); err != nil {
		return err
	}

	// kyse is part of this CLI, not a binary it downloads. One fewer thing to
	// pin, verify and cache -- and the view compiler moving in lockstep with the
	// generator that writes views is what keeps them from drifting.
	if err := compileViews(root, stdout); err != nil {
		return err
	}

	if !hasStylesheet(root) {
		// A project with no stylesheet of its own has nothing to compile, and
		// the framework serves its embedded one in place of it.
		//
		// That fallback is the base layer and nothing else: a reset, the theme
		// tokens and the two rules the framework's own pages need. It carries
		// no utilities, because the stylesheet declares its sources and the
		// framework has no views to declare -- they belong to the project.
		//
		// So a project that deleted resources/css/app.css gets a page with the
		// base layer and no utility classes. That is the honest outcome of
		// deleting your stylesheet, and it is not a silent one: what is missing
		// is every class the views actually use.
		return nil
	}

	tailwind, err := toolchain.Tailwind(pins.Tailwind).Ensure(stdout)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(stylesheetOutput)), 0o755); err != nil {
		return err
	}
	input, err := stylesheetInput(root)
	if err != nil {
		return err
	}
	// The entry comes in on stdin rather than as a path, because what Tailwind
	// compiles is the project's stylesheet plus a line the project never wrote.
	// Writing that line into a file first would leave a generated stylesheet in
	// the tree for somebody to edit or commit.
	args := []string{"--input", "-", "--output", stylesheetOutput, "--minify"}
	if err := runTool(root, tailwind, args, input, stdout, stderr); err != nil {
		return fmt.Errorf("tailwindcss: %w", err)
	}

	fmt.Fprintf(stdout, "wrote %s\n", stylesheetOutput)
	return nil
}

// stylesheetInput is the CSS handed to Tailwind: the project's stylesheet, plus
// the directory of the component library it imports.
//
// Tailwind only reads class names out of files an @source names, and a project's
// app.css can only name directories of the project. The components are an
// imported module whose source lives in the module cache, so every
// class kyse renders and no view of the project repeats was absent from the
// compiled stylesheet -- silently, because missing CSS is not an error anywhere.
// Measured on this repository: 193 KB of built stylesheet with zero occurrences
// of .text-destructive, which components.Field puts on the error line of every
// form, so a rejected sign-in drew its message in the body colour; and zero of
// .size-3 and .rounded-full, which are the theme picker's colour swatches, so
// they were spans of no size at all.
//
// The line is prepended here instead of being added to resources/css/app.css
// because that file is written once by `aru new` and then belongs to the
// project. A fix that lives there reaches the projects created after it and no
// others, and every project that already exists would have to be told to edit a
// stylesheet it has never opened.
//
// Two other shapes were weighed. kyse could ship a compiled utility layer of its
// own, imported beside the component CSS -- that is a second stylesheet, with a
// second Tailwind version and a second copy of the theme tokens, and the pair
// drifts. Or the class names could be listed by hand in
// `@source inline(...)` next to the library -- that is this bug with one extra
// step, because a hand-kept list goes stale the first time somebody adds a class
// and says nothing when it does. Reading the directory costs one `go list` and
// cannot go stale: whatever kyse ships is what Tailwind reads.
func stylesheetInput(root string) (string, error) {
	dir, err := componentLibraryDir(root)
	if err != nil {
		return "", err
	}

	// The project's stylesheet is imported by absolute path, and the relative
	// @source and @import lines inside it keep resolving against its own
	// directory: Tailwind resolves those per file, not against the entry.
	input := "@import " + cssString(filepath.Join(root, stylesheetSource)) + ";\n"
	if dir != "" {
		// Every .go under the module rather than a list of its directories. The
		// generated component sits beside its .kyse.go source and both carry the
		// class names, and a glob naming today's directories is the stale list
		// this function exists to avoid.
		input += "@source " + cssString(filepath.Join(dir, "**", "*.go")) + ";\n"
	}
	return input, nil
}

// componentLibraryDir is where the imported component library's Go files are on
// this machine: the module cache, a working copy a replace or a go.work points
// at, or the project's own vendor directory.
//
// An empty path has exactly one meaning here, and it is a legitimate answer: the
// project does not require the component library at all. It renders none of
// those classes, and asking for a directory would turn "I write my own markup"
// into a build failure.
//
// Every other way of leaving without a directory is a failure and is reported as
// one, because silence is the defect this file exists to close and reading `go
// list` loosely reopens it somewhere else. Two projects proved that, and both
// were read as "you do not depend on kyse": one that had run `go mod vendor`,
// where the source sits under vendor/ and `go list -m` reports no directory for
// any module at all; and the first build on a machine with no network, where it
// reports no directory and a reason. Each wrote a stylesheet without one
// component class in it and exited 0 -- the original bug, through a new door.
//
// What tells the cases apart is the version, not the directory and not whether
// the command failed. A module the project requires has one, from go.mod or from
// vendor/modules.txt, and neither needs the network; a module it does not
// require has none. The directory is checked first because a go.work `use`
// resolves to a directory with no version at all.
func componentLibraryDir(root string) (string, error) {
	if _, err := exec.LookPath("go"); err != nil {
		return "", errors.New("the go toolchain was not found in PATH, and aru needs it to find the component library the stylesheet is compiled against")
	}

	dir, version, err := listComponentLibrary(root)
	if err != nil {
		return "", err
	}

	if dir == "" {
		if version == "" {
			return "", nil
		}

		// `go mod vendor` copies the source into the project, and from then on
		// there is no module cache for `go list -m` to point at. What the
		// compiler reads is the copy under vendor/, so that is what Tailwind has
		// to read: anything else describes a different build than the one that
		// ships.
		vendored := filepath.Join(root, "vendor", filepath.FromSlash(componentLibrary))
		if exists(vendored) {
			return vendored, nil
		}

		// Required, but never extracted: the state of every fresh clone, because
		// `go list -m` reports an empty directory and does not download.
		// Fetching it here is what keeps `git clone && aru dev` true.
		if _, err := goOutput(root, "mod", "download", componentLibrary); err != nil {
			return "", fmt.Errorf("downloading %s, whose components the stylesheet has to see: %w", componentLibrary, err)
		}
		if dir, _, err = listComponentLibrary(root); err != nil {
			return "", err
		}
		if dir == "" {
			return "", fmt.Errorf("this project requires %s and its source is not on this machine, so the stylesheet would compile without a single class the components render", componentLibrary)
		}
	}

	// A replace pointing at a directory that was moved or never existed still
	// comes back as a path. Tailwind matches no file under it, reports nothing,
	// and the page loses the same classes as before -- so the path is confirmed
	// here rather than after somebody notices the colour is wrong.
	if !exists(dir) {
		return "", fmt.Errorf("%s resolves to %s and there is nothing there, so the stylesheet would compile without a single class the components render", componentLibrary, dir)
	}
	return dir, nil
}

// listComponentLibrary asks the go command where the component library is and
// which version of it this project requires.
//
// -e is what makes the answer readable: without it the command exits non-zero
// for a module it cannot place, and an exit status cannot say whether the module
// was unwanted or merely unreachable. With it both come back as fields.
//
// The two fields are independent. A vendored module has a version and no
// directory, a workspace module has a directory and no version, and a module the
// project does not require has neither -- so neither one alone answers "does
// this project render kyse's classes".
func listComponentLibrary(root string) (dir, version string, err error) {
	out, err := goOutput(root, "list", "-m", "-e", "-f", "{{.Dir}}\n{{.Version}}", componentLibrary)
	if err != nil {
		return "", "", fmt.Errorf("asking the go command where %s is: %w", componentLibrary, err)
	}
	// Split before trimming, not after: an absent directory leaves the first
	// line empty, and trimming the leading newline away first would slide the
	// version into the path.
	dir, version, _ = strings.Cut(out, "\n")
	return strings.TrimSpace(dir), strings.TrimSpace(version), nil
}

// goOutput runs the go command in dir and returns what it wrote on stdout,
// exactly as written.
//
// The go command explains a failure on stderr, and exec turns that failure into
// "exit status 1" -- so a build that stopped because a module could not be
// fetched said the exit status of a program the person did not know was
// running. The explanation is folded into the error instead.
func goOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if message := strings.TrimSpace(stderr.String()); message != "" {
			return "", fmt.Errorf("go %s: %s", strings.Join(args, " "), message)
		}
		return "", fmt.Errorf("go %s: %w", strings.Join(args, " "), err)
	}
	return stdout.String(), nil
}

// cssString quotes a filesystem path so it can be used in CSS.
//
// The separator is forced to "/" because a Windows path is full of backslashes
// and a backslash inside a CSS string is an escape character: C:\Users\… would
// reach Tailwind as C:Users… and match no file, which is a stylesheet missing
// every component class on one operating system and correct on the other two.
func cssString(path string) string {
	return `"` + filepath.ToSlash(path) + `"`
}

func runTool(dir, binary string, args []string, stdin string, stdout, stderr io.Writer) error {
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func hasStylesheet(root string) bool {
	_, err := os.Stat(filepath.Join(root, stylesheetSource))
	return !errors.Is(err, fs.ErrNotExist)
}

// skipDir is the set of directories nothing inside is ever an input.
//
// storage/ is the one that matters and the one that is easy to miss: framework/
// views under it is build output, and framework/cache, framework/sessions and
// app/{public,private} are RUNTIME output. Watched, an upload named theme.css
// made the request the developer had just issued restart the process that
// served it, and re-run Tailwind. bin/, tmp/ and .arandu/ are gitignored build
// and tool output for the same reason.
func skipDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "testdata", "storage", "bin", "tmp", ".arandu":
		return true
	}
	return false
}

// ensureViews compiles the view layer when its output is not there yet.
//
// It answers one question -- does what the project imports exist -- and it is
// not a second build path: what it runs is buildViews, the same one `aru
// view:build` runs. The distinction that keeps it from becoming one is that it
// never rebuilds anything that is merely out of date. Staleness is what `aru
// dev` watches for; absence is what stops the project compiling at all.
//
// Absence is the normal state twice: after `aru new`, and after every clone,
// because both the compiled views and the compiled stylesheet are gitignored
// build output that a go:embed and a handful of imports refer to by name.
func ensureViews(root string, stderr io.Writer) error {
	sources, err := findViews(viewsIn(root))
	if err != nil || len(sources) == 0 {
		// No views is a legitimate shape -- an API with no HTML -- and a project
		// that cannot be walked has a problem this is not the place to report.
		return nil
	}

	missing := ""
	for _, source := range sources {
		if _, err := os.Stat(kyse.OutputPath(source)); errors.Is(err, fs.ErrNotExist) {
			missing = source
			break
		}
	}
	if missing == "" && (!hasStylesheet(root) || exists(filepath.Join(root, stylesheetOutput))) {
		return nil
	}

	// On stderr: this is the command explaining itself, not what the command
	// produced, and `aru routes > routes.txt` should not collect it.
	fmt.Fprintln(stderr, "the view layer has not been compiled yet; building it once")
	return buildViews(root, stderr, stderr)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
