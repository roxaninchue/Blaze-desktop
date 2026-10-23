//go:build !windows

package main

import (
	"os"
	"os/exec"
)

func quiet(c *exec.Cmd) *exec.Cmd { return c }

// interrupt asks a recorder to stop so it finishes the WAV header.
func interrupt(c *exec.Cmd) { _ = c.Process.Signal(os.Interrupt) }
