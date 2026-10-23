package main

// Voice input (v0.5): local speech-to-text with whisper.cpp. Free, offline after setup,
// and it runs on the CPU of any PC.
//
//   engine  whisper-cli from whisper.cpp: already on PATH (brew, pacman, …) or downloaded
//           once from the official whisper.cpp release (Windows and Linux builds).
//   model   a ggml Whisper model from Hugging Face, downloaded once after the user agrees
//           (base, 142 MB, by default), kept in ~/.blaze/whisper.
//   audio   recorded by the window (WebView microphone) and sent here as a WAV, or, where
//           the WebView has no microphone (WebKitGTK), recorded here with pw-record,
//           parecord, arecord, sox or ffmpeg.

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/base64"
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
	"strings"
	"sync"
	"time"

	"blaze-desktop/internal/config"
)

type whisperModel struct {
	Name string `json:"name"`
	File string `json:"file"`
	MB   int    `json:"mb"`
	Note string `json:"note"`
}

var whisperModels = []whisperModel{
	{"tiny", "ggml-tiny.bin", 75, "fastest, rougher"},
	{"base", "ggml-base.bin", 142, "good on any PC (default)"},
	{"small", "ggml-small.bin", 466, "most accurate, slower on older PCs"},
}

const modelURL = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/"

func whisperDir() string {
	d := filepath.Join(config.Dir(), "whisper")
	_ = os.MkdirAll(d, 0o700)
	return d
}

func modelInfo(name string) whisperModel {
	for _, m := range whisperModels {
		if m.Name == name {
			return m
		}
	}
	return whisperModels[1]
}

func exe(n string) string {
	if runtime.GOOS == "windows" {
		return n + ".exe"
	}
	return n
}

// whisperBin finds whisper-cli: Blaze's own download first, then PATH.
func whisperBin() string {
	var found string
	_ = filepath.WalkDir(filepath.Join(whisperDir(), "bin"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && (d.Name() == exe("whisper-cli") || d.Name() == exe("whisper-cpp")) {
			found = p
			return filepath.SkipAll
		}
		return nil
	})
	if found != "" {
		return found
	}
	for _, n := range []string{"whisper-cli", "whisper-cpp", "whisper.cpp"} {
		if p := lookCmd(n); p != "" {
			return p
		}
	}
	return ""
}

// releaseAsset is the whisper.cpp release file for this OS/CPU ("" = none published).
func releaseAsset() string {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "windows/amd64":
		return "whisper-bin-x64.zip"
	case "windows/arm64":
		return "whisper-bin-win-cpu-arm64.zip"
	case "windows/386":
		return "whisper-bin-Win32.zip"
	case "linux/amd64":
		return "whisper-bin-ubuntu-x64.tar.gz"
	case "linux/arm64":
		return "whisper-bin-ubuntu-arm64.tar.gz"
	}
	return ""
}

// STTView is what the mic button and Settings show.
type STTView struct {
	Ready     bool           `json:"ready"`
	Engine    string         `json:"engine"`     // path of whisper-cli ("" = not set up)
	EngineHow string         `json:"engine_how"` // download | brew | manual
	EngineMB  int            `json:"engine_mb"`
	Model     string         `json:"model"`
	ModelMB   int            `json:"model_mb"`
	HaveModel bool           `json:"have_model"`
	Models    []whisperModel `json:"models"`
	Lang      string         `json:"lang"`
	Native    string         `json:"native"` // a recorder program for when the window has no microphone
	Busy      bool           `json:"busy"`   // a setup is running
	Note      string         `json:"note"`
}

var stt struct {
	sync.Mutex
	busy bool
	rec  *exec.Cmd
	wav  string
}

func (a *App) sttPrefs() ComposerPrefs { a.mu.Lock(); defer a.mu.Unlock(); return a.prefs.Composer }

// STTStatus says whether voice input is ready and what setting it up would download.
func (a *App) STTStatus() STTView {
	c := a.sttPrefs()
	m := modelInfo(c.STTModel)
	v := STTView{Engine: whisperBin(), Model: m.Name, ModelMB: m.MB, Models: whisperModels, Lang: c.STTLang, Native: nativeRecorder()}
	_, err := os.Stat(filepath.Join(whisperDir(), m.File))
	v.HaveModel = err == nil
	stt.Lock()
	v.Busy = stt.busy
	stt.Unlock()
	switch {
	case v.Engine != "":
		v.EngineHow = ""
	case releaseAsset() != "":
		v.EngineHow, v.EngineMB = "download", 10
	case runtime.GOOS == "darwin" && haveCmd("brew"):
		v.EngineHow = "brew"
		v.Note = "Blaze installs whisper.cpp with Homebrew (brew install whisper-cpp)."
	default:
		v.EngineHow = "manual"
		v.Note = manualNote()
	}
	v.Ready = v.Engine != "" && v.HaveModel
	return v
}

func manualNote() string {
	if runtime.GOOS == "darwin" {
		return "Voice input needs whisper.cpp. Install Homebrew (brew.sh), then run: brew install whisper-cpp"
	}
	return "Voice input needs whisper.cpp, which has no ready-made build for this PC. Install it with your package manager (whisper.cpp / whisper-cpp) and Blaze will find whisper-cli."
}

func haveCmd(n string) bool { return lookCmd(n) != "" }

// lookCmd finds a program on PATH, then where Homebrew and Linuxbrew put them
// (apps opened from the Dock or a launcher often get a short PATH).
func lookCmd(n string) string {
	if p, err := exec.LookPath(n); err == nil {
		return p
	}
	if runtime.GOOS == "windows" {
		return ""
	}
	for _, d := range []string{"/opt/homebrew/bin", "/usr/local/bin", "/home/linuxbrew/.linuxbrew/bin"} {
		p := filepath.Join(d, n)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// SetSTT saves the voice model and language.
func (a *App) SetSTT(model, lang string) STTView {
	a.mu.Lock()
	if model != "" {
		a.prefs.Composer.STTModel = modelInfo(model).Name
	}
	if lang = strings.TrimSpace(strings.ToLower(lang)); lang != "" {
		a.prefs.Composer.STTLang = lang
	}
	a.mu.Unlock()
	a.savePrefs()
	return a.STTStatus()
}

func (a *App) sttProgress(stage string, done, total int64, text string) {
	pct := -1
	if total > 0 {
		pct = int(done * 100 / total)
	}
	emit(a.ctx, "stt_progress", map[string]any{"stage": stage, "pct": pct, "text": text})
}

// STTSetup downloads what voice input needs (the user agreed in the app). Progress
// arrives as stt_progress events; the end as stt_ready or stt_error.
func (a *App) STTSetup() string {
	stt.Lock()
	if stt.busy {
		stt.Unlock()
		return "busy"
	}
	stt.busy = true
	stt.Unlock()
	go func() {
		defer func() { stt.Lock(); stt.busy = false; stt.Unlock() }()
		if err := a.sttSetup(); err != nil {
			emit(a.ctx, "stt_error", err.Error())
			return
		}
		emit(a.ctx, "stt_ready", a.STTStatus())
	}()
	return "ok"
}

func (a *App) sttSetup() error {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if whisperBin() == "" {
		switch {
		case releaseAsset() != "":
			if err := a.fetchEngine(ctx); err != nil {
				return err
			}
		case runtime.GOOS == "darwin" && haveCmd("brew"):
			a.sttProgress("engine", 0, 0, "Installing whisper.cpp with Homebrew… (a minute or two)")
			c := exec.CommandContext(ctx, lookCmd("brew"), "install", "whisper-cpp")
			if out, err := c.CombinedOutput(); err != nil {
				return fmt.Errorf("Homebrew couldn't install whisper.cpp: %s", sttLastLine(string(out)))
			}
		default:
			return errors.New(manualNote())
		}
		if whisperBin() == "" {
			return errors.New("whisper.cpp was installed but whisper-cli wasn't found. " + manualNote())
		}
	}
	m := modelInfo(a.sttPrefs().STTModel)
	dst := filepath.Join(whisperDir(), m.File)
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	return a.fetch(ctx, modelURL+m.File, dst, "model", fmt.Sprintf("Downloading the Whisper %s model", m.Name))
}

// fetchEngine downloads and unpacks the official whisper.cpp build for this PC.
func (a *App) fetchEngine(ctx context.Context) error {
	name := releaseAsset()
	a.sttProgress("engine", 0, 0, "Finding the latest whisper.cpp build…")
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/ggml-org/whisper.cpp/releases?per_page=15", nil)
	req.Header.Set("User-Agent", "Blaze-Desktop/"+version)
	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return errors.New("Couldn't reach GitHub to download whisper.cpp. Check your connection and try again.")
	}
	defer res.Body.Close()
	var rels []struct {
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(res.Body).Decode(&rels); err != nil {
		return fmt.Errorf("GitHub's answer about whisper.cpp couldn't be read (%d).", res.StatusCode)
	}
	url := ""
	for _, r := range rels {
		for _, as := range r.Assets {
			if as.Name == name {
				url = as.URL
				break
			}
		}
		if url != "" {
			break
		}
	}
	if url == "" {
		return errors.New("whisper.cpp has no ready-made build for this PC right now. " + manualNote())
	}
	arch := filepath.Join(whisperDir(), name)
	if err := a.fetch(ctx, url, arch, "engine", "Downloading whisper.cpp"); err != nil {
		return err
	}
	defer os.Remove(arch)
	bin := filepath.Join(whisperDir(), "bin")
	_ = os.RemoveAll(bin)
	if strings.HasSuffix(name, ".zip") {
		err = unzip(arch, bin)
	} else {
		err = untgz(arch, bin)
	}
	if err != nil {
		return errors.New("Couldn't unpack whisper.cpp: " + err.Error())
	}
	return nil
}

// fetch downloads url to dst with progress (via dst.part, so a cut download never counts).
func (a *App) fetch(ctx context.Context, url, dst, stage, what string) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "Blaze-Desktop/"+version)
	res, err := (&http.Client{Timeout: 60 * time.Minute}).Do(req)
	if err != nil {
		return errors.New(what + " failed. Check your connection and try again.")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("%s failed (the server answered %d).", what, res.StatusCode)
	}
	f, err := os.Create(dst + ".part")
	if err != nil {
		return err
	}
	total, done, last := res.ContentLength, int64(0), time.Time{}
	buf := make([]byte, 128*1024)
	for {
		n, rerr := res.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return werr
			}
			done += int64(n)
			if time.Since(last) > 250*time.Millisecond {
				last = time.Now()
				a.sttProgress(stage, done, total, fmt.Sprintf("%s… %.0f of %.0f MB", what, float64(done)/1e6, float64(max(total, done))/1e6))
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			_ = os.Remove(dst + ".part")
			return errors.New(what + " was interrupted. Try again.")
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if total > 0 && done != total {
		_ = os.Remove(dst + ".part")
		return errors.New(what + " was cut short. Try again.")
	}
	a.sttProgress(stage, done, done, what+"… done")
	return os.Rename(dst+".part", dst)
}

func safeJoin(dir, name string) (string, error) {
	p := filepath.Join(dir, filepath.FromSlash(name))
	if !strings.HasPrefix(p, filepath.Clean(dir)+string(filepath.Separator)) {
		return "", errors.New("bad path in archive: " + name)
	}
	return p, nil
}

func unzip(src, dir string) error {
	z, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer z.Close()
	for _, f := range z.File {
		p, err := safeJoin(dir, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(p, 0o755)
			continue
		}
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		r, err := f.Open()
		if err != nil {
			return err
		}
		w, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			r.Close()
			return err
		}
		_, err = io.Copy(w, r)
		r.Close()
		w.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func untgz(src, dir string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		p, err := safeJoin(dir, h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			_ = os.MkdirAll(p, 0o755)
		case tar.TypeSymlink:
			if strings.Contains(h.Linkname, "..") || filepath.IsAbs(h.Linkname) {
				continue
			}
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			_ = os.Remove(p)
			_ = os.Symlink(h.Linkname, p)
		case tar.TypeReg:
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			w, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode)|0o600)
			if err != nil {
				return err
			}
			_, err = io.Copy(w, tr)
			w.Close()
			if err != nil {
				return err
			}
		}
	}
}

// Transcript is the mic button's result.
type Transcript struct {
	Text  string  `json:"text"`
	Error string  `json:"error,omitempty"`
	Secs  float64 `json:"secs"`
}

// Transcribe turns a WAV recorded by the window (base64) into text.
func (a *App) Transcribe(wavB64 string) Transcript {
	b, err := base64.StdEncoding.DecodeString(wavB64)
	if err != nil || len(b) < 1000 {
		return Transcript{Error: "Nothing was recorded."}
	}
	f, err := os.CreateTemp("", "blaze-voice-*.wav")
	if err != nil {
		return Transcript{Error: err.Error()}
	}
	_, _ = f.Write(b)
	f.Close()
	defer os.Remove(f.Name())
	return a.transcribeFile(f.Name())
}

func (a *App) transcribeFile(wav string) Transcript {
	st := a.STTStatus()
	if !st.Ready {
		return Transcript{Error: "Voice input isn't set up yet."}
	}
	started := time.Now()
	threads := runtime.NumCPU() / 2
	threads = max(1, min(threads, 6)) // leave the rest of the PC responsive
	args := []string{"-m", filepath.Join(whisperDir(), modelInfo(st.Model).File), "-f", wav, "-nt", "-np", "-t", fmt.Sprint(threads)}
	lang := st.Lang
	if lang == "" {
		lang = "auto"
	}
	args = append(args, "-l", lang)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	c := quiet(exec.CommandContext(ctx, st.Engine, args...))
	c.Dir = filepath.Dir(st.Engine)
	out, err := c.Output()
	if err != nil {
		msg := err.Error()
		if ee, ok := err.(*exec.ExitError); ok {
			msg = sttLastLine(string(ee.Stderr))
		}
		return Transcript{Error: "whisper.cpp couldn't transcribe: " + msg}
	}
	return Transcript{Text: cleanTranscript(string(out)), Secs: time.Since(started).Seconds()}
}

var noiseRe = regexp.MustCompile(`(?i)\[(blank_audio|music|silence|no speech|inaudible)[^\]]*\]|\((music|silence|eerie music|wind|static|applause|laughs?|coughs?)[^)]*\)|\[\s*_?[A-Z_]+_?\s*\]`)

// cleanTranscript drops whisper's non-speech tags and joins the lines.
func cleanTranscript(s string) string {
	s = noiseRe.ReplaceAllString(s, " ")
	var parts []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			parts = append(parts, l)
		}
	}
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}

func sttLastLine(s string) string {
	ls := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(ls) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(ls[i]); t != "" {
			if len(t) > 200 {
				t = t[:200]
			}
			return t
		}
	}
	return "no details"
}

// ---------------- recording without the WebView ----------------

// nativeRecorder is a program that can record the microphone here ("" = none).
func nativeRecorder() string {
	if runtime.GOOS == "windows" {
		return "" // WebView2 records the microphone itself
	}
	cands := []string{"pw-record", "parecord", "arecord", "rec", "ffmpeg"}
	if runtime.GOOS == "darwin" {
		cands = []string{"rec", "ffmpeg"}
	}
	for _, n := range cands {
		if haveCmd(n) {
			return n
		}
	}
	return ""
}

func recordArgs(prog, wav string) []string {
	switch prog {
	case "pw-record":
		return []string{"--rate", "16000", "--channels", "1", "--format", "s16", wav}
	case "parecord":
		return []string{"--rate=16000", "--channels=1", "--format=s16le", "--file-format=wav", wav}
	case "arecord":
		return []string{"-q", "-f", "S16_LE", "-r", "16000", "-c", "1", "-t", "wav", wav}
	case "rec":
		return []string{"-q", "-r", "16000", "-c", "1", "-b", "16", wav}
	case "ffmpeg":
		in := []string{"-f", "pulse", "-i", "default"}
		if runtime.GOOS == "darwin" {
			in = []string{"-f", "avfoundation", "-i", ":0"}
		}
		return append(append([]string{"-hide_banner", "-loglevel", "error", "-y"}, in...), "-ar", "16000", "-ac", "1", "-t", "180", wav)
	}
	return nil
}

// RecStart records the microphone with a system program (for windows without a WebView microphone).
func (a *App) RecStart() string {
	prog := nativeRecorder()
	if prog == "" {
		return "No microphone recorder was found on this PC (pw-record, parecord, arecord, sox or ffmpeg)."
	}
	stt.Lock()
	defer stt.Unlock()
	if stt.rec != nil {
		return ""
	}
	// CreateTemp: a 0600 file with an unguessable name in the shared temp dir; the
	// recorder truncates it in place and keeps the mode, so the voice stays private.
	tf, err := os.CreateTemp("", "blaze-voice-*.wav")
	if err != nil {
		return "Couldn't start the microphone: " + err.Error()
	}
	wav := tf.Name()
	tf.Close()
	c := quiet(exec.Command(lookCmd(prog), recordArgs(prog, wav)...))
	if err := c.Start(); err != nil {
		_ = os.Remove(wav)
		return "Couldn't start the microphone: " + err.Error()
	}
	stt.rec, stt.wav = c, wav
	go func() { // never record forever
		time.Sleep(3 * time.Minute)
		stt.Lock()
		same := stt.rec == c
		stt.Unlock()
		if same {
			emit(a.ctx, "rec_limit", nil)
		}
	}()
	return ""
}

// RecStop ends a RecStart recording and transcribes it (cancel = throw it away).
func (a *App) RecStop(cancel bool) Transcript {
	stt.Lock()
	c, wav := stt.rec, stt.wav
	stt.rec, stt.wav = nil, ""
	stt.Unlock()
	if c == nil {
		return Transcript{Error: "Nothing was recording."}
	}
	stopGently(c)
	defer os.Remove(wav)
	if cancel {
		return Transcript{}
	}
	if st, err := os.Stat(wav); err != nil || st.Size() < 2000 {
		return Transcript{Error: "Nothing was recorded. Check that a microphone is connected and allowed."}
	}
	return a.transcribeFile(wav)
}

func stopGently(c *exec.Cmd) {
	done := make(chan struct{})
	go func() { _ = c.Wait(); close(done) }()
	interrupt(c)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = c.Process.Kill()
		<-done
	}
}
