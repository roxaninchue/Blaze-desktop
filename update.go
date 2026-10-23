package main

// In-app updates from the Blaze downloads site (no GitHub).
//
// CI publishes every release as vX.Y.Z with one asset per platform, and the site serves
// them at <updateBase>/vX.Y.Z/<asset>, with <updateBase>/latest/ pointing at the newest
// release. The app reads latest/SHA256SUMS: the version comes from the asset names and
// the asset list is the file list. It compares that version with its own (stamped at
// build time) and installs the asset that matches how it was installed: setup.exe or the
// portable exe on Windows, the .dmg on macOS, and the .deb, Arch package or tar.gz on
// Linux. Nothing is installed until the download checks out against the release's
// signed SHA256SUMS (update_verify.go). No token is ever sent.

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"
)

// updateBase is where desktop releases are served. Override at build time with
// -ldflags "-X main.updateBase=https://example.com/dl/desktop".
var updateBase = "https://blazeinchue.pages.dev/dl/desktop"

// updatePage is the downloads page shown when an update has to be installed by hand.
var updatePage = "https://blazeinchue.pages.dev/download"

type UpdateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	Skipped   bool   `json:"skipped"`  // the user chose to skip this version
	Notes     string `json:"notes"`    // release notes
	Page      string `json:"page"`     // downloads page
	Asset     string `json:"asset"`    // file that will be installed
	Size      int64  `json:"size"`     // bytes
	Method    string `json:"method"`   // what Install will do, in words
	CanAuto   bool   `json:"can_auto"` // false: the user has to download it from Page
	Error     string `json:"error"`
}

type ghAsset struct {
	Name     string `json:"name"`
	Download string `json:"browser_download_url"`
	Size     int64  `json:"size"`
}

type ghRelease struct {
	Tag     string    `json:"tag_name"`
	Body    string    `json:"body"`
	HTMLURL string    `json:"html_url"`
	Draft   bool      `json:"draft"`
	Pre     bool      `json:"prerelease"`
	Assets  []ghAsset `json:"assets"`
}

var (
	updMu      sync.Mutex
	updLast    *ghRelease
	installing bool
)

// siteVersion pulls vX.Y.Z out of a release asset name (Blaze-v0.19.1-..., blaze-desktop_0.19.1_amd64.deb).
var siteVersion = regexp.MustCompile(`(?:^|[-_])v?([0-9]+\.[0-9]+\.[0-9]+)(?:[-_.]|$)`)

// releaseFromSums turns a release's SHA256SUMS into the release the rest of the updater
// works with: the tag from the asset names, and one asset per listed file plus the sums
// and their signature, all served from the versioned folder.
func releaseFromSums(base string, sums []byte) (*ghRelease, error) {
	rel := &ghRelease{}
	var names []string
	for _, ln := range strings.Split(string(sums), "\n") {
		f := strings.Fields(ln)
		if len(f) != 2 || len(f[0]) != 64 {
			continue
		}
		n := strings.TrimPrefix(f[1], "*")
		if n == "" || strings.ContainsAny(n, "/\\") {
			continue
		}
		names = append(names, n)
		if m := siteVersion.FindStringSubmatch(n); m != nil {
			v := "v" + m[1]
			if rel.Tag == "" || newer(v, rel.Tag) {
				rel.Tag = v
			}
		}
	}
	if rel.Tag == "" || len(names) == 0 {
		return nil, errors.New("no release listed")
	}
	dir := strings.TrimRight(base, "/") + "/" + rel.Tag + "/"
	for _, n := range append(names, "SHA256SUMS", "SHA256SUMS.sig") {
		rel.Assets = append(rel.Assets, ghAsset{Name: n, Download: dir + n})
	}
	rel.HTMLURL = updatePage
	rel.Body = "Blaze " + rel.Tag + " is ready. What's new is on the downloads page."
	return rel, nil
}

// CheckUpdate asks the downloads site for the latest release.
func (a *App) CheckUpdate() UpdateInfo {
	a.waitReady()
	info := UpdateInfo{Current: version}
	req, _ := http.NewRequest("GET", strings.TrimRight(updateBase, "/")+"/latest/SHA256SUMS", nil)
	req.Header.Set("User-Agent", "Blaze-Desktop/"+version)
	req.Header.Set("Cache-Control", "no-cache")
	res, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		info.Error = "Couldn't reach the Blaze downloads site. Check your connection."
		return info
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == 404:
		info.Error = "No release found yet. Check again later."
		return info
	case res.StatusCode == 429:
		info.Error = "Too many update checks. Try again in a while."
		return info
	case res.StatusCode != 200:
		info.Error = fmt.Sprintf("The downloads site answered %d.", res.StatusCode)
		return info
	}
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	rel, err := releaseFromSums(updateBase, body)
	if err != nil {
		info.Error = "The downloads site sent something unexpected."
		return info
	}
	updMu.Lock()
	updLast = rel
	updMu.Unlock()

	info.Latest, info.Notes, info.Page = rel.Tag, strings.TrimSpace(rel.Body), rel.HTMLURL
	info.Available = newer(rel.Tag, version)
	info.Skipped = info.Available && a.prefs.SkipUpdate == rel.Tag
	if as, method, ok := pickAsset(rel.Assets); ok {
		info.Asset, info.Size, info.Method, info.CanAuto = as.Name, as.Size, method, true
	} else {
		info.Method = "Download it from the downloads page."
	}
	// versions.json (published by the downloads site) says which version each kind of install is
	// on, since not every system gets every patch. Best effort: offline or odd answers change nothing.
	if v, notes, ok := fetchPlatformVersion(); ok {
		if !newer(v, version) {
			info.Available, info.Skipped = false, false
		}
		if info.Notes == "" {
			info.Notes = notes
		}
	}
	// A release with nothing for this system (e.g. a Windows-only patch) isn't an update here.
	if info.Available && !hasOSAsset(rel.Assets, updateGOOS) {
		info.Available, info.Skipped = false, false
	}
	if version == "dev" {
		info.Available, info.CanAuto = false, false
		info.Error = "This is a development build, so it doesn't update itself."
	}
	return info
}

// SetUpdateCheck turns the check on launch on or off.
func (a *App) SetUpdateCheck(on bool) { a.prefs.NoUpdateCheck = !on; a.savePrefs() }

// SkipUpdate hides the update card for this version until a newer one comes out.
func (a *App) SkipUpdate(tag string) { a.prefs.SkipUpdate = tag; a.savePrefs() }

// InstallUpdate downloads the latest release and installs it, then restarts Blaze.
// Progress goes to the UI as "update" events: {stage, pct, text}.
// It returns "" when Blaze is about to restart, or a message saying what went wrong.
func (a *App) InstallUpdate() string {
	a.waitReady()
	updMu.Lock()
	if installing {
		updMu.Unlock()
		return "An update is already installing."
	}
	installing = true
	rel := updLast
	updMu.Unlock()
	defer func() { updMu.Lock(); installing = false; updMu.Unlock() }()

	if rel == nil {
		if i := a.CheckUpdate(); i.Error != "" {
			return i.Error
		}
		updMu.Lock()
		rel = updLast
		updMu.Unlock()
	}
	if rel == nil || !newer(rel.Tag, version) {
		return "You're already on the latest version."
	}
	as, _, ok := pickAsset(rel.Assets)
	if !ok {
		return "There's no download for this computer in " + rel.Tag + ". Get it from the downloads page."
	}
	file, err := a.download(as)
	if err != nil {
		a.progress("error", 0, err.Error())
		return err.Error()
	}
	a.progress("verify", 100, "Checking the signature…")
	if err := a.verifyUpdate(rel, as, file); err != nil { // update_verify.go: nothing installs unless signed
		a.progress("error", 0, unsignedMsg)
		return err.Error()
	}
	a.progress("install", 100, "Installing "+rel.Tag+"…")
	restart, err := installFile(file)
	if err != nil {
		a.progress("error", 0, err.Error())
		return err.Error()
	}
	a.progress("restart", 100, "Restarting Blaze…")
	a.markResume() // running agents carry on after the restart (selfupdate.go)
	go func() {
		time.Sleep(600 * time.Millisecond)
		if restart != nil {
			_ = restart()
		}
		a.quit()
	}()
	return ""
}

func (a *App) progress(stage string, pct int, text string) {
	if a.ctx != nil {
		wr.EventsEmit(a.ctx, "update", map[string]any{"stage": stage, "pct": pct, "text": text})
	}
}

func (a *App) download(as ghAsset) (string, error) {
	dir := filepath.Join(os.TempDir(), "blaze-update")
	_ = os.MkdirAll(dir, 0o755)
	dst := filepath.Join(dir, filepath.Base(as.Name))

	req, _ := http.NewRequest("GET", as.Download, nil)
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", "Blaze-Desktop/"+version)
	res, err := (&http.Client{Timeout: 30 * time.Minute}).Do(req)
	if err != nil {
		return "", errors.New("The download failed. Check your connection and try again.")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", fmt.Errorf("The download failed (the downloads site answered %d).", res.StatusCode)
	}
	f, err := os.Create(dst + ".part")
	if err != nil {
		return "", errors.New("Couldn't save the download: " + err.Error())
	}
	total := as.Size
	if total <= 0 {
		total = res.ContentLength
	}
	var done int64
	last := -1
	buf := make([]byte, 64*1024)
	for {
		n, rerr := res.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return "", errors.New("Couldn't save the download: " + werr.Error())
			}
			done += int64(n)
			if total > 0 {
				if pct := int(done * 100 / total); pct != last {
					last = pct
					a.progress("download", pct, fmt.Sprintf("Downloading… %d%% of %.1f MB", pct, float64(total)/1e6))
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return "", errors.New("The download was interrupted. Try again.")
		}
	}
	f.Close()
	if as.Size > 0 && done != as.Size {
		return "", errors.New("The download was incomplete. Try again.")
	}
	_ = os.Remove(dst)
	if err := os.Rename(dst+".part", dst); err != nil {
		return "", err
	}
	return dst, nil
}

// ---------------- choosing and installing the right file ----------------

// updateGOOS / updateGOARCH / selfPath are variables so the update flow can be exercised end to
// end for another platform in tests (a Windows portable update on a Linux CI machine).
var (
	updateGOOS   = runtime.GOOS
	updateGOARCH = runtime.GOARCH
	selfPath     = selfPathOS
)

func selfPathOS() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p
}

// installKind says how this copy of Blaze was installed.
func installKind() string {
	exe := selfPath()
	switch updateGOOS {
	case "windows":
		dir := filepath.Dir(exe)
		if _, err := os.Stat(filepath.Join(dir, "uninstall.exe")); err == nil {
			return "setup"
		}
		if pf := os.Getenv("ProgramFiles"); pf != "" && strings.HasPrefix(strings.ToLower(exe), strings.ToLower(pf)) {
			return "setup"
		}
		return "portable"
	case "darwin":
		return "dmg"
	default:
		if strings.HasPrefix(exe, "/usr/") {
			if _, err := exec.LookPath("pacman"); err == nil {
				return "arch"
			}
			if _, err := exec.LookPath("dpkg"); err == nil {
				return "deb"
			}
		}
		return "tarball"
	}
}

func pickAsset(assets []ghAsset) (ghAsset, string, bool) {
	if updateGOOS != "darwin" && updateGOARCH != "amd64" {
		return ghAsset{}, "", false // releases are amd64-only for Windows and Linux
	}
	var want func(string) bool
	var method string
	switch installKind() {
	case "setup":
		want = func(n string) bool { return strings.HasSuffix(n, "-windows-amd64-setup.exe") }
		method = "Runs the new installer, then reopens Blaze."
	case "portable":
		want = func(n string) bool { return strings.HasSuffix(n, "-windows-amd64-portable.exe") }
		method = "Replaces this Blaze.exe, then reopens Blaze."
	case "dmg":
		want = func(n string) bool { return strings.HasSuffix(n, "-macos-universal.dmg") }
		method = "Replaces Blaze.app, then reopens Blaze."
	case "arch":
		want = func(n string) bool { return strings.HasSuffix(n, ".pkg.tar.zst") }
		method = "Installs the Arch package with pacman (asks for your password), then reopens Blaze."
	case "deb":
		want = func(n string) bool { return strings.HasSuffix(n, "_amd64.deb") }
		method = "Installs the .deb with apt (asks for your password), then reopens Blaze."
	default:
		want = func(n string) bool { return strings.HasSuffix(n, "-linux-amd64.tar.gz") }
		method = "Replaces this blaze-desktop binary, then reopens Blaze."
	}
	for _, as := range assets {
		if want(as.Name) {
			return as, method, true
		}
	}
	return ghAsset{}, "", false
}

// installFile puts the downloaded file in place and returns how to start the new copy.
func installFile(file string) (func() error, error) {
	exe := selfPath()
	switch kind := installKind(); kind {
	case "setup":
		// The running exe can't be overwritten, so a hidden helper waits for Blaze to
		// quit, runs the installer silently and starts the new Blaze.
		script := fmt.Sprintf(`ping -n 3 127.0.0.1 >nul & "%s" /S & start "" "%s" --relaunch`, file, exe)
		return func() error { return detached("cmd", "/C", script).Start() }, nil

	case "portable":
		// Windows lets a running exe be renamed, just not overwritten.
		old := exe + ".old"
		_ = os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			return nil, errors.New("Couldn't replace Blaze.exe: " + err.Error())
		}
		if err := copyFile(file, exe, 0o755); err != nil {
			_ = os.Rename(old, exe)
			return nil, errors.New("Couldn't write the new Blaze.exe: " + err.Error())
		}
		return func() error { return detached(exe, "--relaunch").Start() }, nil

	case "dmg":
		return installDMG(file, exe)

	case "arch", "deb":
		var args []string
		if kind == "arch" {
			args = []string{"pacman", "-U", "--noconfirm", file}
		} else {
			args = []string{"apt-get", "install", "-y", "--allow-downgrades", file}
		}
		if _, err := exec.LookPath("pkexec"); err != nil {
			return nil, errors.New("Couldn't ask for admin rights (pkexec is missing). Install " + filepath.Base(file) + " from " + filepath.Dir(file) + " yourself.")
		}
		out, err := exec.Command("pkexec", args...).CombinedOutput()
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok && (ee.ExitCode() == 126 || ee.ExitCode() == 127) {
				return nil, errors.New("The update was cancelled at the password prompt.")
			}
			return nil, errors.New("The package install failed: " + lastLine(string(out)))
		}
		return relaunchUnix(exe), nil

	default: // tarball
		if err := replaceFromTarball(file, exe); err != nil {
			return nil, err
		}
		return relaunchUnix(exe), nil
	}
}

func installDMG(dmg, exe string) (func() error, error) {
	// exe is …/Blaze.app/Contents/MacOS/Blaze
	app := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	if !strings.HasSuffix(app, ".app") {
		return nil, errors.New("Couldn't find Blaze.app. Open the downloaded .dmg in " + filepath.Dir(dmg) + " and drag Blaze to Applications.")
	}
	mnt, err := os.MkdirTemp("", "blaze-dmg")
	if err != nil {
		return nil, err
	}
	if out, err := exec.Command("hdiutil", "attach", "-nobrowse", "-readonly", "-mountpoint", mnt, dmg).CombinedOutput(); err != nil {
		return nil, errors.New("Couldn't open the .dmg: " + lastLine(string(out)))
	}
	defer exec.Command("hdiutil", "detach", "-quiet", mnt).Run()

	src := filepath.Join(mnt, "Blaze.app")
	next, old := app+".new", app+".old"
	_ = os.RemoveAll(next)
	_ = os.RemoveAll(old)
	if out, err := exec.Command("ditto", src, next).CombinedOutput(); err != nil {
		_ = exec.Command("open", dmg).Start()
		return nil, errors.New("Couldn't copy the new Blaze.app (" + lastLine(string(out)) + "). The .dmg is open: drag Blaze to Applications.")
	}
	_ = exec.Command("xattr", "-dr", "com.apple.quarantine", next).Run()
	if err := os.Rename(app, old); err != nil {
		_ = os.RemoveAll(next)
		return nil, errors.New("Couldn't replace Blaze.app: " + err.Error())
	}
	if err := os.Rename(next, app); err != nil {
		_ = os.Rename(old, app)
		return nil, errors.New("Couldn't replace Blaze.app: " + err.Error())
	}
	_ = os.RemoveAll(old)
	return func() error { return detached("sh", "-c", `sleep 1; open -n "$0" --args --relaunch`, app).Start() }, nil
}

func replaceFromTarball(tgz, exe string) error {
	f, err := os.Open(tgz)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return errors.New("The download is damaged. Try again.")
	}
	tr := tar.NewReader(gz)
	next := exe + ".new"
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return errors.New("The download has no blaze-desktop binary.")
		}
		if err != nil {
			return errors.New("The download is damaged. Try again.")
		}
		if filepath.Base(h.Name) != "blaze-desktop" {
			continue
		}
		out, err := os.OpenFile(next, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return errors.New("Couldn't write next to " + exe + ": " + err.Error())
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			_ = os.Remove(next)
			return err
		}
		out.Close()
		break
	}
	// Renaming over a running binary is fine on Linux and macOS.
	if err := os.Rename(next, exe); err != nil {
		_ = os.Remove(next)
		return errors.New("Couldn't replace " + exe + ": " + err.Error())
	}
	return nil
}

func relaunchUnix(exe string) func() error {
	return func() error { return detached("sh", "-c", `sleep 1; exec "$0" --relaunch`, exe).Start() }
}

// cleanupOldBinary removes what a portable Windows update left behind.
func cleanupOldBinary() {
	if exe := selfPath(); exe != "" {
		_ = os.Remove(exe + ".old")
	}
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// newer reports whether tag a (v1.2.3) is a higher version than b.
func newer(a, b string) bool {
	pa, okA := parseVer(a)
	pb, okB := parseVer(b)
	if !okA {
		return false
	}
	if !okB {
		return false // dev builds never update
	}
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parseVer(s string) ([3]int, bool) {
	var v [3]int
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// hasOSAsset says whether a release carries any file for this operating system.
func hasOSAsset(assets []ghAsset, goos string) bool {
	tag := map[string][]string{"windows": {"-windows-"}, "darwin": {"-macos-"}, "linux": {"-linux-", ".deb", ".pkg.tar.zst"}}[goos]
	for _, as := range assets {
		for _, t := range tag {
			if strings.Contains(as.Name, t) {
				return true
			}
		}
	}
	return false
}

// versionsURL is the downloads site's machine-readable list of current versions.
var versionsURL = "https://blazeinchue.pages.dev/versions.json"

// platformKey maps installKind() to the downloads site's platform names.
var platformKey = map[string]string{"portable": "windows", "setup": "windows-setup", "dmg": "mac", "deb": "linux-deb", "tarball": "linux-tar", "arch": "arch"}

func fetchPlatformVersion() (string, string, bool) {
	req, err := http.NewRequest("GET", versionsURL, nil)
	if err != nil {
		return "", "", false
	}
	req.Header.Set("User-Agent", "Blaze-Desktop/"+version)
	req.Header.Set("Cache-Control", "no-cache")
	res, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return "", "", false
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", "", false
	}
	body, _ := io.ReadAll(io.LimitReader(res.Body, 256<<10))
	return platformVersion(body, installKind())
}

// platformVersion reads desktop.platforms[<kind>].version (and desktop.notes) from versions.json.
func platformVersion(body []byte, kind string) (string, string, bool) {
	var v struct {
		Desktop struct {
			Notes     string `json:"notes"`
			Platforms map[string]struct {
				Version string `json:"version"`
			} `json:"platforms"`
		} `json:"desktop"`
	}
	if json.Unmarshal(body, &v) != nil {
		return "", "", false
	}
	p, ok := v.Desktop.Platforms[platformKey[kind]]
	if !ok || p.Version == "" {
		return "", "", false
	}
	return p.Version, strings.TrimSpace(v.Desktop.Notes), true
}
