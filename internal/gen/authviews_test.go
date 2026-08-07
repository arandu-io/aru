package gen_test

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/gen"
	"github.com/arandu-io/aru/internal/kyse"
)

// dataType is what a page renders from. Only the layout declares it; the eight
// pages inherit it, which is what `aru view:build` does when a view extends a
// layout and brings no @go block of its own.
const dataType = "AuthPage"

// TestTheAuthViewsCompile is the check that matters: every one of the nine goes
// through the same compiler `aru view:build` runs, and the Go it emits parses.
//
// A view that only looks right is a view nobody has run. This is cheap, it runs
// in CI, and it fails on the commit that breaks a directive rather than in
// somebody's project after make:auth.
func TestTheAuthViewsCompile(t *testing.T) {
	views := gen.AuthViews()
	if len(views) != 9 {
		t.Fatalf("generated %d views, want the nine of laravel/ui", len(views))
	}

	for _, f := range views {
		name := kyse.Name("resources/views", filepath.ToSlash(f.Path))

		file, err := kyse.Parse(f.Path, string(f.Content))
		if err != nil {
			t.Errorf("%s does not compile:\n%v", f.Path, err)
			continue
		}
		out, err := kyse.Generate(file, name, dataType)
		if err != nil {
			t.Errorf("%s: %v", f.Path, err)
			continue
		}
		if _, err := parser.ParseFile(token.NewFileSet(), f.Path, out, parser.AllErrors); err != nil {
			t.Errorf("the Go generated from %s does not parse: %v", f.Path, err)
		}
	}
}

// TestOnlyTheLayoutDeclaresTheData: nine copies of one struct is nine places to
// forget a field. The layout declares it and the pages inherit it, which is also
// what makes @extends type-check.
func TestOnlyTheLayoutDeclaresTheData(t *testing.T) {
	for _, f := range gen.AuthViews() {
		declares := strings.Contains(string(f.Content), "@go")
		isLayout := strings.Contains(filepath.ToSlash(f.Path), "/layouts/")

		switch {
		case isLayout && !declares:
			t.Errorf("%s declares no data: the pages that extend it have nothing to render from", f.Path)
		case !isLayout && declares:
			t.Errorf("%s declares its own data: it should inherit the layout's", f.Path)
		}
	}
}

// TestTheLayoutWiresTheTokenIntoHTMX is the regression guard for the single line
// that breaks every write in an application when it goes missing -- and breaks it
// in a way that reads like a session problem rather than a missing attribute.
func TestTheLayoutWiresTheTokenIntoHTMX(t *testing.T) {
	layout := authView(t, "layouts/app.kyse.go")

	if !strings.Contains(layout, "hx-headers=") || !strings.Contains(layout, "X-CSRF-Token") {
		t.Error("the layout has no hx-headers with the CSRF token: every hx-post would fail the check")
	}
	if !strings.Contains(layout, "@yield('content')") {
		t.Error("the layout yields no content, so nothing that extends it renders")
	}
}

// TestTheFormsCarryTheToken: a form without the hidden field is rejected by the
// CSRF middleware, and the screens are what people copy.
func TestTheFormsCarryTheToken(t *testing.T) {
	for _, f := range gen.AuthViews() {
		body := markup(f)
		if !strings.Contains(body, "<form") {
			continue
		}
		if strings.Count(body, "<form") != strings.Count(body, "@csrf") {
			t.Errorf("%s has a form without @csrf", f.Path)
		}
	}
}

// TestTheAuthViewsInventNoDirective is RULE 15 held against the starter kit: the
// set is closed, and a screen that reaches for a Blade directive kyse does not
// have would fail to compile in somebody else's project rather than in this test.
func TestTheAuthViewsInventNoDirective(t *testing.T) {
	absent := []string{
		"@vite", "@auth", "@guest", "@error", "@can", "@props",
		"@stack", "@push", "@forelse", "@switch", "@fonts", "<x-",
	}
	for _, f := range gen.AuthViews() {
		body := markup(f)
		for _, d := range absent {
			if strings.Contains(body, d) {
				t.Errorf("%s uses %s, which kyse does not have", f.Path, d)
			}
		}
	}
}

// TestTheAuthViewsCarryNoBootstrap: the translation is to Tailwind utilities, and
// a leftover Bootstrap class renders as nothing at all -- an unstyled form that
// looks like a broken build.
func TestTheAuthViewsCarryNoBootstrap(t *testing.T) {
	bootstrap := []string{
		"form-control", "btn btn-", "btn-primary", "card-body", "card-header",
		"navbar-nav", "col-md-", "invalid-feedback", "alert-success",
	}
	for _, f := range gen.AuthViews() {
		body := markup(f)
		for _, class := range bootstrap {
			if strings.Contains(body, class) {
				t.Errorf("%s still uses the Bootstrap class %q", f.Path, class)
			}
		}
	}
}

// TestTheAuthViewsReachForNoHelper: there is no config(), no route(), no auth()
// and no __(). Everything a screen shows came from the handler, in the struct.
func TestTheAuthViewsReachForNoHelper(t *testing.T) {
	helpers := []string{"config(", "route(", "auth()", "__(", "old(", "session(", "Route::has"}
	for _, f := range gen.AuthViews() {
		body := markup(f)
		for _, h := range helpers {
			if strings.Contains(body, h) {
				t.Errorf("%s calls %s, which does not exist: the data comes from the struct", f.Path, h)
			}
		}
	}
}

// markup is the view without its @go block.
//
// The Go a view declares is Go, and it is allowed to name in a doc comment the
// very things the markup may not use -- which is exactly what the layout does
// when it explains why there is no @error and no route(). Scanning the whole
// file would make every one of these checks fail on a comment.
func markup(f gen.File) string {
	body := string(f.Content)
	start := strings.Index(body, "@go")
	end := strings.Index(body, "@endgo")
	if start < 0 || end < start {
		return body
	}
	return body[:start] + body[end+len("@endgo"):]
}

// authView returns one of the nine by its path under resources/views.
func authView(t *testing.T, want string) string {
	t.Helper()
	for _, f := range gen.AuthViews() {
		if strings.HasSuffix(filepath.ToSlash(f.Path), want) {
			return string(f.Content)
		}
	}
	t.Fatalf("there is no view at %s", want)
	return ""
}
