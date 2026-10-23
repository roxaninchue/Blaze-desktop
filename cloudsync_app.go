package main

// Sync across devices (Settings › Account): memory and chat messages follow the Blaze account
// to Blaze on the phone, other computers and blazeinchue.pages.dev/ai, end-to-end encrypted with
// a passphrase only the user knows (internal/cloudsync: PBKDF2-SHA256 600k, AES-256-GCM over
// gzip, AAD "blaze-sync-v1|kind|id", the same envelope as Blaze Mobile and /ai-sync.js).
//
//   - The passphrase is never stored or sent. The key derived from it is kept in the OS
//     keychain under syncKeyAcct + the account id.
//   - A round runs: when sync is switched on, ~20 s after start, every syncEvery, and shortly
//     after memory (syncMemDebounce) or a chat file (syncChatDebounce) changes. One round at a
//     time, and a lock file shared with Blaze CLI (~/.blaze/cloudsync/lock).
//   - D1 budget (gateway free plan, 100k row writes/day for everyone): every chat's newest
//     syncWindow messages sync at once; older ones are backfilled newest first, at most
//     syncBackfill pushes in a round and one backfill round per syncBackfillGap (Sync now
//     backfills at once).
//   - Chats are written to their files while no turn runs in them; a session open on screen is
//     reloaded from its file afterwards, so the app's next save keeps what arrived.
//   - The status line says what really happened (last good sync, waiting, the error).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"blaze-desktop/internal/cloudai"
	"blaze-desktop/internal/cloudsync"
	"blaze-desktop/internal/config"
	"blaze-desktop/internal/llm"
	"blaze-desktop/internal/store"
)

const (
	syncKeyAcct      = "blaze-sync:key:" // + Blaze account id: the derived key (hex), never the passphrase
	syncWindow       = 300               // newest messages of each chat that sync at once
	syncBackfill     = 5                 // pushes (≤200 items each) of older messages in one round
	syncBackfillGap  = time.Hour         // at most one backfill round per this (Sync now: at once)
	syncEvery        = 15 * time.Minute
	syncStartDelay   = 20 * time.Second
	syncPoll         = 20 * time.Second
	syncMemDebounce  = 15 * time.Second
	syncChatDebounce = 60 * time.Second
	syncBusyRetry    = 45 * time.Second
	syncLockStale    = 10 * time.Minute
	syncRoundTimeout = 4 * time.Minute
)

func syncDir() string       { return filepath.Join(config.Dir(), "cloudsync") }
func syncPrefsPath() string { return filepath.Join(config.Dir(), "desktop", "cloudsync.json") }

// Desktop keeps its own state and alias files: Blaze CLI syncs memory only, and a round without
// the chat kinds would move the shared cursor past chat items Desktop never applied.
func syncStatePath() string { return filepath.Join(syncDir(), "desktop-state.json") }
func syncAliasPath() string { return filepath.Join(syncDir(), "desktop-alias.json") }
func syncLockPath() string  { return filepath.Join(syncDir(), "lock") }

// syncPrefs is ~/.blaze/desktop/cloudsync.json (no secrets).
type syncPrefs struct {
	On           bool   `json:"on"`
	User         string `json:"user,omitempty"`          // the Blaze account sync was switched on for
	LastOK       int64  `json:"last_ok,omitempty"`       // unix s of the last good round
	LastTry      int64  `json:"last_try,omitempty"`      // unix s of the last round
	LastBackfill int64  `json:"last_backfill,omitempty"` // unix s of the last round allowed to backfill
	Error        string `json:"error,omitempty"`         // the last round's error, as the user reads it
	NeedPass     bool   `json:"need_pass,omitempty"`     // the key is missing or no longer opens the copy
	Waiting      string `json:"waiting,omitempty"`       // why the round is postponed (a reply is running)
	Backlog      int    `json:"backlog,omitempty"`       // older messages still to upload
	Pulled       int    `json:"pulled,omitempty"`        // last round
	Pushed       int    `json:"pushed,omitempty"`
}

// SyncView is what Settings shows.
type SyncView struct {
	SignedIn       bool   `json:"signedIn"`
	On             bool   `json:"on"`
	Running        bool   `json:"running"`
	NeedPassphrase bool   `json:"needPassphrase"`
	LastSync       int64  `json:"lastSync"` // unix s, 0 = never
	Backlog        int    `json:"backlog"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
}

var syncRT struct {
	sync.Mutex
	started bool
	running bool
	kick    chan string
	memFP   string
	chatFP  string
}

func loadSyncPrefs() syncPrefs {
	var p syncPrefs
	if b, err := os.ReadFile(syncPrefsPath()); err == nil {
		_ = json.Unmarshal(b, &p)
	}
	return p
}

func saveSyncPrefs(p syncPrefs) {
	_ = os.MkdirAll(filepath.Dir(syncPrefsPath()), 0o700)
	b, _ := json.MarshalIndent(p, "", "  ")
	tmp := syncPrefsPath() + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, syncPrefsPath())
	}
}

func updateSyncPrefs(f func(*syncPrefs)) syncPrefs {
	syncRT.Lock()
	defer syncRT.Unlock()
	p := loadSyncPrefs()
	f(&p)
	saveSyncPrefs(p)
	return p
}

// syncUser is the signed-in account's id ("" when signed out).
func (a *App) syncUser() string {
	if a.cloud == nil {
		return ""
	}
	a.cloud.mu.Lock()
	defer a.cloud.mu.Unlock()
	if a.cloud.user == nil {
		return ""
	}
	return a.cloud.user.ID
}

// syncBase is the gateway root the /v1/sync routes live on (the gateway Blaze Cloud uses).
func syncBase() string {
	b := cloudai.BaseFrom("")
	if cloudGateway != nil && cloudGateway.Base != "" {
		b = cloudGateway.Base
	}
	return strings.TrimSuffix(strings.TrimRight(b, "/"), "/v1")
}

func (a *App) syncClient() *cloudsync.Client {
	return &cloudsync.Client{Base: syncBase(), Token: a.blazeToken(), UserAgent: "Blaze-Desktop/" + strings.TrimSpace(version)}
}

func (a *App) syncKey(user string) []byte {
	if a.cloud == nil || a.cloud.sec == nil || user == "" {
		return nil
	}
	h, _ := a.cloud.sec.Get(syncKeyAcct + user)
	k, err := hex.DecodeString(strings.TrimSpace(h))
	if err != nil || len(k) != 32 {
		return nil
	}
	return k
}

// ---------------- Settings bindings ----------------

// SyncState is the current state for Settings.
func (a *App) SyncState() SyncView {
	p := loadSyncPrefs()
	syncRT.Lock()
	running := syncRT.running
	syncRT.Unlock()
	user := a.syncUser()
	v := SyncView{SignedIn: a.blazeToken() != "" && user != "", Running: running, LastSync: p.LastOK, Backlog: p.Backlog}
	v.On = p.On && v.SignedIn && p.User == user
	v.NeedPassphrase = v.On && (p.NeedPass || a.syncKey(user) == nil)
	switch {
	case !v.SignedIn:
		v.Status = "Sign in to your Blaze account to sync."
	case p.On && p.User != "" && p.User != user:
		v.Status = "Sync was switched on for another Blaze account. Switch it on for this one to sync."
	case !v.On:
		v.Status = "Off. Memory and chats stay on this computer."
	case v.NeedPassphrase:
		v.Status = "Enter your sync passphrase to keep syncing."
		v.Error = p.Error
	case running:
		v.Status = "Syncing…"
	case p.Waiting != "":
		v.Status = p.Waiting
	case p.Error != "":
		v.Status, v.Error = "Not synced: "+p.Error, p.Error
	case p.LastOK == 0:
		v.Status = "On. First sync is starting."
	default:
		v.Status = "Synced " + agoText(time.Unix(p.LastOK, 0))
		if p.Backlog > 0 {
			v.Status += fmt.Sprintf(". %d older messages still uploading, a few hundred at a time", p.Backlog)
		}
		v.Status += "."
	}
	return v
}

func agoText(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		n := int(d / time.Minute)
		if n == 1 {
			return "1 minute ago"
		}
		return strconv.Itoa(n) + " minutes ago"
	case d < 24*time.Hour:
		n := int(d / time.Hour)
		if n == 1 {
			return "1 hour ago"
		}
		return strconv.Itoa(n) + " hours ago"
	}
	return "on " + t.Format("2 Jan 15:04")
}

// SyncEnable switches sync on with the passphrase: the first device sets the account's copy up,
// later devices must use the same passphrase. The first round runs in the background.
func (a *App) SyncEnable(passphrase string) SyncView {
	user := a.syncUser()
	if a.blazeToken() == "" || user == "" {
		v := a.SyncState()
		v.Error = "Sign in to your Blaze account first."
		return v
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	key, err := cloudsync.Enable(ctx, a.syncClient(), passphrase, 0)
	if err != nil {
		v := a.SyncState()
		v.Error = syncErrText(err, true)
		return v
	}
	if err := a.cloud.sec.Set(syncKeyAcct+user, hex.EncodeToString(key)); err != nil {
		v := a.SyncState()
		v.Error = "Couldn't keep the sync key in the system keychain: " + err.Error()
		return v
	}
	updateSyncPrefs(func(p *syncPrefs) {
		if p.User != user {
			*p = syncPrefs{}
		}
		p.On, p.User, p.NeedPass, p.Error, p.Waiting = true, user, false, "", ""
		p.LastBackfill = 0
	})
	a.startCloudSync()
	a.syncKick("enable")
	v := a.SyncState()
	v.Running = true
	return v
}

// SyncDisable switches sync off here. Nothing is deleted, here or in the cloud copy; the key is
// removed from the keychain, so switching it on again asks for the passphrase.
func (a *App) SyncDisable() SyncView {
	p := updateSyncPrefs(func(p *syncPrefs) { p.On, p.NeedPass, p.Error, p.Waiting = false, false, "", "" })
	if a.cloud != nil && a.cloud.sec != nil && p.User != "" {
		_ = a.cloud.sec.Delete(syncKeyAcct + p.User)
	}
	a.emitSync()
	return a.SyncState()
}

// SyncNow runs a round now (with backfill and another try for items skipped earlier).
func (a *App) SyncNow() SyncView {
	a.startCloudSync()
	a.syncKick("now")
	v := a.SyncState()
	if v.On && !v.NeedPassphrase {
		v.Running = true
	}
	return v
}

func (a *App) emitSync() { a.emit("sync:state", a.SyncState()) }

// ---------------- runner ----------------

// startCloudSync starts the background runner once (rounds only run while sync is on).
func (a *App) startCloudSync() {
	syncRT.Lock()
	if syncRT.started {
		syncRT.Unlock()
		return
	}
	syncRT.started = true
	syncRT.kick = make(chan string, 4)
	syncRT.Unlock()
	go a.syncLoop()
}

func (a *App) syncKick(reason string) {
	syncRT.Lock()
	k := syncRT.kick
	syncRT.Unlock()
	if k == nil {
		return
	}
	select {
	case k <- reason:
	default:
	}
}

func (a *App) syncLoop() {
	start := time.NewTimer(syncStartDelay)
	tick := time.NewTicker(syncEvery)
	poll := time.NewTicker(syncPoll)
	defer tick.Stop()
	defer poll.Stop()
	var debounce <-chan time.Time
	syncRT.Lock()
	kick := syncRT.kick
	syncRT.Unlock()
	for {
		reason := ""
		select {
		case <-start.C:
			reason = "start"
		case reason = <-kick:
		case <-tick.C:
			reason = "periodic"
		case <-debounce:
			debounce, reason = nil, "changed"
		case <-poll.C:
			if !loadSyncPrefs().On {
				continue
			}
			mem, chats := syncFingerprints()
			syncRT.Lock()
			memChanged := syncRT.memFP != "" && mem != syncRT.memFP
			chatChanged := syncRT.chatFP != "" && chats != syncRT.chatFP
			if syncRT.memFP == "" {
				syncRT.memFP, syncRT.chatFP = mem, chats
			}
			syncRT.Unlock()
			if memChanged {
				debounce = time.After(syncMemDebounce)
			} else if chatChanged && debounce == nil {
				debounce = time.After(syncChatDebounce)
			}
			continue
		}
		_ = a.syncOnce(reason)
	}
}

// syncFingerprints are cheap hashes of memory (every line's text and time) and of the chat
// files (names, sizes, times): a change in either schedules a round.
func syncFingerprints() (string, string) {
	h := sha256.New()
	if d := mindDB(); d != nil {
		for _, m := range d.All() {
			fmt.Fprintf(h, "%d\x00%s\x00%s\x00%s\x00", m.Updated, m.Kind, m.Subject, m.Text)
		}
	}
	mem := hex.EncodeToString(h.Sum(nil))
	h.Reset()
	ents, _ := os.ReadDir(convDir())
	for _, e := range ents {
		if fi, err := e.Info(); err == nil && !isPrivate(strings.TrimSuffix(e.Name(), ".json")) {
			fmt.Fprintf(h, "%s\x00%d\x00%d\x00", e.Name(), fi.Size(), fi.ModTime().UnixNano())
		}
	}
	return mem, hex.EncodeToString(h.Sum(nil))
}

// syncLock is the lock file one round holds (shared with Blaze CLI). A lock older than
// syncLockStale is from a crashed round and is taken over.
func syncLock() (func(), error) {
	p := syncLockPath()
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	for i := 0; i < 2; i++ {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(f, "desktop %d %s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
			_ = f.Close()
			return func() { _ = os.Remove(p) }, nil
		}
		fi, serr := os.Stat(p)
		if serr == nil && time.Since(fi.ModTime()) > syncLockStale {
			_ = os.Remove(p)
			continue
		}
		break
	}
	return nil, errSyncLocked
}

var (
	errSyncLocked   = errors.New("another Blaze app on this computer is syncing")
	errSyncChatBusy = errors.New("a reply is still running in a synced chat")
	errSyncNoKey    = errors.New("no sync key on this computer")
)

// syncOnce runs one round if sync is on, and records what happened.
func (a *App) syncOnce(reason string) error {
	p := loadSyncPrefs()
	user := a.syncUser()
	if !p.On || p.User == "" || p.User != user || a.blazeToken() == "" {
		return nil
	}
	key := a.syncKey(user)
	if key == nil {
		updateSyncPrefs(func(p *syncPrefs) { p.NeedPass = true })
		a.emitSync()
		return errSyncNoKey
	}
	a.mu.Lock()
	busy := a.busy
	a.mu.Unlock()
	if busy && reason != "now" {
		// Chat files change under a running turn; try again when it has finished.
		updateSyncPrefs(func(p *syncPrefs) { p.Waiting = "Waiting for the reply that's running to finish." })
		a.emitSync()
		go func() { time.Sleep(syncBusyRetry); a.syncKick("retry") }()
		return errSyncChatBusy
	}
	syncRT.Lock()
	if syncRT.running {
		syncRT.Unlock()
		return nil
	}
	syncRT.running = true
	syncRT.Unlock()
	defer func() {
		syncRT.Lock()
		syncRT.running = false
		syncRT.Unlock()
		a.emitSync()
	}()
	a.emitSync()

	unlock, err := syncLock()
	if err != nil {
		updateSyncPrefs(func(p *syncPrefs) { p.Waiting = "Waiting: " + err.Error() + "." })
		go func() { time.Sleep(syncBusyRetry); a.syncKick("retry") }()
		return err
	}
	defer unlock()

	backfill := 0
	if reason == "now" || reason == "enable" || time.Since(time.Unix(p.LastBackfill, 0)) >= syncBackfillGap {
		backfill = syncBackfill
	}
	res, err := a.syncRound(key, backfill, reason == "now")
	now := time.Now().Unix()
	updateSyncPrefs(func(q *syncPrefs) {
		q.LastTry, q.Waiting = now, ""
		if backfill > 0 && err == nil {
			q.LastBackfill = now
		}
		switch {
		case err == nil:
			q.LastOK, q.Error, q.NeedPass = now, "", false
			q.Backlog, q.Pulled, q.Pushed = res.rep.Backlog, res.rep.Pulled, res.rep.Pushed
			if res.rep.Full {
				q.Error = "your cloud copy is full (25 MB), so newer changes aren't uploading"
			}
		case errors.Is(err, errSyncChatBusy):
			q.Waiting = "Waiting for the reply that's running to finish."
		case errors.Is(err, cloudsync.ErrWrongKey):
			q.NeedPass, q.Error = true, syncErrText(err, false)
		case errors.Is(err, cloudsync.ErrNotSetUp):
			q.On, q.Error = false, syncErrText(err, false)
		default:
			q.Error = syncErrText(err, false)
		}
	})
	if errors.Is(err, cloudsync.ErrNotSetUp) && a.cloud != nil && a.cloud.sec != nil {
		_ = a.cloud.sec.Delete(syncKeyAcct + user)
	}
	if errors.Is(err, errSyncChatBusy) {
		go func() { time.Sleep(syncBusyRetry); a.syncKick("retry") }()
	}
	var he *cloudsync.HTTPError
	if errors.As(err, &he) && he.Status == 401 {
		go a.checkSession()
	}
	a.afterSync(res)
	mem, chats := syncFingerprints()
	syncRT.Lock()
	syncRT.memFP, syncRT.chatFP = mem, chats // our own writes don't schedule another round
	syncRT.Unlock()
	return err
}

// syncResult is one round's report and what it changed here.
type syncResult struct {
	rep    cloudsync.Report
	memory int
	chats  *appChats
}

// syncRound runs one engine round over memory and chats.
func (a *App) syncRound(key []byte, backfill int, retrySkipped bool) (syncResult, error) {
	var res syncResult
	md := mindDB()
	if md == nil {
		return res, errors.New("memory isn't available on this computer")
	}
	var bdb *store.DB
	if db, err := store.Default(); err == nil {
		bdb = db
	}
	st := cloudsync.LoadState(syncStatePath())
	if retrySkipped {
		st.RetrySkipped()
	}
	mem := &countingMemory{MemoryStore: &cloudsync.MindStore{Mind: md, Blaze: bdb}, n: &res.memory}
	ch := &appChats{a: a, f: &cloudsync.ConvFiles{Dir: convDir(), AliasPath: syncAliasPath()}, applied: map[string][]string{}}
	res.chats = ch
	adapters := append([]cloudsync.Adapter{&cloudsync.MemoryAdapter{Store: mem}}, cloudsync.ChatAdapters(ch, st)...)
	e := &cloudsync.Engine{API: a.syncClient(), Adapters: adapters, Key: key, State: st, Window: syncWindow, BackfillPushes: backfill}
	ctx, cancel := context.WithTimeout(context.Background(), syncRoundTimeout)
	defer cancel()
	rep, err := e.Run(ctx)
	res.rep = rep
	// A message written into a chat whose turn started during the round would be lost when the
	// turn saves the chat: forget it in the state so the next round applies it again.
	if lost := ch.lostToTurns(); len(lost) > 0 {
		for _, id := range lost {
			st.Remove(cloudsync.KindMessage, id)
		}
		if err == nil {
			_ = st.Save()
		}
	}
	return res, err
}

// afterSync shows what arrived: open chats reload from their files, Settings and the memory
// view refresh.
func (a *App) afterSync(res syncResult) {
	if res.chats != nil {
		if ids := res.chats.reload(); len(ids) > 0 {
			a.emit("sync:chats", ids)
		}
	}
	if res.memory > 0 {
		a.emit("sync:memory", res.memory)
	}
}

// syncErrText is an error as the user reads it.
func syncErrText(err error, enabling bool) string {
	var he *cloudsync.HTTPError
	var ne net.Error
	switch {
	case err == nil:
		return ""
	case errors.Is(err, cloudsync.ErrBadPassphrase):
		return "That passphrase doesn't open your synced copy. Use the one you chose on your first device."
	case errors.Is(err, cloudsync.ErrWrongKey):
		return "Your synced copy was reset with a new passphrase on another device. Enter that passphrase here."
	case errors.Is(err, cloudsync.ErrNotSetUp):
		return "Your synced copy was deleted from another device, so sync is off here. Switch it on again to start a new one."
	case errors.Is(err, cloudsync.ErrBusy):
		return "another device was syncing at the same moment; trying again shortly"
	case errors.As(err, &he):
		switch {
		case he.Status == 401:
			return "your Blaze sign-in has expired. Sign in again"
		case he.Code == "quota_exceeded":
			return "your cloud copy is full (25 MB)"
		case he.Status == 429:
			return "Blaze's sync service is busy (rate limit); trying again later"
		case he.Status >= 500:
			return fmt.Sprintf("Blaze's sync service had a problem (%d); trying again later", he.Status)
		}
		return "Blaze's sync service refused the request (" + he.Code + ")"
	case errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne):
		if enabling {
			return "Couldn't reach Blaze's sync service. Check your connection and try again."
		}
		return "couldn't reach Blaze's sync service; trying again later"
	}
	return err.Error()
}

// ---------------- adapters over the app ----------------

// countingMemory counts the lines a round changed here.
type countingMemory struct {
	cloudsync.MemoryStore
	n *int
}

func (c *countingMemory) Add(l cloudsync.MemLine) error {
	*c.n++
	return c.MemoryStore.Add(l)
}

func (c *countingMemory) Remove(l cloudsync.MemLine) error {
	*c.n++
	return c.MemoryStore.Remove(l)
}

// appChats is the chat files with the app around them: a chat whose turn is running is never
// written (the round fails and runs again later), and the chats a round changed are reloaded
// into their sessions afterwards.
type appChats struct {
	a       *App
	f       *cloudsync.ConvFiles
	mu      sync.Mutex
	touched []string            // chats written, in order
	applied map[string][]string // chat → msg item ids written
}

func (c *appChats) runningIn(id string) bool {
	if c.a == nil {
		return false
	}
	c.a.mu.Lock()
	defer c.a.mu.Unlock()
	if s := c.a.live[id]; s != nil && s.running() {
		return true
	}
	return c.a.cur != nil && c.a.cur.id == id && c.a.cur.running()
}

func (c *appChats) write(id string, f func() error) error {
	if isPrivate(id) {
		return nil
	}
	if c.runningIn(id) {
		return errSyncChatBusy
	}
	if err := f(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range c.touched {
		if t == id {
			return nil
		}
	}
	c.touched = append(c.touched, id)
	return nil
}

func (c *appChats) Chats() []string {
	var out []string
	for _, id := range c.f.Chats() {
		if !isPrivate(id) {
			out = append(out, id)
		}
	}
	return out
}
func (c *appChats) Head(id string) (cloudsync.ChatHead, bool) { return c.f.Head(id) }
func (c *appChats) Messages(id string) ([]cloudsync.ChatMsg, bool) {
	return c.f.Messages(id)
}
func (c *appChats) PutHead(h cloudsync.ChatHead) error {
	return c.write(h.ID, func() error { return c.f.PutHead(h) })
}
func (c *appChats) PutMsg(chat string, m cloudsync.ChatMsg) error {
	return c.write(chat, func() error {
		if err := c.f.PutMsg(chat, m); err != nil {
			return err
		}
		if id, ok := cloudsync.MsgItemID(chat, m.GID); ok {
			c.mu.Lock()
			c.applied[chat] = append(c.applied[chat], id)
			c.mu.Unlock()
		}
		return nil
	})
}
func (c *appChats) RemoveMsg(chat, gid string) error {
	return c.write(chat, func() error { return c.f.RemoveMsg(chat, gid) })
}
func (c *appChats) Delete(id string) error {
	return c.write(id, func() error { return c.f.Delete(id) })
}

// lostToTurns are the msg items written into chats whose turn has started since.
func (c *appChats) lostToTurns() []string {
	c.mu.Lock()
	chats := append([]string(nil), c.touched...)
	c.mu.Unlock()
	var out []string
	for _, id := range chats {
		if c.runningIn(id) {
			c.mu.Lock()
			out = append(out, c.applied[id]...)
			c.mu.Unlock()
		}
	}
	return out
}

// reload puts what a round wrote into the sessions that hold those chats (not running ones),
// so the app's next save keeps it. Returns the chats it changed, sorted.
func (c *appChats) reload() []string {
	c.mu.Lock()
	ids := append([]string(nil), c.touched...)
	c.mu.Unlock()
	if len(ids) == 0 || c.a == nil {
		return ids
	}
	for _, id := range ids {
		var conv Conv
		b, err := os.ReadFile(filepath.Join(convDir(), id+".json"))
		gone := err != nil
		if !gone {
			_ = json.Unmarshal(b, &conv)
		}
		c.a.mu.Lock()
		if gone { // deleted on another device: close it here the way DeleteConversation does
			if s := c.a.live[id]; s != nil && !s.running() {
				delete(c.a.live, id)
			}
			if c.a.convID == id && c.a.cur != nil && !c.a.cur.running() {
				c.a.convID, c.a.cur = "", &session{}
			}
			c.a.mu.Unlock()
			continue
		}
		for _, s := range []*session{c.a.cur, c.a.live[id]} {
			if s == nil || s.id != id || s.running() {
				continue
			}
			s.hist = append([]llm.Message(nil), conv.Messages...)
		}
		c.a.mu.Unlock()
	}
	sort.Strings(ids)
	return ids
}
