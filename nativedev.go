package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"
)

// nativeDev rebuilds the native target and reopens it whenever the project
// changes.
//
// There is no hot reload here, and there is none to have: Go compiles to a
// binary, and a running binary does not take new code. What this does instead
// is make the loop short -- notice, rebuild, replace the window -- which is the
// honest version of the same thing.
//
// Most changes do not need it at all. A native application draws for a server,
// so a handler, a value, a validation rule or a policy changes on the server
// side and the next screen the application asks for is already different, with
// nothing rebuilt here. This loop is for the other half: the screens themselves.
func nativeDev(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("native:dev", flag.ContinueOnError)
	flags.SetOutput(stderr)
	server := flags.String("server", "", "the address of the running application")
	dark := flags.Bool("dark", false, "open with the dark palette")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("native:dev: %w", err)
	}

	root, err := nativeRoot()
	if err != nil {
		return err
	}
	if _, err := exec.LookPath("go"); err != nil {
		return errors.New("the go toolchain was not found in PATH, and aru needs it to build the native target")
	}

	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, devSignals...)
	defer signal.Stop(interrupted)

	binary := filepath.Join(root, "bin", "native-dev")
	running := (*exec.Cmd)(nil)
	// A closure rather than a bare defer: the process is replaced on every
	// rebuild, and the argument of a defer is evaluated where it is written --
	// so the bare form would close the first window and leave every later one
	// open, each drawing from a build somebody has forgotten about.
	defer func() { closeWindow(running) }()

	restart := func() {
		closeWindow(running)
		running = nil

		if err := compileNative(root, binary, runtime.GOOS, runtime.GOARCH, stdout, stderr); err != nil {
			// A compile error is the normal state of a file somebody is in the
			// middle of writing. Say it and keep watching; the next save is
			// usually the fix.
			fmt.Fprintf(stderr, "%v\n", err)
			return
		}

		opened, err := openWindow(root, binary, *server, *dark, stdout, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return
		}
		running = opened
	}

	restart()
	fmt.Fprintln(stdout, "watching for changes; ctrl-c to stop")

	state := nativeSnapshot(root)
	dirty := false

	for {
		select {
		case <-interrupted:
			return stop(stdout, interrupted)
		case <-time.After(pollInterval):
		}

		current := nativeSnapshot(root)
		if !sameTree(state, current) {
			// One settled tick before acting, for the reason the web loop
			// gives: a save-all or a checkout spans a poll and is otherwise
			// seen half written, which rebuilds a tree that never existed.
			state, dirty = current, true
			continue
		}
		if dirty {
			dirty = false
			fmt.Fprintln(stdout, "rebuilding")
			restart()
		}
	}
}

// openWindow starts the built target and answers the process.
func openWindow(root, binary, server string, dark bool, stdout, stderr io.Writer) (*exec.Cmd, error) {
	var args []string
	if server != "" {
		args = append(args, "-server", server)
	}
	if dark {
		args = append(args, "-dark")
	}

	cmd := exec.Command(binary, args...)
	cmd.Dir = root
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// Its own process group, for the reason the server loop states: without it
	// a window survives the restart that was meant to replace it, and the next
	// build opens a second one beside the stale first.
	cmd.SysProcAttr = processGroup()

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("the native application did not start: %w", err)
	}
	return cmd, nil
}

// closeWindow ends a running window, and waits for it to be gone.
//
// Waiting matters: the next build writes to the same path, and on some systems
// a file still open as a running program cannot be replaced. Without the wait
// the rebuild fails with a message about a busy file, which reads as a
// permission problem.
func closeWindow(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	killGroup(cmd.Process.Pid)
	_, _ = cmd.Process.Wait()
}

// nativeSnapshot is the modification time of everything the native target is
// built from.
//
// It walks the target's own directory rather than the whole project, and that
// is the difference between this loop and the web one: a change to a handler
// does not rebuild an application that fetches from it over the wire.
func nativeSnapshot(root string) map[string]time.Time {
	state := map[string]time.Time{}

	target := filepath.Join(root, nativeDir)
	_ = filepath.WalkDir(target, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if !watched(path) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		state[path] = info.ModTime()
		return nil
	})

	// The module files, because a dependency that moved changes what the
	// screens are drawn with even though no screen was edited.
	for _, name := range []string{"go.mod", "go.sum"} {
		if info, err := os.Stat(filepath.Join(root, name)); err == nil {
			state[filepath.Join(root, name)] = info.ModTime()
		}
	}
	return state
}

// sameTree reports whether two snapshots describe the same files at the same
// times.
func sameTree(before, after map[string]time.Time) bool {
	if len(before) != len(after) {
		return false
	}
	for path, when := range before {
		other, found := after[path]
		if !found || !other.Equal(when) {
			return false
		}
	}
	return true
}
