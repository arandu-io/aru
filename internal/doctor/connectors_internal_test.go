package doctor

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/arandu-io/hesape/cache"
	"github.com/arandu-io/hesape/database"
	"github.com/arandu-io/hesape/queue"
)

// bootError answers the error the required library gives when a setting asks
// for a driver no imported package registered. This test binary imports no
// connector, so for every driver the answer is that error and never nil.
func bootError(t *testing.T, setting, driver string) error {
	t.Helper()

	switch setting {
	case "DATABASE_URL":
		_, err := database.DriverName(database.Dialect(driver))
		return err
	case "CACHE_STORE", "SESSION_DRIVER":
		return cache.Linked(setting, driver)
	case "QUEUE_CONNECTION":
		return queue.Linked(setting, driver)
	}
	t.Fatalf("no registry of the library answers for %s, so nothing checks its entries", setting)
	return nil
}

// valueAsking is a value of the setting that asks for driver: the driver
// itself, or for DATABASE_URL a URL whose scheme selects it.
func valueAsking(t *testing.T, setting, driver string) string {
	t.Helper()

	if setting != "DATABASE_URL" {
		return driver
	}
	for scheme, dialect := range databaseSchemes {
		if dialect == driver {
			return scheme + "://app:secret@127.0.0.1:1/app"
		}
	}
	t.Fatalf("no DATABASE_URL scheme selects %s, so the rule can never ask for its connector", driver)
	return ""
}

// TestTheConnectorTableIsTheOnePublished compares every entry of
// connectorModules with the error the library this module requires gives at
// boot, and the finding the rule writes from that entry with the same error.
//
// The table is a copy because the library keeps it unexported, and a copy is
// what goes stale: a connector that moves to another module would leave the
// finding telling people to `go get` the old one. So each entry is checked
// against what the boot actually prints -- the `go get` line, the import, and
// the first sentence the finding quotes -- in the version go.mod requires, which
// is the version the next upgrade of that requirement re-runs this against.
func TestTheConnectorTableIsTheOnePublished(t *testing.T) {
	for _, setting := range connectorSettings {
		if len(connectorModules[setting]) == 0 {
			t.Errorf("%s is a setting the rule reads and no connector is written down for it", setting)
		}

		for driver, module := range connectorModules[setting] {
			err := bootError(t, setting, driver)
			if err == nil {
				t.Errorf("%s=%s: the library says a connector is linked into this test binary, so nothing here compares anything", setting, driver)
				continue
			}
			printed := err.Error()

			for _, line := range []string{"\n    go get " + module + "\n", "\n    _ " + strconv.Quote(module)} {
				if !strings.Contains(printed, line) {
					t.Errorf("%s=%s: the table says %s and the boot error says:\n%s", setting, driver, module, printed)
				}
			}

			// The finding quotes the first sentence, without the list of what
			// is linked: that list describes the binary the error came from.
			sentence, _, found := strings.Cut(printed, " (linked:")
			if !found {
				t.Errorf("%s=%s: the boot error no longer lists what is linked, so the sentence the finding quotes cannot be cut from it:\n%s", setting, driver, printed)
				continue
			}

			p := &project{env: map[string]envSetting{setting: {value: valueAsking(t, setting, driver), line: 1}}}
			findings := configuredEnginesAreLinked(p)
			if len(findings) != 1 {
				t.Errorf("%s=%s in a project that imports nothing produced %d findings, want 1", setting, driver, len(findings))
				continue
			}
			for _, want := range []string{sentence, "go get " + module, "_ " + strconv.Quote(module)} {
				if !strings.Contains(findings[0].Why, want) {
					t.Errorf("%s=%s: the finding does not say %q, which the boot error does:\n  finding %s\n  boot    %s",
						setting, driver, want, findings[0].Why, printed)
				}
			}
		}
	}
}

// TestTheSchemesAreTheOnesTheBootReads checks databaseSchemes against the
// parser the boot runs DATABASE_URL through, in both directions it can: every
// scheme written here selects the dialect the parser selects, every dialect the
// library declares has a connector written down, and a scheme the parser
// refuses selects nothing here either.
func TestTheSchemesAreTheOnesTheBootReads(t *testing.T) {
	for scheme, dialect := range databaseSchemes {
		raw := scheme + "://app:secret@127.0.0.1:1/app"
		cfg, err := database.ParseURL(raw)
		if err != nil {
			t.Errorf("the table reads %s:// as %s and the boot refuses it: %v", scheme, dialect, err)
			continue
		}
		if string(cfg.Connection) != dialect {
			t.Errorf("the table reads %s:// as %s and the boot reads it as %s", scheme, dialect, cfg.Connection)
		}
		if got := databaseDialect(strings.ToUpper(scheme) + raw[len(scheme):]); got != dialect {
			t.Errorf("databaseDialect(%s) = %q, want %q: the boot does not care about the case of a scheme", raw, got, dialect)
		}
	}

	for _, d := range []database.Dialect{database.DialectSQLite, database.DialectPostgres, database.DialectMySQL} {
		if connectorModules["DATABASE_URL"][string(d)] == "" {
			t.Errorf("the library speaks %s and the table names no connector for it", d)
		}
	}

	const refused = "redis://127.0.0.1:6379/0"
	if _, err := database.ParseURL(refused); err == nil {
		t.Fatalf("the boot accepts %s, so this half checks nothing", refused)
	}
	if got := databaseDialect(refused); got != "" {
		t.Errorf("databaseDialect(%s) = %q: the boot refuses the scheme, and a finding about a connector for it would send people after one that does not exist", refused, got)
	}
}

// TestNoPublishedConnectorIsMissingFromTheTable is the other direction, which
// the exported calls cannot answer: they say which module links a driver
// somebody names, and nothing about which drivers exist. A connector the
// library gained and this table did not would leave the rule silent on a
// setting that stops the boot.
//
// So the two tables are read from the source of the version go.mod requires,
// where the module cache keeps it. They are the declaration `var modules =
// map[string]string{...}` in each package, and finding them anywhere else fails
// here rather than passing over nothing.
func TestNoPublishedConnectorIsMissingFromTheTable(t *testing.T) {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/arandu-io/hesape").Output()
	if err != nil {
		t.Fatalf("locating the required library's source: %v", err)
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		t.Fatal("the required library has no source on disk, so its tables cannot be read")
	}

	for _, c := range []struct {
		file     string
		settings []string
	}{
		{"cache/connector.go", []string{"CACHE_STORE", "SESSION_DRIVER"}},
		{"queue/connector.go", []string{"QUEUE_CONNECTION"}},
	} {
		published := declaredModules(t, filepath.Join(dir, filepath.FromSlash(c.file)))
		for _, setting := range c.settings {
			if !reflect.DeepEqual(published, connectorModules[setting]) {
				t.Errorf("%s: %s publishes %v and the table here says %v", setting, c.file, published, connectorModules[setting])
			}
		}
	}
}

// declaredModules reads the string map a file declares as `var modules`.
func declaredModules(t *testing.T, path string) map[string]string {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 || vs.Names[0].Name != "modules" || len(vs.Values) != 1 {
				continue
			}
			lit, ok := vs.Values[0].(*ast.CompositeLit)
			if !ok {
				continue
			}
			out := map[string]string{}
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					t.Fatalf("%s: an entry of modules is not a key and a value", path)
				}
				key, keyOK := stringLiteral(kv.Key)
				value, valueOK := stringLiteral(kv.Value)
				if !keyOK || !valueOK {
					t.Fatalf("%s: an entry of modules is not two string literals, so it cannot be compared", path)
				}
				out[key] = value
			}
			return out
		}
	}
	t.Fatalf("%s declares no `var modules = map[string]string{...}`: the table moved, and this check has to follow it", path)
	return nil
}
