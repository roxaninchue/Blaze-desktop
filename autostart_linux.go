package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func autostartFile() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "autostart", "blaze-desktop.desktop")
}

// setAutostart writes ~/.config/autostart/blaze-desktop.desktop.
func setAutostart(on bool) error {
	p := autostartFile()
	if !on {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	exe := selfPath()
	if strings.ContainsAny(exe, " \"'\\") {
		exe = `"` + strings.ReplaceAll(exe, `"`, `\"`) + `"`
	}
	entry := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=Blaze\nComment=Blaze assistant, in the background\nExec=%s --background\nIcon=blaze\nTerminal=false\nX-GNOME-Autostart-enabled=true\n", exe)
	return os.WriteFile(p, []byte(entry), 0o644)
}

func autostartEnabled() bool {
	b, err := os.ReadFile(autostartFile())
	return err == nil && strings.Contains(string(b), selfPath())
}
