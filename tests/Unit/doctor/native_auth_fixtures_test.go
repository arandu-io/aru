package doctor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/doctor"
)

// TestTheCanonicalFixturesDoNotTeachTheLegacyAuthModule keeps Doctor's example
// projects on the same native boundary as the application and UI generators.
func TestTheCanonicalFixturesDoNotTeachTheLegacyAuthModule(t *testing.T) {
	for _, item := range []struct {
		project string
		file    string
	}{
		{project: "clean", file: "main.go"},
		{project: "gaps", file: "bootstrap/app.go"},
	} {
		body, err := os.ReadFile(filepath.Join(fixture(t, item.project), filepath.FromSlash(item.file)))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "github.com/arandu-io/framework/modules/auth") {
			t.Errorf("%s/%s teaches the legacy Framework auth module", item.project, item.file)
		}
	}
}

func TestALookalikeOutboxCallIsNotAnImportedWriter(t *testing.T) {
	dir := t.TempDir()
	source := `package main

type eventTools struct{}

func (eventTools) NewOutbox(any) {}

func main() {
	var events eventTools
	events.NewOutbox(nil)
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	findings, err := doctor.Run(dir, doctor.Conventional)
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range findings {
		if finding.Rule == "outbox-not-registered" {
			t.Errorf("a local lookalike was treated as the imported events package: %s", finding)
		}
	}
}
