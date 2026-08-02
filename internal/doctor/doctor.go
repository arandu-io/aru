// Package doctor turns the architecture rules into static analysis.
//
// Without it, "mandatory architecture" is documentation nobody reads. The rules
// here are the ones the type system cannot reach: the compiler guarantees that a
// repository needs a Grant, but it cannot tell you that the policy was never
// opened, that a handler talks to the database, or that a tenant came from the
// request.
//
// It reads the AST and never runs the code, so it works on a project that does
// not compile -- which is exactly when someone needs to be told what is wrong.
package doctor

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/arandu-io/aru/internal/manifest"
)

// Severity says whether a finding blocks CI.
type Severity int

// The two levels. There is no "info": a check that does not change what anyone
// does is noise that trains people to ignore the output.
const (
	// Warning is reported and does not fail, unless --strict.
	Warning Severity = iota
	// Error fails, always.
	Error
)

func (s Severity) String() string {
	if s == Error {
		return "error"
	}
	return "warning"
}

// Finding is one problem, at one place.
type Finding struct {
	Rule     string
	Severity Severity
	File     string
	Line     int
	Message  string
	// Why explains the consequence, not the rule. A finding that only says what
	// is forbidden gets suppressed; one that says what breaks gets fixed.
	Why string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: [%s] %s\n    %s", f.File, f.Line, f.Rule, f.Message, f.Why)
}

// project is everything a rule can look at: the parsed Go, and the manifest each
// module declares about itself.
type project struct {
	root      string
	files     []*file
	manifests map[string]*manifest.Module
	// templates are the .templ sources. They are not Go, so they are kept as
	// text -- the rules that look at them are looking for markup, not for
	// syntax.
	templates []template
}

// template is one .templ source.
type template struct {
	rel  string
	body string
}

// modules returns the module directory names that have Go in them, in a stable
// order, so two runs report the same findings in the same sequence.
func (p *project) modules() []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range p.files {
		if f.module == "" || seen[f.module] {
			continue
		}
		seen[f.module] = true
		out = append(out, f.module)
	}
	sort.Strings(out)
	return out
}

// file is one parsed Go file, with what the rules need.
type file struct {
	path    string
	rel     string
	pkg     string
	ast     *ast.File
	fset    *token.FileSet
	imports map[string]string // path -> local name
	// module is the directory under modules/, empty outside one.
	module string
	isTest bool
}

// Run analyzes the project rooted at dir.
//
// Findings come back sorted by file and line, so the output is stable and a diff
// between two runs means something.
func Run(dir string) ([]Finding, error) {
	files, err := parseProject(dir)
	if err != nil {
		return nil, err
	}

	manifests, err := manifest.ReadAll(dir)
	if err != nil {
		return nil, err
	}
	templates, err := parseTemplates(dir)
	if err != nil {
		return nil, err
	}
	p := &project{root: dir, files: files, manifests: manifests, templates: templates}

	var findings []Finding
	for _, rule := range rules {
		findings = append(findings, rule(p)...)
	}

	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})
	return findings, nil
}

// parseTemplates collects the .templ sources.
func parseTemplates(dir string) ([]template, error) {
	var out []template

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", "testdata", "bin":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".templ") {
			return nil
		}

		body, readErr := os.ReadFile(path)
		if readErr != nil {
			// A template that cannot be read is not a finding: it is a
			// permission problem, and the build reports it better.
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			rel = path
		}
		out = append(out, template{rel: rel, body: string(body)})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out, nil
}

func parseProject(dir string) ([]*file, error) {
	fset := token.NewFileSet()
	var out []*file

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", "testdata", "bin":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		parsed, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			// A file that does not parse is a compile error, and the compiler
			// reports it better than this ever could.
			return nil
		}

		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			rel = path
		}

		f := &file{
			path:    path,
			rel:     rel,
			pkg:     parsed.Name.Name,
			ast:     parsed,
			fset:    fset,
			imports: map[string]string{},
			isTest:  strings.HasSuffix(path, "_test.go"),
		}
		for _, imp := range parsed.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			name := filepath.Base(p)
			if imp.Name != nil {
				name = imp.Name.Name
			}
			f.imports[p] = name
		}
		if parts := strings.Split(filepath.ToSlash(rel), "/"); len(parts) > 1 && parts[0] == "modules" {
			f.module = parts[1]
		}
		out = append(out, f)
		return nil
	})
	return out, err
}

// at returns the file and line of a node, for a finding.
func (f *file) at(n ast.Node) (string, int) {
	return f.rel, f.fset.Position(n.Pos()).Line
}

// calls walks every call expression in the file.
func (f *file) calls(fn func(call *ast.CallExpr, name string)) {
	ast.Inspect(f.ast, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		fn(call, callName(call))
		return true
	})
}

// callName renders the called function as written: "security.SystemGrant",
// "g.Check", "fmt.Sprintf".
func callName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		if x, ok := fun.X.(*ast.Ident); ok {
			return x.Name + "." + fun.Sel.Name
		}
		return fun.Sel.Name
	}
	return ""
}

// funcBodyContains reports whether the function calls something matching pred.
func funcBodyContains(fn *ast.FuncDecl, pred func(name string) bool) bool {
	if fn.Body == nil {
		return false
	}
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && pred(callName(call)) {
			found = true
			return false
		}
		return !found
	})
	return found
}

// receiverType returns the receiver type name of a method, without the pointer.
func receiverType(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	switch t := fn.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return id.Name
		}
	case *ast.Ident:
		return t.Name
	}
	return ""
}
