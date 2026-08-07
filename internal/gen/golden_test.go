package gen_test

import (
	"bytes"
	"flag"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/gen"
)

// update rewrites the golden files: go test ./internal/gen -update
var update = flag.Bool("update", false, "rewrite the golden files")

// spec is the fixture every golden test generates from. The date is fixed rather
// than time.Now(), or the migration id would change every day and the golden
// files would test the calendar.
func spec(tenant bool) gen.Module {
	return gen.Module{
		Name: "purchase_order",
		Fields: []gen.Field{
			{Name: "reference", Type: gen.TypeString, Required: true, Unique: true},
			{Name: "supplier_email", Type: gen.TypeEmail, Required: true},
			{Name: "total", Type: gen.TypeMoney, Required: true},
			{Name: "approved", Type: gen.TypeBool},
			{Name: "notes", Type: gen.TypeText},
			{Name: "delivery_date", Type: gen.TypeDate},
		},
		Tenant:     tenant,
		ModulePath: "example.test/project",
		Date:       "2026_07_31",
	}
}

// TestGolden is what makes regeneration trustworthy: the same specification has
// to produce the same bytes, forever. A change in the templates that alters the
// output shows up here as a diff, in review, instead of surprising someone whose
// project regenerated differently.
func TestGolden(t *testing.T) {
	for _, c := range []struct {
		name   string
		tenant bool
	}{
		{"tenant", true},
		{"global", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			files, err := gen.Generate(spec(c.tenant))
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if len(files) != 12 {
				t.Fatalf("generated %d files, want 12", len(files))
			}

			for _, f := range files {
				golden := filepath.Join("testdata", c.name, filepath.Base(f.Path)+".golden")
				if *update {
					if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(golden, f.Content, 0o644); err != nil {
						t.Fatal(err)
					}
					continue
				}

				want, err := os.ReadFile(golden)
				if err != nil {
					t.Fatalf("%s: %v -- run: go test ./internal/gen -update", golden, err)
				}
				if !bytes.Equal(want, f.Content) {
					t.Errorf("%s differs from the golden file.\nRun `go test ./internal/gen -update` and review the diff.", f.Path)
				}
			}
		})
	}
}

// TestGeneratedCodeIsDeterministic: two runs of the same specification produce
// identical bytes. Without this the golden files above would be flaky rather
// than useful.
func TestGeneratedCodeIsDeterministic(t *testing.T) {
	first, err := gen.Generate(spec(true))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	second, err := gen.Generate(spec(true))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	for i := range first {
		if !bytes.Equal(first[i].Content, second[i].Content) {
			t.Fatalf("%s differs between two runs of the same spec", first[i].Path)
		}
	}
}

// TestEveryRepositoryMethodChecksTheGrant reads the generated source and refuses
// a method that reaches the database without checking first. It is a crude test
// on purpose: it would catch a template edit that quietly drops the check, which
// is the one change nobody would notice in review.
func TestEveryRepositoryMethodChecksTheGrant(t *testing.T) {
	files, err := gen.Generate(spec(true))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var repo string
	for _, f := range files {
		if strings.HasSuffix(f.Path, "Repository.go") {
			repo = string(f.Content)
		}
	}
	if repo == "" {
		t.Fatal("no repository was generated")
	}

	// The receiver carries the entity now: app/Repositories/ is one package for
	// every entity, and `Repo` would collide on the second module generated.
	entity := spec(true).Entity()

	for _, method := range []string{"func (r *" + entity + "Repository) Find", "func (r *" + entity + "Repository) List",
		"func (r *" + entity + "Repository) Create", "func (r *" + entity + "Repository) Update", "func (r *" + entity + "Repository) Delete"} {
		i := strings.Index(repo, method)
		if i < 0 {
			t.Errorf("%s is missing", method)
			continue
		}
		body := repo[i:]
		if end := strings.Index(body[1:], "\nfunc "); end > 0 {
			body = body[:end]
		}
		if !strings.Contains(body, "g.Check(") {
			t.Errorf("%s reaches the database without checking the Grant", method)
		}
	}
}

// TestTheGeneratedPolicyDeniesByDefault: a generated policy that allowed anything
// would be a hole shipped in every project that ran the generator.
func TestTheGeneratedPolicyDeniesByDefault(t *testing.T) {
	files, err := gen.Generate(spec(true))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	for _, f := range files {
		if !strings.HasSuffix(f.Path, "Policy.go") {
			continue
		}
		// Comments are stripped first: the template carries an example of how to
		// open the policy, and that example contains "return nil".
		var code strings.Builder
		for _, line := range strings.Split(string(f.Content), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "//") {
				code.WriteString(line + "\n")
			}
		}
		policy := code.String()
		if strings.Contains(policy, "return nil") {
			t.Fatal("the generated policy allows something out of the box")
		}
		if !strings.Contains(policy, "no rule allows") {
			t.Fatal("the generated policy does not deny explicitly")
		}
		return
	}
	t.Fatal("no policy was generated")
}

// TestTenantScopesEveryQuery: with --tenant, no statement may read without the
// tenant from the Grant.
func TestTenantScopesEveryQuery(t *testing.T) {
	files, err := gen.Generate(spec(true))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	for _, f := range files {
		if !strings.HasSuffix(f.Path, "Repository.go") {
			continue
		}
		repo := string(f.Content)
		if strings.Count(repo, "data.Tenant(g)") < 4 {
			t.Errorf("the tenant from the Grant appears %d times; every statement needs it",
				strings.Count(repo, "data.Tenant(g)"))
		}
		return
	}
}

// TestTheReceiverDoesNotShadowTheSignature is a bug a real measurement found,
// and one that testing a single module could never find.
//
// The generated Policy binds ctx, s and a:
//
//	Can(ctx context.Context, s security.Subject, a security.Action, x Entity)
//
// An entity whose initial is one of those -- Subscription, Account, Category --
// shadowed the parameter it needed, and the file did not compile. Every
// existing golden file used purchase_order, which starts with p.
func TestTheReceiverDoesNotShadowTheSignature(t *testing.T) {
	for _, name := range []string{
		"subscription",   // s, like security.Subject
		"stock_movement", // s again
		"account",        // a, like security.Action
		"category",       // c, like context in some templates
		"warehouse",      // w, like http.ResponseWriter
		"reservation",    // r, like *http.Request
	} {
		t.Run(name, func(t *testing.T) {
			m := gen.Module{
				Name:       name,
				Fields:     []gen.Field{{Name: "label", Type: gen.TypeString, Required: true}},
				Tenant:     true,
				ModulePath: "example.test/project",
				Date:       "2026_08_05",
			}

			files, err := gen.Generate(m)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}

			// Every generated Go file has to parse, and the receiver has to be
			// something other than what the signature already binds.
			for _, f := range files {
				// A .kyse.go is a view, not Go. It ends in .go so the build tag
				// can exclude it, and everything below the package clause is
				// markup — which is exactly why the Go parser refuses it.
				if !strings.HasSuffix(f.Path, ".go") || strings.HasSuffix(f.Path, ".kyse.go") {
					continue
				}
				if _, err := parser.ParseFile(token.NewFileSet(), f.Path, f.Content, parser.AllErrors); err != nil {
					t.Errorf("%s does not parse: %v", f.Path, err)
				}
			}

			if receiver := m.Receiver(); receiver == "s" || receiver == "a" || receiver == "c" {
				t.Errorf("the receiver is %q, which shadows a parameter of the Can signature", receiver)
			}
		})
	}
}

// TestTheReceiverStaysShortWhenItCan: two letters only where one collides, or
// every generated file reads worse for a problem six names have.
func TestTheReceiverStaysShortWhenItCan(t *testing.T) {
	for name, want := range map[string]string{
		"invoice":        "i",
		"purchase_order": "p",
		"customer":       "cu", // c collides
		"subscription":   "su", // s collides
		"account":        "ac", // a collides
	} {
		got := gen.Module{Name: name}.Receiver()
		if got != want {
			t.Errorf("%s has receiver %q, want %q", name, got, want)
		}
	}
}

// TestEveryGeneratedViewFitsTheLayout is the other half of the regression the
// starter kit exposed.
//
// A module's pages are written before `aru make:auth` runs and are not touched
// when it does. They fit the layout by embedding views.Page, which is what
// implements the layout's contract -- so the layout can be replaced and they
// keep rendering. The `var _ Layout` line is where a page that stopped fitting
// stops the build, in the project, naming the page.
func TestEveryGeneratedViewFitsTheLayout(t *testing.T) {
	files, err := gen.Generate(spec(true))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	seen := 0
	for _, f := range files {
		if !strings.HasSuffix(f.Path, ".kyse.go") {
			continue
		}
		seen++
		body := string(f.Content)

		i := strings.Index(body, " struct {")
		if i < 0 {
			t.Errorf("%s declares no page data", f.Path)
			continue
		}
		decl := body[i:]
		if end := strings.Index(decl, "\n}"); end > 0 {
			decl = decl[:end]
		}
		if !strings.Contains(decl, "\n\tPage\n") {
			t.Errorf("%s does not embed Page, so it does not fit the layout make:auth installs:\n%s", f.Path, decl)
		}
		if !strings.Contains(body, "var _ Layout = ") {
			t.Errorf("%s has no compile-time proof that it fits the layout", f.Path)
		}

		// The chrome is declared once, in views.Page. A page that declared its
		// own Title would shadow the promoted one and the layout would read the
		// empty one -- a blank tab on a page that answered 200.
		for _, field := range []string{"Title string", "CSRF string"} {
			if strings.Contains(decl, "\n\t"+field) {
				t.Errorf("%s declares %q, which views.Page already carries", f.Path, field)
			}
		}
	}
	if seen != 4 {
		t.Errorf("checked %d views, want the four screens", seen)
	}
}

// TestGeneratedViewsUseOnlyTheSectionEveryLayoutYields.
//
// A page fills sections and a layout yields them; a section nobody yields is
// dropped in silence. `aru make:auth` replaces the layout with one that yields
// content and nothing else, so a generated page that put its back link in
// @section('header') lost the link the moment the command ran -- no error, no
// warning, just a screen you can no longer navigate out of.
func TestGeneratedViewsUseOnlyTheSectionEveryLayoutYields(t *testing.T) {
	files, err := gen.Generate(spec(true))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// What the layout `aru make:auth` publishes yields, which is the smaller of
	// the two layouts a project can have.
	yielded := map[string]bool{"content": true}

	for _, f := range files {
		if !strings.HasSuffix(f.Path, ".kyse.go") {
			continue
		}
		for rest := string(f.Content); ; {
			i := strings.Index(rest, "@section('")
			if i < 0 {
				break
			}
			rest = rest[i+len("@section('"):]
			end := strings.Index(rest, "'")
			if end < 0 {
				break
			}
			if name := rest[:end]; !yielded[name] {
				t.Errorf("%s fills @section('%s'), which the layout make:auth installs does not yield: it would disappear without a word", f.Path, name)
			}
			rest = rest[end:]
		}
	}
}
