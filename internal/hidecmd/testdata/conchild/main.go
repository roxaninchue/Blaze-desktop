//go:build windows

// conchild is a console program: it prints "window" when Windows gave it a console window.
package main

import (
	"fmt"
	"syscall"
)

func main() {
	h, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleWindow").Call()
	if h != 0 {
		fmt.Print("window")
	} else {
		fmt.Print("none")
	}
}
