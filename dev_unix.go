//go:build !windows

package main

import "syscall"

// processGroup puts the child in its own group.
//
// `go run` compiles to a temporary binary and execs it as a child, so signalling
// only `go run` leaves the application running and holding the port.
func processGroup() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// killGroup terminates the whole group, by the negative pid convention.
func killGroup(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGTERM)
}
