package main

import (
	"bytes"
	"strings"
	"testing"
)

// Without this, the loop says "the application exited: exit status 1" and
// restarts forever, while a previous `aru dev` that outlived its parent goes on
// answering the browser from the same port. The site looks like it works and
// ignores every change, and the terminal repeats a line naming the symptom and
// not the cause.
func TestAPortAlreadyTakenStopsTheLoopAndSaysWhy(t *testing.T) {
	const said = `time=2026-08-10T12:15:55.761-03:00 level=INFO msg="server listening" addr=:8080 env=dev
2026/08/10 12:15:55 listen tcp :8080: bind: address already in use
exit status 1
`
	message, fatal := diagnoseExit(said, "aru dev")
	if !fatal {
		t.Fatal("the loop carries on retrying a port that will be taken on every attempt")
	}
	for _, want := range []string{"8080", "kill ", "HTTP_ADDR"} {
		if !strings.Contains(message, want) {
			t.Errorf("the message does not mention %q:\n%s", want, message)
		}
	}
}

// The address the application named is read out of what it printed, not from
// configuration -- aru does not know HTTP_ADDR and must not learn it.
func TestThePortComesFromWhatTheApplicationSaid(t *testing.T) {
	for _, c := range []struct{ said, port string }{
		{"listen tcp :8080: bind: address already in use", "8080"},
		{"listen tcp 127.0.0.1:3000: bind: address already in use", "3000"},
		{"listen tcp [::]:9999: bind: address already in use", "9999"},
	} {
		message, fatal := diagnoseExit(c.said, "aru dev")
		if !fatal {
			t.Errorf("%q was not recognised", c.said)
			continue
		}
		if !strings.Contains(message, c.port) {
			t.Errorf("%q produced a message without the port:\n%s", c.said, message)
		}
	}
}

// Everything else is fixed by the next save, and stopping there would make this
// a command people restart by hand all day.
func TestAnOrdinaryFailureKeepsTheLoopRunning(t *testing.T) {
	for _, said := range []string{
		"app/Http/Controllers/HomeController.go:8:2: undefined: middleware\nexit status 1\n",
		"panic: runtime error: invalid memory address\n",
		"2026/08/10 12:15:55 invalid APP_ENV: \"local\"\n",
		"",
	} {
		if _, fatal := diagnoseExit(said, "aru dev"); fatal {
			t.Errorf("the loop stopped for something the next save fixes:\n%s", said)
		}
	}
}

// The application's output belongs to the person watching: it is kept as well
// as printed, never instead of.
func TestWhatTheApplicationSaysStillReachesTheTerminal(t *testing.T) {
	var terminal bytes.Buffer
	said := newTail(&terminal)

	if _, err := said.Write([]byte("server listening\n")); err != nil {
		t.Fatal(err)
	}
	if terminal.String() != "server listening\n" {
		t.Errorf("the output was swallowed: %q", terminal.String())
	}
	if said.String() != "server listening\n" {
		t.Errorf("the output was not kept: %q", said.String())
	}
}

// A chatty boot must not grow the window without bound, and the end is the half
// worth keeping: the sentence that explains an exit is the last one.
func TestTheWindowKeepsTheEndAndDoesNotGrow(t *testing.T) {
	said := newTail(new(bytes.Buffer))
	for i := 0; i < 100; i++ {
		if _, err := said.Write(bytes.Repeat([]byte("x"), 1024)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := said.Write([]byte("bind: address already in use\n")); err != nil {
		t.Fatal(err)
	}

	if got := len(said.String()); got > tailSize+64 {
		t.Errorf("the window grew to %d bytes", got)
	}
	if !strings.Contains(said.String(), "bind: address already in use") {
		t.Error("the last thing the application said was dropped, which is the one that explains the exit")
	}
}
