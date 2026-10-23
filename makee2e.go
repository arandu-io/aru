package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/arandu-io/aru/internal/gen"
)

// makeE2EUsage is the usage line of make:e2e.
const makeE2EUsage = "aru make:e2e <Journey> [--route=<name-or-path>] [--smoke] [--force] [--dry-run]"

// makeContractUsage is the usage line of make:contract.
const makeContractUsage = `aru make:contract <operation> [--document=docs/contracts/<name>.openapi.json] [--force] [--dry-run]`

// makeE2E writes an end-to-end journey: a test behind the e2e tag that boots
// the server, opens a page on it and visits the route the journey starts at,
// with the steps left to write in its custom block.
//
// The journey opens its page through tests.Serve and tests.Open, so a project
// whose tests package does not declare them is refused with the two helpers
// to paste: a generated test that does not compile is worse than none.
func makeE2E(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:e2e", flag.ContinueOnError)
	fs.SetOutput(stderr)
	route := fs.String("route", "", "the route name, or the path, the journey starts at")
	smoke := fs.Bool("smoke", false, "name the test TestSmoke<Journey>, which aru test --smoke runs")
	force := fs.Bool("force", false, "overwrite an existing journey, preserving the custom block")
	dryRun := fs.Bool("dry-run", false, "print what would be written, and write nothing")

	name, args := takeName(args)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:e2e: %w", err)
	}
	if name == "" {
		return fmt.Errorf("usage: %s", makeE2EUsage)
	}
	if err := checkFlatTree("make:e2e", name); err != nil {
		return err
	}
	root, err := projectRoot()
	if err != nil {
		return err
	}
	modulePath, err := readModulePath(root)
	if err != nil {
		return err
	}
	if missing := missingHelpers(root, "Serve", "Open"); len(missing) > 0 {
		return fmt.Errorf("make:e2e: the journey opens its page with tests.%s, and tests/ does not declare it.\n\n%s",
			strings.Join(missing, " and tests."), journeyHelpers)
	}

	start := goString("/")
	switch {
	case *route == "":
	case strings.HasPrefix(*route, "/"):
		start = goString(*route)
	default:
		known := false
		for _, r := range projectRoutes(root) {
			if r.Name != *route {
				continue
			}
			known = true
			if strings.Contains(r.Pattern, "{") {
				start = goString(*route) + ", " + strings.Join(placeholders(r.Pattern), ", ")
			} else {
				start = goString(*route)
			}
		}
		if !known {
			return fmt.Errorf("make:e2e: %s is not the name of a GET route of this project; give its name or its path", *route)
		}
	}

	j := journey{
		Name: journeyName(name), Smoke: *smoke, ModulePath: modulePath, Custom: true,
	}
	j.Doc = []string{
		j.testName() + " walks the " + strings.ToLower(gen.Humanise(j.Name)) + " journey the way a person does,",
		"in a real browser over the running server, and fails on what that person",
		"would have seen go wrong: an exception, a console error, a response of 500",
		"or more.",
	}
	j.Steps = []string{
		"page.Visit(" + start + ")",
		"",
		"// The steps of the journey go here, in the words of the person walking it:",
		`// page.ClickRole("button", "New invoice"), page.Fill("Customer", "Ada"),`,
		`// page.ClickRole("button", "Save"), page.ExpectText("Invoice saved").`,
	}
	file, err := renderJourney(j)
	if err != nil {
		return fmt.Errorf("make:e2e: %w", err)
	}
	if err := emit("make:e2e", root, []gen.File{file}, *force, *dryRun, stdout); err != nil {
		return err
	}
	if *dryRun {
		return nil
	}
	fmt.Fprintf(stdout, "\nRun it with: aru test --e2e -run '^%s$'\n", j.testName())
	return nil
}

// placeholders are the values a journey visits a route with parameters by,
// one per {name}, for the person to replace with a row the test arranged.
func placeholders(pattern string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`\{([^}.]+)(\.\.\.)?\}`).FindAllStringSubmatch(pattern, -1) {
		out = append(out, goString("the "+m[1]+" the test arranged"))
	}
	return out
}

// contractOperation is one operation of an OpenAPI document, as much of it as
// a contract test is written from.
type contractOperation struct {
	Method, Path, ID string
	Params           []string
	Statuses         []string
	HasBody          bool
}

// makeContract writes the contract test of one operation of the OpenAPI
// document the project exports: a call to the running server, held to what
// the document says that operation answers.
func makeContract(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:contract", flag.ContinueOnError)
	fs.SetOutput(stderr)
	document := fs.String("document", "", "the exported OpenAPI document, when docs/contracts holds more than one")
	force := fs.Bool("force", false, "overwrite an existing test, preserving the custom block")
	dryRun := fs.Bool("dry-run", false, "print what would be written, and write nothing")

	operation, args := takeName(args)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:contract: %w", err)
	}
	// "GET /invoices/{id}" is two words when it is not quoted.
	if operation != "" && fs.NArg() == 1 && strings.HasPrefix(fs.Arg(0), "/") {
		operation += " " + fs.Arg(0)
	}
	if operation == "" {
		return fmt.Errorf("usage: %s", makeContractUsage)
	}
	root, err := projectRoot()
	if err != nil {
		return err
	}
	modulePath, err := readModulePath(root)
	if err != nil {
		return err
	}
	if missing := missingHelpers(root, "Serve"); len(missing) > 0 {
		return fmt.Errorf("make:contract: the test calls the server tests.Serve boots, and tests/ does not declare it.\n\n%s", journeyHelpers)
	}
	docPath, err := contractDocument(root, *document)
	if err != nil {
		return err
	}
	ops, err := readOperations(filepath.Join(root, filepath.FromSlash(docPath)))
	if err != nil {
		return fmt.Errorf("make:contract: %s: %w", docPath, err)
	}
	op, ok := findOperation(ops, operation)
	if !ok {
		names := make([]string, 0, len(ops))
		for _, o := range ops {
			n := o.Method + " " + o.Path
			if o.ID != "" {
				n += " (" + o.ID + ")"
			}
			names = append(names, n)
		}
		return fmt.Errorf("make:contract: %s documents no operation %q. It documents:\n  %s", docPath, operation, strings.Join(names, "\n  "))
	}
	file, err := renderContract(modulePath, docPath, op)
	if err != nil {
		return fmt.Errorf("make:contract: %w", err)
	}
	if err := emit("make:contract", root, []gen.File{file}, *force, *dryRun, stdout); err != nil {
		return err
	}
	if *dryRun {
		return nil
	}
	fmt.Fprintf(stdout, "\nIt runs with go test ./tests/Contract/..., against a server over a database of its own.\n")
	return nil
}

// contractDocument answers the exported document: the one given, or the only
// one under docs/contracts.
func contractDocument(root, given string) (string, error) {
	if given != "" {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(given))); err != nil {
			return "", fmt.Errorf("make:contract: --document=%s does not exist", given)
		}
		return filepath.ToSlash(given), nil
	}
	found, _ := filepath.Glob(filepath.Join(root, "docs", "contracts", "*.openapi.json"))
	switch len(found) {
	case 0:
		return "", errors.New("make:contract: there is no docs/contracts/*.openapi.json. The contract is the document " +
			"the application exports from its routes: run the binary's openapi:export, and aru openapi:check keeps it current")
	case 1:
		rel, _ := filepath.Rel(root, found[0])
		return filepath.ToSlash(rel), nil
	}
	return "", errors.New("make:contract: docs/contracts holds more than one document: name it with --document")
}

// readOperations reads every operation of an OpenAPI document.
func readOperations(path string) ([]contractOperation, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("not an OpenAPI document: %w", err)
	}
	var out []contractOperation
	for path, item := range doc.Paths {
		for method, raw := range item {
			switch method {
			case "get", "put", "post", "delete", "options", "head", "patch", "trace":
			default:
				continue
			}
			var op struct {
				OperationID string                     `json:"operationId"`
				Responses   map[string]json.RawMessage `json:"responses"`
				RequestBody json.RawMessage            `json:"requestBody"`
			}
			if json.Unmarshal(raw, &op) != nil {
				continue
			}
			c := contractOperation{Method: strings.ToUpper(method), Path: path, ID: op.OperationID, HasBody: len(op.RequestBody) > 0}
			for _, m := range regexp.MustCompile(`\{([^}]+)\}`).FindAllStringSubmatch(path, -1) {
				c.Params = append(c.Params, m[1])
			}
			for code := range op.Responses {
				c.Statuses = append(c.Statuses, code)
			}
			sort.Strings(c.Statuses)
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out, nil
}

// findOperation finds an operation by its operationId, or by its method and
// path template as the document writes them.
func findOperation(ops []contractOperation, want string) (contractOperation, bool) {
	method, path, spaced := strings.Cut(strings.TrimSpace(want), " ")
	for _, o := range ops {
		if o.ID != "" && o.ID == want {
			return o, true
		}
		if spaced && strings.EqualFold(o.Method, method) && o.Path == strings.TrimSpace(path) {
			return o, true
		}
	}
	return contractOperation{}, false
}

// statusConstants are the net/http names of the statuses a contract test is
// most often written for.
var statusConstants = map[string]string{
	"200": "http.StatusOK", "201": "http.StatusCreated", "202": "http.StatusAccepted", "204": "http.StatusNoContent",
	"301": "http.StatusMovedPermanently", "302": "http.StatusFound", "303": "http.StatusSeeOther",
}

// renderContract writes the contract test of one operation.
func renderContract(modulePath, docPath string, op contractOperation) (gen.File, error) {
	name := journeyName(op.ID)
	if op.ID == "" {
		name = journeyName(strings.ToLower(op.Method) + " " + op.Path)
	}
	status := ""
	for _, code := range op.Statuses {
		if n, err := strconv.Atoi(code); err == nil && n >= 200 && n < 400 {
			status = code
			break
		}
	}
	statusExpr := ""
	if status != "" {
		statusExpr = statusConstants[status]
		if statusExpr == "" {
			statusExpr = status
		}
	}
	operation := op.Method + " " + op.Path

	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\nimport (\n", contractPackage)
	if strings.HasPrefix(statusExpr, "http.") {
		b.WriteString("\t\"net/http\"\n")
	}
	fmt.Fprintf(&b, "\t\"testing\"\n\n\t%q\n\n\t%q\n)\n\n", openAPIKitImport, modulePath+"/tests")
	fmt.Fprintf(&b, "// Test%sAnswersItsContract calls\n//\n//\t%s\n//\n", name, operation)
	fmt.Fprintf(&b, "// on the running application and holds the answer to what\n// %s documents for it:\n", docPath)
	b.WriteString("// the status, the content type, the JSON schema of the body and the required\n// headers.\n")
	fmt.Fprintf(&b, "func Test%sAnswersItsContract(t *testing.T) {\n", name)
	b.WriteString("\tserver := tests.Serve(t)\n")
	fmt.Fprintf(&b, "\tcontract := openapi.Load(t, %q)\n", "../../"+docPath)
	if len(op.Params) > 0 {
		b.WriteString("\tparams := map[string]string{\n")
		for _, p := range op.Params {
			fmt.Fprintf(&b, "\t\t%q: \"\",\n", p)
		}
		b.WriteString("\t}\n")
	}
	if op.HasBody {
		b.WriteString("\tbody := map[string]any{}\n")
	}
	b.WriteString("\t// arandu:begin custom\n")
	b.WriteString("\t// Arrange what the call reaches here -- a row on server.DB(), a session\n")
	b.WriteString("\t// signed in with test.As -- and fill the path and the body with it.\n")
	b.WriteString("\t// arandu:end custom\n")
	b.WriteString("\tcontract.Check(t, openapi.URL(server.URL, nil), openapi.Call{\n")
	fmt.Fprintf(&b, "\t\tOperation: %q,\n", operation)
	if len(op.Params) > 0 {
		b.WriteString("\t\tParams: params,\n")
	}
	if op.HasBody {
		b.WriteString("\t\tBody: body,\n")
	}
	if statusExpr != "" {
		fmt.Fprintf(&b, "\t\tStatus: %s,\n", statusExpr)
	}
	b.WriteString("\t})\n}\n")
	src, err := format.Source([]byte(b.String()))
	if err != nil {
		return gen.File{}, fmt.Errorf("the test did not format: %w\n%s", err, b.String())
	}
	return gen.File{Path: contractTestsDir + "/" + name + "_test.go", Content: src}, nil
}
