package doctor_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/doctor"
)

const requestHead = `package requests

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/arandu-io/hesape/validation"
)

var emailRE = regexp.MustCompile(` + "`^.+@.+$`" + `)
var uuidRE = regexp.MustCompile(` + "`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`" + `)
var urlRE = regexp.MustCompile(` + "`^https?://`" + `)
var plateRE = regexp.MustCompile(` + "`^[A-Z]{3}[0-9]{4}$`" + `)

var _ = strconv.Itoa
var _ = strings.TrimSpace
var _ = slices.Contains
var _ = utf8.RuneCountInString

type Person struct {
	Name  string ` + "`form:\"full_name\"`" + `
	Email string ` + "`form:\"email\"`" + `
	Ref   string
	Kind  string
	Age   string
}

`

// handRuleCases are the bodies of a Validate method, and the "field|rule" pairs
// the rule must report for each.
func TestRequestValidatesByHand(t *testing.T) {
	validate := func(body string) string {
		return requestHead + "func (r Person) Validate() validation.Errors {\n\te := validation.Errors{}\n" + body + "\n\treturn e\n}\n"
	}
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"empty text", "if r.Name == \"\" {\n e.Add(\"full_name\", \"required\")\n}", []string{"full_name|required"}},
		{"empty after a trim", "if strings.TrimSpace(r.Name) == \"\" {\n e.Add(\"full_name\", \"required\")\n}", []string{"full_name|required"}},
		{"no length", "if len(r.Name) == 0 {\n e.Add(\"full_name\", \"required\")\n}", []string{"full_name|required"}},
		{"too long", "if len(r.Name) > 255 {\n e.Add(\"full_name\", \"long\")\n}", []string{"full_name|max:255"}},
		{"too long by runes", "if utf8.RuneCountInString(r.Name) > 80 {\n e.Add(\"full_name\", \"long\")\n}", []string{"full_name|max:80"}},
		{"too short", "if len(r.Name) < 3 {\n e.Add(\"full_name\", \"short\")\n}", []string{"full_name|min:3"}},
		{"the field is the one the failure is added to", "if r.Name == \"\" {\n e.Add(\"x\", \"required\")\n}", []string{"x|required"}},
		{"an address", "if !emailRE.MatchString(r.Email) {\n e.Add(\"email\", \"bad\")\n}", []string{"email|email"}},
		{"an address behind a guard", "if r.Email != \"\" && !emailRE.MatchString(r.Email) {\n e.Add(\"email\", \"bad\")\n}", []string{"email|email"}},
		{"a uuid", "if !uuidRE.MatchString(r.Ref) {\n e.Add(\"ref\", \"bad\")\n}", []string{"ref|uuid"}},
		{"a url", "if !urlRE.MatchString(r.Ref) {\n e.Add(\"ref\", \"bad\")\n}", []string{"ref|url"}},
		{"an integer", "if _, err := strconv.Atoi(r.Age); err != nil {\n e.Add(\"age\", \"bad\")\n}", []string{"age|integer"}},
		{"the name of the input is the form tag of the field", "if _, err := strconv.Atoi(r.Name); err != nil {\n e.Add(\"full_name\", \"bad\")\n}\nif _, err := strconv.Atoi(r.Email); err != nil {\n e.Add(\"email\", \"bad\")\n}", []string{"full_name|integer", "email|integer"}},
		{"a number", "_, err := strconv.ParseFloat(r.Age, 64)\nif err != nil {\n e.Add(\"age\", \"bad\")\n}", []string{"age|numeric"}},
		{"a list the value must be in", "if !slices.Contains([]string{\"a\", \"b\"}, r.Kind) {\n e.Add(\"kind\", \"bad\")\n}", []string{"kind|in"}},
		{"a loop over the cases", "ok := false\nfor _, k := range []string{\"a\", \"b\"} {\n if k == r.Kind {\n ok = true\n }\n}\nif !ok {\n e.Add(\"kind\", \"bad\")\n}", []string{"kind|in"}},
		{"the helpers of the validation package", "validation.Required(e, \"name\", r.Name)\nvalidation.MaxLen(e, \"name\", r.Name, 255)\nvalidation.Email(e, \"email\", r.Email)\nvalidation.MinLen(e, \"ref\", r.Ref, 3)",
			[]string{"name|required", "name|max:255", "email|email", "ref|min:3"}},
		{"one finding per field and rule", "if r.Name == \"\" {\n e.Add(\"full_name\", \"a\")\n}\nif r.Name == \"\" {\n e.Add(\"full_name\", \"b\")\n}", []string{"full_name|required"}},

		{"a comparison of two fields", "if r.Name == r.Ref {\n e.Add(\"ref\", \"same\")\n}", nil},
		{"a length compared with a field", "if len(r.Name) > len(r.Ref) {\n e.Add(\"name\", \"long\")\n}", nil},
		{"a pattern of the domain", "if !plateRE.MatchString(r.Ref) {\n e.Add(\"ref\", \"bad\")\n}", nil},
		{"a parse whose number is compared", "if n, err := strconv.Atoi(r.Age); err != nil || n > 120 {\n e.Add(\"age\", \"bad\")\n}", nil},
		{"a text compared with a literal", "if r.Kind == \"forbidden\" {\n e.Add(\"kind\", \"bad\")\n}", nil},
		{"a check with no failure added", "if r.Name == \"\" {\n return nil\n}", nil},
		{"a lookup", "if taken(r.Email) {\n e.Add(\"email\", \"taken\")\n}", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := structureProject(t, map[string]string{"app/Http/Requests/Person.go": validate(c.body)})
			var got []string
			for _, f := range findingsOf(t, root, "request-validates-by-hand") {
				if f.Severity != doctor.Warning {
					t.Errorf("reported at %s; the rule is a warning until it is promoted", f.Severity)
				}
				if !strings.Contains(f.Why, "A warning until 2026-12-15, an error after it.") {
					t.Errorf("the finding does not say when it becomes an error: %s", f.Why)
				}
				if !strings.Contains(f.Message, "Person.Rules()") {
					t.Errorf("the message does not name Rules(): %s", f.Message)
				}
				got = append(got, between(f.Message, "checks \"", "\" by hand")+"|"+between(f.Message, "the rule is `", "`"))
			}
			sort.Strings(got)
			want := append([]string(nil), c.want...)
			sort.Strings(want)
			if strings.Join(got, " ") != strings.Join(want, " ") {
				t.Errorf("reported %v, want %v", got, want)
			}
		})
	}
}

// TestRequestWithRulesIsLeftAlone: a request that declares its Rules keeps
// whatever Validate does, in this file or in another one of the package.
func TestRequestWithRulesIsLeftAlone(t *testing.T) {
	validate := requestHead + "func (r Person) Validate() validation.Errors {\n\te := validation.Errors{}\n\tif r.Name == \"\" {\n\t\te.Add(\"full_name\", \"required\")\n\t}\n\treturn e\n}\n"
	rules := "package requests\n\nimport \"github.com/arandu-io/hesape/validation\"\n\nfunc (Person) Rules() validation.Rules { return validation.Rules{\"full_name\": \"required\"} }\n"

	for name, files := range map[string]map[string]string{
		"in the same file":     {"app/Http/Requests/Person.go": validate + "\nfunc (Person) Rules() validation.Rules { return validation.Rules{} }\n"},
		"in another file":      {"app/Http/Requests/Person.go": validate, "app/Http/Requests/PersonRules.go": rules},
		"outside the requests": {"app/Services/Person.go": strings.Replace(validate, "package requests", "package services", 1)},
	} {
		root := structureProject(t, files)
		if found := findingsOf(t, root, "request-validates-by-hand"); len(found) != 0 {
			t.Errorf("%s: reported %d findings on a request that declares its Rules or is no request", name, len(found))
		}
	}
}

// TestRequestRulesStaysQuietWhenARequestFileDoesNotParse: a Rules declared in a
// file the doctor could not read would be missed, and the rule concludes from a
// Rules that is absent.
func TestRequestRulesStaysQuietWhenARequestFileDoesNotParse(t *testing.T) {
	validate := requestHead + "func (r Person) Validate() validation.Errors {\n\te := validation.Errors{}\n\tif r.Name == \"\" {\n\t\te.Add(\"full_name\", \"required\")\n\t}\n\treturn e\n}\n"
	root := structureProject(t, map[string]string{
		"app/Http/Requests/Person.go":      validate,
		"app/Http/Requests/PersonRules.go": "package requests\n\nfunc (Person) Rules() {\n",
	})
	if found := findingsOf(t, root, "request-validates-by-hand"); len(found) != 0 {
		t.Errorf("reported %d findings with a request file unread", len(found))
	}
}

func between(s, from, to string) string {
	i := strings.Index(s, from)
	if i < 0 {
		return ""
	}
	s = s[i+len(from):]
	j := strings.Index(s, to)
	if j < 0 {
		return ""
	}
	return s[:j]
}

// TestThePlantedRequestIsReportedFieldByField: the fixture's request checks five
// fields by hand and one of them twice, and the near misses beside it in the
// gaps fixture are rules of the domain that no string of rules says.
func TestThePlantedRequestIsReportedFieldByField(t *testing.T) {
	var got []string
	for _, f := range findingsOfRun(t, fixture(t, "violations")) {
		if f.Rule == "request-validates-by-hand" {
			got = append(got, between(f.Message, "checks \"", "\" by hand")+"|"+between(f.Message, "the rule is `", "`"))
		}
	}
	sort.Strings(got)
	want := []string{"age|integer", "bio|max:500", "email|email", "kind|in", "name|max:255", "name|required"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("the planted request reported %v, want %v", got, want)
	}

	for _, f := range gaps(t) {
		if f.Rule == "request-validates-by-hand" {
			t.Errorf("a check of the domain was reported: %s", f.Message)
		}
	}
}

func findingsOfRun(t *testing.T, root string) []doctor.Finding {
	t.Helper()
	findings, err := doctor.Run(root, doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return findings
}
