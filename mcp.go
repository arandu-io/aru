package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/arandu-io/aru/internal/catalog"
	"github.com/arandu-io/aru/internal/contract"
	"github.com/arandu-io/aru/internal/doctor"
	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/mcp"
)

// mcpUsage is the usage line of mcp, in the dispatch table and in its refusal.
const mcpUsage = "aru mcp"

// mcpHelp is what `aru mcp --help` prints under the usage line.
const mcpHelp = `It speaks the Model Context Protocol over standard input and output, one
message per line, for the project the current directory is in. Standard output
carries protocol frames and nothing else; every log line goes to standard
error. An assistant's client starts it the way it starts any stdio server:

    {"command": "aru", "args": ["mcp"]}

Reading tools: doctor, project_map, where_does_it_go, feature_recipe,
commands and imports_catalog. One tool writes, generate: it runs a make:*
command as a preview, and writes only when called with apply=true. Nothing
it does runs a migration or edits bootstrap/app.go or routes/: the wiring a
generator prints is handed back for a person to paste.`

// mcpSubject is who the developer server acts as. It reaches no application
// data -- every tool reads the source tree or runs a generator -- so the
// identity only names the caller in the log.
var mcpSubject = auth.Subject{ID: "aru-mcp"}

// mcpCommands is how the server reaches the dispatch table. It is assigned in
// init, because runMCP is an entry of the table and a function the table's
// initializer refers to cannot refer to the table in turn.
var mcpCommands func() []command

func init() { mcpCommands = func() []command { return commands } }

// runMCP serves the developer MCP server for the project the current
// directory is in, over the process's standard input and output.
func runMCP(args []string, stdout, stderr io.Writer) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: %s", mcpUsage)
	}
	root, err := projectRoot()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = serveMCP(ctx, root, os.Stdin, stdout, stderr)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// serveMCP serves the server for the project at root over in and out, with
// every log line on logs. out receives protocol frames and nothing else.
func serveMCP(ctx context.Context, root string, in io.Reader, out, logs io.Writer) error {
	logger := slog.New(slog.NewTextHandler(logs, nil))
	server := developerServer(root)
	logger.Info("aru mcp: serving over stdio", "project", root, "tools", len(server.Tools), "version", Version())
	return mcp.Local(ctx, server, mcpSubject, in, out)
}

// developerServer is the server: the six reading tools and generate.
func developerServer(root string) *mcp.Server {
	return &mcp.Server{
		Name:    "aru",
		Version: Version(),
		Instructions: "aru answers questions about this Arandu project from its source: what the doctor " +
			"finds, the typed project map, where each kind of code goes and in what shape (the " +
			"implementation contract), the order a feature is built in, aru's commands and the import " +
			"path of each framework symbol. Ask where_does_it_go before writing a file, and feature_recipe " +
			"before starting a feature. generate runs a make:* generator, as a preview unless apply is true; " +
			"it never runs a migration and never edits bootstrap or routes -- paste the wiring it returns.",
		Tools: []mcp.Tool{
			doctorTool{root: root},
			projectMapTool{root: root},
			whereTool{},
			recipeTool{},
			commandsTool{},
			importsTool{root: root},
			generateTool{root: root},
		},
	}
}

// jsonText answers a value as indented JSON, the shape every reading tool
// answers in so a client parses one format. Angle brackets stay as they are:
// a path like app/Services/<Entity>Service.go is read by a model, not by a
// browser.
func jsonText(v any) (mcp.Response, error) {
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return mcp.Response{}, err
	}
	return mcp.Response{Text: strings.TrimRight(body.String(), "\n")}, nil
}

// doctorTool runs the doctor.
type doctorTool struct{ root string }

func (doctorTool) Name() string { return "doctor" }
func (doctorTool) Description() string {
	return "Run aru doctor on this project and return every finding with its rule, severity, file, line, " +
		"message, why it matters, and the contract card (kind of code) whose shape answers it."
}
func (doctorTool) Schema() mcp.Schema {
	return mcp.Object(
		mcp.String("profile", "the deployment profile to check against; conventional by default").
			Enum(string(doctor.Conventional), string(doctor.Performance)),
		mcp.String("rule", "keep only the findings of this rule"),
	)
}

// mcpFinding is a finding as the doctor tool answers it.
type mcpFinding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Message  string `json:"message"`
	Why      string `json:"why"`
	Contract string `json:"contract,omitempty"`
}

func (t doctorTool) Handle(_ context.Context, r mcp.Request) (mcp.Response, error) {
	name, _ := r.String("profile")
	if name == "" {
		name = string(doctor.Conventional)
	}
	profile, err := doctor.ParseProfile(name)
	if err != nil {
		return mcp.Response{}, err
	}
	findings, err := doctor.Run(t.root, profile)
	if err != nil {
		return mcp.Response{}, err
	}
	only, _ := r.String("rule")
	out := struct {
		Profile  string       `json:"profile"`
		Errors   int          `json:"errors"`
		Warnings int          `json:"warnings"`
		Findings []mcpFinding `json:"findings"`
	}{Profile: string(profile), Findings: []mcpFinding{}}
	for _, f := range findings {
		if only != "" && f.Rule != only {
			continue
		}
		if f.Severity == doctor.Error {
			out.Errors++
		} else {
			out.Warnings++
		}
		out.Findings = append(out.Findings, mcpFinding{
			Rule: f.Rule, Severity: f.Severity.String(), File: f.File, Line: f.Line,
			Message: f.Message, Why: f.Why, Contract: f.Contract,
		})
	}
	return jsonText(out)
}

// projectMapTool answers the typed project map, schema 2.
type projectMapTool struct{ root string }

func (projectMapTool) Name() string { return "project_map" }
func (projectMapTool) Description() string {
	return "Return the typed map of this project (schema 2): features, routes with method, pattern and name, " +
		"every file classified by what it declares, the typed edges between them (routes-to, validates-with, " +
		"authorizes, persists, renders, tested-by, dispatches, listens-to) and the doctor's findings. " +
		"The profile is the one arandu.mod.toml declares."
}
func (projectMapTool) Schema() mcp.Schema { return mcp.Object() }

func (t projectMapTool) Handle(context.Context, mcp.Request) (mcp.Response, error) {
	profile, err := doctor.DeclaredProfile(t.root)
	if err != nil {
		return mcp.Response{}, err
	}
	analysis, err := doctor.AnalyzeWithMap(t.root, profile)
	if err != nil {
		return mcp.Response{}, err
	}
	return jsonText(analysis.Map)
}

// whereTool answers one card of the implementation contract.
type whereTool struct{}

func (whereTool) Name() string { return "where_does_it_go" }
func (whereTool) Description() string {
	return "Given a kind of code, return its card from the implementation contract: the path it lives at, " +
		"the canonical imports, the signature, who calls it, what it may and may not do, how errors " +
		"travel, the generator that writes it, the skeleton's example and the doctor rules that verify it. " +
		"Ask before creating a file."
}
func (whereTool) Schema() mcp.Schema {
	return mcp.Object(mcp.String("kind", "the kind of code").Enum(contract.Kinds()...).Required())
}

func (whereTool) Handle(_ context.Context, r mcp.Request) (mcp.Response, error) {
	kind, _ := r.String("kind")
	card, ok := contract.Lookup(kind)
	if !ok {
		return mcp.Error("there is no card for %q. Kinds: %s", kind, strings.Join(contract.Kinds(), ", ")), nil
	}
	return jsonText(card)
}

// recipeTool answers one recipe, with the cards it touches.
type recipeTool struct{}

func (recipeTool) Name() string { return "feature_recipe" }
func (recipeTool) Description() string {
	return "Return the order a kind of feature is built in -- crud, an action outside CRUD, nested, a job, " +
		"a received webhook, an external integration and the rest -- with the commands to run and the " +
		"contract card of every kind of code it touches."
}
func (recipeTool) Schema() mcp.Schema {
	return mcp.Object(mcp.String("recipe", "the kind of feature").Enum(contract.RecipeNames()...).Required())
}

func (recipeTool) Handle(_ context.Context, r mcp.Request) (mcp.Response, error) {
	name, _ := r.String("recipe")
	recipe, ok := contract.RecipeNamed(name)
	if !ok {
		return mcp.Error("there is no recipe %q. Recipes: %s", name, strings.Join(contract.RecipeNames(), ", ")), nil
	}
	out := struct {
		contract.Recipe
		Sheets []contract.Card `json:"sheets"`
	}{Recipe: recipe}
	for _, kind := range recipe.Cards {
		if card, ok := contract.Lookup(kind); ok {
			out.Sheets = append(out.Sheets, card)
		}
	}
	return jsonText(out)
}

// commandsTool answers aru's command table.
type commandsTool struct{}

func (commandsTool) Name() string { return "commands" }
func (commandsTool) Description() string {
	return "Return every aru command with its usage line, what it does and the flags it takes, in the order " +
		"aru help prints them."
}
func (commandsTool) Schema() mcp.Schema {
	return mcp.Object(mcp.String("prefix", "keep only the commands whose name starts with this: make:, queue:"))
}

func (commandsTool) Handle(_ context.Context, r mcp.Request) (mcp.Response, error) {
	prefix, _ := r.String("prefix")
	all := commandCatalogue()
	out := all[:0:0]
	for _, c := range all {
		if strings.HasPrefix(c.Name, prefix) {
			out = append(out, c)
		}
	}
	return jsonText(out)
}

// importsTool answers the import catalogue of the framework version go.mod
// requires.
type importsTool struct{ root string }

func (importsTool) Name() string { return "imports_catalog" }
func (importsTool) Description() string {
	return "Return the import path each exported symbol of the framework should be named by, for the version " +
		"this project's go.mod requires: a symbol a bridge only re-exports is imported from hesape, one the " +
		"framework declares (Router, SessionStore) stays on the framework. May download that framework " +
		"version into the module cache."
}
func (importsTool) Schema() mcp.Schema {
	return mcp.Object(
		mcp.String("package", "keep only this framework package: github.com/arandu-io/framework/security"),
		mcp.Bool("moved_only", "keep only the symbols whose canonical path is not the framework's"),
	)
}

func (t importsTool) Handle(_ context.Context, r mcp.Request) (mcp.Response, error) {
	c, err := catalog.Fetch(t.root)
	if err != nil {
		return mcp.Response{}, err
	}
	only, _ := r.String("package")
	movedOnly, _ := r.Bool("moved_only")
	out := struct {
		Module   string            `json:"module"`
		Version  string            `json:"version,omitempty"`
		Packages []catalog.Package `json:"packages"`
	}{Module: c.Module, Version: c.Version, Packages: []catalog.Package{}}
	for _, pkg := range c.Packages {
		if only != "" && pkg.Path != only {
			continue
		}
		kept := pkg
		kept.Symbols = nil
		for _, s := range pkg.Symbols {
			if !movedOnly || s.Moved() {
				kept.Symbols = append(kept.Symbols, s)
			}
		}
		if len(kept.Symbols) > 0 {
			out.Packages = append(out.Packages, kept)
		}
	}
	return jsonText(out)
}

// generateTool runs one make:* command.
type generateTool struct{ root string }

func (generateTool) Name() string { return "generate" }
func (generateTool) Description() string {
	return "Run one aru make:* generator. Without apply it is a preview: it lists the files the generator " +
		"would write and writes nothing. With apply=true it writes them and returns what the generator " +
		"printed, including the wiring to paste into bootstrap/app.go and routes -- which this tool never " +
		"edits. It never runs a migration."
}
func (generateTool) Schema() mcp.Schema {
	return mcp.Object(
		mcp.String("command", "the generator").Enum(generatorNames()...).Required(),
		mcp.String("arguments", `the name and flags as typed after the command: invoice --fields "title:string!" --tenant`),
		mcp.Bool("apply", "write the files; false, the default, only previews them"),
	)
}

// generatorNames are the make:* commands of the dispatch table.
func generatorNames() []string {
	var out []string
	for _, c := range mcpCommands() {
		if strings.HasPrefix(c.name, "make:") {
			out = append(out, c.name)
		}
	}
	sort.Strings(out)
	return out
}

// untouchable are the paths no generator run from here may write: the wiring
// is a person's to paste.
var untouchable = []string{"bootstrap/", "routes/"}

func (t generateTool) Handle(_ context.Context, r mcp.Request) (mcp.Response, error) {
	name, _ := r.String("command")
	var cmd command
	found := false
	for _, c := range mcpCommands() {
		if c.name == name && strings.HasPrefix(c.name, "make:") {
			cmd, found = c, true
		}
	}
	if !found {
		return mcp.Error("%q is not a generator. Generators: %s", name, strings.Join(generatorNames(), ", ")), nil
	}
	raw, _ := r.String("arguments")
	args, err := splitArguments(raw)
	if err != nil {
		return mcp.Error("arguments: %v", err), nil
	}
	kept := args[:0]
	for _, a := range args {
		if a != "--dry-run" {
			kept = append(kept, a)
		}
	}
	args = kept
	apply, _ := r.Bool("apply")

	// The preview runs first, every time: what the generator plans is checked
	// before anything is written, apply or not.
	var preview, previewErr bytes.Buffer
	if err := cmd.run(append(append([]string(nil), args...), "--dry-run"), &preview, &previewErr); err != nil {
		return mcp.Error("%s: %v\n%s", name, err, previewErr.String()), nil
	}
	planned := plannedPaths(preview.String())
	for _, p := range planned {
		for _, prefix := range untouchable {
			if strings.HasPrefix(p, prefix) {
				return mcp.Error("%s would write %s, and this tool does not write wiring: run it in a terminal "+
					"if that is meant", name, p), nil
			}
		}
	}
	if !apply {
		return mcp.Text("aru %s %s --dry-run\n\n%s\nNothing was written. Call again with apply=true to write these files.",
			name, strings.Join(args, " "), preview.String()), nil
	}

	var out, errOut bytes.Buffer
	if err := cmd.run(args, &out, &errOut); err != nil {
		return mcp.Error("%s: %v\n%s", name, err, errOut.String()), nil
	}
	return mcp.Text("aru %s %s\n\n%s\nThe wiring above, if any, is for a person to paste: nothing here edits "+
		"bootstrap/app.go or routes, or runs a migration.", name, strings.Join(args, " "), out.String()), nil
}

// plannedPaths reads the paths a --dry-run listed: one per line, followed by
// its size in parentheses.
func plannedPaths(listing string) []string {
	var out []string
	for _, line := range strings.Split(listing, "\n") {
		if i := strings.LastIndex(line, " ("); i > 0 && strings.HasSuffix(line, " bytes)") {
			out = append(out, strings.TrimSpace(line[:i]))
		}
	}
	return out
}

// splitArguments splits a line into arguments the way a shell does for the
// cases a generator's flags need: whitespace separates, and single or double
// quotes keep a value with spaces or commas whole.
func splitArguments(line string) ([]string, error) {
	var out []string
	var current strings.Builder
	inWord := false
	var quote rune
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, inWord = r, true
		case r == ' ' || r == '\t' || r == '\n':
			if inWord {
				out = append(out, current.String())
				current.Reset()
				inWord = false
			}
		default:
			current.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("a %c quote is never closed", quote)
	}
	if inWord {
		out = append(out, current.String())
	}
	return out, nil
}
