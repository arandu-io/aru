package kyse_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/kyse"
)

// The tests in this file run what the generator wrote rather than reading it.
//
// What they are about is a value refused at render time, and what the page
// around it looks like afterwards. Reading the generated Go can show that a
// refusal is checked; only running it shows what the browser would be handed,
// which is the thing an attacker gets to shape.

// writeRuntimeModule lays out a module whose native view package behaves like
// the runtime for every call these tests exercise.
//
// It is a stand-in and says so: the escapers refuse and escape as the runtime
// documents -- an address with a scheme outside http, https, mailto and tel, or
// that starts with two slashes, is refused with an empty string beside the
// error; a value between quotes has its six characters escaped; a JavaScript
// literal hex-escapes what would end a script element. What is under test is
// the control flow the generator writes around those calls, so the stand-in
// only has to refuse and escape the values the tests hand it the way the
// runtime does, and it keeps the test from needing a network.
func writeRuntimeModule(t *testing.T, root string) {
	t.Helper()

	writeFile(t, filepath.Join(root, "go.mod"), `module example.test/render

go 1.21

require github.com/arandu-io/hesape v0.0.0

replace github.com/arandu-io/hesape => ./hesape
`)
	writeFile(t, filepath.Join(root, "hesape", "go.mod"), "module github.com/arandu-io/hesape\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "hesape", "view", "view.go"), `package view

import (
	"errors"
	"fmt"
	"html/template"
	"io"
	"sort"
	"strconv"
	"strings"
)

type Func func(w io.Writer, data any) error

var views = map[string]Func{}

func Register(name string, f Func) { views[name] = f }

func Render(w io.Writer, name string, data any) error { return views[name](w, data) }

func WrongData(view, want string, got any) error {
	return fmt.Errorf("%s wants %s, got %T", view, want, got)
}

func Text(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	case fmt.Stringer:
		return x.String()
	}
	return fmt.Sprint(v)
}

func TextAttr(v any) string { return template.HTMLEscapeString(Text(v)) }

func TextURL(v any) (string, error) {
	raw := Text(v)
	for i := 0; i < len(raw); i++ {
		if raw[i] < 0x20 || raw[i] == 0x7f {
			return "", errors.New("view: a URL may not carry a control character")
		}
	}
	if i := strings.IndexByte(raw, ':'); i >= 0 && !strings.ContainsAny(raw[:i], "/?#") {
		switch strings.ToLower(raw[:i]) {
		case "http", "https", "mailto", "tel":
			return template.HTMLEscapeString(raw), nil
		}
		return "", errors.New("view: this URL's scheme is refused")
	}
	if len(raw) >= 2 && (raw[0] == '/' || raw[0] == '\\') && (raw[1] == '/' || raw[1] == '\\') {
		return "", errors.New("view: a URL that starts with two slashes is refused")
	}
	return template.HTMLEscapeString(raw), nil
}

func TextJS(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range Text(v) {
		switch r {
		case '\\':
			b.WriteString("\\\\")
		case '"':
			b.WriteString("\\\"")
		case '\n':
			b.WriteString("\\n")
		case '<', '>', '&', '\u2028', '\u2029':
			fmt.Fprintf(&b, "\\u%04x", r)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, "\\u%04x", r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func TextCSS(v any) (string, error) {
	raw := Text(v)
	if strings.ContainsAny(raw, ";{}()/\\:\"'<>") {
		return "", errors.New("view: a CSS value may not contain that")
	}
	return raw, nil
}

func Attributes(attrs map[string]string) (string, error) {
	names := make([]string, 0, len(attrs))
	for name := range attrs {
		names = append(names, name)
	}
	sort.Strings(names)
	var out strings.Builder
	for _, name := range names {
		if strings.HasPrefix(name, "on") || strings.HasPrefix(name, "hx-") || strings.HasPrefix(name, "data-hx-") {
			return "", fmt.Errorf("view: %q is refused", name)
		}
		out.WriteString(" " + name + "=\"" + template.HTMLEscapeString(attrs[name]) + "\"")
	}
	return out.String(), nil
}
`)
}

// compileView compiles one view source into the module, at the path its
// package lives under.
func compileView(t *testing.T, root, dir, name, dataType, source string) {
	t.Helper()
	path := "resources/views/" + strings.ReplaceAll(name, ".", "/") + ".kyse.go"
	file, err := kyse.Parse(path, source)
	if err != nil {
		t.Fatalf("Parse %s: %v", name, err)
	}
	if dataType == "" {
		dataType = kyse.RenderType(file)
	}
	out, err := kyse.Generate(file, name, dataType, dir+"/"+filepath.Base(path)+".go")
	if err != nil {
		t.Fatalf("Generate %s: %v", name, err)
	}
	writeFile(t, filepath.Join(root, dir, strings.TrimSuffix(filepath.Base(path), ".kyse.go")+".go"), string(out))
}

// runModule runs the module's main package and returns what it printed.
func runModule(t *testing.T, root string) string {
	t.Helper()
	tool := goTool(t)
	run := exec.Command(tool, "run", ".")
	run.Dir = root
	run.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off", "GOTOOLCHAIN=local")
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("the generated Go does not run: %v\n%s", err, out)
	}
	return string(out)
}

const avatarComponent = `//go:build kyse

package components

@go
type AvatarProps struct {
	ImageURL string
	Name     string
}
@endgo

<span class="avatar"><img src="{{ .ImageURL }}" alt="{{ .Name }}"></span>
`

const commentsPage = `//go:build kyse

package views

import "example.test/render/components"

@go
type CommentsPage struct{ Comments []Comment }
type Comment struct{ Author, Avatar, Body string }
@endgo

<ul>
@foreach(.Comments as c)
	<li>{!! components.Avatar(components.AvatarProps{ImageURL: c.Avatar, Name: c.Author}) !!}<strong title="{{ c.Author }}">{{ c.Author }}</strong><p>{{ c.Body }}</p></li>
@endforeach
</ul>
`

// attackerAuthor is a display name chosen to be read as attributes of the
// element a truncated component leaves open: a same-origin POST that sends
// itself on load, carrying fields the server would take as the visitor's own.
const attackerAuthor = ` hx-post=/profile hx-trigger=load hx-vals={"role":"admin"} x`

// A component whose value is refused returns nothing, and never the markup it
// had written up to the refusal.
//
// The prefix is the attack. It ends inside the attribute whose value was
// refused, with the closing quote never written, so the first quote the page
// writes after the component closes that value instead -- and the text between
// that quote and the next is read as attributes. A display name written there
// becomes an hx-post the browser sends on load, with the page's own CSRF header.
func TestARefusedComponentReturnsNothing(t *testing.T) {
	root := t.TempDir()
	writeRuntimeModule(t, root)
	compileView(t, root, "components", "components.avatar", "AvatarProps", avatarComponent)
	writeFile(t, filepath.Join(root, "main.go"), `package main

import (
	"fmt"

	"example.test/render/components"
)

func main() {
	fmt.Printf("refused=%q\n", components.Avatar(components.AvatarProps{ImageURL: "//attacker.example/a.png", Name: "Eve"}))
	fmt.Printf("scheme=%q\n", components.Avatar(components.AvatarProps{ImageURL: "javascript:alert(1)", Name: "Eve"}))
	fmt.Printf("accepted=%q\n", components.Avatar(components.AvatarProps{ImageURL: "https://cdn.example/ana.png", Name: "Ana"}))
}
`)

	got := runModule(t, root)
	for _, want := range []string{
		`refused=""`,
		`scheme=""`,
		`accepted="\n<span class=\"avatar\"><img src=\"https://cdn.example/ana.png\" alt=\"Ana\"></span>\n\n"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("want %s in the output, got:\n%s", want, got)
		}
	}
}

// The page around a refused component parses as it was written, and the
// attacker's display name stays text.
//
// This is the scenario end to end: a comment list draws an avatar per comment
// and the author's name beside it. One author supplies an avatar address the
// escape refuses and a name shaped as attributes. The assertion is the whole
// page, byte for byte, because the property is structural -- every quote the
// view wrote is where the view wrote it, and nothing an author typed lands
// where an attribute name goes.
func TestARefusedComponentLeavesThePageWellFormed(t *testing.T) {
	root := t.TempDir()
	writeRuntimeModule(t, root)
	compileView(t, root, "components", "components.avatar", "AvatarProps", avatarComponent)
	compileView(t, root, "views", "comments", "", commentsPage)
	writeFile(t, filepath.Join(root, "main.go"), `package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/arandu-io/hesape/view"

	"example.test/render/views"
)

func main() {
	var page strings.Builder
	err := view.Render(&page, "comments", views.CommentsPage{Comments: []views.Comment{
		{Author: "Ana", Avatar: "https://cdn.example/ana.png", Body: "hello"},
		{Author: `+"`"+attackerAuthor+"`"+`, Avatar: "//attacker.example/a.png", Body: "hi"},
	}})
	if err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
	fmt.Print(page.String())
}
`)

	got := runModule(t, root)
	const escaped = ` hx-post=/profile hx-trigger=load hx-vals={&#34;role&#34;:&#34;admin&#34;} x`
	want := "<ul>\n" +
		"\t<li>\n<span class=\"avatar\"><img src=\"https://cdn.example/ana.png\" alt=\"Ana\"></span>\n\n" +
		"<strong title=\"Ana\">Ana</strong><p>hello</p></li>\n" +
		"\t<li><strong title=\"" + escaped + "\">" + escaped + "</strong><p>hi</p></li>\n" +
		"</ul>\n"
	if strings.Trim(got, "\n") != strings.TrimSuffix(want, "\n") {
		t.Errorf("the page is not the markup the view wrote:\n got: %q\nwant: %q", got, want)
	}

	// The same property stated without the expected bytes: outside a quoted
	// value, no attribute the page writes is one the author typed.
	if names := attributeNamesIn(got); containsHX(names) {
		t.Errorf("an attribute a visitor wrote reached the markup: %v\n%s", names, got)
	}
}

// attributeNamesIn returns the attribute names a browser would read in markup,
// following quoted values the way the HTML syntax does. It reads only the
// constructs these tests write -- tags, quoted and unquoted values, text -- and
// is not a parser for anything else.
func attributeNamesIn(markup string) []string {
	var names []string
	for i := 0; i < len(markup); i++ {
		if markup[i] != '<' || i+1 >= len(markup) || !isLetter(markup[i+1]) {
			continue
		}
		i++
		for i < len(markup) && !isSpace(markup[i]) && markup[i] != '>' {
			i++
		}
		for i < len(markup) && markup[i] != '>' {
			if isSpace(markup[i]) || markup[i] == '/' {
				i++
				continue
			}
			start := i
			for i < len(markup) && !isSpace(markup[i]) && !strings.ContainsRune("=>/", rune(markup[i])) {
				i++
			}
			names = append(names, markup[start:i])
			for i < len(markup) && isSpace(markup[i]) {
				i++
			}
			if i < len(markup) && markup[i] == '=' {
				i++
				for i < len(markup) && isSpace(markup[i]) {
					i++
				}
				if i < len(markup) && (markup[i] == '"' || markup[i] == '\'') {
					quote := markup[i]
					end := strings.IndexByte(markup[i+1:], quote)
					if end < 0 {
						return names
					}
					i += end + 2
				} else {
					for i < len(markup) && !isSpace(markup[i]) && markup[i] != '>' {
						i++
					}
				}
			}
		}
	}
	return names
}

func containsHX(names []string) bool {
	for _, n := range names {
		if strings.HasPrefix(n, "hx-") || strings.HasPrefix(n, "data-hx-") {
			return true
		}
	}
	return false
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }

// The generated component decides between its markup and nothing on the error,
// and never returns the builder unconditionally.
func TestAComponentReturnsItsMarkupOnlyWhenEveryWriteSucceeded(t *testing.T) {
	file, err := kyse.Parse("resources/views/components/avatar.kyse.go", avatarComponent)
	if err != nil {
		t.Fatal(err)
	}
	out, err := kyse.Generate(file, "components.avatar", "AvatarProps", "resources/views/components/avatar.go")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "if kyse__err != nil {\n\t\treturn \"\"\n\t}\n\treturn kyse__template.HTML(kyse__w.String())") {
		t.Errorf("the component does not return nothing on a refusal:\n%s", got)
	}
	if strings.Contains(got, "_ = kyse__err") {
		t.Errorf("the component still discards the refusal:\n%s", got)
	}
}
