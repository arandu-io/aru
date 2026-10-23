package doctor_test

import (
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/doctor"
)

// suiteCase is one project for one suite rule: the files, and the lines the
// rule must report, by file and line.
type suiteCase struct {
	name  string
	files map[string]string
	want  []string // "file:line"; empty means the rule stays quiet
}

func runSuiteCases(t *testing.T, rule string, cases []suiteCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := structureProject(t, c.files)
			var got []string
			for _, f := range findingsOf(t, root, rule) {
				if f.Severity != doctor.Warning {
					t.Errorf("%s reported at %s; every suite rule is a warning until it is promoted", rule, f.Severity)
				}
				if !strings.Contains(f.Why, "A warning until 2026-12-15, an error after it.") {
					t.Errorf("%s does not say when it becomes an error: %s", rule, f.Why)
				}
				got = append(got, f.File+":"+itoa(f.Line))
			}
			if strings.Join(got, " ") != strings.Join(c.want, " ") {
				t.Errorf("%s reported %v, want %v", rule, got, c.want)
			}
		})
	}
}

func TestTestSleep(t *testing.T) {
	sleeper := "package feature_test\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestX(t *testing.T) {\n\ttime.Sleep(time.Second)\n}\n"
	runSuiteCases(t, "test-sleep", []suiteCase{
		{"a feature test sleeps", map[string]string{"tests/Feature/x_test.go": sleeper}, []string{"tests/Feature/x_test.go:9"}},
		{"an internal test sleeps", map[string]string{"app/Services/x_internal_test.go": strings.Replace(sleeper, "feature_test", "services", 1)},
			[]string{"app/Services/x_internal_test.go:9"}},
		{"a helper of tests/ sleeps", map[string]string{"tests/Helpers/wait.go": "package helpers\n\nimport \"time\"\n\nfunc Wait() { time.Sleep(time.Second) }\n"},
			[]string{"tests/Helpers/wait.go:5"}},
		{"code that ships sleeps", map[string]string{"app/Jobs/Retry.go": "package jobs\n\nimport \"time\"\n\nfunc Back() { time.Sleep(time.Second) }\n"}, nil},
		{"a test waits with a deadline", map[string]string{"tests/Feature/x_test.go": "package feature_test\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestX(t *testing.T) {\n\t<-time.After(time.Second)\n}\n"}, nil},
		{"a local Sleep that is not time's", map[string]string{"tests/Feature/x_test.go": "package feature_test\n\nimport \"testing\"\n\ntype clock struct{}\n\nfunc (clock) Sleep() {}\n\nfunc TestX(t *testing.T) { clock{}.Sleep() }\n"}, nil},
	})
}

func TestTestForgedSubject(t *testing.T) {
	head := "package feature_test\n\nimport (\n\t\"context\"\n\t\"testing\"\n\n\t\"github.com/arandu-io/framework/test\"\n\t\"github.com/arandu-io/hesape/auth\"\n)\n\n"
	runSuiteCases(t, "test-forged-subject", []suiteCase{
		{"a literal reaches a sign-in helper", map[string]string{"tests/Feature/x_test.go": head +
			"func TestX(t *testing.T) {\n\tsignedIn(t, auth.Subject{ID: \"u1\", Roles: []string{\"admin\"}})\n}\n"},
			[]string{"tests/Feature/x_test.go:12"}},
		{"a literal in a variable reaches a session", map[string]string{"tests/E2E/x_test.go": head +
			"func TestX(t *testing.T) {\n\tadmin := auth.Subject{ID: \"u1\"}\n\tsessions.Start(context.Background(), nil, admin)\n}\n"},
			[]string{"tests/E2E/x_test.go:12"}},
		{"the helper of tests/ writes one", map[string]string{"tests/TestCase.go": "package tests\n\nimport \"github.com/arandu-io/framework/security\"\n\nfunc Admin() *security.Subject { return &security.Subject{ID: \"a\"} }\n"},
			[]string{"tests/TestCase.go:5"}},
		{"ActingAs", map[string]string{"tests/Feature/x_test.go": head + "var _ = context.Background\nvar _ = test.As\n\nfunc TestX(t *testing.T) {\n\tclient.ActingAs(alice).Get(\"/\")\n}\n"},
			[]string{"tests/Feature/x_test.go:15"}},
		{"a literal only asks Can", map[string]string{"tests/Feature/x_test.go": head +
			"func TestX(t *testing.T) {\n\tr := auth.Subject{ID: \"u1\", Roles: []string{\"reader\"}}\n\t_ = policy.Can(context.Background(), r, \"a\", nil)\n\t_ = policy.Can(context.Background(), &auth.Subject{ID: \"u2\"}, \"a\", nil)\n}\n"}, nil},
		{"the kit's As signs it in", map[string]string{"tests/Feature/x_test.go": head +
			"func TestX(t *testing.T) {\n\ttest.As(t, server, auth.Subject{ID: \"u1\", Tenant: \"t1\"})\n\tme := auth.Subject{ID: \"u2\", Tenant: \"t1\"}\n\ttest.As(t, server, me)\n}\n"}, nil},
		{"an empty subject is nobody", map[string]string{"tests/Feature/x_test.go": head + "var _ = test.As\n\nfunc TestX(t *testing.T) {\n\t_ = context.Background\n\tsignedIn(t, auth.Subject{})\n}\n"}, nil},
		{"a policy unit test", map[string]string{"tests/Unit/x_test.go": strings.Replace(head, "feature_test", "unit_test", 1) +
			"func TestX(t *testing.T) {\n\tsignedIn(t, auth.Subject{ID: \"u1\", Roles: []string{\"admin\"}})\n}\n"}, nil},
	})
}

func TestTestCSSLocator(t *testing.T) {
	head := "//go:build e2e\n\npackage e2e_test\n\nimport (\n\t\"testing\"\n\n\t\"github.com/arandu-io/framework/test/browser\"\n)\n\n"
	runSuiteCases(t, "test-css-locator", []suiteCase{
		{"CSS with no reason", map[string]string{"tests/E2E/x_test.go": head + "func TestX(t *testing.T) {\n\tpage.Click(browser.CSS(\".pay\", \"\"))\n}\n"},
			[]string{"tests/E2E/x_test.go:12"}},
		{"a harness clicks a selector", map[string]string{"tests/Browser/x_test.go": "package browser_test\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {\n\th.Click(\"#submit\")\n\th.WaitVisible(\"form > button\")\n}\n"},
			[]string{"tests/Browser/x_test.go:6", "tests/Browser/x_test.go:7"}},
		{"querySelector in a script", map[string]string{"tests/E2E/x_test.go": head + "var _ = browser.Text\n\nfunc TestX(t *testing.T) {\n\tpage.Evaluate(`document.querySelector('.x')`, nil)\n}\n"},
			[]string{"tests/E2E/x_test.go:14"}},
		{"CSS with its reason, and the user's words", map[string]string{"tests/E2E/x_test.go": head +
			"func TestX(t *testing.T) {\n\tpage.Click(browser.CSS(\"canvas.chart\", \"the chart has no accessible name\"))\n\tpage.ClickText(\".Pay\")\n\tpage.Fill(\"#tag\", \"urgent\")\n\tpage.ClickRole(\"button\", \"Pay\")\n}\n"}, nil},
		{"outside the journeys", map[string]string{"tests/Feature/x_test.go": "package feature_test\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) { h.Click(\"#submit\") }\n"}, nil},
	})
}

func TestTestCopiedConformance(t *testing.T) {
	runSuiteCases(t, "test-copied-conformance", []suiteCase{
		{"a copy in its directory", map[string]string{"tests/NativeAuth/x_test.go": "package nativeauth_test\n\nimport \"testing\"\n\nfunc TestMine(t *testing.T) {}\n"},
			[]string{"tests/NativeAuth/x_test.go:1"}},
		{"a copied test by name", map[string]string{"tests/Feature/x_test.go": "package feature_test\n\nimport \"testing\"\n\nfunc TestAccountContract(t *testing.T) {}\n"},
			[]string{"tests/Feature/x_test.go:5"}},
		{"the kit, called", map[string]string{"tests/Conformance/x_test.go": "package conformance_test\n\nimport (\n\t\"testing\"\n\n\t\"github.com/arandu-io/hesape/arandutest/conformance\"\n)\n\nfunc TestAccountContract(t *testing.T) { conformance.Boundaries(t, \"../..\") }\n"}, nil},
		{"an application's own test", map[string]string{"tests/Feature/x_test.go": "package feature_test\n\nimport \"testing\"\n\nfunc TestInvoicesAreListed(t *testing.T) {}\n"}, nil},
	})
}

func TestE2ENotRunInCI(t *testing.T) {
	journey := map[string]string{"tests/E2E/x_test.go": "//go:build e2e\n\npackage e2e_test\n"}
	with := func(path, body string) map[string]string {
		out := map[string]string{path: body}
		for k, v := range journey {
			out[k] = v
		}
		return out
	}
	runSuiteCases(t, "e2e-not-run-in-ci", []suiteCase{
		{"no pipeline", journey, []string{"tests/E2E/x_test.go:1"}},
		{"a pipeline that only vets", with(".github/workflows/ci.yml", "steps:\n  - run: go vet -tags e2e ./...\n  - run: go test ./...\n"), []string{"tests/E2E/x_test.go:1"}},
		{"aru test --smoke", with(".github/workflows/ci.yml", "steps:\n  - run: aru test --smoke\n"), nil},
		{"go test with the tag", with(".gitlab-ci.yml", "test:\n  script: go test -race -tags=integration,e2e ./tests/E2E/...\n"), nil},
		{"aru test --e2e at night", with(".github/workflows/nightly.yaml", "on: schedule\nsteps:\n  - run: aru test --e2e --driver=playwright\n"), nil},
		{"no journeys", map[string]string{"tests/Feature/x_test.go": "package feature_test\n"}, nil},
	})
}

func TestSkipWithoutReason(t *testing.T) {
	body := func(call string) string {
		return "package feature_test\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {\n\t" + call + "\n}\n"
	}
	runSuiteCases(t, "skip-without-reason", []suiteCase{
		{"Skip()", map[string]string{"tests/Feature/x_test.go": body("t.Skip()")}, []string{"tests/Feature/x_test.go:6"}},
		{"SkipNow", map[string]string{"tests/Feature/x_test.go": body("t.SkipNow()")}, []string{"tests/Feature/x_test.go:6"}},
		{"an empty reason", map[string]string{"app/x_internal_test.go": strings.Replace(body(`t.Skipf("")`), "feature_test", "app", 1)}, []string{"app/x_internal_test.go:6"}},
		{"a reason", map[string]string{"tests/Feature/x_test.go": body(`t.Skip("needs Postgres: set ARANDU_TEST_DATABASE_URL")`)}, nil},
		{"a reason with a value", map[string]string{"tests/Feature/x_test.go": body(`t.Skipf("no browser: %v", err)`)}, nil},
	})
}

func TestDriverSpecificAPIInTest(t *testing.T) {
	runSuiteCases(t, "driver-specific-api-in-test", []suiteCase{
		{"a journey imports the native driver", map[string]string{"tests/E2E/x_test.go": "//go:build e2e\n\npackage e2e_test\n\nimport \"github.com/arandu-io/framework/test/browser/cdp\"\n\nvar _ = cdp.Launch\n"},
			[]string{"tests/E2E/x_test.go:5"}},
		{"a harness imports chromedp", map[string]string{"tests/Browser/harness.go": "package browser\n\nimport \"github.com/chromedp/chromedp\"\n\nvar _ = chromedp.Navigate\n"},
			[]string{"tests/Browser/harness.go:3"}},
		{"a test reaches the driver's type", map[string]string{"tests/E2E/x_test.go": "package e2e_test\n\nfunc f() { _ = page.Driver().(interface{ Close() error }) }\n"},
			[]string{"tests/E2E/x_test.go:3"}},
		{"the Registry in the test case", map[string]string{"tests/TestCase.go": "package tests\n\nimport (\n\t\"github.com/arandu-io/framework/test/browser\"\n\t\"github.com/arandu-io/framework/test/browser/cdp\"\n\t\"github.com/hyz-is/arandu-playwright\"\n)\n\nvar Drivers = browser.Registry{\"cdp\": cdp.Launch, \"playwright\": playwright.Launch}\n"}, nil},
		{"a journey on the page alone", map[string]string{"tests/E2E/x_test.go": "package e2e_test\n\nimport \"github.com/arandu-io/framework/test/browser\"\n\nvar _ = browser.Role\n"}, nil},
	})
}

// TestABrowserProgramIsSentToTheJourneys: program-under-scripts names the
// end-to-end layer and the kit, not the browser directory it absorbed.
func TestABrowserProgramIsSentToTheJourneys(t *testing.T) {
	root := structureProject(t, map[string]string{
		"scripts/tour/main.go": "package main\n\nimport _ \"github.com/chromedp/chromedp\"\n\nfunc main() {}\n",
	})
	got := findingsOf(t, root, "program-under-scripts")
	if len(got) != 1 {
		t.Fatalf("%d findings, want 1", len(got))
	}
	for _, want := range []string{"tests/E2E", "aru test --e2e", "github.com/arandu-io/framework/test/browser"} {
		if !strings.Contains(got[0].Message+got[0].Why, want) {
			t.Errorf("the finding does not name %s: %s", want, got[0])
		}
	}
	if strings.Contains(got[0].Message+got[0].Why, "tests/Browser") {
		t.Errorf("the finding still sends the program to tests/Browser: %s", got[0])
	}
}
