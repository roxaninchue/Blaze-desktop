//go:build !windows

package media

import (
	"os/exec"
	"syscall"
)

// freeBytes is the free space where downloads are written (0 = unknown).
func freeBytes(dir string) int64 {
	var st syscall.Statfs_t
	if syscall.Statfs(dir, &st) != nil {
		return 0
	}
	return int64(st.Bavail) * int64(st.Bsize)
}

func hideWindow(*exec.Cmd) {}
