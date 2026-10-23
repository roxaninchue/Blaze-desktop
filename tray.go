package main

import (
	_ "embed"
	"sync"

	"fyne.io/systray"
)

// Tray icons: the pixel "B" drawn at integer cell sizes (build/tray, made from logo.svg).
var (
	//go:embed build/tray/tray.ico
	trayICO []byte
	//go:embed build/tray/tray.png
	trayPNG []byte
	//go:embed build/tray/tray-template.png
	trayTemplate []byte
)

var (
	trayMu     sync.Mutex
	trayStatus *systray.MenuItem
	trayRouter *systray.MenuItem
)

// trayReady builds the menu: Quick ask, Open Blaze, a status line, Quit Blaze.
func (a *App) trayReady() {
	setTrayIcon()
	systray.SetTooltip("Blaze")
	ask := systray.AddMenuItem("Quick ask", "Open the Blaze orb")
	open := systray.AddMenuItem("Open Blaze", "Show the Blaze window")
	br := systray.AddMenuItem("BlazeRouter dashboard", "Open BlazeRouter's dashboard in your browser")
	systray.AddSeparator()
	st := systray.AddMenuItem("Idle", "What Blaze is doing")
	st.Disable()
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit Blaze", "Stop Blaze completely")
	trayMu.Lock()
	trayStatus = st
	trayRouter = br
	br.Hide() // shown once BlazeRouter is on (setTrayRouter)
	trayMu.Unlock()
	a.sh.mu.Lock()
	a.sh.trayOK = trayAvailable()
	if !a.sh.trayOK {
		a.sh.trayErr = "No system tray found (on GNOME, install the AppIndicator extension)"
	}
	a.sh.mu.Unlock()
	go func() {
		for {
			select {
			case <-ask.ClickedCh:
				a.ShowOrb()
			case <-open.ClickedCh:
				a.ShowMain()
			case <-br.ClickedCh:
				a.OpenRouterDashboard()
			case <-quit.ClickedCh:
				a.quit()
				return
			}
		}
	}()
}

// setTrayStatus updates the disabled status line in the tray menu.
func setTrayStatus(s string) {
	trayMu.Lock()
	st := trayStatus
	trayMu.Unlock()
	if st != nil {
		st.SetTitle(s)
		systray.SetTooltip("Blaze · " + s)
	}
}

// setTrayRouter shows BlazeRouter's state on its tray item.
func setTrayRouter(v RouterView) {
	trayMu.Lock()
	it := trayRouter
	trayMu.Unlock()
	if it == nil {
		return
	}
	if v.Enabled {
		it.Show()
	} else {
		it.Hide() // Blaze Cloud answers; the local router is an advanced option
	}
	dot := map[string]string{"green": "●", "amber": "◐", "red": "○"}[v.Level]
	it.SetTitle(dot + " BlazeRouter dashboard")
	it.SetTooltip(v.Note)
}
