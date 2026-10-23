//go:build linux

package screen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

func helperInit()                {}
func hideConsole(cmd *exec.Cmd)  {}
func thumb(w Win) (uint64, bool) { return 0, false }
func wayland() bool {
	return strings.EqualFold(os.Getenv("XDG_SESSION_TYPE"), "wayland") || (os.Getenv("WAYLAND_DISPLAY") != "" && os.Getenv("DISPLAY") == "")
}
func hyprland() bool       { return os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != "" }
func have(bin string) bool { _, err := exec.LookPath(bin); return err == nil }
func x11() bool            { return os.Getenv("DISPLAY") != "" && !wayland() && x11Built }

func run(timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

func probe(skip int, id uint64) (Win, error) {
	if hyprland() && have("hyprctl") {
		if w, err := hyprWin(skip, id); err == nil {
			return w, nil
		}
	}
	if os.Getenv("SWAYSOCK") != "" && have("swaymsg") {
		if w, err := swayWin(); err == nil {
			return w, nil
		}
	}
	if x11() {
		if w, err := x11Win(skip, id); err == nil {
			return w, nil
		}
	}
	return atspiActive(skip)
}

// ---------- Hyprland ----------

type hyprClient struct {
	Address string          `json:"address"`
	Mapped  bool            `json:"mapped"`
	Hidden  bool            `json:"hidden"`
	At      [2]int          `json:"at"`
	Size    [2]int          `json:"size"`
	Class   string          `json:"class"`
	Title   string          `json:"title"`
	PID     int             `json:"pid"`
	Full    json.RawMessage `json:"fullscreen"`
	Focus   int             `json:"focusHistoryID"`
}

func hyprWin(skip int, id uint64) (Win, error) {
	out, err := run(2*time.Second, "hyprctl", "clients", "-j")
	if err != nil {
		return Win{}, err
	}
	var cs []hyprClient
	if err := json.Unmarshal(out, &cs); err != nil {
		return Win{}, err
	}
	var best *hyprClient
	for i := range cs {
		c := &cs[i]
		if !c.Mapped || c.Hidden || (skip != 0 && c.PID == skip) || c.Size[0] <= 0 {
			continue
		}
		addr, _ := strconv.ParseUint(strings.TrimPrefix(c.Address, "0x"), 16, 64)
		if id != 0 && addr == id {
			best = c
			break
		}
		if best == nil || c.Focus < best.Focus {
			best = c
		}
	}
	if best == nil {
		return Win{}, errors.New("no window is open")
	}
	addr, _ := strconv.ParseUint(strings.TrimPrefix(best.Address, "0x"), 16, 64)
	fs := strings.TrimSpace(string(best.Full))
	return Win{ID: addr, PID: best.PID, App: best.Class, Title: best.Title, X: best.At[0], Y: best.At[1], W: best.Size[0], H: best.Size[1],
		Fullscreen: fs == "true" || (fs != "" && fs != "0" && fs != "false" && fs != "null")}, nil
}

// ---------- sway ----------

type swayNode struct {
	ID       uint64                            `json:"id"`
	Name     string                            `json:"name"`
	PID      int                               `json:"pid"`
	AppID    string                            `json:"app_id"`
	Focused  bool                              `json:"focused"`
	Full     int                               `json:"fullscreen_mode"`
	Rect     struct{ X, Y, Width, Height int } `json:"rect"`
	Nodes    []swayNode                        `json:"nodes"`
	Floating []swayNode                        `json:"floating_nodes"`
}

func swayWin() (Win, error) {
	out, err := run(2*time.Second, "swaymsg", "-t", "get_tree")
	if err != nil {
		return Win{}, err
	}
	var root swayNode
	if err := json.Unmarshal(out, &root); err != nil {
		return Win{}, err
	}
	var find func(n *swayNode) *swayNode
	find = func(n *swayNode) *swayNode {
		if n.Focused && n.PID > 0 {
			return n
		}
		for i := range n.Nodes {
			if f := find(&n.Nodes[i]); f != nil {
				return f
			}
		}
		for i := range n.Floating {
			if f := find(&n.Floating[i]); f != nil {
				return f
			}
		}
		return nil
	}
	f := find(&root)
	if f == nil {
		return Win{}, errors.New("no focused window")
	}
	return Win{ID: f.ID, PID: f.PID, App: f.AppID, Title: f.Name, X: f.Rect.X, Y: f.Rect.Y, W: f.Rect.Width, H: f.Rect.Height, Fullscreen: f.Full != 0}, nil
}

// ---------- capture ----------

func capture(w Win, full bool) (image.Image, image.Point, string, error) {
	if wayland() && os.Getenv("WAYLAND_DISPLAY") != "" && have("grim") && (hyprland() || os.Getenv("SWAYSOCK") != "" || !portalAvailable()) {
		args := []string{"-t", "jpeg", "-q", "90"}
		origin := image.Pt(0, 0)
		if !full && w.W > 0 && w.H > 0 {
			args = append(args, "-g", strconv.Itoa(w.X)+","+strconv.Itoa(w.Y)+" "+strconv.Itoa(w.W)+"x"+strconv.Itoa(w.H))
			origin = image.Pt(w.X, w.Y)
		}
		out, err := run(10*time.Second, "grim", append(args, "-")...)
		if err == nil {
			img, _, err := image.Decode(bytes.NewReader(out))
			if err == nil {
				return img, origin, "grim", nil
			}
		}
	}
	if wayland() {
		img, err := portalShot()
		if err == nil {
			return img, image.Pt(0, 0), "desktop portal", nil
		}
		if !x11() {
			return nil, image.Point{}, "desktop portal", err
		}
	}
	if x11() {
		img, origin, err := x11Capture(w, full)
		if err == nil {
			return img, origin, "X11", nil
		}
	}
	if have("import") && !wayland() {
		out, err := run(10*time.Second, "import", "-silent", "-window", "root", "jpeg:-")
		if err == nil {
			if img, _, err := image.Decode(bytes.NewReader(out)); err == nil {
				return img, image.Pt(0, 0), "ImageMagick import", nil
			}
		}
	}
	return nil, image.Point{}, "", errors.New("no way to take a screenshot here. On Hyprland or sway install grim; on GNOME or KDE make sure xdg-desktop-portal is running")
}

func portalAvailable() bool {
	c, err := dbus.ConnectSessionBus()
	if err != nil {
		return false
	}
	defer c.Close()
	var ok bool
	if c.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, "org.freedesktop.portal.Desktop").Store(&ok) == nil && ok {
		return true
	}
	var names []string
	_ = c.BusObject().Call("org.freedesktop.DBus.ListActivatableNames", 0).Store(&names)
	for _, n := range names {
		if n == "org.freedesktop.portal.Desktop" {
			return true
		}
	}
	return false
}

// portalShot asks xdg-desktop-portal for a screenshot (GNOME/KDE may ask the user once).
func portalShot() (image.Image, error) {
	c, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, err
	}
	defer c.Close()
	token := "blaze" + strconv.FormatInt(time.Now().UnixNano()%1e9, 10)
	sender := strings.ReplaceAll(strings.TrimPrefix(c.Names()[0], ":"), ".", "_")
	reqPath := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + sender + "/" + token)
	if err := c.AddMatchSignal(dbus.WithMatchObjectPath(reqPath), dbus.WithMatchInterface("org.freedesktop.portal.Request"), dbus.WithMatchMember("Response")); err != nil {
		return nil, err
	}
	ch := make(chan *dbus.Signal, 4)
	c.Signal(ch)
	obj := c.Object("org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop")
	call := obj.Call("org.freedesktop.portal.Screenshot.Screenshot", 0, "", map[string]dbus.Variant{
		"handle_token": dbus.MakeVariant(token), "interactive": dbus.MakeVariant(false), "modal": dbus.MakeVariant(false)})
	if call.Err != nil {
		return nil, errors.New("the desktop portal can't take screenshots: " + call.Err.Error())
	}
	timeout := time.After(35 * time.Second)
	for {
		select {
		case s := <-ch:
			if s == nil || s.Path != reqPath || len(s.Body) < 2 {
				continue
			}
			code, _ := s.Body[0].(uint32)
			if code != 0 {
				return nil, errors.New("screenshot permission was declined (allow Blaze in your desktop's privacy settings)")
			}
			res, _ := s.Body[1].(map[string]dbus.Variant)
			u, _ := res["uri"].Value().(string)
			pu, err := url.Parse(u)
			if err != nil || pu.Path == "" {
				return nil, errors.New("the portal returned no picture")
			}
			b, err := os.ReadFile(pu.Path)
			_ = os.Remove(pu.Path) // never leave copies of the user's screen in ~/Pictures
			if err != nil {
				return nil, err
			}
			img, _, err := image.Decode(bytes.NewReader(b))
			return img, err
		case <-timeout:
			return nil, errors.New("the screenshot permission prompt wasn't answered")
		}
	}
}

// ---------- idle ----------

func IdleSeconds() int {
	c, err := dbus.ConnectSessionBus()
	if err != nil {
		return -1
	}
	defer c.Close()
	var ms uint64
	if c.Object("org.gnome.Mutter.IdleMonitor", "/org/gnome/Mutter/IdleMonitor/Core").Call("org.gnome.Mutter.IdleMonitor.GetIdletime", 0).Store(&ms) == nil {
		return int(ms / 1000)
	}
	var sec uint32
	if c.Object("org.freedesktop.ScreenSaver", "/org/freedesktop/ScreenSaver").Call("org.freedesktop.ScreenSaver.GetSessionIdleTime", 0).Store(&sec) == nil {
		return int(sec)
	}
	return -1
}

// ---------- capabilities ----------

func Capabilities() Caps {
	c := Caps{TextVia: "AT-SPI"}
	if n, err := atspiApps(); err != nil {
		c.TextNote = "AT-SPI isn't reachable (" + err.Error() + "). Install at-spi2-core (Arch: sudo pacman -S at-spi2-core) and sign in again."
	} else if n == 0 {
		c.Text = true
		c.TextNote = "AT-SPI is on but no app has registered yet. Apps opened from now on can be read."
	} else {
		c.Text = true
		c.TextNote = strconv.Itoa(n) + " apps readable"
	}
	switch {
	case wayland() && (hyprland() || os.Getenv("SWAYSOCK") != ""):
		if have("grim") {
			c.Pixels, c.PixelsVia = true, "grim"
		} else {
			c.PixelsVia, c.PixelsNote = "grim", "Install grim for screenshots (Arch: sudo pacman -S grim)"
		}
	case wayland():
		if portalAvailable() {
			c.Pixels, c.PixelsVia = true, "desktop portal"
			c.PixelsNote = "Your desktop may ask once to allow screenshots"
		} else if have("grim") {
			c.Pixels, c.PixelsVia = true, "grim"
		} else {
			c.PixelsVia, c.PixelsNote = "desktop portal", "xdg-desktop-portal isn't running, so screenshots can't be taken"
		}
	case x11():
		c.Pixels, c.PixelsVia = true, "X11"
	case have("import"):
		c.Pixels, c.PixelsVia = true, "ImageMagick import"
	default:
		c.PixelsNote = "No screenshot method found"
	}
	return c
}

// AskScreenRecording is macOS only.
func AskScreenRecording() {}
