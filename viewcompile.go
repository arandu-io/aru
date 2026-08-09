package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/arandu-io/aru/internal/kyse"
)

// viewsDir is where a project keeps its views.
const viewsDir = "resources/views"

// viewsIn says which directory of a module holds its views.
//
// A project keeps them in resources/views, and that is the answer almost every
// time. A component library keeps them at its own root: it has no controllers,
// no routes and no stylesheet, so a resources/views inside it would be a
// directory named after a structure it does not have.
//
// One rule and no flag. `aru view:build` is told where the views are by looking,
// and a flag saying it again would be a second way to answer a question the tree
// already answers -- which is how the two get to disagree.
//
// It matters for more than finding the files: the name a view is registered
// under is its path relative to this directory. With the wrong base,
// `components/button.kyse.go` compiles to a component called
// `resources.views.components.button`, and nothing that draws it can find it.
func viewsIn(root string) string {
	if _, err := os.Stat(filepath.Join(root, viewsDir)); err == nil {
		return filepath.Join(root, viewsDir)
	}
	return root
}

// viewSuffix is what a view source is named.
//
// One constant, because two places have to agree on it: the compiler that reads
// the sources and the watcher that rebuilds when one changes. They disagreed
// once -- the watcher was still asking for ".templ" after ADR 0020 -- and the
// result was `aru dev` serving the previous save.
const viewSuffix = ".kyse.go"

// compileViews turns every `.kyse.go` under resources/views into Go.
//
// It replaces the `templ generate` step, and it is not a binary the CLI
// downloads -- kyse is part of `aru`. One fewer thing to pin, verify and cache
// (REGRA 13).
func compileViews(root string, stdout io.Writer) error {
	dir := viewsIn(root)

	sources, err := findViews(dir)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return nil
	}

	// Layouts first: a page that extends one and declares no `@go` block takes
	// the layout's data type, which is the contract a layout needs -- the page
	// hands the layout what the layout asks for.
	sort.Slice(sources, func(i, j int) bool {
		li := strings.Contains(sources[i], string(filepath.Separator)+"layouts"+string(filepath.Separator))
		lj := strings.Contains(sources[j], string(filepath.Separator)+"layouts"+string(filepath.Separator))
		if li != lj {
			return li
		}
		return sources[i] < sources[j]
	})

	inherited := map[string]string{}
	var problems []string

	for _, source := range sources {
		body, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, source)

		file, err := kyse.Parse(rel, string(body))
		if err != nil {
			// Every view is compiled even after one fails, so a person fixing
			// views sees all of them rather than one build at a time.
			problems = append(problems, err.Error())
			continue
		}

		name := kyse.Name(dir, source)

		// A layout renders with the interface it declares; a page renders with
		// its struct. Two questions, two answers, from one @go block.
		//
		// What a layout PUBLISHES to its children is still the struct: a child
		// interpolates fields, so it asserts the concrete type and hands the
		// same value on to the layout, which only needs the methods.
		dataType := kyse.RenderType(file)
		if dataType == "" && file.Extends != "" {
			dataType = inherited[file.Extends]
		}
		if published := kyse.PageType(file); published != "" {
			inherited[name] = published
		}

		// Both paths are relative to the project root, because that is where
		// `go build` runs and the compiler prints a //line file name exactly as
		// it reads it. Handing it an absolute path, or one relative to
		// resources/views, would report a position nobody can click.
		target := kyse.OutputPath(source)
		targetRel, err := filepath.Rel(root, target)
		if err != nil {
			return err
		}

		out, err := kyse.Generate(file, name, dataType, targetRel)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		// The directory is created here, not assumed. The compiled views live
		// under storage, which is gitignored -- so in a fresh clone every one of
		// these directories is missing, and the first build failed with
		// "no such file or directory" naming a path nobody had ever created.
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, out, 0o644); err != nil {
			return err
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	fmt.Fprintf(stdout, "kyse: %d view(s) compiled\n", len(sources))
	return nil
}

// findViews collects the sources.
//
// It matches `.kyse.go` and nothing else. The distinction matters more than it
// looks: a cleanup that globs `*.go` in this directory deletes the sources, and
// that is not hypothetical -- it happened while this was being written.
func findViews(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if strings.HasSuffix(path, viewSuffix) {
			out = append(out, path)
		}
		return nil
	})
	return out, err
}
