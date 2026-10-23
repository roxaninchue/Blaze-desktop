package media

// The download queue (mobile DownloadLogic.kt + Downloads.kt): one at a time, in
// order, saved in ~/.blaze/media/downloads.json so it survives a restart. Each stream
// is fetched in 8 MB HTTP ranges into a .part file, so a dropped connection resumes
// where it stopped. Expired or refused urls (403/410) are fetched again from the
// source; network errors retry with backoff (2 s … 5 min, up to 8 tries, reset when
// bytes arrive). Pause keeps the .part files; Cancel deletes them. Finished files go
// to ~/Music/Blaze/Music and ~/Videos/Blaze/Videos (~/Movies on macOS). Video + audio
// streams are merged and DASH audio remuxed with ffmpeg when it's installed
// (mobile used Android's MediaMuxer); without it the stream is kept as sent.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type DlStatus string

const (
	DlQueued   DlStatus = "QUEUED"
	DlRunning  DlStatus = "RUNNING"
	DlPaused   DlStatus = "PAUSED"
	DlFailed   DlStatus = "FAILED"
	DlDone     DlStatus = "DONE"
	DlCanceled DlStatus = "CANCELED"
)

// DlPart is one stream of a job.
type DlPart struct {
	Key       string `json:"key"`
	URL       string `json:"url"`
	Total     int64  `json:"total"`
	Done      int64  `json:"done"`
	Container string `json:"c"`
}

// DlJob is one download (mobile's JSON keys).
type DlJob struct {
	ID          string   `json:"id"`
	VideoID     string   `json:"vid"`
	Kind        string   `json:"kind"`
	Title       string   `json:"title"`
	Channel     string   `json:"ch"`
	DurationSec int      `json:"dur"`
	Thumb       string   `json:"thumb"`
	Quality     string   `json:"q"`
	ChoiceID    string   `json:"cid"`
	Ext         string   `json:"ext"`
	Parts       []DlPart `json:"parts"`
	Status      DlStatus `json:"st"`
	Attempts    int      `json:"att"`
	Error       string   `json:"err"`
	NextTryAt   int64    `json:"next"`
	CreatedAt   int64    `json:"at"`
	Out         string   `json:"out"`
	Source      string   `json:"src"`
	Licence     string   `json:"lic"`
	FileTitle   string   `json:"ft"`
	ProgressPct int      `json:"pct"` // for the UI
}

func (j DlJob) Total() int64 {
	var n int64
	for _, p := range j.Parts {
		n += p.Total
	}
	return n
}
func (j DlJob) DoneBytes() int64 {
	var n int64
	for _, p := range j.Parts {
		d := p.Done
		if p.Total > 0 && d > p.Total {
			d = p.Total
		}
		n += d
	}
	return n
}
func (j DlJob) Progress() float64 {
	t := j.Total()
	if t <= 0 {
		return 0
	}
	f := float64(j.DoneBytes()) / float64(t)
	if f > 1 {
		f = 1
	}
	return f
}
func (j DlJob) Active() bool { return j.Status == DlQueued || j.Status == DlRunning }

const (
	DlMaxAttempts = 8
	DlChunk       = int64(8 << 20)
	DlSpaceMargin = int64(50 << 20)
)

// Backoff: 2 s, 4 s, 8 s … capped at 5 min.
func Backoff(attempt int) int64 {
	v := int64(2000) << clamp(attempt, 0, 20)
	if v > 300000 {
		v = 300000
	}
	return v
}

// DlOnError: transient errors retry with backoff until the cap; others fail now.
func DlOnError(j DlJob, msg string, transient bool, now int64) DlJob {
	a := j.Attempts + 1
	j.Attempts, j.Error = a, msg
	if transient && a < DlMaxAttempts {
		j.Status, j.NextTryAt = DlQueued, now+Backoff(a-1)
	} else {
		j.Status, j.NextTryAt = DlFailed, 0
	}
	return j
}

// DlOnProgress: bytes written reset the attempt count.
func DlOnProgress(j DlJob, part int, done, total int64) DlJob {
	ps := append([]DlPart{}, j.Parts...)
	if part >= 0 && part < len(ps) {
		ps[part].Done = done
		if total >= 0 {
			ps[part].Total = total
		}
	}
	j.Parts, j.Attempts, j.Error, j.Status = ps, 0, "", DlRunning
	return j
}

// DlRange is the Range header for the next chunk.
func DlRange(done, total, chunk int64) string {
	end := done + chunk - 1
	if total > 0 && total-1 < end {
		end = total - 1
	}
	return fmt.Sprintf("bytes=%d-%d", done, end)
}

var reExpire = regexp.MustCompile(`[?&]expire=(\d+)`)

// ExpiryOf reads expire=<unix s> from a stream url (0 when absent).
func ExpiryOf(u string) int64 {
	if m := reExpire.FindStringSubmatch(u); m != nil {
		v, _ := strconv.ParseInt(m[1], 10, 64)
		return v
	}
	return 0
}

// URLExpired: about to expire (2 min margin) or has.
func URLExpired(expireSec, nowMs int64) bool { return expireSec > 0 && nowMs/1000+120 >= expireSec }

// TotalFromContentRange: "bytes 100-199/5000" → 5000; -1 when unknown.
func TotalFromContentRange(v string) int64 {
	if v == "" {
		return -1
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v[strings.LastIndex(v, "/")+1:]), 10, 64)
	if err != nil {
		return -1
	}
	return n
}

type resume int

const (
	rsAppend resume = iota
	rsRestart
	rsComplete
	rsRetry
	rsFail
)

func onStatus(code int) resume {
	switch {
	case code == 206:
		return rsAppend
	case code == 200:
		return rsRestart
	case code == 416:
		return rsComplete
	case code == 403 || code == 410 || code == 408 || code == 429 || (code >= 500 && code <= 599):
		return rsRetry
	}
	return rsFail
}

// DlNext is the oldest queued job whose backoff has passed (RUNNING from a killed process counts).
func DlNext(jobs []DlJob, now int64) *DlJob {
	var best *DlJob
	for i := range jobs {
		j := &jobs[i]
		if (j.Status == DlQueued || j.Status == DlRunning) && j.NextTryAt <= now && (best == nil || j.CreatedAt < best.CreatedAt) {
			best = j
		}
	}
	return best
}

// DlNextWake is the earliest backoff end (0 = nothing waiting).
func DlNextWake(jobs []DlJob, now int64) int64 {
	var w int64
	for _, j := range jobs {
		if j.Status == DlQueued && j.NextTryAt > now && (w == 0 || j.NextTryAt < w) {
			w = j.NextTryAt
		}
	}
	return w
}

// SpaceNeeded is the rest of every part plus a full copy for merging, plus a margin.
func SpaceNeeded(j DlJob) int64 {
	var left int64
	for _, p := range j.Parts {
		if d := p.Total - p.Done; d > 0 {
			left += d
		}
	}
	return left + j.Total() + DlSpaceMargin
}

func DlPause(j DlJob) DlJob {
	if j.Active() {
		j.Status, j.NextTryAt = DlPaused, 0
	}
	return j
}

func DlResume(j DlJob) DlJob {
	if j.Status == DlPaused || j.Status == DlFailed {
		j.Status, j.Attempts, j.Error, j.NextTryAt = DlQueued, 0, "", 0
	}
	return j
}

var reBadName = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]`)

// SafeFileName: "AC/DC: Back in Black" → "AC-DC- Back in Black.mp3".
func SafeFileName(title, ext string) string {
	b := strings.Trim(strings.TrimSpace(spaces.ReplaceAllString(reBadName.ReplaceAllString(title, "-"), " ")), ".")
	if r := []rune(b); len(r) > 80 {
		b = string(r[:80])
	}
	if b == "" {
		b = "Blaze"
	}
	return b + "." + ext
}

// ---------------------------------------------------------------- the worker

type downloader struct {
	m    *Media
	mu   sync.Mutex
	jobs []DlJob
	kick chan struct{}
	once sync.Once
	stop context.CancelFunc
	ctx  context.Context
}

func newDownloader(m *Media) *downloader {
	d := &downloader{m: m, kick: make(chan struct{}, 1)}
	m.readJSON("downloads.json", &d.jobs)
	d.ctx, d.stop = context.WithCancel(context.Background())
	return d
}

func (d *downloader) workDir() string {
	p := filepath.Join(d.m.dir, "dl")
	_ = os.MkdirAll(p, 0o755)
	return p
}

func (d *downloader) partFile(id string, i int) string {
	return filepath.Join(d.workDir(), id+"."+itoa(i)+".part")
}

func (d *downloader) clean(id string) {
	ents, _ := os.ReadDir(d.workDir())
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), id+".") {
			_ = os.Remove(filepath.Join(d.workDir(), e.Name()))
		}
	}
}

func (d *downloader) saveLocked() {
	for i := range d.jobs {
		d.jobs[i].ProgressPct = int(d.jobs[i].Progress() * 100)
	}
	d.m.writeJSON("downloads.json", d.jobs)
}

func (d *downloader) list() []DlJob {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := append([]DlJob{}, d.jobs...)
	for i := range out {
		out[i].ProgressPct = int(out[i].Progress() * 100)
	}
	return out
}

func (d *downloader) get(id string) (DlJob, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, j := range d.jobs {
		if j.ID == id {
			return j, true
		}
	}
	return DlJob{}, false
}

func (d *downloader) update(id string, fn func(DlJob) DlJob) (DlJob, bool) {
	d.mu.Lock()
	var out DlJob
	ok := false
	for i, j := range d.jobs {
		if j.ID == id {
			d.jobs[i] = fn(j)
			out, ok = d.jobs[i], true
		}
	}
	d.saveLocked()
	d.mu.Unlock()
	if ok {
		d.m.emit("media:downloads", nil)
	}
	return out, ok
}

func (d *downloader) start() {
	d.once.Do(func() { go d.loop() })
	select {
	case d.kick <- struct{}{}:
	default:
	}
}

func (d *downloader) loop() {
	for {
		now := d.m.now()
		d.mu.Lock()
		next := DlNext(d.jobs, now)
		var job DlJob
		if next != nil {
			job = *next
		}
		wake := DlNextWake(d.jobs, now)
		d.mu.Unlock()
		if next != nil {
			d.run(job)
			continue
		}
		wait := time.Hour
		if wake > 0 {
			wait = time.Duration(wake-now) * time.Millisecond
		}
		select {
		case <-d.kick:
		case <-time.After(wait):
		case <-d.ctx.Done():
			return
		}
	}
}

type errStop struct{}

func (errStop) Error() string { return "stopped" }

type errFatal struct{ msg string }

func (e errFatal) Error() string { return e.msg }

type errTransient struct{ msg string }

func (e errTransient) Error() string { return e.msg }

func (d *downloader) run(j0 DlJob) {
	job, ok := d.update(j0.ID, func(j DlJob) DlJob { j.Status = DlRunning; return j })
	if !ok {
		return
	}
	err := func() error {
		if free := freeBytes(d.workDir()); free > 0 && free < SpaceNeeded(job) {
			return errFatal{"Not enough space: needs " + SizeText(SpaceNeeded(job)) + " free"}
		}
		refreshed := 0
		for i := range job.Parts {
			for {
				cur, ok := d.get(job.ID)
				if !ok || cur.Status != DlRunning {
					return errStop{}
				}
				job = cur
				p := job.Parts[i]
				if p.Total > 0 && p.Done >= p.Total {
					break
				}
				if URLExpired(ExpiryOf(p.URL), d.m.now()) {
					var err error
					if job, err = d.refresh(job); err != nil {
						return err
					}
				}
				r, err := d.chunk(job, i)
				if err != nil {
					return err
				}
				if r == chunkDone {
					break
				}
				if r == chunkExpired {
					refreshed++
					if refreshed > 3 {
						return errTransient{"The server refused the download"}
					}
					if job, err = d.refresh(job); err != nil {
						return err
					}
				}
			}
		}
		return d.finish(job)
	}()
	if err == nil {
		return
	}
	var stop errStop
	var fatal errFatal
	switch {
	case errors.As(err, &stop):
		if j, ok := d.get(j0.ID); ok && j.Status == DlCanceled {
			d.clean(j0.ID)
		}
	case errors.As(err, &fatal):
		j, _ := d.update(j0.ID, func(j DlJob) DlJob { return DlOnError(j, fatal.msg, false, d.m.now()) })
		d.notify(j.Title, "Download failed: "+fatal.msg)
	default:
		var tr errTransient
		var ne net.Error
		var se *StoreError
		transient := errors.As(err, &tr) || errors.As(err, &ne) || errors.As(err, &se) || errors.Is(err, io.ErrUnexpectedEOF)
		j, _ := d.update(j0.ID, func(j DlJob) DlJob {
			if j.Status != DlRunning {
				return j
			}
			return DlOnError(j, err.Error(), transient, d.m.now())
		})
		if j.Status == DlFailed {
			d.notify(j.Title, "Download failed: "+err.Error())
		}
	}
}

func (d *downloader) notify(title, body string) {
	if d.m.Notify != nil {
		d.m.Notify(title, body)
	}
}

type chunkResult int

const (
	chunkMore chunkResult = iota
	chunkDone
	chunkExpired
)

func (d *downloader) chunk(job DlJob, i int) (chunkResult, error) {
	p := job.Parts[i]
	file := d.partFile(job.ID, i)
	var have int64
	if st, err := os.Stat(file); err == nil {
		have = st.Size()
	}
	if p.Total > 0 && have > p.Total {
		_ = os.Remove(file)
		have = 0
	}
	ctx, cancel := context.WithTimeout(d.ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", p.URL, nil)
	if err != nil {
		return 0, errFatal{err.Error()}
	}
	ua := blazeUA
	if s := d.m.Source(job.Source); s != nil {
		ua = s.UserAgent()
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Range", DlRange(have, p.Total, DlChunk))
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return 0, errTransient{err.Error()}
	}
	defer resp.Body.Close()
	code := resp.StatusCode
	switch onStatus(code) {
	case rsComplete:
		d.update(job.ID, func(j DlJob) DlJob { return DlOnProgress(j, i, have, have) })
		return chunkDone, nil
	case rsRetry:
		if code == 403 || code == 410 {
			return chunkExpired, nil
		}
		return 0, errTransient{fmt.Sprintf("Server busy (%d)", code)}
	case rsFail:
		return 0, errFatal{fmt.Sprintf("The server answered %d", code)}
	case rsRestart:
		have = 0
	}
	total := TotalFromContentRange(resp.Header.Get("Content-Range"))
	if total <= 0 {
		total = p.Total
		if code == 200 && resp.ContentLength > 0 {
			total = resp.ContentLength
		}
	}
	flags := os.O_CREATE | os.O_WRONLY
	if have > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(file, flags, 0o644)
	if err != nil {
		return 0, errFatal{err.Error()}
	}
	buf := make([]byte, 64<<10)
	var since int64
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return 0, errFatal{werr.Error()}
			}
			have += int64(n)
			since += int64(n)
			if since >= 512<<10 {
				since = 0
				j, ok := d.update(job.ID, func(j DlJob) DlJob { return DlOnProgress(j, i, have, total) })
				if !ok || j.Status != DlRunning {
					f.Close()
					return 0, errStop{}
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			d.update(job.ID, func(j DlJob) DlJob { return DlOnProgress(j, i, have, total) })
			return 0, errTransient{rerr.Error()}
		}
	}
	f.Close()
	j, ok := d.update(job.ID, func(j DlJob) DlJob { return DlOnProgress(j, i, have, total) })
	if !ok || j.Status != DlRunning {
		return 0, errStop{}
	}
	if total > 0 && have >= total {
		return chunkDone, nil
	}
	if total <= 0 && code == 200 {
		d.update(job.ID, func(j DlJob) DlJob { return DlOnProgress(j, i, have, have) })
		return chunkDone, nil
	}
	return chunkMore, nil
}

func (d *downloader) refresh(job DlJob) (DlJob, error) {
	src := d.m.Source(job.Source)
	if src == nil {
		return job, errFatal{"This store isn't available in this build"}
	}
	fresh, err := src.Refresh(job.VideoID, job.Kind)
	if err != nil {
		return job, errTransient{err.Error()}
	}
	if fresh == nil {
		return job, errFatal{"The server refused the download"}
	}
	missing := false
	j, ok := d.update(job.ID, func(j DlJob) DlJob {
		ps := append([]DlPart{}, j.Parts...)
		for k, p := range ps {
			found := false
			for _, f := range fresh.Formats {
				if f.Key == p.Key {
					ps[k].URL = f.URL
					if ps[k].Total <= 0 {
						ps[k].Total = f.ContentLength
					}
					found = true
				}
			}
			if !found {
				missing = true
			}
		}
		j.Parts = ps
		return j
	})
	if missing {
		return j, errFatal{"The source no longer offers this quality"}
	}
	if !ok {
		return j, errStop{}
	}
	return j, nil
}

func (d *downloader) ffmpeg(args ...string) error {
	if d.m.ffmpeg == "" {
		return errors.New("ffmpeg isn't installed")
	}
	ctx, cancel := context.WithTimeout(d.ctx, 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, d.m.ffmpeg, append([]string{"-y", "-hide_banner", "-loglevel", "error"}, args...)...)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (d *downloader) finish(job DlJob) error {
	parts := make([]string, len(job.Parts))
	for i := range job.Parts {
		parts[i] = d.partFile(job.ID, i)
	}
	ext := job.Ext
	src := parts[0]
	out := filepath.Join(d.workDir(), job.ID+".out."+ext)
	switch {
	case len(parts) == 2:
		if err := d.ffmpeg("-i", parts[0], "-i", parts[1], "-map", "0:v:0", "-map", "1:a:0", "-c", "copy", out); err != nil {
			return errFatal{"Couldn't merge the video and audio (" + err.Error() + ")"}
		}
		src = out
	case job.Kind == "audio" && (job.Parts[0].Container == "mp4" || job.Parts[0].Container == "webm"):
		if err := d.ffmpeg("-i", parts[0], "-vn", "-c", "copy", out); err == nil {
			src = out
		} else {
			// Kept as YouTube sent it (the player still plays it).
			ext = job.Parts[0].Container
			if ext == "mp4" {
				ext = "m4a"
			}
		}
	}
	dir := d.m.MusicDir()
	if job.Kind == "video" {
		dir = d.m.VideoDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errFatal{err.Error()}
	}
	dest := filepath.Join(dir, SafeFileName(job.Title, ext))
	for n := 2; ; n++ {
		if _, err := os.Stat(dest); os.IsNotExist(err) {
			break
		}
		dest = filepath.Join(dir, SafeFileName(job.Title+" ("+itoa(n)+")", ext))
	}
	if err := moveFile(src, dest); err != nil {
		return errFatal{err.Error()}
	}
	art := d.saveArt(job)
	var size, mt int64
	if st, err := os.Stat(dest); err == nil {
		size, mt = st.Size(), st.ModTime().UnixMilli()
	}
	d.m.AddDownload(Track{Key: FileKey(dest), Path: dest, Title: job.Title, Artist: job.Channel, DurationMs: int64(job.DurationSec) * 1000, Art: art,
		Source: "download", AddedAt: d.m.now(), VideoID: job.VideoID, Kind: job.Kind, Licence: job.Licence, StoreSource: job.Source, Size: size, MTime: mt})
	d.update(job.ID, func(j DlJob) DlJob { j.Status, j.Out, j.Error = DlDone, dest, ""; return j })
	d.clean(job.ID)
	where := "Saved to Music"
	if job.Kind == "video" {
		where = "Saved to Videos"
	}
	d.notify(job.Title, where)
	return nil
}

func moveFile(src, dest string) error {
	if err := os.Rename(src, dest); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dest)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	in.Close()
	return os.Remove(src)
}

func (d *downloader) saveArt(job DlJob) string {
	if !strings.HasPrefix(job.Thumb, "http") {
		return job.Thumb
	}
	f := filepath.Join(d.m.dir, "art", reSafe.ReplaceAllString(job.Source+"_"+job.VideoID, "_")+".jpg")
	if _, err := os.Stat(f); err == nil {
		return f
	}
	if downloadTo(strings.Replace(job.Thumb, "/mqdefault.jpg", "/hqdefault.jpg", 1), f) != nil {
		return job.Thumb
	}
	return f
}

// ---------------------------------------------------------------- Media API

// Downloads lists the queue.
func (m *Media) Downloads() []DlJob { return m.dl.list() }

// StartDownloads resumes anything left queued (call once at app start).
func (m *Media) StartDownloads() {
	for _, j := range m.dl.list() {
		if j.Active() {
			m.dl.start()
			return
		}
	}
}

// AddDownloadJob queues choice of d (from item's source) and starts the queue.
func (m *Media) AddDownloadJob(it Item, d Detail, ch Choice, kind string) DlJob {
	var parts []DlPart
	for _, f := range formatsFor(d, ch.Keys) {
		parts = append(parts, DlPart{Key: f.Key, URL: f.URL, Total: f.ContentLength, Container: f.Container()})
	}
	now := m.now()
	dur := d.LengthSec
	if ch.Title != "" {
		dur = 0
	} else if dur <= 0 {
		dur = it.DurationSec
	}
	job := DlJob{ID: "dl" + strconv.FormatInt(now, 36) + itoa(100+rand.Intn(900)), VideoID: orStr(d.ID, it.ID), Kind: kind,
		Title: orStr(ch.Title, orStr(d.Title, it.Title)), Channel: orStr(d.Author, it.Author), DurationSec: dur, Thumb: orStr(it.Thumb, d.Thumb),
		Quality: ch.Label, ChoiceID: ch.ID, Ext: ch.Ext, Parts: parts, Status: DlQueued, CreatedAt: now, Source: it.Source,
		Licence: orStr(d.Licence, it.Licence), FileTitle: ch.Title}
	m.dl.mu.Lock()
	m.dl.jobs = append(m.dl.jobs, job)
	m.dl.saveLocked()
	m.dl.mu.Unlock()
	m.NoteTaste(kind, job.Title, job.Channel, TasteDownload)
	m.emit("media:downloads", nil)
	m.dl.start()
	return job
}

func (m *Media) PauseDownload(id string) { m.dl.update(id, DlPause) }
func (m *Media) ResumeDownload(id string) {
	m.dl.update(id, DlResume)
	m.dl.start()
}

// CancelDownload stops a job and deletes its partial files.
func (m *Media) CancelDownload(id string) {
	j, _ := m.dl.update(id, func(j DlJob) DlJob {
		if j.Status != DlDone {
			j.Status = DlCanceled
		}
		return j
	})
	if j.Status == DlCanceled {
		m.dl.clean(id)
	}
}

// DismissDownload takes a job off the list (a finished file stays in the library).
func (m *Media) DismissDownload(id string) {
	if j, ok := m.dl.get(id); ok && j.Active() {
		m.CancelDownload(id)
	}
	m.dl.clean(id)
	m.dl.mu.Lock()
	keep := []DlJob{}
	for _, j := range m.dl.jobs {
		if j.ID != id {
			keep = append(keep, j)
		}
	}
	m.dl.jobs = keep
	m.dl.saveLocked()
	m.dl.mu.Unlock()
	m.emit("media:downloads", nil)
}

// Close stops the queue (the app is quitting).
func (m *Media) Close() { m.dl.stop() }
