package gen_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/gen"
	"github.com/arandu-io/aru/internal/gomod"
	"github.com/arandu-io/aru/tests"
)

// TestTheGeneratedQuerySurfaceIsPinned counts what the query file declares, by
// what each method returns.
//
// The surface is the API every package of an application programs against, so
// a method appearing or disappearing is a change to every project at its next
// build. Counting it here makes that a reviewed change -- the number moves in
// the same diff as the template -- rather than something noticed when a call
// stops compiling.
func TestTheGeneratedQuerySurfaceIsPinned(t *testing.T) {
	for _, c := range []struct {
		softDeletes                     bool
		chainable, rows, values, ofRows int
	}{
		{false, 48, 20, 19, 14},
		// The six only a soft-deleting table has: three that build, one that
		// returns a row and two that return a count.
		{true, 51, 21, 21, 14},
	} {
		spec := querySpec("Invoice")
		spec.SoftDeletes = c.softDeletes
		file := parseQuery(t, spec)

		counts := map[string]int{}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil {
				continue
			}
			switch receiver := exprString(fn.Recv.List[0].Type); receiver {
			case "*InvoiceQuery":
				switch {
				case fn.Name.Name == "Base" || fn.Name.Name == "New":
					counts["extra"]++
				case results(fn) == "*InvoiceQuery":
					counts["chainable"]++
				case !takesGrant(fn):
					t.Errorf("soft=%v: %s builds nothing and runs without a Grant", c.softDeletes, fn.Name.Name)
				case handsBackRows(fn):
					counts["rows"]++
				default:
					counts["values"]++
				}
			case "InvoiceCollection":
				counts["ofRows"]++
			case "*Invoice":
				counts["entity"]++
			default:
				t.Errorf("soft=%v: a method on %s, which the file should not declare", c.softDeletes, receiver)
			}
		}

		for _, want := range []struct {
			what string
			n    int
		}{
			{"chainable", c.chainable}, {"rows", c.rows}, {"values", c.values},
			{"ofRows", c.ofRows}, {"entity", 2}, {"extra", 2},
		} {
			if counts[want.what] != want.n {
				t.Errorf("soft=%v: %d %s methods, want %d", c.softDeletes, counts[want.what], want.what, want.n)
			}
		}
	}
}

// TestTheGeneratedQueryHasNoTypeParameter is the rule the file exists for. A
// type parameter, or an instantiation of a hesape generic, in code every
// package of the application sees is compiled again in each of them; the
// standard library's iter.Seq2 is a function type with no methods, and costs
// nothing to instantiate.
func TestTheGeneratedQueryHasNoTypeParameter(t *testing.T) {
	spec := querySpec("Invoice")
	spec.SoftDeletes = true
	file := parseQuery(t, spec)

	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncType:
			if x.TypeParams != nil {
				t.Errorf("a function with type parameters: %s", exprString(x))
			}
		case *ast.TypeSpec:
			if x.TypeParams != nil {
				t.Errorf("%s has type parameters", x.Name.Name)
			}
		case *ast.IndexListExpr:
			if text := exprString(x); !strings.HasPrefix(text, "iter.Seq2[") {
				t.Errorf("an instantiation the file should not hold: %s", text)
			}
		case *ast.IndexExpr:
			// A qualified name with an index is an instantiation; out[i] is not.
			if _, qualified := x.X.(*ast.SelectorExpr); qualified {
				t.Errorf("an instantiation the file should not hold: %s", exprString(x))
			}
		}
		return true
	})
}

// TestAPluralEqualToTheSingularGetsAConstructorOfItsOwn: the constructor is the
// plural, and the plural of an uncountable noun is the noun. A function and a
// type of one name in one package do not compile, so the rule appends Records,
// and it is the same rule for the query, the factory and the seeder.
func TestAPluralEqualToTheSingularGetsAConstructorOfItsOwn(t *testing.T) {
	for entity, want := range map[string]string{
		"Media":       "MediaRecords",
		"SocialMedia": "SocialMediaRecords",
		"News":        "NewsRecords",
		"Invoice":     "Invoices",
		"Category":    "Categories",
		"Address":     "Addresses",
	} {
		if got := gen.Constructor(entity); got != want {
			t.Errorf("Constructor(%q) = %q, want %q", entity, got, want)
		}
	}

	media := gen.Module{Name: "media", Fields: []gen.Field{{Name: "path", Type: gen.TypeString}}, ModulePath: "example.test/project", Date: "2026_07_31"}
	if media.Table() != "media" {
		t.Errorf("the table of media is %q, want media", media.Table())
	}
	files, err := gen.Generate(media)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	byPath := map[string]string{}
	for _, f := range files {
		byPath[f.Path] = string(f.Content)
	}
	for path, want := range map[string]string{
		"app/Models/MediaQuery.go":                "func MediaRecords(db model.DB) *MediaQuery",
		"app/Models/Media.go":                     "type Media struct",
		"app/Services/MediaService.go":            "models.MediaRecords(s.db)",
		"app/Http/Controllers/MediaController.go": "views.MediaIndexData{",
	} {
		if !strings.Contains(byPath[path], want) {
			t.Errorf("%s does not contain %q", path, want)
		}
	}
}

// TestTheQueryHeaderIsHowAGeneratedFileIsKnown: model:build removes a file
// only when it carries the header, so the header has to read back exactly and
// a file that merely looks similar must not.
func TestTheQueryHeaderIsHowAGeneratedFileIsKnown(t *testing.T) {
	content, err := gen.RenderQuery(querySpec("Invoice"))
	if err != nil {
		t.Fatal(err)
	}
	source, ok := gen.GeneratedQuerySource(content)
	if !ok || source != "Invoice.go" {
		t.Fatalf("GeneratedQuerySource = %q, %v; want Invoice.go, true", source, ok)
	}
	for _, other := range []string{
		"package models\n",
		"// Code generated by some other tool. DO NOT EDIT.\n",
		"// Code generated by aru model:build from Invoice.go.\n",
		"\n" + string(content),
	} {
		if _, ok := gen.GeneratedQuerySource([]byte(other)); ok {
			t.Errorf("%q reads as a file model:build wrote", other)
		}
	}
}

func querySpec(entity string) gen.QuerySpec {
	return gen.QuerySpec{
		Package: "models", Entity: entity, Source: entity + ".go",
		Constructor: gen.Constructor(entity), TableVar: strings.ToLower(entity[:1]) + entity[1:] + "Table",
	}
}

func parseQuery(t *testing.T, spec gen.QuerySpec) *ast.File {
	t.Helper()
	content, err := gen.RenderQuery(spec)
	if err != nil {
		t.Fatalf("RenderQuery: %v", err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), spec.Path(), content, 0)
	if err != nil {
		t.Fatalf("the rendered query does not parse: %v", err)
	}
	return file
}

func exprString(n ast.Node) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, token.NewFileSet(), n); err != nil {
		return ""
	}
	return buf.String()
}

func results(fn *ast.FuncDecl) string {
	if fn.Type.Results == nil {
		return ""
	}
	parts := make([]string, 0, len(fn.Type.Results.List))
	for _, r := range fn.Type.Results.List {
		parts = append(parts, exprString(r.Type))
	}
	return strings.Join(parts, ",")
}

func takesGrant(fn *ast.FuncDecl) bool {
	for _, p := range fn.Type.Params.List {
		if exprString(p.Type) == "auth.Grant" {
			return true
		}
	}
	return false
}

// handsBackRows reports whether a method that runs returns, or yields to its
// callback, rows of the entity rather than a value.
func handsBackRows(fn *ast.FuncDecl) bool {
	if strings.Contains(results(fn), "Invoice") {
		return true
	}
	for _, p := range fn.Type.Params.List {
		if strings.Contains(exprString(p.Type), "Invoice") {
			return true
		}
	}
	return false
}

// TestTheHesapeReleaseIsTheOneThisModuleRequires: the release the printed
// instructions tell a project to take is the one go.mod pins, so the generated
// code a project receives is the code this module compiled and tested. A
// constant that stayed behind would send every project that follows it to an
// older release than the one checked here, and nothing else would notice.
func TestTheHesapeReleaseIsTheOneThisModuleRequires(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(tests.Root(t), "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	pinned, ok := gomod.Parse(string(body)).Pinned("github.com/arandu-io/hesape")
	if !ok {
		t.Fatal("go.mod does not require github.com/arandu-io/hesape")
	}
	if gen.HesapeRelease != pinned {
		t.Errorf("gen.HesapeRelease is %s and go.mod requires %s: move the constant with the require", gen.HesapeRelease, pinned)
	}
	if gomod.Less(gen.HesapeRelease, gen.ModelCoreRelease) {
		t.Errorf("gen.HesapeRelease %s is below gen.ModelCoreRelease %s, the oldest release the generated code compiles with",
			gen.HesapeRelease, gen.ModelCoreRelease)
	}
}
