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
//
// # The thing that can be stale is the watcher, not the compiler
//
// Every report of "aru dev does not pick up my change, and aru build fixes it"
// so far has been a hole in the loop below: output watched as input, a failed
// build forgetting it was owed, a deleted view whose generated Go stayed
// registered. The Go build cache is the obvious suspect and it is never the
// cause -- embedded file contents are hashed into the action ID, and `aru
// build`'s -trimpath/-ldflags entries are disjoint from `go run`'s, so `aru
// build` cannot warm anything this command reads. Look here instead.
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
	if err := buildViews(root, stdout, stderr); err != nil {
		return err
	}

	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interrupted)

	server, err := startServer(root, args, stdout, stderr)
	if err != nil {
		return err
	}
	// A closure, not `defer stopServer(server)`: the argument of a defer is
	// evaluated where the defer is written, and `server` is reassigned on every
	// restart -- so the bare form kills the first process and orphans the live
	// one, which keeps the port and serves a build from hours ago.
	defer func() { stopServer(server) }()

	fmt.Fprintln(stdout, "watching for changes; ctrl-c to stop")
	state := snapshot(root)

	// A rebuild that failed stays owed. `state` below has already consumed the
	// change that asked for it, and `views` is recomputed from the current tick
	// alone -- so without this the next save of any ordinary .go file restarts
	// the server against generated views from before the last view edit, and
	// only `aru build` ever repairs it. That is the reported symptom, exactly.
	pending := false
	// One settled tick before acting. A multi-file write -- a checkout, a save
	// all, a formatter -- spans a poll and is otherwise seen half done: the loop
	// restarts against a tree that never existed on disk and prints compile
	// errors nobody caused.
	dirty := false

	for {
		select {
		case <-interrupted:
			return stop(stdout, interrupted)
		case err := <-server.exited:
			// The application died on its own: a panic in an init, a compile
			// error, a port already taken. Say so once, rather than leaving the
			// loop printing "restarting" with nothing behind it.
			server.done = true
			fmt.Fprintf(stderr, "the application exited: %v\n", err)

			// And when carrying on cannot help, stop instead of pretending.
			// A port that is already in use is taken by something this loop
			// cannot outlast -- usually a previous `aru dev` that outlived its
			// parent, still answering the browser, which is why the site looks
			// like it works and ignores every change.
			if message, fatal := diagnoseExit(server.output()); fatal {
				fmt.Fprintf(stderr, "\n%s", message)
				return errors.New("the application cannot start")
			}
			continue
		case <-time.After(pollInterval):
		}

		current := snapshot(root)
		changed, views := diff(state, current)
		state = current

		if changed {
			dirty = true
			pending = pending || views
			continue
		}
		if !dirty {
			continue
		}
		dirty = false

		if pending {
			if err := buildViews(root, stdout, stderr); err != nil {
				// A broken template must not kill the loop: the next save is
				// usually the fix, and an editor that has to be restarted after
				// every typo is worse than a stale page. `pending` stays true, so
				// the retry rides the next change rather than re-running the
				// build every 500ms and printing the same error twice a second.
				fmt.Fprintf(stderr, "view build failed: %v\n", err)
				continue
			}
			pending = false
		}

		// Read the signal once more before spending seconds on a restart: ctrl-c
		// during a build is otherwise answered after the build, which reads as
		// the key doing nothing and is what makes people reach for kill -9 --
		// and kill -9 on the group leader is how the orphan above comes back.
		select {
		case <-interrupted:
			return stop(stdout, interrupted)
		default:
		}

		fmt.Fprintln(stdout, "restarting")
		stopServer(server)
		if server, err = startServer(root, args, stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
		}
	}
}

// stop says so and re-arms the escape hatch.
//
// After signal.Stop a second ctrl-c reaches the default disposition and kills
// aru outright, instead of being dropped by a buffer of one that is already
// full -- which is what "ctrl-c does nothing" means when the first one is
// already being handled.
func stop(stdout io.Writer, interrupted chan os.Signal) error {
	fmt.Fprintln(stdout, "\nstopping")
	signal.Stop(interrupted)
	return nil
}

// serverProcess is the running application plus the one goroutine allowed to
// reap it.
//
// Two callers of cmd.Wait() is one panic and one silently wrong exit status;
// one owner and a channel is neither. The channel is buffered and never closed,
// so once the value is taken the case simply stops being selectable.
type serverProcess struct {
	cmd    *exec.Cmd
	exited chan error
	done   bool

	// said is the last of what the application printed, kept so that an exit
	// can be explained rather than only reported.
	said *tail
}

// output is what the application last said.
func (s *serverProcess) output() string {
	if s == nil || s.said == nil {
		return ""
	}
	return s.said.String()
}

func startServer(root string, args []string, stdout, stderr io.Writer) (*serverProcess, error) {
	// The application's own words are kept as well as printed, because the
	// sentence that explains an exit is in them and the loop has to read it to
	// know whether trying again can help. See diagnoseExit.
	said := newTail(stderr)

	cmd := exec.Command("go", append([]string{"run", appPackage, "serve"}, args...)...)
	cmd.Dir = root
	cmd.Stdout = stdout
	cmd.Stderr = said
	cmd.Stdin = os.Stdin
	// A process group, so stopping kills `go run` and the binary it spawned.
	// Without this the compiled application survives every restart and the next
	// one fails to bind the port -- which reads as "the port is in use" and
	// costs an afternoon.
	cmd.SysProcAttr = processGroup()

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting the application: %w", err)
	}

	s := &serverProcess{cmd: cmd, exited: make(chan error, 1), said: said}
	go func() { s.exited <- cmd.Wait() }()
	return s, nil
}

func stopServer(s *serverProcess) {
	if s == nil || s.done || s.cmd == nil || s.cmd.Process == nil {
		return
	}
	killGroup(s.cmd.Process.Pid)
	// The goroutine started above owns Wait; this is the only place that waits
	// on it, and it is what makes the restart sequential rather than a race
	// between the old process releasing the port and the new one binding it.
	<-s.exited
	s.done = true
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

	// The stylesheet is output for exactly the same reason, and the loop above
	// does not cover it: it is written by Tailwind, not by kyse. Watched, one
	// edit to resources/css/app.css was three restarts, and nothing in aru
	// bounded that -- it terminated only because Tailwind happens to skip
	// writing identical bytes, which is an external binary's behaviour and not
	// a guarantee. arandu.toml exists to raise that pin.
	delete(state, filepath.Join(root, stylesheetOutput))
	return state
}

func watched(path string) bool {
	switch filepath.Ext(path) {
	case ".go", ".css", ".sql":
		return true
	// Compiled into the binary by go:embed -- public/public.go, assets/fonts.go
	// -- so a swap that is not watched is a swap that never appears, with no
	// message, which reads as a caching bug that does not exist. .toml brings
	// arandu.toml, which pins the toolchain, into scope for the same reason.
	case ".svg", ".png", ".ico", ".webp", ".woff2", ".txt", ".json", ".toml":
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
