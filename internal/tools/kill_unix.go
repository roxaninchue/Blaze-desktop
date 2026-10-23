//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
	"time"
)

// killTree makes a cancelled command take its whole process group with it (a shell's
// children, such as du or npm, would otherwise keep running and keep the output open).
func killTree(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process == nil {
			return nil
		}
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		return c.Process.Kill()
	}
	c.WaitDelay = 2 * time.Second
}
