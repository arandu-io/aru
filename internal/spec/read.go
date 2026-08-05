package spec

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is what a module keeps its specification in.
//
// Beside the code it generated, and committed with it. That is what makes the
// round trip real: regenerating reads this file, and the bytes it produces are
// the bytes already there.
const FileName = "module.arandu.yaml"

// Read parses a specification file and validates it.
//
// Parse and validate together, always. A Read that returned an invalid document
// would be an invitation to skip the check, and the whole value of the DSL is
// that a bad spec dies before it becomes code.
func Read(path string) (Module, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Module{}, fmt.Errorf("reading %s: %w", path, err)
	}
	return Parse(body, path)
}

// Parse reads a specification from bytes.
func Parse(body []byte, name string) (Module, error) {
	var m Module

	decoder := yaml.NewDecoder(strings.NewReader(string(body)))
	// KnownFields is what turns a typo into an error. Without it,
	// `requried: true` is silently a field that does not exist, and the
	// generated code is missing a NOT NULL nobody asked about -- which is the
	// exact failure mode a schema exists to prevent.
	decoder.KnownFields(true)

	if err := decoder.Decode(&m); err != nil {
		return Module{}, fmt.Errorf("%s is not a valid specification: %w", name, explain(err, body))
	}
	if err := m.Validate(); err != nil {
		return Module{}, fmt.Errorf("%s: %w", name, err)
	}
	return m, nil
}

// Write saves a specification, formatted.
//
// Two-space indent, which is what every YAML example anyone has read uses --
// including the ones a model was trained on. A file that comes back formatted
// differently than it went in makes the round trip look broken when it is not.
func Write(path string, m Module) error {
	body, err := Marshal(m)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// Marshal renders a specification as YAML.
func Marshal(m Module) ([]byte, error) {
	var b strings.Builder
	encoder := yaml.NewEncoder(&b)
	encoder.SetIndent(2)

	if err := encoder.Encode(m); err != nil {
		return nil, fmt.Errorf("rendering the specification: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("rendering the specification: %w", err)
	}
	return []byte(b.String()), nil
}

// explain turns a yaml error into something a person can act on.
//
// The library says `field requried not found in type spec.Field`, which names
// the Go type rather than the file. Anyone reading it is looking at YAML.
func explain(err error, body []byte) error {
	message := err.Error()
	message = strings.ReplaceAll(message, "yaml: unmarshal errors:\n  ", "")
	message = strings.ReplaceAll(message, "in type spec.Module", "at the top level")
	message = strings.ReplaceAll(message, "in type spec.Field", "in a field")

	// The one syntax error people actually hit, and the one whose message says
	// the least. A description is a sentence, sentences carry colons, and an
	// unquoted colon ends the value -- so YAML reads the rest as a nested map.
	//
	// What the library says is "mapping values are not allowed in this context",
	// which names a YAML concept and no action. This names the line, the colon,
	// and the fix.
	if strings.Contains(message, "mapping values are not allowed") {
		if line, text := offendingLine(body, message); text != "" {
			return fmt.Errorf("%s\n\n  line %d: %s\n\nA value containing \": \" has to be quoted, or YAML reads what follows the\ncolon as a nested key. Wrap it:\n\n  description: \"%s\"",
				message, line, text, strings.TrimSpace(afterKey(text)))
		}
	}
	return fmt.Errorf("%s", message)
}

// offendingLine pulls the line number out of a yaml error and returns its text.
func offendingLine(body []byte, message string) (int, string) {
	// The library formats it as "yaml: line N: ...", one-indexed.
	marker := "line "
	i := strings.Index(message, marker)
	if i < 0 {
		return 0, ""
	}
	rest := message[i+len(marker):]
	end := strings.IndexByte(rest, ':')
	if end < 0 {
		return 0, ""
	}

	number := 0
	for _, c := range rest[:end] {
		if c < '0' || c > '9' {
			return 0, ""
		}
		number = number*10 + int(c-'0')
	}

	lines := strings.Split(string(body), "\n")
	if number < 1 || number > len(lines) {
		return 0, ""
	}
	return number, strings.TrimRight(lines[number-1], "\r")
}

// afterKey returns what follows the first "key: " on a line, which is the value
// that needed quoting.
func afterKey(line string) string {
	if _, value, found := strings.Cut(line, ": "); found {
		return value
	}
	return line
}
