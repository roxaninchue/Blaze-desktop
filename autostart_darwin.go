package main

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
)

func launchAgent() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", "com.blaze.desktop.plist")
}

// setAutostart writes a LaunchAgent that starts Blaze hidden at login.
func setAutostart(on bool) error {
	p := launchAgent()
	if !on {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>com.blaze.desktop</string>
	<key>ProgramArguments</key>
	<array><string>%s</string><string>--background</string></array>
	<key>RunAtLoad</key><true/>
	<key>ProcessType</key><string>Interactive</string>
</dict>
</plist>
`, html.EscapeString(selfPath()))
	return os.WriteFile(p, []byte(plist), 0o644)
}

func autostartEnabled() bool {
	b, err := os.ReadFile(launchAgent())
	return err == nil && strings.Contains(string(b), html.EscapeString(selfPath()))
}
