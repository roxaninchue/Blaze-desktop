package tools

import (
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// killTree makes a cancelled command take its children with it: taskkill /T ends the
// whole tree (PowerShell's children would otherwise keep running).
func killTree(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	c.Cancel = func() error {
		if c.Process == nil {
			return nil
		}
		k := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(c.Process.Pid))
		k.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		if k.Run() != nil {
			return c.Process.Kill()
		}
		return nil
	}
	c.WaitDelay = 2 * time.Second
}
