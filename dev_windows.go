//go:build windows

package main

import (
	"os/exec"
	"strconv"
	"syscall"
)

// processGroup has no Unix equivalent here: Windows gets the job done through
// taskkill in killGroup.
func processGroup() *syscall.SysProcAttr { return nil }

// killGroup terminates the process and its children.
func killGroup(pid int) {
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
}
