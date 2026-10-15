package doctor_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/arandu-io/aru/internal/doctor"
)

func warning(file, rule string) doctor.Finding {
	return doctor.Finding{Rule: rule, Severity: doctor.Warning, File: file, Line: 1}
}

// TestABaselineCountsWarningsByRuleAndFile: errors are left out, the lines a
// finding sits on are not part of the key, and the entries come out sorted by
// file and then by rule whatever order the findings arrived in.
func TestABaselineCountsWarningsByRuleAndFile(t *testing.T) {
	findings := []doctor.Finding{
		warning("b.go", "z-rule"),
		warning("a.go", "y-rule"),
		{Rule: "x-error", Severity: doctor.Error, File: "a.go", Line: 3},
		warning("a.go", "y-rule"),
		warning("a.go", "a-rule"),
	}
	findings[3].Line = 40
	got := doctor.NewBaseline(doctor.Conventional, findings)
	want := doctor.Baseline{Profile: doctor.Conventional, Warnings: []doctor.BaselineEntry{
		{File: "a.go", Rule: "a-rule", Count: 1},
		{File: "a.go", Rule: "y-rule", Count: 2},
		{File: "b.go", Rule: "z-rule", Count: 1},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NewBaseline = %+v, want %+v", got, want)
	}
}

// TestABaselineReadsBackWhatItWrote: the file Marshal writes, an empty one
// included, is what ReadBaseline reads, and writing it twice gives the same
// bytes.
func TestABaselineReadsBackWhatItWrote(t *testing.T) {
	dir := t.TempDir()
	for name, b := range map[string]doctor.Baseline{
		"empty": {Profile: doctor.Performance, Warnings: []doctor.BaselineEntry{}},
		"entries": doctor.NewBaseline(doctor.Conventional, []doctor.Finding{
			warning(`app/"quoted".go`, "html-template-in-app"),
			warning("app/Services/Page.go", "service-takes-http"),
		}),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name+".json")
			if err := doctor.WriteBaseline(path, b); err != nil {
				t.Fatal(err)
			}
			read, err := doctor.ReadBaseline(path)
			if err != nil {
				t.Fatalf("ReadBaseline: %v", err)
			}
			if !reflect.DeepEqual(read, b) {
				t.Errorf("read %+v, wrote %+v", read, b)
			}
			first, _ := os.ReadFile(path)
			if string(read.Marshal()) != string(first) {
				t.Errorf("a second write differs:\n%s\n%s", first, read.Marshal())
			}
		})
	}
}

// TestAComparisonSeparatesWhatGrewFromWhatShrank: a pair the baseline does
// not name is above it at a baseline of zero, a pair that went away is below
// it at zero found, and New counts warnings rather than pairs.
func TestAComparisonSeparatesWhatGrewFromWhatShrank(t *testing.T) {
	b := doctor.Baseline{Profile: doctor.Conventional, Warnings: []doctor.BaselineEntry{
		{File: "a.go", Rule: "r", Count: 1},
		{File: "b.go", Rule: "r", Count: 2},
		{File: "c.go", Rule: "r", Count: 1},
	}}
	c := b.Compare([]doctor.Finding{
		warning("a.go", "r"), warning("a.go", "r"), warning("a.go", "r"),
		warning("b.go", "r"),
		warning("d.go", "r"),
		{Rule: "e", Severity: doctor.Error, File: "d.go"},
	})
	wantAbove := []doctor.BaselineDelta{
		{File: "a.go", Rule: "r", Found: 3, Baseline: 1},
		{File: "d.go", Rule: "r", Found: 1, Baseline: 0},
	}
	wantBelow := []doctor.BaselineDelta{
		{File: "b.go", Rule: "r", Found: 1, Baseline: 2},
		{File: "c.go", Rule: "r", Found: 0, Baseline: 1},
	}
	if !reflect.DeepEqual(c.Above, wantAbove) {
		t.Errorf("Above = %+v, want %+v", c.Above, wantAbove)
	}
	if !reflect.DeepEqual(c.Below, wantBelow) {
		t.Errorf("Below = %+v, want %+v", c.Below, wantBelow)
	}
	if c.New() != 3 {
		t.Errorf("New() = %d, want 3", c.New())
	}
}
