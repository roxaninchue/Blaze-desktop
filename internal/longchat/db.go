// Package longchat keeps long chats cheap: every message is indexed for search_chat,
// older turns fold into a rolling state note, each project has a pinned brief, and big
// tool outputs are set aside as artifacts the model reads on demand.
//
// It has its own database (~/.blaze/desktop/longchat.db) rather than a new table in
// blaze.db: Blaze CLI shares blaze.db and refuses a newer schema, so a migration there
// would cost CLI users their memory.
package longchat

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"blaze-desktop/internal/config"
)

type DB struct {
	sql  *sql.DB
	Path string
	mu   sync.Mutex
}

const schema = `
CREATE VIRTUAL TABLE IF NOT EXISTS chat_fts USING fts5(chat_id UNINDEXED, turn_id UNINDEXED, role UNINDEXED, content, tokenize='porter unicode61');
CREATE TABLE IF NOT EXISTS chat_state(chat_id TEXT PRIMARY KEY, summary TEXT NOT NULL DEFAULT '', upto INTEGER NOT NULL DEFAULT 0, indexed INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS project_brief(project_id TEXT PRIMARY KEY, brief TEXT NOT NULL DEFAULT '', updated INTEGER NOT NULL DEFAULT 0);`

var (
	defMu sync.Mutex
	defDB *DB
)

// Default opens (once per config dir) config.Dir()/desktop/longchat.db.
func Default() (*DB, error) {
	defMu.Lock()
	defer defMu.Unlock()
	p := filepath.Join(config.Dir(), "desktop", "longchat.db")
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

// Open opens or creates a longchat database.
func Open(path string) (*DB, error) {
	s, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	s.SetMaxOpenConns(1)
	if _, err := s.Exec(schema + topicSchema); err != nil { // topicSchema: additive v0.19 tables (topics.go)
		s.Close()
		return nil, fmt.Errorf("longchat.db: %w", err)
	}
	return &DB{sql: s, Path: path}, nil
}

func (d *DB) Close() error { return d.sql.Close() }

// Msg is one message to index: its position in the chat (the turn id), role and text.
type Msg struct {
	Turn    int
	Role    string
	Content string
}

// Indexed is how many messages of a chat are already in the index.
func (d *DB) Indexed(chat string) int {
	var n int
	_ = d.sql.QueryRow(`SELECT indexed FROM chat_state WHERE chat_id=?`, chat).Scan(&n)
	return n
}

// Index adds msgs (turn ids >= Indexed) and records the new high-water mark. Messages
// with no text (bare tool calls) only move the mark.
func (d *DB) Index(chat string, upto int, msgs []Msg) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, m := range msgs {
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO chat_fts(chat_id, turn_id, role, content) VALUES (?,?,?,?)`, chat, m.Turn, m.Role, m.Content); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO chat_state(chat_id, indexed) VALUES (?, ?) ON CONFLICT(chat_id) DO UPDATE SET indexed=excluded.indexed`, chat, upto); err != nil {
		return err
	}
	return tx.Commit()
}

// Hit is one search_chat result.
type Hit struct {
	Turn    int    `json:"turn"`
	Role    string `json:"role"`
	Snippet string `json:"snippet"`
}

var nonWord = regexp.MustCompile(`[^\p{L}\p{N}_]+`)

// ftsQuery turns free text into an FTS5 OR query of quoted words (no syntax errors).
func ftsQuery(q string) string {
	var ws []string
	for _, w := range nonWord.Split(strings.ToLower(q), -1) {
		if len([]rune(w)) >= 2 {
			ws = append(ws, `"`+w+`"`)
		}
	}
	return strings.Join(ws, " OR ")
}

// Search finds past messages of one chat, best match first.
func (d *DB) Search(chat, q string, limit int) []Hit {
	fq := ftsQuery(q)
	if fq == "" {
		return nil
	}
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	rows, err := d.sql.Query(`SELECT turn_id, role, snippet(chat_fts, 3, '«', '»', ' … ', 24) FROM chat_fts WHERE chat_fts MATCH ? AND chat_id=? ORDER BY rank LIMIT ?`, fq, chat, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Hit
	for rows.Next() {
		var h Hit
		if rows.Scan(&h.Turn, &h.Role, &h.Snippet) == nil {
			out = append(out, h)
		}
	}
	return out
}

// Turn returns one indexed message in full.
func (d *DB) Turn(chat string, turn int) (Msg, bool) {
	m := Msg{Turn: turn}
	err := d.sql.QueryRow(`SELECT role, content FROM chat_fts WHERE chat_id=? AND turn_id=?`, chat, turn).Scan(&m.Role, &m.Content)
	return m, err == nil
}

// ChatState is a chat's rolling summary and the first turn it doesn't cover.
type ChatState struct {
	Summary string
	Upto    int
}

func (d *DB) State(chat string) ChatState {
	var st ChatState
	_ = d.sql.QueryRow(`SELECT summary, upto FROM chat_state WHERE chat_id=?`, chat).Scan(&st.Summary, &st.Upto)
	return st
}

func (d *DB) SetState(chat string, st ChatState) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sql.Exec(`INSERT INTO chat_state(chat_id, summary, upto) VALUES (?,?,?) ON CONFLICT(chat_id) DO UPDATE SET summary=excluded.summary, upto=excluded.upto`, chat, st.Summary, st.Upto)
	return err
}

// Forget drops a deleted chat's index and summary.
func (d *DB) Forget(chat string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, _ = d.sql.Exec(`DELETE FROM chat_fts WHERE chat_id=?`, chat)
	_, _ = d.sql.Exec(`DELETE FROM chat_state WHERE chat_id=?`, chat)
}

// BriefMax is the brief's size cap in characters (~600 tokens).
const BriefMax = 2400

func (d *DB) Brief(project string) string {
	var b string
	_ = d.sql.QueryRow(`SELECT brief FROM project_brief WHERE project_id=?`, project).Scan(&b)
	return b
}

// SetBrief saves a project's brief, cut to BriefMax. It returns what was saved.
func (d *DB) SetBrief(project, brief string) (string, error) {
	brief = strings.TrimSpace(brief)
	if r := []rune(brief); len(r) > BriefMax {
		brief = string(r[:BriefMax])
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sql.Exec(`INSERT INTO project_brief(project_id, brief, updated) VALUES (?,?,?) ON CONFLICT(project_id) DO UPDATE SET brief=excluded.brief, updated=excluded.updated`, project, brief, time.Now().Unix())
	return brief, err
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
