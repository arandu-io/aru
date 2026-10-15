package catalog_test

import (
	"go/format"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/catalog"
	"github.com/arandu-io/aru/tests"
)

func fixtureCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Read(tests.Fixture(t, "catalog", "framework"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// rewriteTwice rewrites src, checks the output is gofmt-clean, and checks a
// second rewrite of the output changes nothing.
func rewriteTwice(t *testing.T, c *catalog.Catalog, src string) string {
	t.Helper()
	out, _ := rewriteTwiceRenaming(t, c, src)
	return out
}

// rewriteTwiceRenaming is rewriteTwice, answering the renames of the first
// rewrite as well.
func rewriteTwiceRenaming(t *testing.T, c *catalog.Catalog, src string) (string, []catalog.Rename) {
	t.Helper()
	out, renames, err := c.Rewrite("file.go", []byte(src))
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if len(renames) == 0 || string(out) == src {
		t.Fatalf("nothing changed in:\n%s", src)
	}
	formatted, err := format.Source(out)
	if err != nil || string(formatted) != string(out) {
		t.Errorf("the output is not gofmt-clean (%v):\n%s", err, out)
	}
	again, renamedAgain, err := c.Rewrite("file.go", out)
	if err != nil || len(renamedAgain) > 0 || string(again) != string(out) {
		t.Errorf("a second rewrite changed the file (%v):\n%s", err, again)
	}
	return string(out), renames
}

// TestARewriteMovesWhatTheBridgeOnlyReExports: aliases and forwards move to
// the component, a forward under another name is renamed, and what the
// framework declares keeps the bridge import alive.
func TestARewriteMovesWhatTheBridgeOnlyReExports(t *testing.T) {
	out := rewriteTwice(t, fixtureCatalog(t), `package services

import (
	"context"

	"github.com/arandu-io/framework/security"
)

func Settle(ctx context.Context, g security.Grant, p security.Policy[int], s security.Subject, store *security.SessionStore) error {
	_, err := security.Authorize(ctx, p, s, "ledger.settle", 1)
	_, _ = security.HashPassword("x")
	return err
}
`)
	for _, want := range []string{
		`"github.com/arandu-io/hesape/auth"`,
		`"github.com/arandu-io/hesape/hashing"`,
		`"github.com/arandu-io/framework/security"`,
		"g auth.Grant, p auth.Policy[int], s auth.Subject, store *security.SessionStore",
		"auth.Authorize(ctx, p, s",
		`hashing.Make("x")`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the rewrite does not carry %q:\n%s", want, out)
		}
	}
}

// TestARewriteNeverShadowsAName: a parameter called auth means the new import
// cannot be called auth, and a bridge nothing else uses goes.
func TestARewriteNeverShadowsAName(t *testing.T) {
	out := rewriteTwice(t, fixtureCatalog(t), `package services

import "github.com/arandu-io/framework/security"

func Check(auth string, g security.Grant) string { return auth }
`)
	if !strings.Contains(out, `hauth "github.com/arandu-io/hesape/auth"`) || !strings.Contains(out, "g hauth.Grant") {
		t.Errorf("the new import was not given a free name:\n%s", out)
	}
	if strings.Contains(out, "framework/security") {
		t.Errorf("the bridge stayed although nothing names it any more:\n%s", out)
	}
}

// TestAnEnvelopeIsLeftWhereItIs: Router is the framework's own, and a file
// that names only it is not touched.
func TestAnEnvelopeIsLeftWhereItIs(t *testing.T) {
	src := `package routes

import fhttp "github.com/arandu-io/framework/http"

func Web(r *fhttp.Router) *fhttp.Router { return r }
`
	out, renames, err := fixtureCatalog(t).Rewrite("web.go", []byte(src))
	if err != nil || len(renames) > 0 || string(out) != src {
		t.Errorf("a file naming only an envelope changed (%v):\n%s", err, out)
	}
}

// TestAViewIsRewrittenAsText: a .kyse.go is not Go below its header, so its
// import lines and its uses are rewritten as text, and a second pass changes
// nothing.
func TestAViewIsRewrittenAsText(t *testing.T) {
	c := fixtureCatalog(t)
	src := `//go:build kyse

package portal

import (
	hview "github.com/arandu-io/hesape/view"
	"github.com/arandu-io/framework/security"
	fhttp "github.com/arandu-io/framework/http"
)

@go
type QuoteData struct {
	hview.Page
	Grant  security.Grant
	Router *fhttp.Router
}
@endgo

<p>{{ .Grant }}</p>
`
	out, changed := c.RewriteView(src)
	if !changed {
		t.Fatal("the view did not change")
	}
	for _, want := range []string{
		"\t\"github.com/arandu-io/hesape/auth\"\n",
		"Grant  auth.Grant",
		"Router *fhttp.Router",
		"\tfhttp \"github.com/arandu-io/framework/http\"\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the view does not carry %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "framework/security") {
		t.Errorf("the bridge import stayed in the view:\n%s", out)
	}
	if again, changedAgain := c.RewriteView(out); changedAgain || again != out {
		t.Errorf("a second rewrite changed the view:\n%s", again)
	}
}

// TestALocalThatShadowsTheBridgeIsNotThePackage: inside a function whose
// variable is called security, security.Grant is a field of that variable and
// moves nowhere; the package's own use outside it still moves.
func TestALocalThatShadowsTheBridgeIsNotThePackage(t *testing.T) {
	out := rewriteTwice(t, fixtureCatalog(t), `package services

import "github.com/arandu-io/framework/security"

type holder struct{ Grant int }

func Read(g security.Grant) int {
	security := holder{}
	return security.Grant
}
`)
	if !strings.Contains(out, "g auth.Grant") {
		t.Errorf("the package's own use did not move:\n%s", out)
	}
	if !strings.Contains(out, "return security.Grant") {
		t.Errorf("the field of the local variable was rewritten as if it were the package:\n%s", out)
	}
}

// TestARewriteNamesWhatItMoved: the renames are the qualified names as the
// file spelled them and as it spells them now, once each and sorted, and a
// file the rewrite leaves alone has none.
func TestARewriteNamesWhatItMoved(t *testing.T) {
	_, renames := rewriteTwiceRenaming(t, fixtureCatalog(t), `package services

import "github.com/arandu-io/framework/security"

func Hash(g security.Grant, h security.Grant) (string, error) { return security.HashPassword("x") }
`)
	want := []catalog.Rename{
		{From: "security.Grant", To: "auth.Grant"},
		{From: "security.HashPassword", To: "hashing.Make"},
	}
	if len(renames) != len(want) {
		t.Fatalf("renames %v, want %v", renames, want)
	}
	for i := range want {
		if renames[i] != want[i] {
			t.Errorf("rename %d is %v, want %v", i, renames[i], want[i])
		}
	}
}

// TestAnAliasedBridgeIsReadThroughItsAlias: a bridge imported under another
// name is found by that name, what moves leaves it, and what the framework
// declares keeps it under the same alias.
func TestAnAliasedBridgeIsReadThroughItsAlias(t *testing.T) {
	out, renames := rewriteTwiceRenaming(t, fixtureCatalog(t), `package services

import sec "github.com/arandu-io/framework/security"

func Load(g sec.Grant, store *sec.SessionStore) error { return store.Load() }
`)
	for _, want := range []string{
		`sec "github.com/arandu-io/framework/security"`,
		`"github.com/arandu-io/hesape/auth"`,
		"g auth.Grant, store *sec.SessionStore",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the rewrite does not carry %q:\n%s", want, out)
		}
	}
	if len(renames) != 1 || renames[0] != (catalog.Rename{From: "sec.Grant", To: "auth.Grant"}) {
		t.Errorf("renames %v, want sec.Grant to auth.Grant", renames)
	}
}

// TestTheCanonicalImportAlreadyThereIsReused: a file that already imports the
// component, under its own name or another, gets no second import of it, and
// the moved names take the name it already has.
func TestTheCanonicalImportAlreadyThereIsReused(t *testing.T) {
	for _, tc := range []struct{ spec, local string }{
		{`"github.com/arandu-io/hesape/auth"`, "auth"},
		{`hauth "github.com/arandu-io/hesape/auth"`, "hauth"},
		{`identity "github.com/arandu-io/hesape/auth"`, "identity"},
	} {
		t.Run(tc.local, func(t *testing.T) {
			out := rewriteTwice(t, fixtureCatalog(t), `package services

import (
	`+tc.spec+`
	"github.com/arandu-io/framework/security"
)

func Read(s `+tc.local+`.Subject, g security.Grant) `+tc.local+`.Subject { return s }
`)
			if n := strings.Count(out, `"github.com/arandu-io/hesape/auth"`); n != 1 {
				t.Errorf("the component is imported %d times:\n%s", n, out)
			}
			if !strings.Contains(out, "g "+tc.local+".Grant") {
				t.Errorf("the moved name does not take the import's name %s:\n%s", tc.local, out)
			}
			if strings.Contains(out, "framework/security") {
				t.Errorf("the bridge stayed although nothing names it any more:\n%s", out)
			}
		})
	}
}

// TestAnImportOfAnotherPackageWithTheSameNameIsNotShadowed: the project's own
// package called auth keeps the name, and the component is imported under a
// free one beside it.
func TestAnImportOfAnotherPackageWithTheSameNameIsNotShadowed(t *testing.T) {
	out := rewriteTwice(t, fixtureCatalog(t), `package services

import (
	"example.test/project/app/auth"
	"github.com/arandu-io/framework/security"
)

func Read(g security.Grant) auth.Session { return auth.Session{} }
`)
	for _, want := range []string{
		`"example.test/project/app/auth"`,
		`hauth "github.com/arandu-io/hesape/auth"`,
		"g hauth.Grant) auth.Session",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the rewrite does not carry %q:\n%s", want, out)
		}
	}
}

// TestAnExternalTestFileIsRewrittenLikeAnyOther: a _test package is Go like
// any other file, and the selectors in its assertions move with the rest.
func TestAnExternalTestFileIsRewrittenLikeAnyOther(t *testing.T) {
	out := rewriteTwice(t, fixtureCatalog(t), `package services_test

import (
	"testing"

	"github.com/arandu-io/framework/security"
)

func TestGrant(t *testing.T) {
	var g security.Grant
	if g != (security.Grant{}) {
		t.Fatal("not zero")
	}
}
`)
	if !strings.Contains(out, "var g auth.Grant") || !strings.Contains(out, "g != (auth.Grant{})") {
		t.Errorf("the test file did not move:\n%s", out)
	}
	if !strings.Contains(out, "package services_test") {
		t.Errorf("the package clause changed:\n%s", out)
	}
}
