package kyse

import "testing"

// TestABareDotIsThePageItself proves the rule that lets a component ask the
// page a question: `.Title` is a field of the data, so `.` is the data.
func TestABareDotIsThePageItself(t *testing.T) {
	g := &generator{}

	cases := []struct {
		expression string
		want       string
	}{
		{`components.FieldProps{Name: "email", Page: .}`, `components.FieldProps{Name: "email", Page: d}`},
		{".", "d"},
		{"f(.)", "f(d)"},
		// The rule it comes from still holds.
		{".Title", "d.Title"},
		// A number is still a number, and a selector on a loop variable is
		// still a selector.
		{"1.5", "1.5"},
		{".5", ".5"},
		{"feature.Title", "feature.Title"},
		// A dot inside a literal is text somebody wrote.
		{`"a. b"`, `"a. b"`},
	}

	for _, c := range cases {
		if got := g.expr(c.expression); got != c.want {
			t.Errorf("expr(%q) = %q, want %q", c.expression, got, c.want)
		}
	}
}
