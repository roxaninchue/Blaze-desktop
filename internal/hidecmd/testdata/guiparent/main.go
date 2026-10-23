//go:build windows

// guiparent is built as a GUI program, like Blaze.exe, and starts conchild either through
// hidecmd.Hide ("hidden") or plainly ("plain"), writing what conchild saw to the out file.
package main

import (
	"os"
	"os/exec"

	"blaze-desktop/internal/hidecmd"
)

func main() {
	child, mode, out := os.Args[1], os.Args[2], os.Args[3]
	c := exec.Command(child)
	if mode == "hidden" {
		hidecmd.Hide(c)
	}
	b, err := c.Output()
	if err != nil {
		b = []byte("error: " + err.Error())
	}
	_ = os.WriteFile(out, b, 0o644)
}
