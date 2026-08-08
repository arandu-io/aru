package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/arandu-io/aru/internal/kyse"
)

// pollInterval is how often the working tree is checked for changes.
//
// Polling rather than an OS watcher, because a watcher means a third-party
// dependency in a CLI that has none, and half a second is below the threshold
// where anyone notices. On a very large tree this is the first thing to replace.
const pollInterval = 500 * time.Millisecond

// dev runs the application and rebuilds it when a file changes.
//
// This is the command RULE 13 promises: `git clone && aru dev`, with no Node
// installed and no package manager involved. It builds the views, starts the
// server, and restarts it on every change -- one command, one terminal.
func dev(args []string, stdout, stderr io.Writer) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	if _, err := exec.LookPath("go"); err != nil {
		return errors.New("the go toolchain was not found in PATH, and aru needs it to run the project")
	}

	// The build runs once here rather than in watch mode, because the restart
	// loop below already reacts to view and stylesheet changes -- two watchers
	// over the same files would race on the generated output.
	if err := buildViews(root, false, stdout, stderr); err != nil {
		return err
	}

	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interrupted)

	server := startServer(root, args, stdout, stderr)
	defer stopServer(server)

	fmt.Fprintln(stdout, "watching for changes; ctrl-c to stop")
	state := snapshot(root)

	for {
		select {
		case <-interrupted:
			fmt.Fprintln(stdout, "\nstopping")
			return nil
		case <-time.After(pollInterval):
		}

		current := snapshot(root)
		changed, views := diff(state, current)
		if !changed {
			continue
		}
		state = current

		if views {
			if err := buildViews(root, false, stdout, stderr); err != nil {
				// A broken template must not kill the loop: the next save is
				// usually the fix, and an editor that has to be restarted after
				// every typo is worse than a stale page.
				fmt.Fprintf(stderr, "view build failed: %v\n", err)
				continue
			}
		}

		fmt.Fprintln(stdout, "restarting")
		stopServer(server)
		server = startServer(root, args, stdout, stderr)
	}
}

func startServer(root string, args []string, stdout, stderr io.Writer) *exec.Cmd {
	cmd := exec.Command("go", append([]string{"run", appPackage, "serve"}, args...)...)
	cmd.Dir = root
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Stdin = os.Stdin
	// A process group, so stopping kills `go run` and the binary it spawned.
	// Without this the compiled application survives every restart and the next
	// one fails to bind the port -- which reads as "the port is in use" and
	// costs an afternoon.
	cmd.SysProcAttr = processGroup()

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "starting the application: %v\n", err)
		return nil
	}
	return cmd
}

func stopServer(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	killGroup(cmd.Process.Pid)
	_ = cmd.Wait()
}

// snapshot records the modification time of every file that can affect the
// running application.
func snapshot(root string) map[string]time.Time {
	state := map[string]time.Time{}
	var sources []string

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !watched(path) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		state[path] = info.ModTime()
		if isViewSource(path) {
			sources = append(sources, path)
		}
		return nil
	})

	// The Go kyse writes is an output, not an input: watching it would make
	// every view build trigger the next one.
	//
	// Which file that is comes from kyse rather than from a suffix, because the
	// generated file sits beside its source with nothing in the name that
	// announces it: auth/login.kyse.go compiles to auth/login.go, and there is no
	// way to tell that from a hand-written auth/helpers.go by looking. Guessing
	// would either watch the output, which is the rebuild loop, or stop watching
	// a hand-written file that happened to match.
	for _, source := range sources {
		delete(state, kyse.OutputPath(source))
	}
	return state
}

func watched(path string) bool {
	switch filepath.Ext(path) {
	case ".go", ".css", ".sql":
		return true
	case ".env":
		return true
	}
	return filepath.Base(path) == ".env"
}

// isViewSource reports whether a path is a view somebody writes, as opposed to
// the Go compiled from one.
func isViewSource(path string) bool { return strings.HasSuffix(path, viewSuffix) }

// isViewInput reports whether a change to this file has to go through
// `aru view:build` before the server is restarted.
func isViewInput(path string) bool {
	return isViewSource(path) || filepath.Ext(path) == ".css"
}

// diff reports whether anything changed, and whether the view layer has to be
// rebuilt before restarting.
func diff(before, after map[string]time.Time) (changed, views bool) {
	for path, mod := range after {
		previous, existed := before[path]
		if !existed || !previous.Equal(mod) {
			changed = true
			if isViewInput(path) {
				views = true
			}
		}
	}
	for path := range before {
		if _, exists := after[path]; !exists {
			changed = true
			if isViewInput(path) {
				views = true
			}
		}
	}
	return changed, views
}
