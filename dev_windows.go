//go:build windows

package main

import (
	"os/exec"
	"strconv"
	"syscall"
)

// These two are the Windows half of a pair, and dev.go calls both. A whole-file
// build constraint means no analysis run on another platform can see either
// one, so a reachability sweep that does not name them has not looked at them --
// silence here is the tool being out of the build, not a caller being absent.
// The Unix half is in dev_unix.go.

// processGroup has no Unix equivalent here: Windows gets the job done through
// taskkill in killGroup.
func processGroup() *syscall.SysProcAttr { return nil }

// killGroup terminates the process and its children.
func killGroup(pid int) {
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
}
