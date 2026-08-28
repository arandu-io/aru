package main

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
)

// What the commands that start the application do when it dies on its own.
//
// Most of the time the answer is to keep watching: a compile error or a panic in
// an init is fixed by the next save, and stopping would make the loop something
// people restart by hand all day.
//
// One failure is not like that. When the address is already in use, every
// restart fails the same way forever -- and the process holding the port is
// usually a previous run that outlived its parent, still serving the browser. So
// the developer sees a working site, edits a file, nothing changes, and the
// terminal repeats a line that names the symptom and not the cause. Nothing
// points at `lsof`.

// bindFailure matches what the Go runtime prints when a listener cannot bind,
// and captures the address the APPLICATION named.
//
// Read out of the child's own output rather than from configuration: `aru` does
// not know HTTP_ADDR and must not learn it, because that would be a second
// source of truth for something the application owns. What the application
// printed is the application speaking.
var bindFailure = regexp.MustCompile(`listen tcp (\S*?):(\d+): bind: address already in use`)

// diagnoseExit turns the application's last words into something actionable, and
// reports whether trying the same thing again could ever work.
//
// command is what the reader typed. The message ends in a line they are meant to
// run, and a suggestion that names the other command is a suggestion that starts
// something they did not ask for -- a watch loop for somebody who wanted one
// process, or one process for somebody watching.
func diagnoseExit(output, command string) (message string, fatal bool) {
	m := bindFailure.FindStringSubmatch(output)
	if m == nil {
		return "", false
	}

	port := m[2]
	var b strings.Builder
	fmt.Fprintf(&b, "port %s is already in use, so this will fail the same way on every restart.\n\n", port)

	if holders := holdersOf(port); len(holders) > 0 {
		b.WriteString("It is held by:\n\n")
		for _, h := range holders {
			fmt.Fprintf(&b, "    %s\n", h)
		}
		fmt.Fprintf(&b, "\nA previous `%s` that outlived its parent looks exactly like this,\n"+
			"and it is still answering the browser -- which is why the site appears to\n"+
			"work and ignores every change you make.\n\n", command)
	}

	fmt.Fprintf(&b, "Stop it, or serve somewhere else:\n\n"+
		"    kill $(lsof -t -iTCP:%s -sTCP:LISTEN)\n"+
		"    HTTP_ADDR=:%s %s\n", port, port, command)

	return b.String(), true
}

// holdersOf asks the system who is listening, in a line a person can read.
//
// Best effort: lsof is not on every machine and the message is worth printing
// without it. What it adds is the pid and the command, which is what turns
// "something has the port" into "this is the process, here is how it started".
func holdersOf(port string) []string {
	out, err := exec.Command("lsof", "-nP", "-iTCP:"+port, "-sTCP:LISTEN", "-F", "pcn").Output()
	if err != nil {
		return nil
	}

	var lines []string
	var pid, command string
	for _, field := range strings.Split(string(out), "\n") {
		if field == "" {
			continue
		}
		switch field[0] {
		case 'p':
			pid = field[1:]
		case 'c':
			command = field[1:]
		case 'n':
			lines = append(lines, fmt.Sprintf("pid %s  %s  (%s)", pid, command, field[1:]))
		}
	}
	return lines
}

// tail keeps the last bytes written through it, and passes everything on.
//
// The application's output belongs to the person watching, so it is never
// swallowed; what is kept is a window big enough to hold the sentence that
// explains an exit, and small enough that a chatty boot cannot grow it.
type tail struct {
	mu   sync.Mutex
	to   io.Writer
	kept bytes.Buffer
}

const tailSize = 8 << 10

func newTail(to io.Writer) *tail { return &tail{to: to} }

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	t.kept.Write(p)
	if t.kept.Len() > tailSize {
		t.kept.Next(t.kept.Len() - tailSize)
	}
	t.mu.Unlock()

	return t.to.Write(p)
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.kept.String()
}
