package pack

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/packages"
)

// runtimePackage is the package an application's window is opened by.
//
// Every platform needs two things from it and they are not the same thing: the
// import path, which is what the linker is told to write the application's
// identity into, and the directory, which is where the files that are not Go
// are read from -- the Java sources compiled into an APK, the header an iOS
// framework exports.
type runtimePackage struct {
	path string
	dir  string
}

// runtimeMarkers are the files only the package binding a window to a platform
// carries.
//
// None of them is a Go file, and that is why one rule is enough for every
// target: a Go file appears and disappears with the platform the graph was read
// for, and these sit in the directory whichever platform is being packaged.
var runtimeMarkers = []string{"*.java", "framework_ios.h"}

// findRuntimePackage answers the runtime package of an application's graph.
//
// Found rather than written down. A path written into these sources goes on
// compiling after the package it names has moved, and every symptom of that is
// silent: the linker is told to set a symbol that is no longer there and sets
// nothing, so the artifact builds, installs, runs, and carries an empty
// application identifier.
//
// Two matches are refused rather than resolved. Nothing here knows which of
// them a build meant, and picking one would be picking it in every build after
// this, invisibly.
func findRuntimePackage(root *packages.Package) (runtimePackage, error) {
	var found []runtimePackage

	err := walkGraph(root, func(p *packages.Package) (bool, error) {
		dir := packageDir(p)
		if dir == "" {
			return true, nil
		}
		carries, err := carriesMarker(dir)
		if err != nil {
			return false, err
		}
		if carries {
			found = append(found, runtimePackage{path: p.PkgPath, dir: dir})
		}
		return true, nil
	})
	if err != nil {
		return runtimePackage{}, err
	}

	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return runtimePackage{}, fmt.Errorf(
			"no package reachable from %s carries the platform bindings (%s beside its sources), and a packaged application is opened by one",
			root.PkgPath, strings.Join(runtimeMarkers, " or "))
	default:
		names := make([]string, 0, len(found))
		for _, candidate := range found {
			names = append(names, candidate.path)
		}
		slices.Sort(names)
		return runtimePackage{}, fmt.Errorf(
			"%d packages carry the platform bindings and only one of them can be the runtime: %s. Nothing here chooses between them",
			len(found), strings.Join(names, ", "))
	}
}

// carriesMarker answers whether a directory holds one of the marker files.
func carriesMarker(dir string) (bool, error) {
	for _, marker := range runtimeMarkers {
		matches, err := filepath.Glob(filepath.Join(dir, marker))
		if err != nil {
			return false, err
		}
		if len(matches) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// loadPackageGraph reads an application's package graph as the target sees it.
//
// Once, and kept: three platforms want the files that sit beside the Go sources
// of everything an application imports, and asking the go tool again for each
// of them is the same question with a second answer.
func loadPackageGraph(pkgPath string) (*packages.Package, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedImports |
			packages.NeedDeps |
			packages.NeedModule,
		Env: append(os.Environ(), targetEnv()...),
	}
	if *extraTags != "" {
		cfg.BuildFlags = []string{"-tags=" + *extraTags}
	}

	pkgs, err := packages.Load(cfg, pkgPath)
	if err != nil {
		return nil, err
	}
	if len(pkgs) != 1 {
		return nil, fmt.Errorf("%q names %d packages, and one artifact is built from one", pkgPath, len(pkgs))
	}
	if len(pkgs[0].Errors) > 0 {
		return nil, fmt.Errorf("reading the package graph of %s: %v", pkgPath, pkgs[0].Errors[0])
	}
	return pkgs[0], nil
}

// targetEnv is what the go tool has to be told before it can answer about the
// target's files rather than the machine's.
//
// The cgo setting is part of the question and not a build detail: it decides
// which files of a package are in it, so a graph read without it names a
// different set of files than the build that follows will compile.
func targetEnv() []string {
	switch *target {
	case "js":
		return []string{"GOOS=js", "GOARCH=wasm", "CGO_ENABLED=0"}
	case "android":
		return []string{"GOOS=android", "CGO_ENABLED=1"}
	case "ios", "tvos":
		return []string{"GOOS=ios", "CGO_ENABLED=1"}
	case "windows":
		return []string{"GOOS=windows"}
	case "macos":
		return []string{"GOOS=darwin", "CGO_ENABLED=1"}
	}
	return nil
}

// walkGraph calls visit once for each package reachable from root, root
// included. A visit answering false stops the walk from descending into that
// package's own imports.
//
// The error is propagated rather than dropped. What the callers collect is the
// files an import brings with it, so a failure here is a file missing from the
// artifact -- which builds, installs, and fails on the screen that needed it.
func walkGraph(root *packages.Package, visit func(*packages.Package) (bool, error)) error {
	seen := make(map[string]bool)

	var walk func(*packages.Package) error
	walk = func(p *packages.Package) error {
		if seen[p.ID] {
			return nil
		}
		seen[p.ID] = true

		descend, err := visit(p)
		if err != nil {
			return err
		}
		if !descend {
			return nil
		}
		for _, imported := range p.Imports {
			if err := walk(imported); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(root)
}

// packageDir answers the directory a package's sources are in.
//
// The Go files are asked first, as the only files the graph names outright. The
// module is the fallback for a package whose Go sources are all behind a
// constraint this target does not select: what a package path says below its
// module's path, a directory says below its module's directory.
func packageDir(p *packages.Package) string {
	if len(p.GoFiles) > 0 {
		return filepath.Dir(p.GoFiles[0])
	}
	if p.Module == nil || p.Module.Dir == "" {
		return ""
	}
	if p.PkgPath == p.Module.Path {
		return p.Module.Dir
	}
	below, found := strings.CutPrefix(p.PkgPath, p.Module.Path+"/")
	if !found {
		return ""
	}
	return filepath.Join(p.Module.Dir, filepath.FromSlash(below))
}
