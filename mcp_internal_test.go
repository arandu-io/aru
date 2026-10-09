package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/contract"
	"github.com/arandu-io/mcp"
)

// mcpProject is a project with a module, whose go.mod requires the framework
// and replaces it with the catalogue's fixture, so imports_catalog has a
// framework to read without a module cache.
func mcpProject(t *testing.T) string {
	t.Helper()
	root := projectWithModule(t, "purchase_order")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	stub := filepath.Join(wd, "internal", "catalog", "testdata", "framework")
	writeFile(t, filepath.Join(root, "go.mod"), "module example.test/project\n\ngo 1.26\n\n"+
		"require github.com/arandu-io/framework v0.50.2\n\nreplace github.com/arandu-io/framework => "+stub+"\n")
	return root
}

// rpcAnswer is one line the server wrote.
type rpcAnswer struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	} `json:"result"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// converse sends the messages, one per line, and answers what came back on
// standard output by id, and what went to the log.
func converse(t *testing.T, root string, messages ...string) (map[string]rpcAnswer, string) {
	t.Helper()
	var out, logs bytes.Buffer
	in := strings.NewReader(strings.Join(messages, "\n") + "\n")
	if err := serveMCP(context.Background(), root, in, &out, &logs); err != nil {
		t.Fatalf("serveMCP: %v", err)
	}
	answers := map[string]rpcAnswer{}
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		var a rpcAnswer
		if err := json.Unmarshal([]byte(line), &a); err != nil || a.JSONRPC != "2.0" {
			t.Fatalf("standard output carries a line that is not a JSON-RPC frame: %q", line)
		}
		answers[string(a.ID)] = a
	}
	return answers, logs.String()
}

func call(id int, tool, arguments string) string {
	return `{"jsonrpc":"2.0","id":` + itoa(id) + `,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + arguments + `}}`
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// TestTheDeveloperServerAnswersEveryToolOverStdio drives the server the way a
// client does: initialize, list, and one call of each tool. Standard output
// carries frames and nothing else; the log goes to its own stream.
func TestTheDeveloperServerAnswersEveryToolOverStdio(t *testing.T) {
	root := mcpProject(t)
	t.Chdir(root)

	answers, logs := converse(t, root,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		call(3, "doctor", `{}`),
		call(4, "project_map", `{}`),
		call(5, "where_does_it_go", `{"kind":"service"}`),
		call(6, "feature_recipe", `{"recipe":"webhook"}`),
		call(7, "commands", `{"prefix":"make:"}`),
		call(8, "imports_catalog", `{"moved_only":true}`),
		call(9, "generate", `{"command":"make:job","arguments":"SendInvoice --fields \"invoice_id:uuid\""}`),
	)

	if !strings.Contains(logs, "aru mcp: serving over stdio") {
		t.Errorf("the log did not reach its stream: %q", logs)
	}
	if a := answers["1"]; a.Error != nil {
		t.Fatalf("initialize was refused: %s", a.Error.Message)
	}

	var names []string
	for _, tool := range answers["2"].Result.Tools {
		names = append(names, tool.Name)
	}
	want := "doctor project_map where_does_it_go feature_recipe commands imports_catalog generate"
	if strings.Join(names, " ") != want {
		t.Errorf("tools/list = %v, want %s", names, want)
	}

	for id, check := range map[string]string{
		"3": `"findings"`,
		"4": `"schemaVersion": 2`,
		"5": `"path": "app/Services/<Entity>Service.go"`,
		"6": "github.com/arandu-io/hesape/webhook",
		"7": `"make:module"`,
		"8": `"canonical": "github.com/arandu-io/hesape/auth"`,
		"9": "Nothing was written",
	} {
		a, ok := answers[id]
		if !ok || len(a.Result.Content) == 0 {
			t.Errorf("call %s got no answer", id)
			continue
		}
		text := a.Result.Content[0].Text
		if a.Result.IsError {
			t.Errorf("call %s failed: %s", id, text)
			continue
		}
		if !strings.Contains(text, check) {
			t.Errorf("call %s does not carry %s:\n%.600s", id, check, text)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "app", "Jobs", "SendInvoice.go")); err == nil {
		t.Error("a preview wrote the job")
	}
}

// TestGenerateWritesOnlyWhenAskedAndOnlyGenerators: the one tool that writes
// writes on apply=true and on nothing else, and only runs make:*.
func TestGenerateWritesOnlyWhenAskedAndOnlyGenerators(t *testing.T) {
	root := mcpProject(t)
	t.Chdir(root)
	job := filepath.Join(root, "app", "Jobs", "SendInvoice.go")

	answers, _ := converse(t, root,
		call(1, "generate", `{"command":"make:job","arguments":"SendInvoice --dry-run","apply":false}`),
		call(2, "generate", `{"command":"migrate"}`),
		call(3, "generate", `{"command":"make:job","arguments":"SendInvoice \"unclosed"}`),
	)
	if _, err := os.Stat(job); err == nil {
		t.Fatal("generate wrote without apply")
	}
	if !answers["2"].Result.IsError || !strings.Contains(answers["2"].Result.Content[0].Text, "make:module") {
		t.Errorf("migrate was not refused with the list of generators: %+v", answers["2"].Result)
	}
	if !answers["3"].Result.IsError {
		t.Error("an unclosed quote was not refused")
	}

	answers, _ = converse(t, root, call(1, "generate", `{"command":"make:job","arguments":"SendInvoice","apply":true}`))
	if answers["1"].Result.IsError {
		t.Fatalf("apply failed: %s", answers["1"].Result.Content[0].Text)
	}
	if _, err := os.Stat(job); err != nil {
		t.Fatalf("apply=true did not write the job: %v", err)
	}
	if !strings.Contains(answers["1"].Result.Content[0].Text, "registerHandlers") {
		t.Errorf("the wiring the generator printed was not handed back: %s", answers["1"].Result.Content[0].Text)
	}
}

// TestGenerateRefusesToWriteWiring: a generator whose plan names a file under
// bootstrap/ or routes/ is refused before it runs, even with apply=true. No
// generator of this binary plans one today, so the test puts one in the table.
func TestGenerateRefusesToWriteWiring(t *testing.T) {
	for _, planned := range []string{"bootstrap/app.go", "routes/web.go"} {
		ran := false
		wiring := command{name: "make:wiring", run: func(args []string, stdout, _ io.Writer) error {
			for _, a := range args {
				if a == "--dry-run" {
					fmt.Fprintf(stdout, "app/Jobs/X.go (10 bytes)\n%s (99 bytes)\n", planned)
					return nil
				}
			}
			ran = true
			return nil
		}}
		saved := mcpCommands
		mcpCommands = func() []command { return []command{wiring} }
		answer, err := generateTool{}.Handle(context.Background(), mcpRequest(t, `{"command":"make:wiring","apply":true}`))
		mcpCommands = saved
		if err != nil {
			t.Fatal(err)
		}
		if !answer.IsError || !strings.Contains(answer.Text, planned) {
			t.Errorf("a plan naming %s was not refused: %+v", planned, answer)
		}
		if ran {
			t.Errorf("a plan naming %s was written", planned)
		}
	}
}

// mcpRequest builds a request with the arguments given as JSON.
func mcpRequest(t *testing.T, arguments string) mcp.Request {
	t.Helper()
	var args map[string]any
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		t.Fatal(err)
	}
	return mcp.Request{Arguments: args}
}

// TestEveryGeneratorACardNamesIsACommand: the contract names generators by
// what a person types, and each of those is a command of this binary.
func TestEveryGeneratorACardNamesIsACommand(t *testing.T) {
	for _, card := range contract.Cards() {
		for _, g := range card.Generators {
			name := strings.Fields(g)[0]
			if _, ok := lookup(name); !ok {
				t.Errorf("card %q names %q, which is not an aru command", card.Kind, name)
			}
		}
	}
	for _, r := range contract.Recipes() {
		for _, step := range r.Steps {
			if rest, ok := strings.CutPrefix(step, "aru "); ok {
				name := strings.TrimSuffix(strings.Fields(rest)[0], ",")
				if _, found := lookup(name); !found {
					t.Errorf("recipe %q runs %q, which is not an aru command", r.Name, name)
				}
			}
		}
	}
}

// TestSplitArgumentsKeepsAQuotedValueWhole: --fields carries commas and,
// sometimes, spaces.
func TestSplitArgumentsKeepsAQuotedValueWhole(t *testing.T) {
	got, err := splitArguments(`invoice --fields "title:string!, amount:money" --tenant 'a b'`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"invoice", "--fields", "title:string!, amount:money", "--tenant", "a b"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("splitArguments = %q, want %q", got, want)
	}
}
