package gen

import (
	"os"
	"path/filepath"
	"strings"
)

// ProjectGates reads the gate block of the AGENTS.md at the project root: the
// first fenced block whose first command is `export GOWORK=off`, without its
// fences. It answers empty when there is no AGENTS.md or no such block, and
// the module's skill then carries the block this generator knows.
//
// The project's own file is read rather than a list kept here because the
// project is where the gates are decided. A skill that listed a second set
// would be a second answer to "what has to pass", and the two drift the first
// time either changes.
func ProjectGates(root string) string {
	body, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		return ""
	}
	return GatesIn(string(body))
}

// GatesIn finds the gate block in the text of an AGENTS.md. See ProjectGates.
func GatesIn(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
			continue
		}
		var block []string
		j := i + 1
		for ; j < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[j]), "```"); j++ {
			block = append(block, lines[j])
		}
		first := ""
		for _, line := range block {
			if strings.TrimSpace(line) != "" {
				first = strings.TrimSpace(line)
				break
			}
		}
		if first == "export GOWORK=off" {
			return strings.TrimSpace(strings.Join(block, "\n"))
		}
		i = j
	}
	return ""
}
