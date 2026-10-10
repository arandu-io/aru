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
	out, changed, err := c.Rewrite("file.go", []byte(src))
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if !changed {
		t.Fatalf("nothing changed in:\n%s", src)
	}
	formatted, err := format.Source(out)
	if err != nil || string(formatted) != string(out) {
		t.Errorf("the output is not gofmt-clean (%v):\n%s", err, out)
	}
	again, changedAgain, err := c.Rewrite("file.go", out)
	if err != nil || changedAgain || string(again) != string(out) {
		t.Errorf("a second rewrite changed the file (%v):\n%s", err, again)
	}
	return string(out)
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
	out, changed, err := fixtureCatalog(t).Rewrite("web.go", []byte(src))
	if err != nil || changed || string(out) != src {
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
