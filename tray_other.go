//go:build !darwin

package main

import (
	"runtime"

	"fyne.io/systray"
)

// startTray runs the tray on its own locked OS thread (Windows and Linux).
func (a *App) startTray() {
	go func() {
		runtime.LockOSThread()
		defer func() { recover() }()
		systray.Run(func() {
			systray.SetOnTapped(func() { go a.ToggleOrb() })
			a.trayReady()
		}, nil)
	}()
}

func setTrayIcon() {
	if runtime.GOOS == "windows" {
		systray.SetIcon(trayICO)
	} else {
		systray.SetIcon(trayPNG)
	}
}

func stopTray() { systray.Quit() }

func askAccessibility() {}
