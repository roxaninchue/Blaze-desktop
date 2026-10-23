package main

import "github.com/godbus/dbus/v5"

// trayAvailable: the icon only shows when something hosts StatusNotifierItems
// (KDE, Cinnamon, XFCE, waybar, GNOME with the AppIndicator extension).
func trayAvailable() bool {
	conn, err := dbus.SessionBus()
	if err != nil {
		return false
	}
	var has bool
	err = conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, "org.kde.StatusNotifierWatcher").Store(&has)
	return err == nil && has
}
