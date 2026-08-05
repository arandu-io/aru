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
			if len(files) != 9 {
				t.Fatalf("generated %d files, want 9", len(files))
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
		if strings.HasSuffix(f.Path, ".repo.go") {
			repo = string(f.Content)
		}
	}
	if repo == "" {
		t.Fatal("no repository was generated")
	}

	for _, method := range []string{"func (r *Repo) Find", "func (r *Repo) List",
		"func (r *Repo) Create", "func (r *Repo) Update", "func (r *Repo) Delete"} {
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
		if !strings.HasSuffix(f.Path, ".policy.go") {
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
		if !strings.HasSuffix(f.Path, ".repo.go") {
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
				if !strings.HasSuffix(f.Path, ".go") {
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
