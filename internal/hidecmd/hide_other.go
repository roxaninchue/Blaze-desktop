//go:build !windows

package hidecmd

import "os/exec"

func hide(*exec.Cmd) {}
