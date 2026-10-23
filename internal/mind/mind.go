// Package mind is Blaze's "one mind" memory (v0.16+): one continuous main conversation
// whose every turn goes into a journal (SQLite FTS5), and a memories table that is the
// source of truth for what Blaze knows. The prompt carries only a pinned core and an
// index of subjects; everything else is looked up with memory_search / search_chat.
//
// It lives in its own database (~/.blaze/desktop/mind.db): Blaze CLI shares blaze.db and
// refuses a newer schema, so the new tables can't go there. The schema and the shared
// prompt text are in docs/plans/plan-one-mind.md so Blaze Android can mirror them.
package mind

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"blaze-desktop/internal/config"
)

// Kinds of memory, in pinned-core order.
var Kinds = []string{"profile", "preference", "person", "project", "decision", "fact"}

// Schema is the whole database (shared with Android; see plan-one-mind.md).
const Schema = `
CREATE TABLE IF NOT EXISTS journal(
  id INTEGER PRIMARY KEY,
  conv TEXT NOT NULL,             -- 'main', a project chat id, or an archived chat id
  turn INTEGER NOT NULL,          -- position in that conversation
  role TEXT NOT NULL,             -- user | assistant | tool
  content TEXT NOT NULL,
  at INTEGER NOT NULL,            -- unix seconds
  reviewed INTEGER NOT NULL DEFAULT 0,
  UNIQUE(conv, turn)
);
CREATE INDEX IF NOT EXISTS journal_unreviewed ON journal(reviewed, id);
CREATE VIRTUAL TABLE IF NOT EXISTS journal_fts USING fts5(content, content='journal', content_rowid='id', tokenize='porter unicode61');
CREATE TRIGGER IF NOT EXISTS journal_ai AFTER INSERT ON journal BEGIN
  INSERT INTO journal_fts(rowid, content) VALUES (new.id, new.content);
END;
CREATE TABLE IF NOT EXISTS memories(
  id INTEGER PRIMARY KEY,
  kind TEXT NOT NULL,             -- profile | preference | person | project | decision | fact
  subject TEXT NOT NULL,          -- who/what it is about: 'Ada', 'HomeGlow', 'food'
  text TEXT NOT NULL,             -- one line, the user's conditions kept; '(inferred)' when inferred
  related TEXT NOT NULL DEFAULT '', -- comma-separated subjects
  source_turn INTEGER NOT NULL DEFAULT 0, -- journal.id it came from (0 = typed or imported)
  created INTEGER NOT NULL,
  updated INTEGER NOT NULL,
  mirror INTEGER NOT NULL DEFAULT 0 -- blaze.db memories.id kept in step for Blaze CLI
);
CREATE INDEX IF NOT EXISTS memories_subject ON memories(kind, subject);
CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(subject, text, content='memories', content_rowid='id', tokenize='porter unicode61');
CREATE TRIGGER IF NOT EXISTS memories_ai AFTER INSERT ON memories BEGIN
  INSERT INTO memories_fts(rowid, subject, text) VALUES (new.id, new.subject, new.text);
END;
CREATE TRIGGER IF NOT EXISTS memories_ad AFTER DELETE ON memories BEGIN
  INSERT INTO memories_fts(memories_fts, rowid, subject, text) VALUES ('delete', old.id, old.subject, old.text);
END;
CREATE TRIGGER IF NOT EXISTS memories_au AFTER UPDATE OF subject, text ON memories BEGIN
  INSERT INTO memories_fts(memories_fts, rowid, subject, text) VALUES ('delete', old.id, old.subject, old.text);
  INSERT INTO memories_fts(rowid, subject, text) VALUES (new.id, new.subject, new.text);
END;
CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY, value TEXT NOT NULL);`

type DB struct {
	sql  *sql.DB
	Path string
	mu   sync.Mutex
}

var (
	defMu sync.Mutex
	defDB *DB
)

// Now is the clock (tests move it).
var Now = time.Now

// Default opens config.Dir()/desktop/mind.db (once per config dir).
func Default() (*DB, error) {
	defMu.Lock()
	defer defMu.Unlock()
	p := filepath.Join(config.Dir(), "desktop", "mind.db")
	if defDB != nil && defDB.Path == p {
		return defDB, nil
	}
	if defDB != nil {
		_ = defDB.sql.Close()
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	ownerOnly(p) // conversations/memory: 0600 even when ~/.blaze predates 0700
	d, err := Open(p)
	if err != nil {
		return nil, err
	}
	defDB = d
	return d, nil
}

// CloseDefault closes the shared database (tests: Windows can't delete an open file).
func CloseDefault() {
	defMu.Lock()
	defer defMu.Unlock()
	if defDB != nil {
		_ = defDB.sql.Close()
		defDB = nil
	}
}

// Open opens or creates a mind database.
func Open(path string) (*DB, error) {
	s, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	s.SetMaxOpenConns(1)
	if _, err := s.Exec(Schema); err != nil {
		s.Close()
		return nil, fmt.Errorf("mind.db: %w", err)
	}
	return &DB{sql: s, Path: path}, nil
}

func (d *DB) Close() error { return d.sql.Close() }

// ---------- meta ----------

func (d *DB) Meta(k string) string {
	var v string
	_ = d.sql.QueryRow(`SELECT value FROM meta WHERE key=?`, k).Scan(&v)
	return v
}

func (d *DB) SetMeta(k, v string) error {
	_, err := d.sql.Exec(`INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v)
	return err
}

// ---------- journal ----------

// Turn is one journal row.
type Turn struct {
	ID       int64  `json:"id"`
	Conv     string `json:"conv"`
	Turn     int    `json:"turn"`
	Role     string `json:"role"`
	Content  string `json:"content"`
	At       int64  `json:"at"`
	Reviewed bool   `json:"reviewed"`
}

// Msg is a message to journal.
type Msg struct {
	Turn    int
	Role    string
	Content string
	At      time.Time
}

// Journal adds messages of conv; a (conv, turn) already there is left as it is, so
// importing twice never duplicates. Returns how many rows were added.
func (d *DB) Journal(conv string, msgs []Msg) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	tx, err := d.sql.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n := 0
	for _, m := range msgs {
		c := strings.TrimSpace(m.Content)
		if c == "" {
			continue
		}
		if len(c) > 8000 {
			c = c[:8000]
		}
		at := m.At
		if at.IsZero() {
			at = Now()
		}
		r, err := tx.Exec(`INSERT OR IGNORE INTO journal(conv, turn, role, content, at) VALUES (?,?,?,?,?)`, conv, m.Turn, m.Role, c, at.Unix())
		if err != nil {
			return n, err
		}
		if k, _ := r.RowsAffected(); k > 0 {
			n++
		}
	}
	return n, tx.Commit()
}

// Journaled is the highest turn of conv in the journal, or -1.
func (d *DB) Journaled(conv string) int {
	var n sql.NullInt64
	_ = d.sql.QueryRow(`SELECT max(turn) FROM journal WHERE conv=?`, conv).Scan(&n)
	if !n.Valid {
		return -1
	}
	return int(n.Int64)
}

// Unreviewed returns the oldest unreviewed journal turns that fit maxChars.
func (d *DB) Unreviewed(maxChars int) []Turn {
	rows, err := d.sql.Query(`SELECT id, conv, turn, role, content, at FROM journal WHERE reviewed=0 AND role IN ('user','assistant') ORDER BY id LIMIT 400`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Turn
	used := 0
	for rows.Next() {
		var t Turn
		if rows.Scan(&t.ID, &t.Conv, &t.Turn, &t.Role, &t.Content, &t.At) != nil {
			continue
		}
		if used+len(t.Content) > maxChars && len(out) > 0 {
			break
		}
		used += len(t.Content)
		out = append(out, t)
	}
	return out
}

// CountUnreviewed is how many user/assistant turns wait for the idle review.
func (d *DB) CountUnreviewed() int {
	var n int
	_ = d.sql.QueryRow(`SELECT count(*) FROM journal WHERE reviewed=0 AND role IN ('user','assistant')`).Scan(&n)
	return n
}

// MarkReviewed marks turns reviewed (and every tool row up to the last of them in the
// same conversations, which the review never needs).
func (d *DB) MarkReviewed(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	max := int64(0)
	for _, id := range ids {
		if _, err := tx.Exec(`UPDATE journal SET reviewed=1 WHERE id=?`, id); err != nil {
			return err
		}
		if id > max {
			max = id
		}
	}
	_, _ = tx.Exec(`UPDATE journal SET reviewed=1 WHERE role='tool' AND id<=?`, max)
	return tx.Commit()
}

var nonWord = regexp.MustCompile(`[^\p{L}\p{N}_]+`)

// FTSQuery turns free text into an FTS5 OR query of quoted words (no syntax errors).
func FTSQuery(q string) string {
	var ws []string
	for _, w := range nonWord.Split(strings.ToLower(q), -1) {
		if len([]rune(w)) >= 2 {
			ws = append(ws, `"`+w+`"`)
		}
	}
	return strings.Join(ws, " OR ")
}

// SearchJournal finds past turns across every conversation (conv "" = all), best first.
func (d *DB) SearchJournal(q, conv string, limit int) []Turn {
	fq := FTSQuery(q)
	if fq == "" {
		return nil
	}
	if limit <= 0 || limit > 30 {
		limit = 8
	}
	query := `SELECT j.id, j.conv, j.turn, j.role, snippet(journal_fts, 0, '[', ']', ' … ', 24), j.at FROM journal_fts JOIN journal j ON j.id = journal_fts.rowid WHERE journal_fts MATCH ?`
	args := []any{fq}
	if conv != "" {
		query += ` AND j.conv = ?`
		args = append(args, conv)
	}
	query += ` ORDER BY rank LIMIT ?`
	args = append(args, limit)
	rows, err := d.sql.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Turn
	for rows.Next() {
		var t Turn
		if rows.Scan(&t.ID, &t.Conv, &t.Turn, &t.Role, &t.Content, &t.At) == nil {
			out = append(out, t)
		}
	}
	return out
}

// JournalTurn reads one full journal row.
func (d *DB) JournalTurn(id int64) (Turn, bool) {
	var t Turn
	var rv int
	err := d.sql.QueryRow(`SELECT id, conv, turn, role, content, at, reviewed FROM journal WHERE id=?`, id).Scan(&t.ID, &t.Conv, &t.Turn, &t.Role, &t.Content, &t.At, &rv)
	t.Reviewed = rv == 1
	return t, err == nil
}

// ---------- memories ----------

// Memory is one line Blaze knows.
type Memory struct {
	ID         int64  `json:"id"`
	Kind       string `json:"kind"`
	Subject    string `json:"subject"`
	Text       string `json:"text"`
	Related    string `json:"related,omitempty"`
	SourceTurn int64  `json:"source_turn,omitempty"`
	Created    int64  `json:"created"`
	Updated    int64  `json:"updated"`
	Mirror     int64  `json:"mirror,omitempty"`
}

// NormKind maps a free word onto a kind ("" or unknown = fact).
func NormKind(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	k = strings.TrimSuffix(k, "s")
	for _, x := range Kinds {
		if k == x {
			return x
		}
	}
	switch k {
	case "pref", "like", "rule", "habit":
		return "preference"
	case "people", "contact":
		return "person"
	case "user", "me", "identity":
		return "profile"
	}
	return "fact"
}

func clean(s string) string { return strings.Join(strings.Fields(s), " ") }

const memCols = `id, kind, subject, text, related, source_turn, created, updated, mirror`

func scan(rows *sql.Rows, err error) []Memory {
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		var m Memory
		if rows.Scan(&m.ID, &m.Kind, &m.Subject, &m.Text, &m.Related, &m.SourceTurn, &m.Created, &m.Updated, &m.Mirror) == nil {
			out = append(out, m)
		}
	}
	return out
}

// All lists every memory, newest first.
func (d *DB) All() []Memory {
	return scan(d.sql.Query(`SELECT ` + memCols + ` FROM memories ORDER BY updated DESC, id DESC`))
}

// Get reads one memory.
func (d *DB) Get(id int64) (Memory, bool) {
	ms := scan(d.sql.Query(`SELECT `+memCols+` FROM memories WHERE id=?`, id))
	if len(ms) == 0 {
		return Memory{}, false
	}
	return ms[0], true
}

// Count is how many memories there are.
func (d *DB) Count() int {
	var n int
	_ = d.sql.QueryRow(`SELECT count(*) FROM memories`).Scan(&n)
	return n
}

func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range nonWord.Split(strings.ToLower(s), -1) {
		if len(w) > 2 {
			out[w] = true
		}
	}
	return out
}

// similar is the word overlap of a and b (Jaccard).
func similar(a, b string) float64 {
	wa, wb := words(a), words(b)
	if len(wa) == 0 || len(wb) == 0 {
		return 0
	}
	in := 0
	for w := range wa {
		if wb[w] {
			in++
		}
	}
	return float64(in) / float64(len(wa)+len(wb)-in)
}

// Saved says what Save did.
type Saved struct {
	ID     int64  `json:"id"`
	Action string `json:"action"` // added | updated | same
	Old    string `json:"old,omitempty"`
}

// Save adds a memory, or rewrites the line it repeats or supersedes (same kind and
// subject and mostly the same words), so there are no duplicates and the newest wins.
func (d *DB) Save(m Memory) (Saved, error) {
	m.Kind, m.Subject, m.Text, m.Related = NormKind(m.Kind), clean(m.Subject), clean(m.Text), clean(m.Related)
	if m.Text == "" {
		return Saved{}, fmt.Errorf("empty memory")
	}
	if m.Subject == "" {
		m.Subject = "general"
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	now := Now().Unix()
	cands := scan(d.sql.Query(`SELECT `+memCols+` FROM memories WHERE kind=? AND lower(subject)=lower(?)`, m.Kind, m.Subject))
	best, bestS := Memory{}, 0.0
	for _, c := range cands {
		if s := similar(c.Text, m.Text); s > bestS {
			best, bestS = c, s
		}
	}
	if bestS >= 0.5 {
		if strings.EqualFold(best.Text, m.Text) {
			return Saved{ID: best.ID, Action: "same"}, nil
		}
		rel := best.Related
		if m.Related != "" {
			rel = m.Related
		}
		src := best.SourceTurn
		if m.SourceTurn != 0 {
			src = m.SourceTurn
		}
		if _, err := d.sql.Exec(`UPDATE memories SET text=?, related=?, source_turn=?, updated=? WHERE id=?`, m.Text, rel, src, now, best.ID); err != nil {
			return Saved{}, err
		}
		return Saved{ID: best.ID, Action: "updated", Old: best.Text}, nil
	}
	created := m.Created
	if created == 0 {
		created = now
	}
	r, err := d.sql.Exec(`INSERT INTO memories(kind, subject, text, related, source_turn, created, updated, mirror) VALUES (?,?,?,?,?,?,?,?)`,
		m.Kind, m.Subject, m.Text, m.Related, m.SourceTurn, created, now, m.Mirror)
	if err != nil {
		return Saved{}, err
	}
	id, _ := r.LastInsertId()
	return Saved{ID: id, Action: "added"}, nil
}

// Insert stores one memory exactly as given (no dedupe), keeping its times (unix seconds; 0 =
// now). Cloud sync uses it: a line that arrives from another device was already deduplicated
// there, and merging it into a similar line here would change that line's sync id and delete
// it on the other devices.
func (d *DB) Insert(m Memory) (int64, error) {
	m.Kind, m.Subject, m.Text, m.Related = NormKind(m.Kind), clean(m.Subject), clean(m.Text), clean(m.Related)
	if m.Text == "" {
		return 0, fmt.Errorf("empty memory")
	}
	if m.Subject == "" {
		m.Subject = "general"
	}
	now := Now().Unix()
	if m.Updated <= 0 {
		m.Updated = now
	}
	if m.Created <= 0 {
		m.Created = m.Updated
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	r, err := d.sql.Exec(`INSERT INTO memories(kind, subject, text, related, source_turn, created, updated, mirror) VALUES (?,?,?,?,?,?,?,?)`,
		m.Kind, m.Subject, m.Text, m.Related, m.SourceTurn, m.Created, m.Updated, m.Mirror)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

// Update rewrites one memory's line (and optionally its kind/subject).
func (d *DB) Update(id int64, text, kind, subject string) error {
	m, ok := d.Get(id)
	if !ok {
		return fmt.Errorf("no memory %d", id)
	}
	if t := clean(text); t != "" {
		m.Text = t
	}
	if kind != "" {
		m.Kind = NormKind(kind)
	}
	if s := clean(subject); s != "" {
		m.Subject = s
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sql.Exec(`UPDATE memories SET text=?, kind=?, subject=?, updated=? WHERE id=?`, m.Text, m.Kind, m.Subject, Now().Unix(), id)
	return err
}

// SetMirror records the blaze.db row kept in step with this memory.
func (d *DB) SetMirror(id, mirror int64) {
	_, _ = d.sql.Exec(`UPDATE memories SET mirror=? WHERE id=?`, mirror, id)
}

// Delete forgets one memory.
func (d *DB) Delete(id int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sql.Exec(`DELETE FROM memories WHERE id=?`, id)
	return err
}

// Search finds memories by words in subject or text, best first (kind "" = any).
func (d *DB) Search(q, kind string, limit int) []Memory {
	if limit <= 0 || limit > 40 {
		limit = 10
	}
	fq := FTSQuery(q)
	if fq == "" {
		return nil
	}
	query := `SELECT m.id, m.kind, m.subject, m.text, m.related, m.source_turn, m.created, m.updated, m.mirror FROM memories_fts JOIN memories m ON m.id = memories_fts.rowid WHERE memories_fts MATCH ?`
	args := []any{fq}
	if kind != "" {
		query += ` AND m.kind = ?`
		args = append(args, NormKind(kind))
	}
	query += ` ORDER BY bm25(memories_fts, 2.0, 1.0) LIMIT ?`
	args = append(args, limit)
	return scan(d.sql.Query(query, args...))
}

// Core is the pinned core for the system prompt: every profile line and the newest
// standing preferences, within maxChars.
func (d *DB) Core(maxChars int) []Memory {
	var out []Memory
	used := 0
	add := func(ms []Memory) {
		for _, m := range ms {
			if used+len(m.Text) > maxChars {
				return
			}
			used += len(m.Text) + 12
			out = append(out, m)
		}
	}
	add(scan(d.sql.Query(`SELECT ` + memCols + ` FROM memories WHERE kind='profile' ORDER BY updated DESC LIMIT 20`)))
	add(scan(d.sql.Query(`SELECT ` + memCols + ` FROM memories WHERE kind='preference' ORDER BY updated DESC LIMIT 12`)))
	return out
}

// Index lists the subjects Blaze knows about, per kind with counts ("person: Gloria (3), Mutale (1)").
func (d *DB) Index(maxPerKind int) map[string][]string {
	rows, err := d.sql.Query(`SELECT kind, subject, count(*), max(updated) FROM memories GROUP BY kind, lower(subject) ORDER BY max(updated) DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var k, s string
		var n int
		var u int64
		if rows.Scan(&k, &s, &n, &u) != nil {
			continue
		}
		if len(out[k]) < maxPerKind {
			out[k] = append(out[k], fmt.Sprintf("%s (%d)", s, n))
		}
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

// ownerOnly makes the database file (and any WAL/SHM beside it) readable by the user
// only. SQLite creates files 0644 and copies the db's mode to its journals, so
// pre-creating the db as 0600 keeps every file it writes private.
func ownerOnly(p string) {
	if f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600); err == nil {
		f.Close()
	}
	for _, s := range []string{"", "-wal", "-shm"} {
		_ = os.Chmod(p+s, 0o600)
	}
}
