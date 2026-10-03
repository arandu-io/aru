// Command model-upgrade moves an application or a module from the generic
// model of hesape to the concrete one, once.
//
//	go run github.com/arandu-io/aru/cmd/model-upgrade@latest ./...
//
// It reads the source and never compiles it, because the code it reads is
// written against a hesape it is about to stop compiling with. What it
// rewrites is the mechanical part:
//
//   - a constructor returning *model.Model[X] becomes
//     var xTable = model.NewTable(model.TableSpec{...}), from the table name,
//     UseUniqueIDs and the settings it assigns (PrimaryKey, KeyType,
//     Incrementing, Timestamps, UpdatedAtColumn, TenantColumn, SoftDeletes,
//     PerPage) and the events it registers
//   - model.Model[X] embedded in X becomes model.Model, and an entity that
//     embedded nothing gets it
//   - model.Builder[X] becomes XQuery, model.Collection[X] XCollection and
//     factories.Factory[X] *factories.XFactory
//   - .NewQuery() and .Query() on a chain that starts at a constructor go, and
//     .NewInstance(nil, false) becomes .New(), with .Entity dropped from what
//     it returned
//   - a factory constructor becomes the typed factory model:build renders, with
//     its definition and everything else in the file moved into the custom block
//   - a call of a constructor or a factory whose name changes is renamed
//
// and then it runs model:build, which writes the query file beside each
// entity. Anything it cannot read as one of those shapes stops it before a
// single file is written, with the file and line of every such place, so a
// run either rewrites everything it was given or nothing. What is left after
// a successful run is what the compiler reports: the methods whose results
// changed shape, and the code that leaned on the generic types.
//
// It is not one of aru's commands. It runs once per repository, against a
// tree that does not build yet, and a command kept in the table forever for a
// migration done once would be a second way to write models.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/arandu-io/aru/internal/gen"
	"github.com/arandu-io/aru/internal/modelbuild"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the whole command, so a test drives it without a process.
func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("model-upgrade", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dryRun := flags.Bool("dry-run", false, "name the files that would change, and write nothing")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	patterns := flags.Args()
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}

	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	root, err := moduleRoot(wd)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	u, err := load(root, wd, patterns)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	changed := u.upgrade()
	if len(u.problems) > 0 {
		sort.Slice(u.problems, func(i, j int) bool { return u.problems[i].less(u.problems[j]) })
		for _, p := range u.problems {
			fmt.Fprintln(stderr, p)
		}
		fmt.Fprintf(stderr, "\nmodel-upgrade: %d place(s) it cannot rewrite, so it wrote nothing. "+
			"Rewrite them by hand, or into one of the shapes it reads, and run it again.\n", len(u.problems))
		return 1
	}

	if *dryRun {
		for _, f := range changed {
			fmt.Fprintf(stdout, "would rewrite %s\n", f.rel)
		}
		return 0
	}
	for _, f := range changed {
		if err := os.WriteFile(f.path, f.src, 0o644); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "rewrote %s\n", f.rel)
	}

	changes, err := modelbuild.Plan(root)
	if err == nil {
		err = modelbuild.Apply(root, changes)
	}
	if err != nil {
		fmt.Fprintf(stderr, "model-upgrade: the models are rewritten, and model:build stopped: %v\n", err)
		return 1
	}
	for _, c := range changes {
		fmt.Fprintf(stdout, "wrote %s\n", c.Path)
	}

	fmt.Fprintf(stdout, `
Next, take the hesape release with the model core and let the compiler name
what is left -- results whose shape changed, such as SimplePaginate returning
the rows and the page, and code that leaned on the generic types:

    go get %s@%s
    go build ./... && go vet ./...
`, hesapeModule, gen.ModelCoreRelease)
	return 0
}

// hesapeModule is what the closing instruction names, at gen.ModelCoreRelease.
const hesapeModule = "github.com/arandu-io/hesape"

// moduleRoot walks up from dir to the nearest go.mod.
func moduleRoot(dir string) (string, error) {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("model-upgrade: no go.mod here or above: run it from inside the module to upgrade")
		}
		dir = parent
	}
}

// problem is one place the tool cannot rewrite.
type problem struct {
	file    string
	line    int
	message string
}

func (p problem) String() string { return fmt.Sprintf("%s:%d: %s", p.file, p.line, p.message) }

func (p problem) less(q problem) bool {
	if p.file != q.file {
		return p.file < q.file
	}
	return p.line < q.line
}

// selected reports whether rel, a slashed path relative to the module root, is
// under one of the patterns, which are read relative to wd the way the go tool
// reads them: ./... for everything, ./dir/... for a tree, ./dir for one
// directory.
func selected(root, wd, rel string, patterns []string) bool {
	dir := filepath.ToSlash(filepath.Dir(rel))
	for _, pattern := range patterns {
		recursive := strings.HasSuffix(pattern, "/...") || pattern == "..."
		base := strings.TrimSuffix(strings.TrimSuffix(pattern, "..."), "/")
		if base == "" {
			base = "."
		}
		abs := filepath.Join(wd, base)
		want, err := filepath.Rel(root, abs)
		if err != nil {
			continue
		}
		want = filepath.ToSlash(want)
		switch {
		case want == "." && recursive:
			return true
		case dir == want:
			return true
		case recursive && strings.HasPrefix(dir, want+"/"):
			return true
		}
	}
	return false
}
