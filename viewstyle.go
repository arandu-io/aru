package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// The module a scoped style block comes from, and the function that marks one.
const (
	componentModule = "github.com/arandu-io/kyse"
	styleFunc       = "CSS"
)

// styleRoots are the directories a scoped block is read out of, relative to the
// project.
//
// The compiled views rather than their sources: a block is written inside a
// component call, in the middle of an interpolation, and a .kyse.go is not Go
// -- go/ast cannot read one. The output of the view build is Go, holds the
// expression verbatim, and carries //line directives back to the source, so a
// refusal names the line the person actually wrote.
//
// And the application's own package, because a screen assembled in Go rather
// than in a view is a shape this framework supports.
//
// Anywhere else is not read: a block written in internal/, in pkg/ or at the
// root compiles to nothing, and the element carries a class no rule was emitted
// for. That is the failure this pass exists to remove, so it is stated here and
// in the upgrade guide rather than left to be discovered.
//
// It is NOT the same boundary Tailwind draws -- an earlier version of this
// comment said it was, and the two differ in both directions. Tailwind reads
// resources/views/**, their compiled output and the kyse module, and not app/;
// this reads app/ and not resources/views/**, because the sources there are not
// Go. What they have in common is only that both are decided at build time from
// the text in the source.
var styleRoots = []string{"storage/framework/views", "app"}

// scopedStylesheet is the CSS compiled from every kyse.CSS block in the project,
// ready to be appended to what Tailwind is given.
//
// # Why the block does not travel in the page
//
// The policy is style-src 'self' with no unsafe-inline. A style attribute and a
// style element are both dropped by the browser, and dropped silently -- the
// markup reads correctly and nothing happens. So the rules go into the
// stylesheet the origin already serves, and what the element carries is a class.
//
// # How the two sides agree on the class
//
// The class is the hash of the block's text, computed here and computed again at
// render time by the same three lines. Neither side knows about the other and
// they agree because they are reading the same bytes. There is no table, and
// therefore no table to fall out of step.
//
// # Why the argument has to be a literal
//
// A block built at run time is a hash the render can compute and this never saw,
// so no rule is emitted and the element carries a class nothing styles. That
// renders, passes every test that reads the markup, and is simply unstyled. It
// is refused here instead, with the file and the line.
//
// # Why the rules are not in a layer
//
// Everything else in the stylesheet is: the component classes are in
// @layer components and the utilities in @layer utilities, and that ordering is
// what lets a utility beat a component class without an !important. Unlayered
// CSS beats every layer, so a scoped block wins over both -- which is what the
// block is for. It is the escape hatch, and an escape hatch that loses to a
// utility is not one.
func scopedStylesheet(root string) (string, error) {
	blocks, err := scopedStyleBlocks(root)
	if err != nil {
		return "", err
	}
	if len(blocks) == 0 {
		return "", nil
	}

	var out strings.Builder
	out.WriteString("\n/* Scoped component styles, compiled from kyse.CSS. */\n")
	for _, text := range blocks {
		rules, _ := scopeRules(text, styleClass(text))
		out.WriteString(rules)
	}
	return out.String(), nil
}

// scopedStyleBlocks are the distinct blocks the project writes, in the order
// their classes sort.
//
// Distinct, because two callers writing the same block get the same class, and
// emitting the rules twice would put the same declarations in the stylesheet
// twice. Sorted, because the output is compared byte for byte by whoever wants
// to know whether a build changed anything.
func scopedStyleBlocks(root string) ([]string, error) {
	found := map[string]string{}
	var problems []string

	for _, dir := range styleRoots {
		at := filepath.Join(root, dir)
		if _, err := os.Stat(at); err != nil {
			// A project with no app package, or one whose views have not been
			// compiled, is not an error: it has no blocks.
			continue
		}
		err := filepath.WalkDir(at, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			return readStyleBlocks(path, found, &problems)
		})
		if err != nil {
			return nil, err
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("%s", strings.Join(problems, "\n"))
	}

	classes := make([]string, 0, len(found))
	for class := range found {
		classes = append(classes, class)
	}
	sort.Strings(classes)

	out := make([]string, 0, len(classes))
	for _, class := range classes {
		out = append(out, found[class])
	}
	return out, nil
}

// readStyleBlocks adds the blocks in one file to found, and the reasons any
// could not be read to problems.
func readStyleBlocks(path string, found map[string]string, problems *[]string) error {
	fset := token.NewFileSet()
	// Comments are parsed because //line directives are among them, and they are
	// what puts a refusal on the line of the .kyse.go somebody wrote rather than
	// on a line of generated Go they have never seen.
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		// Not this pass's error to report. The view build already refused what
		// it could not compile, and the project's own Go is the Go compiler's
		// to complain about -- in a message about the actual mistake rather
		// than about a stylesheet.
		return nil
	}

	names, dot := styleNames(file)
	if dot {
		where := fset.Position(file.Package)
		*problems = append(*problems, fmt.Sprintf(
			"%s:%d: %s is imported with a dot, and a scoped block written here would be lost.\n"+
				"    A dot import writes CSS(…) with no package name, which this pass cannot tell from any "+
				"other call of that name. Import it under its own name.",
			where.Filename, where.Line, componentModule))
		return nil
	}
	if len(names) == 0 {
		return nil
	}

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != styleFunc {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok || !names[pkg.Name] {
			return true
		}

		where := fset.Position(call.Lparen)
		if len(call.Args) != 1 {
			*problems = append(*problems, fmt.Sprintf(
				"%s:%d: kyse.%s takes one argument", where.Filename, where.Line, styleFunc))
			return true
		}

		text, literal := stringLiteral(call.Args[0])
		if !literal {
			*problems = append(*problems, fmt.Sprintf(
				"%s:%d: the argument to kyse.%s has to be written out here.\n"+
					"    The stylesheet is compiled from the text in the source, so a block built at run time "+
					"is a class the page asks for and no rule was emitted for -- which renders, and is unstyled.",
				where.Filename, where.Line, styleFunc))
			return true
		}
		// Asked of the substitution rather than of the text, so the two cannot
		// disagree about what an & is. A block whose only ampersand is inside
		// quotes, a comment or a url() has none that is a selector, and it read
		// as scoped while compiling to a rule for the whole page.
		if _, replaced := scopeRules(text, "probe"); replaced == 0 {
			*problems = append(*problems, fmt.Sprintf(
				"%s:%d: this scoped block has no & that is a selector.\n"+
					"    & is the component's own element, and a block without one is a rule that applies to "+
					"the whole page under a name that says otherwise. An & inside quotes, a comment or a "+
					"url() is not one.",
				where.Filename, where.Line))
			return true
		}

		found[styleClass(text)] = text
		return true
	})
	return nil
}

// styleNames are the identifiers this file calls the component module by. It is
// usually one, and it is read from the import block rather than assumed, because
// an alias is legal Go and a compiler that only knows one spelling emits no rule
// for the file that used the other.
func styleNames(file *ast.File) (map[string]bool, bool) {
	names := map[string]bool{}
	dot := false
	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil || path != componentModule {
			continue
		}
		if imported.Name != nil {
			switch imported.Name.Name {
			case "_":
			case ".":
				// A dot import writes CSS("…") with no package in front of it,
				// which is an Ident and never the SelectorExpr this walk looks
				// for. Reported rather than searched for: finding it would mean
				// resolving every bare call in the file against the package, and
				// the third silent way to lose a block is not worth a resolver.
				dot = true
			default:
				names[imported.Name.Name] = true
			}
			continue
		}
		names[path[strings.LastIndexByte(path, '/')+1:]] = true
	}
	return names, dot
}

// stringLiteral is the text of an untyped string literal, and whether the
// expression was one.
//
// Concatenation of literals counts: a long block split across lines with + is
// still text that is entirely in the source, which is the whole of what this
// pass needs. Anything holding a name does not, however constant it looks -- a
// constant is a name here and reading it would mean resolving the package.
func stringLiteral(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		text, err := strconv.Unquote(e.Value)
		if err != nil {
			return "", false
		}
		return text, true
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		left, ok := stringLiteral(e.X)
		if !ok {
			return "", false
		}
		right, ok := stringLiteral(e.Y)
		if !ok {
			return "", false
		}
		return left + right, true
	case *ast.ParenExpr:
		return stringLiteral(e.X)
	}
	return "", false
}

// styleClass is view.StyleClass, computed here.
//
// The CLI is a separate module and cannot import the framework, so it computes
// the same three lines -- the arrangement AssetHash already carries, and the
// reason both sides have a named function and a test rather than an expression
// written twice.
func styleClass(css string) string {
	sum := sha256.Sum256([]byte(css))
	return "k-" + hex.EncodeToString(sum[:])[:12]
}

// scopeRules replaces & with the block's own class, which is what makes the
// rules apply to one component and not to the page. It answers the rewritten
// text and how many ampersands it actually replaced.
//
// It is a substitution and not a CSS parse, for the reason the rest of this
// toolchain avoids parsers it does not need: the block is a few lines somebody
// wrote about one element, and a parser here would be a second CSS
// implementation to keep in step with the one that compiles the stylesheet.
//
// # The three places an & is not a selector
//
// Inside quotes -- content: "&" is an ampersand on the page. Inside a comment.
// And inside an unquoted url(), where the query string of
// url(/i/x.png?a=1&b=2) is two parameters and not a selector.
//
// The comment case is the one that was wrong first, and it was wrong in the
// direction that matters. This read quotes and not comments, so an apostrophe in
// English prose -- /* the card's own gap */ -- opened quote mode and nothing
// closed it: every & after that comment was left alone, the element got a class,
// and the stylesheet got a bare & at the top. The count is the other half of
// that fix: the caller refuses a block this substituted nothing in, so the guard
// and the substitution can no longer disagree about what an & is.
func scopeRules(css, class string) (string, int) {
	var out strings.Builder
	out.Grow(len(css) + 32)

	var quote byte
	inComment := false
	inURL := false
	replaced := 0

	for i := 0; i < len(css); i++ {
		c := css[i]
		switch {
		case inComment:
			if c == '*' && i+1 < len(css) && css[i+1] == '/' {
				out.WriteByte(c)
				i++
				c = css[i]
				inComment = false
			}
		case quote != 0:
			if c == '\\' && i+1 < len(css) {
				out.WriteByte(c)
				i++
				c = css[i]
				break
			}
			if c == quote {
				quote = 0
			}
		case inURL:
			if c == ')' {
				inURL = false
			}
		case c == '/' && i+1 < len(css) && css[i+1] == '*':
			out.WriteByte(c)
			i++
			c = css[i]
			inComment = true
		case c == '\'' || c == '"':
			quote = c
		case c == '&':
			out.WriteString("." + class)
			replaced++
			continue
		case opensURL(css[i:]):
			out.WriteString(css[i : i+4])
			i += 3
			inURL = true
			continue
		}
		out.WriteByte(c)
	}

	text := strings.TrimSpace(out.String())
	if text == "" {
		return "", replaced
	}
	return text + "\n", replaced
}

// opensURL reports whether the text begins with a url( token, in any case.
func opensURL(text string) bool {
	return len(text) >= 4 && strings.EqualFold(text[:4], "url(")
}
