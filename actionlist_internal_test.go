package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestActionListPrintsTheCatalogueOfTheTreeItIsIn covers the command rather
// than the reader: what a person sees, and that it came from this tree.
//
// It runs in a plain Go module with no arandu.toml, deliberately. A package
// that declares actions has to be able to list its own before any application
// has taken it on, which is why the command finds the nearest module rather
// than the nearest project.
func TestActionListPrintsTheCatalogueOfTheTreeItIsIn(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.test/catalogue\n\ngo 1.26\n")
	write("app/Policies/InvoicePolicy.go", `package policies

import "github.com/arandu-io/framework/security"

const (
	// InvoiceView is reading one record.
	InvoiceView security.Action = "invoice.view"
	// InvoiceDelete is removing one.
	InvoiceDelete security.Action = "invoice.delete"
)
`)
	write("app/Policies/PaymentPolicy.go", `package policies

import "github.com/arandu-io/framework/security"

// PaymentRefund is sending the money back.
const PaymentRefund security.Action = "payment.refund"
`)
	t.Chdir(root)

	code, stdout, stderr := exercise(t, "action:list")
	if code != 0 {
		t.Fatalf("action:list exited %d: %s", code, stderr)
	}

	for _, want := range []string{
		// The value Grant.Check compares, the identifier a screen stores, the
		// line to open, and the sentence written where the constant is.
		"invoice.delete", "InvoiceDelete", "app/Policies/InvoicePolicy.go:9", "removing one.",
		"payment.refund", "PaymentRefund",
		// The module halves are the grouping, read out of the names.
		"invoice\n", "payment\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("action:list does not print %q:\n%s", want, stdout)
		}
	}

	// --module narrows it, and narrowing to one has to leave the other out --
	// a filter that printed everything would pass every check above.
	code, filtered, stderr := exercise(t, "action:list", "--module=payment")
	if code != 0 {
		t.Fatalf("action:list --module exited %d: %s", code, stderr)
	}
	if !strings.Contains(filtered, "payment.refund") {
		t.Errorf("--module=payment dropped the action it asked for:\n%s", filtered)
	}
	if strings.Contains(filtered, "invoice.") {
		t.Errorf("--module=payment printed another module's actions:\n%s", filtered)
	}
}

// TestActionListSaysWhatToWriteWhenThereIsNothing covers the empty answer.
//
// A command that printed nothing would be indistinguishable from one pointed at
// the wrong directory, and this is the first thing somebody runs.
func TestActionListSaysWhatToWriteWhenThereIsNothing(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/empty\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	code, stdout, stderr := exercise(t, "action:list")
	if code != 0 {
		t.Fatalf("action:list exited %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "no action is declared") {
		t.Errorf("an empty catalogue prints nothing a reader can act on:\n%s", stdout)
	}
	if !strings.Contains(stdout, "security.Action") {
		t.Errorf("the empty answer does not say what an action looks like:\n%s", stdout)
	}
}
