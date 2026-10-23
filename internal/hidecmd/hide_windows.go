//go:build windows

package hidecmd

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

func hide(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.HideWindow = true
	c.SysProcAttr.CreationFlags |= createNoWindow
}
