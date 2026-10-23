// Package hidecmd keeps helper programs from opening console windows.
//
// On Windows a GUI app that starts a console program (git, node, schtasks,
// powershell…) gets a new console window for it unless the process is created
// with CREATE_NO_WINDOW. Every background command Blaze runs goes through Hide,
// so nothing flashes on screen. On macOS and Linux child processes never open a
// terminal window, so Hide does nothing there.
package hidecmd

import "os/exec"

// Hide marks c to run without a window and returns it, so it can wrap a call:
// hidecmd.Hide(exec.Command("git", "status")).Output().
func Hide(c *exec.Cmd) *exec.Cmd {
	if c != nil {
		hide(c)
	}
	return c
}
