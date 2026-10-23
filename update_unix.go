//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detached starts a process in its own session so it outlives Blaze.
func detached(name string, args ...string) *exec.Cmd {
	c := exec.Command(name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return c
}
