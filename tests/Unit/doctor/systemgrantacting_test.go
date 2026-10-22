package doctor_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arandu-io/aru/internal/doctor"
)

// SystemGrantActing is SystemGrant with the person who set the work in motion
// recorded on it: the same way past a Policy, with the tenant in the same
// place. Every rule that watches SystemGrant watches it too -- an empty
// tenant, a call outside a seeder, a job and a command, and a tenant read off
// the request -- under any import name, and it is excused where SystemGrant is.
func TestSystemGrantActingIsAuditedLikeSystemGrant(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.test/acting\n\ngo 1.26\n")
	write("app/Services/ActingService.go", `package services

import (
	"net/http"

	hauth "github.com/arandu-io/hesape/auth"
)

func emptyTenant(impersonator string) hauth.Grant {
	return hauth.SystemGrantActing("report.view", "", impersonator)
}

func outsideScope(impersonator string) hauth.Grant {
	return hauth.SystemGrantActing("report.view", "acme", impersonator)
}

func tenantFromRequest(r *http.Request, impersonator string) hauth.Grant {
	org := r.Header.Get("X-Org")
	return hauth.SystemGrantActing("report.view", org, impersonator)
}

func tenantInline(r *http.Request, impersonator string) hauth.Grant {
	return hauth.SystemGrantActing("report.view", r.FormValue("org"), impersonator)
}

func deliberate(impersonator string) hauth.Grant {
	//arandu:system-grant rebuilds the Grant of a job pushed while acting in an account
	return hauth.SystemGrantActing("report.view", "acme", impersonator)
}
`)
	write("app/Jobs/RebuildJob.go", `package jobs

import "github.com/arandu-io/hesape/auth"

func rebuild(tenant, impersonator string) auth.Grant {
	return auth.SystemGrantActing("report.view", tenant, impersonator)
}
`)

	findings, err := doctor.Run(root, doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !mentions(findings, "system-grant-without-tenant", "SystemGrantActing with an empty tenant") {
		t.Errorf("SystemGrantActing with an empty tenant was not reported:\n%v", findings)
	}
	if !mentions(findings, "system-grant-outside-scope", "SystemGrantActing in outsideScope") {
		t.Errorf("SystemGrantActing on a request path was not reported:\n%v", findings)
	}
	for _, fn := range []string{"which line 18 read", "by r.FormValue"} {
		if !mentions(findings, "tenant-from-request", fn) {
			t.Errorf("a tenant from the request reaching SystemGrantActing (%s) was not reported:\n%v", fn, findings)
		}
	}

	// Where SystemGrant is excused, so is this.
	if mentions(findings, "system-grant-outside-scope", "deliberate") {
		t.Error("a call marked //arandu:system-grant with a reason was still reported")
	}
	if findAt(findings, "system-grant-outside-scope", "app/Jobs") != nil {
		t.Error("SystemGrantActing in a job, where the skeleton puts work with no request behind it, was reported")
	}
}
