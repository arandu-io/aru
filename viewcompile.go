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

// viewsDir is where a project keeps its views, mirroring Laravel.
const viewsDir = "resources/views"

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
	dir := filepath.Join(root, viewsDir)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}

	sources, err := findViews(dir)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return nil
	}

	// Layouts first: a page that extends one and declares no `@go` block takes
	// the layout's data type, which is the same contract Blade has -- the page
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
		dataType := declaredType(file)
		if dataType == "" && file.Extends != "" {
			dataType = inherited[file.Extends]
		}
		if declaredType(file) != "" {
			inherited[name] = dataType
		}

		out, err := kyse.Generate(file, name, dataType)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		if err := os.WriteFile(kyse.OutputPath(dir, source), out, 0o644); err != nil {
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

// declaredType finds the type a view declared in its `@go` block.
//
// The first `type X struct` or `type X interface` is the page data. A view that
// declares none and extends a layout takes the layout's.
func declaredType(f *kyse.File) string {
	for _, block := range f.Go {
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "type ") {
				continue
			}
			if strings.Contains(line, " struct") || strings.Contains(line, " interface") {
				if fields := strings.Fields(line); len(fields) >= 2 {
					return fields[1]
				}
			}
		}
	}
	return ""
}
