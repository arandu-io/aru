// Package doctor turns the architecture rules into static analysis.
//
// Without it, "mandatory architecture" is documentation nobody reads. The rules
// here are the ones the type system cannot reach: the compiler guarantees that a
// repository needs a Grant, but it cannot tell you that the policy was never
// opened, that a controller talks to the database, or that a tenant came from
// the request.
//
// It reads the AST and never runs the code, so it works on a project that does
// not compile -- which is exactly when someone needs to be told what is wrong.
//
// # The tree it reads
//
// The project layout is conventional, directory by directory, so a file is
// placed by the app/ subtree it sits in rather than by a module directory:
// app/Policies/InvoicePolicy.go is the Policies of Invoice, and
// app/Repositories/InvoiceRepository.go is the Repositories of the same entity.
// The rules reason about the ENTITY, which is the thing a policy protects -- the
// directory is only how the entity is spelled on disk.
package doctor

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/arandu-io/aru/internal/kyse"
	"github.com/arandu-io/aru/internal/manifest"
)

// viewsDir is where a project keeps its views.
const viewsDir = "resources/views"

// Profile is the deployment profile a project is checked against.
//
// The two labels are the ones arandu.mod.toml already spells in its profiles
// list, so a project asks for the check in the same word it declares support in.
type Profile string

// The two profiles. There is no third, and no "auto": a profile that is guessed
// from the code is a profile nobody can put in a pipeline.
const (
	// Conventional is the SQL profile, and what Run checks when nothing else is
	// asked for.
	Conventional Profile = "conventional"
	// Performance is the wide-column profile. It stores one aggregate per
	// partition, which is why a statement reaching two tables and a transaction
	// spanning two aggregates are findings here and correct code elsewhere.
	Performance Profile = "performance"
)

// ParseProfile reads what was passed to the profile flag.
//
// An unknown value is an error rather than a fallback to Conventional: a typo
// that silently checks less than was asked for is the failure this returns an
// error to prevent.
func ParseProfile(s string) (Profile, error) {
	switch p := Profile(s); p {
	case Conventional, Performance:
		return p, nil
	}
	return "", fmt.Errorf("unknown profile %q, want %s or %s", s, Conventional, Performance)
}

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

// project is everything a rule can look at: the parsed Go, the views, and what
// the project declares about itself.
type project struct {
	root string
	// profile is what the project is being checked against.
	//
	// It reaches the rules rather than selecting them, so `rules` stays the whole
	// check surface and a profile rule says in its own first lines when it does
	// not apply -- which is where somebody reading it will look.
	profile Profile
	files   []*file
	// manifest is the arandu.mod.toml at the root, or nil.
	//
	// It sits at the root because the unit of distribution is the Go module:
	// `aru add github.com/fulano/crm` resolves through the Go proxy, so the
	// thing that declares permissions is the repository, not a directory inside
	// it.
	manifest *manifest.Module
	// views are the `.kyse.go` sources. They are not Go -- the build tag keeps
	// the compiler away from the markup -- so they are kept as text, and the
	// rules that read them are looking at markup, not at syntax.
	views []view
	// unreadable are the .go files that did not parse, with the reason.
	//
	// They are carried rather than dropped because every rule below reasons over
	// the file set: a project whose InvoicePolicy.go does not parse looks like a
	// project with no policy for Invoice, and doctor reported exactly that -- an
	// invented finding pointing at the wrong file. See unreadableFiles.
	unreadable []unreadable
}

// unreadable is one .go file the parser refused, and why.
//
// It keeps the category and the entity because both come from the path, which
// is readable even when the contents are not -- that is what lets blind answer
// honestly about a file nobody could open.
type unreadable struct {
	rel      string
	line     int
	reason   string
	category string
	entity   string
}

// blind reports whether doctor failed to read a file that could hold a
// declaration about entity.
//
// A rule that concludes something from the ABSENCE of a declaration has to ask
// this first. "No policy for Invoice" and "the policy is in the file I could not
// parse" look identical from here, and only one of them is worth telling
// somebody about.
//
// Rules that conclude from what is PRESENT -- a repository method that does not
// check its Grant, SQL built with Sprintf -- need no such guard: what they
// found, they found.
func (p *project) blind(entity string) bool {
	for _, u := range p.unreadable {
		if u.entity == entity {
			return true
		}
		// A file under app/ whose name says nothing about an entity could
		// declare any of them.
		if u.category != "" && u.entity == "" {
			return true
		}
	}
	return false
}

// hasView reports whether the project has a source for the view rendered under
// this name: "auth.login" is resources/views/auth/login.kyse.go.
func (p *project) hasView(name string) bool {
	for _, v := range p.views {
		if v.name == name {
			return true
		}
	}
	return false
}

// view is one `.kyse.go` source.
type view struct {
	rel string
	// name is what ctx.View is called with: "auth.login".
	name string
	body string
}

// file is one parsed Go file, with what the rules need.
type file struct {
	path    string
	rel     string
	dir     string
	pkg     string
	ast     *ast.File
	fset    *token.FileSet
	imports map[string]string // path -> local name
	// category is the app/ subtree the file belongs to: "Controllers",
	// "Policies", "Repositories". Empty outside app/.
	category string
	// entity is what the file is about: app/Policies/InvoicePolicy.go is about
	// Invoice. Empty when the name carries no entity, as in Controller.go.
	entity string
	isTest bool
}

// appCategories maps a directory under app/ to the suffix its files carry, so
// InvoicePolicy.go in Policies is about Invoice.
//
// It is a closed list on purpose. A directory nobody named still classifies --
// see classify -- but the three the framework requires (Policies, Repositories,
// Services) have to be spelled the same way everywhere, or a rule silently stops
// applying to half the project.
var appCategories = map[string]string{
	"Controllers":  "Controller",
	"Middleware":   "Middleware",
	"Requests":     "Request",
	"Models":       "",
	"Enums":        "",
	"Policies":     "Policy",
	"Services":     "Service",
	"Repositories": "Repository",
	"Jobs":         "Job",
	"Events":       "Event",
	"Listeners":    "Listener",
	"Mail":         "Mail",
	"Providers":    "Provider",
	"Rules":        "Rule",
	"Commands":     "Command",
}

// classify derives the category and the entity of a file from its path.
//
// The tree says app/Policies/InvoicePolicy.go, and the question every rule asks
// is which ENTITY the file is about -- because that is what a policy protects
// and what a repository reaches.
//
// The deepest known directory wins, so app/Http/Controllers/Auth/Login.go is a
// Controller and Auth is a grouping the developer chose. A directory outside the
// list still classifies under its own name, so a rule that reasons about app/
// does not go blind because somebody invented a folder.
func classify(rel string) (category, entity string) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 3 || parts[0] != "app" {
		return "", ""
	}
	base := strings.TrimSuffix(parts[len(parts)-1], ".go")

	for i := len(parts) - 2; i >= 1; i-- {
		suffix, known := appCategories[parts[i]]
		if !known {
			continue
		}
		return parts[i], strings.TrimSuffix(base, suffix)
	}
	return parts[len(parts)-2], base
}

// Run analyzes the project rooted at dir against one profile.
//
// Every rule runs on both profiles except the three that only make sense on
// Performance, so asking for a profile adds checks and never removes any.
//
// Findings come back sorted by file and line, so the output is stable and a diff
// between two runs means something.
func Run(dir string, profile Profile) ([]Finding, error) {
	files, unreadable, err := parseProject(dir)
	if err != nil {
		return nil, err
	}

	declared, err := manifest.Read(dir)
	if err != nil {
		return nil, err
	}
	views, err := parseViews(dir)
	if err != nil {
		return nil, err
	}
	p := &project{root: dir, profile: profile, files: files, manifest: declared, views: views, unreadable: unreadable}

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

// parseViews collects the `.kyse.go` sources under resources/views.
//
// They are read rather than parsed: a view is Go only down to the package
// clause, and everything a rule cares about -- an Alpine directive, the name the
// view answers to -- is below that line.
func parseViews(dir string) ([]view, error) {
	root := filepath.Join(dir, viewsDir)
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil, nil
	}

	var out []view
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".kyse.go") {
			return nil
		}

		body, readErr := os.ReadFile(path)
		if readErr != nil {
			// A view that cannot be read is not a finding: it is a permission
			// problem, and the build reports it better.
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			rel = path
		}
		out = append(out, view{
			rel:  filepath.ToSlash(rel),
			name: kyse.Name(filepath.ToSlash(root), filepath.ToSlash(path)),
			body: string(body),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out, nil
}

func parseProject(dir string) ([]*file, []unreadable, error) {
	fset := token.NewFileSet()
	var out []*file
	var skipped []unreadable

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
		// A view ends in .go and is not Go: the `kyse` build tag keeps the
		// compiler from reading the markup, and go/parser does not evaluate
		// build tags. Parsing one here would report every view in the project as
		// a syntax error. They are collected by parseViews instead.
		if strings.HasSuffix(path, ".kyse.go") {
			return nil
		}

		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		category, entity := classify(rel)

		parsed, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			// Recorded, not dropped. The compiler reports the syntax error
			// better than this ever could -- but every rule reasons over the
			// file set, so a file missing from it makes the rules wrong rather
			// than merely incomplete.
			line := 1
			reason := err.Error()
			if list, ok := err.(scanner.ErrorList); ok && len(list) > 0 {
				line = list[0].Pos.Line
				reason = list[0].Msg
			}
			skipped = append(skipped, unreadable{
				rel: rel, line: line, reason: reason,
				category: category, entity: entity,
			})
			return nil
		}

		f := &file{
			path:     path,
			rel:      rel,
			dir:      filepath.ToSlash(filepath.Dir(rel)),
			pkg:      parsed.Name.Name,
			ast:      parsed,
			fset:     fset,
			imports:  map[string]string{},
			category: category,
			entity:   entity,
			isTest:   strings.HasSuffix(path, "_test.go"),
		}
		for _, imp := range parsed.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			name := filepath.Base(p)
			if imp.Name != nil {
				name = imp.Name.Name
			}
			f.imports[p] = name
		}
		out = append(out, f)
		return nil
	})
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].rel < skipped[j].rel })
	return out, skipped, err
}

// at returns the file and line of a node, for a finding.
func (f *file) at(n ast.Node) (string, int) {
	return f.rel, f.fset.Position(n.Pos()).Line
}

// line returns the line of a node.
func (f *file) line(n ast.Node) int {
	return f.fset.Position(n.Pos()).Line
}

// types walks every type declared in the file.
//
// A file name says where something lives; a type name says what it is. Both are
// read, because InvoicePolicy declared in a file called Policies.go is still the
// policy of Invoice.
func (f *file) types(fn func(ts *ast.TypeSpec)) {
	for _, decl := range f.ast.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			if ts, ok := spec.(*ast.TypeSpec); ok {
				fn(ts)
			}
		}
	}
}

// functions walks every function and method the file declares.
//
// It is the shape types() has, for the rules that reason about a signature and
// its body together -- which is most of the authorization surface.
func (f *file) functions(visit func(fn *ast.FuncDecl)) {
	for _, decl := range f.ast.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			visit(fn)
		}
	}
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
// "g.Check", "r.Header.Get", "r.URL.Query().Get".
//
// The whole chain, not the last two segments. Giving up at the first selector
// whose left side is not a plain identifier would return only the final name --
// so `r.Header.Get("X-Tenant")` would render as "Get", and the rule looking for
// "r.Header.Get" would never match anything.
//
// A call in the middle of the chain becomes "()", which is what distinguishes
// r.URL.Query().Get from a field access.
func callName(call *ast.CallExpr) string {
	return exprName(call.Fun)
}

func exprName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		left := exprName(x.X)
		if left == "" {
			return x.Sel.Name
		}
		return left + "." + x.Sel.Name
	case *ast.CallExpr:
		return exprName(x.Fun) + "()"
	case *ast.IndexExpr:
		return exprName(x.X)
	case *ast.ParenExpr:
		return exprName(x.X)
	case *ast.StarExpr:
		return exprName(x.X)
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

// stringLiteral returns the contents of a string literal argument.
func stringLiteral(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	return strings.Trim(lit.Value, "\"`"), true
}
