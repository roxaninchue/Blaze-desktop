//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// detached starts a process with no console window that outlives Blaze.
func detached(name string, args ...string) *exec.Cmd {
	c := exec.Command(name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return c
}
