package gen_test

import (
	"testing"

	"github.com/arandu-io/aru/internal/gen"
)

// An --int enum is stored as an integer, so its numbers are the meaning of every
// row already written. Re-running the command to add a value -- which is what
// --force is for -- must not renumber by position: inserting "sent" between
// "draft" and "paid" would move paid from 2 to 3, and every row holding 2 would
// silently become "sent". Nothing fails, nothing logs, and the report arrives
// months later as "some invoices changed status by themselves".
func TestANumberCanBePinnedSoAddingAValueDoesNotRepointTheRows(t *testing.T) {
	got, err := gen.ParseEnumValues("draft=1,paid=2,sent=3", "Status", true)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"draft": 1, "paid": 2, "sent": 3}
	for _, v := range got {
		if want[v.Name] != v.Number {
			t.Errorf("%s is %d, want %d -- the rows already written mean the old number", v.Name, v.Number, want[v.Name])
		}
	}
}

// Half-pinned is refused rather than guessed at: what "draft,sent=7,paid" means
// is not knowable, and a wrong guess is the same silent repointing.
func TestMixingPinnedAndPositionalIsRefused(t *testing.T) {
	if _, err := gen.ParseEnumValues("draft,sent=7,paid", "Status", true); err == nil {
		t.Fatal("a half-pinned list was accepted, so two of the three numbers were guessed")
	}
}

// Two values on one number is a lookup that answers whichever the compiler
// ordered first.
func TestTwoValuesCannotShareANumber(t *testing.T) {
	if _, err := gen.ParseEnumValues("draft=1,sent=1", "Status", true); err == nil {
		t.Fatal("two values were given the same number")
	}
}

// Zero is the zero value of the type, so a value numbered zero is
// indistinguishable from a field nobody set.
func TestZeroIsNotAValue(t *testing.T) {
	if _, err := gen.ParseEnumValues("unset=0,draft=1", "Status", true); err == nil {
		t.Fatal("a value was numbered zero, which is what an unset field reads as")
	}
}

// A text enum has no numbers to pin, and accepting the syntax would suggest it
// stores one.
func TestPinningANumberOnATextEnumIsRefused(t *testing.T) {
	if _, err := gen.ParseEnumValues("draft=1,sent=2", "Status", false); err == nil {
		t.Fatal("a text enum accepted pinned numbers")
	}
}

// The first run has nothing to repoint, and writing "=1,=2,=3" there would be
// ceremony.
func TestTheFirstRunStillNumbersByPosition(t *testing.T) {
	got, err := gen.ParseEnumValues("draft,sent,paid", "Status", true)
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range got {
		if v.Number != i+1 {
			t.Errorf("%s is %d, want %d", v.Name, v.Number, i+1)
		}
	}
}
