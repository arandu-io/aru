package lsp_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/arandu-io/aru/internal/lsp"
	"github.com/arandu-io/aru/tests"
)

var updateSchemaOneGolden = flag.Bool("update-schema-one-golden", false, "rewrite the schema 1 project graph goldens")

// TestProjectGraphWithoutAVersionIsSchemaOneByteForByte pins what a client
// that names no schema receives: the exact bytes of the result, for three
// shapes of request that say nothing about a version and the one that names
// the first.
//
// The goldens were written by the server before schema 2 existed, and they
// are what an editor adapter built against schema 1 parses. A result that
// gains a field, loses one, reorders the groups or renames a kind is a result
// that adapter refuses, so the comparison is on bytes rather than on a decoded
// value that would forgive all four.
func TestProjectGraphWithoutAVersionIsSchemaOneByteForByte(t *testing.T) {
	// An empty module cache, so the answer is about the fixture and not about
	// what happens to be downloaded on the machine running this.
	t.Setenv("GOMODCACHE", t.TempDir())

	for _, fixture := range []string{"clean", "violations", "gaps"} {
		root := tests.Fixture(t, "doctor", fixture)
		for name, params := range map[string]string{
			"absent": ``,
			"null":   `,"params":null`,
			"empty":  `,"params":{}`,
			// Naming the first schema is the same request as naming none.
			"one": `,"params":{"schemaVersion":1}`,
		} {
			t.Run(fixture+"/"+name, func(t *testing.T) {
				got := projectGraphResult(t, root, params)
				path := filepath.Join("testdata", "projectGraph.v1."+fixture+".golden.json")
				if *updateSchemaOneGolden && name == "absent" {
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, got, 0o644); err != nil {
						t.Fatal(err)
					}
					return
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read schema 1 golden: %v", err)
				}
				if !bytes.Equal(got, want) {
					t.Errorf("schema 1 result for %s with params %s is not the golden, byte for byte", fixture, name)
				}
			})
		}
	}
}

// projectGraphResult asks for the project graph and answers the raw result,
// with the root URI replaced by a placeholder so the golden holds on any
// machine.
func projectGraphResult(t *testing.T, root, params string) []byte {
	t.Helper()
	rootURI := (&url.URL{Scheme: "file", Path: root}).String()
	input := frames(
		fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":%q}}`, rootURI),
		`{"jsonrpc":"2.0","id":"graph","method":"arandu/projectGraph"`+params+`}`,
		`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	)
	var output bytes.Buffer
	if err := lsp.Serve(bytes.NewReader(input), &output); err != nil {
		t.Fatalf("serve: %v", err)
	}
	for _, body := range readFrames(t, output.Bytes()) {
		var message struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(body, &message); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if string(message.ID) != `"graph"` {
			continue
		}
		if len(message.Result) == 0 {
			t.Fatalf("arandu/projectGraph answered without a result: %s", body)
		}
		return append(bytes.ReplaceAll(message.Result, []byte(rootURI), []byte("file:///ROOT")), '\n')
	}
	t.Fatal("arandu/projectGraph received no response")
	return nil
}
