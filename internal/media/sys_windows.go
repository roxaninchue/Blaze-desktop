//go:build windows

package media

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// freeBytes is the free space where downloads are written (0 = unknown).
func freeBytes(dir string) int64 {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0
	}
	var free, total, all uint64
	if windows.GetDiskFreeSpaceEx(p, &free, &total, &all) != nil {
		return 0
	}
	return int64(free)
}

// hideWindow keeps ffmpeg from flashing a console window.
func hideWindow(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
