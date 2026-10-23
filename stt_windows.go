//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// quiet runs a helper program without flashing a console window.
func quiet(c *exec.Cmd) *exec.Cmd {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return c
}

func interrupt(c *exec.Cmd) { _ = c.Process.Kill() }
