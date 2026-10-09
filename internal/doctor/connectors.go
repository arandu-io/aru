package doctor

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// envExample is the configuration a project commits: the file `aru new`
// copies into .env, and the only configuration in the repository.
const envExample = ".env.example"

// connectorSettings are the settings that name an engine, in the order the
// report reads them.
var connectorSettings = []string{"DATABASE_URL", "CACHE_STORE", "SESSION_DRIVER", "QUEUE_CONNECTION"}

// connectorModules is the module that links each engine into the binary, by
// setting and then by the driver name the setting asks for. For DATABASE_URL
// the driver name is the dialect its scheme selects, see databaseDialect.
//
// A value that is not here needs no import -- memory, database -- or is not one
// the collection provides a connector for, and either way there is nothing to
// tell anybody to add.
//
// The answer belongs to the library that registers the connectors, and it
// keeps the answer unexported, so it is written out here. The test beside this
// file holds the copy to the version go.mod requires, both ways: every entry
// against the error the library's exported calls give at boot, and the
// library's own tables, read from its source, against the entries -- so a
// connector that moves, or one the library gains, fails there instead of
// leaving this table quietly wrong.
var connectorModules = map[string]map[string]string{
	"DATABASE_URL": {
		"sqlite": "github.com/arandu-io/hesape/database/connectors/sqlite",
		"pgsql":  "github.com/arandu-io/hesape/database/connectors/pgx",
		"mysql":  "github.com/arandu-io/hesape/database/connectors/mysql",
	},
	"CACHE_STORE":      {"redis": "github.com/arandu-io/hesape/redis"},
	"SESSION_DRIVER":   {"redis": "github.com/arandu-io/hesape/redis"},
	"QUEUE_CONNECTION": {"redis": "github.com/arandu-io/hesape/queue/connectors/redis"},
}

// databaseSchemes maps a DATABASE_URL scheme to the dialect it selects. The
// dialect is the name the boot error uses for the engine, so a finding and the
// error it predicts say the same word.
var databaseSchemes = map[string]string{
	"sqlite":     "sqlite",
	"sqlite3":    "sqlite",
	"file":       "sqlite",
	"postgres":   "pgsql",
	"postgresql": "pgsql",
	"mysql":      "mysql",
	"mariadb":    "mysql",
}

// databaseDialect answers the dialect a DATABASE_URL selects, or empty when
// its scheme is missing or one no connector speaks. Those two stop the boot
// with an error of their own, about the URL rather than about an import, so
// there is no connector to name for them.
func databaseDialect(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return databaseSchemes[strings.ToLower(u.Scheme)]
}

// connectorFor answers which driver a setting asks for and which module links
// it, or empty when the value needs no import.
func connectorFor(name, value string) (driver, module string) {
	driver = value
	if name == "DATABASE_URL" {
		driver = databaseDialect(value)
	}
	module = connectorModules[name][driver]
	if module == "" {
		return "", ""
	}
	return driver, module
}

// envSetting is one variable .env.example sets, with the line that set it.
type envSetting struct {
	value string
	line  int
}

// readEnvExample answers what .env.example sets, by name, or nil when the
// project has none.
//
// The format is the one the application's loader reads, and nothing more:
// KEY=value, blank lines, whole-line comments, an optional `export ` prefix and
// one optional layer of matching quotes around the value. There is no inline
// comment, because there is none at boot either -- a # in a value is part of
// the value.
//
// A line outside that format is skipped rather than reported. The boot refuses
// it and names the line, and a second answer to a question the loader already
// owns is how two tools come to disagree about one file.
//
// A name set twice keeps its first line, because that is the one the loader
// keeps: it never overwrites a variable that is already set, and the first line
// is what set it.
func readEnvExample(dir string) map[string]envSetting {
	return readEnvFile(filepath.Join(dir, envExample))
}

// EnvFile answers what one file in the format of .env sets, by name, read the
// way readEnvExample reads .env.example, or nil when there is no such file.
//
// It is how `aru about` reads a project's .env: one reader of the format, so
// the configuration the report shows and the one this package checks cannot
// be two readings of one file.
func EnvFile(path string) map[string]string {
	settings := readEnvFile(path)
	if settings == nil {
		return nil
	}
	out := make(map[string]string, len(settings))
	for name, s := range settings {
		out[name] = s.value
	}
	return out
}

// DatabaseDialect answers the engine a DATABASE_URL selects -- sqlite, pgsql
// or mysql -- or empty when no connector speaks its scheme. The URL itself
// carries credentials, and the dialect is the part of it that can be shown.
func DatabaseDialect(raw string) string { return databaseDialect(raw) }

func readEnvFile(path string) map[string]envSetting {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	out := map[string]envSetting{}
	for i, raw := range strings.Split(string(body), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if _, seen := out[name]; seen || name == "" {
			continue
		}

		value = strings.TrimSpace(value)
		if n := len(value); n >= 2 && (value[0] == '"' || value[0] == '\'') && value[n-1] == value[0] {
			value = value[1 : n-1]
		}
		out[name] = envSetting{value: value, line: i + 1}
	}
	return out
}
