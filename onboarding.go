package main

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"

	"blaze-desktop/internal/notify"
)

// First-run onboarding (v0.19.14): two steps, sign in and an optional folder, then a chat
// that answers. Permissions (notifications, shortcut, start at login) are no longer asked up
// front: they're asked when a task first needs them (permissions.go) or live in Settings.
// The step is saved so a restart picks up where the user left off.

// onboardSteps are the steps in order; the frontend draws the same list (onboard.js).
var onboardSteps = []string{"signin", "folder"}

// OnboardState is what the onboarding screen needs beyond State().
type OnboardState struct {
	Step    int      `json:"step"`    // last step the user reached
	Steps   []string `json:"steps"`   // the steps, in order
	Skipped bool     `json:"skipped"` // the user chose Skip (don't open it by itself again)
	Done    bool     `json:"done"`
}

// clampStep keeps a saved step inside the flow (a step saved by the old six-step flow resumes
// at the last step instead of past the end).
func clampStep(n int) int {
	if n < 0 {
		return 0
	}
	if n >= len(onboardSteps) {
		return len(onboardSteps) - 1
	}
	return n
}

func (a *App) Onboarding() OnboardState {
	a.waitReady()
	return OnboardState{Step: clampStep(a.prefs.OnboardStep), Steps: append([]string(nil), onboardSteps...), Skipped: a.prefs.OnboardSkipped, Done: a.cfg.SetupDone}
}

// SetOnboardStep remembers the step so onboarding resumes there.
func (a *App) SetOnboardStep(n int) {
	a.prefs.OnboardStep = clampStep(n)
	a.savePrefs()
}

// SkipOnboarding closes the flow; it can be reopened from Settings › Behaviour.
func (a *App) SkipOnboarding() {
	a.prefs.OnboardSkipped = true
	a.FinishSetup()
}

// FinishOnboarding is the last step's button.
func (a *App) FinishOnboarding() {
	a.prefs.OnboardStep = 0
	a.prefs.OnboardSkipped = true // they've seen it all; don't open it again by itself
	a.FinishSetup()
}

// OnboardNotifyTest sends one native notification so the OS asks for (or shows)
// its permission. "" means it was handed to the OS. Since v0.19.14 it runs when the user
// says yes to notifications at their first real need (PermAnswer), not during setup.
// It probes and caches the working method (~/.blaze/notify.json) first, so the
// onboarding test and every later notification use the same one.
func (a *App) OnboardNotifyTest() string {
	a.probeNotify()
	_, err := a.send(notify.Note{Title: "Blaze", Body: "Notifications are on. Blaze only uses them for things that matter.", Kind: "test"})
	if err != nil {
		// The direct per-OS call is the last resort (it also triggers macOS's permission prompt).
		if err2 := sendNotification("Blaze", "Notifications are on. Blaze only uses them for things that matter."); err2 != nil {
			return err.Error()
		}
	}
	return ""
}

func sendNotification(title, body string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	switch runtime.GOOS {
	case "linux":
		return notifyDBus(title, body)
	case "darwin":
		// Shown by the system; the first one makes macOS ask for permission.
		script := `display notification "` + appleQuote(body) + `" with title "` + appleQuote(title) + `"`
		out, err := exec.CommandContext(ctx, "osascript", "-e", script).CombinedOutput()
		if err != nil {
			return errors.New("macOS didn't show it: " + strings.TrimSpace(string(out)))
		}
		return nil
	case "windows":
		// Shown through PowerShell's registered app id: an unregistered one is dropped silently.
		ps := `[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null;` +
			`$x = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02);` +
			`$t = $x.GetElementsByTagName('text'); $t.Item(0).AppendChild($x.CreateTextNode('` + psQuote(title) + `')) > $null;` +
			`$t.Item(1).AppendChild($x.CreateTextNode('` + psQuote(body) + `')) > $null;` +
			`[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe').Show([Windows.UI.Notifications.ToastNotification]::new($x))`
		c := detached("powershell", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", ps)
		out, err := c.CombinedOutput()
		if err != nil {
			return errors.New("Windows didn't show it: " + strings.TrimSpace(string(out)))
		}
		return nil
	}
	return errors.New("notifications aren't supported on " + runtime.GOOS)
}

// notifyDBus uses the freedesktop notification service (GNOME, KDE, mako, dunst, swaync…).
func notifyDBus(title, body string) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return errors.New("no desktop session bus: " + err.Error())
	}
	defer conn.Close()
	obj := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	call := obj.Call("org.freedesktop.Notifications.Notify", 0, "Blaze", uint32(0), "blaze", title, body,
		[]string{}, map[string]dbus.Variant{}, int32(6000))
	if call.Err != nil {
		return errors.New("no notification service is running (install mako, dunst or swaync on a tiling desktop): " + call.Err.Error())
	}
	return nil
}

func appleQuote(s string) string { return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) }
func psQuote(s string) string    { return strings.ReplaceAll(s, "'", "''") }
