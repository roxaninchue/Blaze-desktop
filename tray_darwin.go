package main

/*
#cgo LDFLAGS: -framework Cocoa -framework ApplicationServices
void blazeOnMain(void);
void blazeAskAccessibility(void);
*/
import "C"

import "fyne.io/systray"

var (
	trayStart func()
	trayApp   *App
)

// startTray: Wails owns NSApp and its run loop, so the menu-bar item uses systray's
// external loop, started on the main thread through a tiny dispatch helper.
func (a *App) startTray() {
	start, _ := systray.RunWithExternalLoop(a.trayReady, nil)
	trayStart, trayApp = start, a
	C.blazeOnMain()
}

//export blazeTrayStart
func blazeTrayStart() {
	if trayStart != nil {
		trayStart()
	}
}

//export blazeReopen
func blazeReopen() {
	if trayApp != nil {
		go trayApp.ShowMain()
	}
}

func setTrayIcon() { systray.SetTemplateIcon(trayTemplate, trayTemplate) }

func trayAvailable() bool { return true }

// stopTray: never systray.Quit on a Mac, it stops NSApp.
func stopTray() {}

// askAccessibility shows macOS's own Accessibility prompt for Blaze.
func askAccessibility() { C.blazeAskAccessibility() }
