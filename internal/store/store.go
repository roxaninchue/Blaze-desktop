// Package store is Blaze's local database (~/.blaze/blaze.db): memories, an index
// of skills and an index of tools, each with an FTS5 full-text table so a turn
// only pulls the rows it needs instead of pasting everything into the prompt.
//
// It uses modernc.org/sqlite (pure Go, no cgo) so Windows and macOS cross-builds
// keep working. The skills folder stays the source of truth for skills (it is
// shared with the Android app and the CLI); the tools table is rebuilt from the
// Go registry. Only memories live here for real.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	_ "modernc.org/sqlite"

	"blaze-desktop/internal/config"
)

// DB is an open blaze.db.
type DB struct {
	sql  *sql.DB
	Path string
	mu   sync.Mutex // serialises writes (SQLite allows one writer)
}

var (
	defMu  sync.Mutex
	defDB  *DB
	defErr error
)

// Default opens (once) config.Dir()/blaze.db, migrating an old memory.md on the
// way. It reopens when the config dir changes (tests point BLAZE_HOME elsewhere).
func Default() (*DB, error) {
	defMu.Lock()
	defer defMu.Unlock()
	p := filepath.Join(config.Dir(), "blaze.db")
	if defDB != nil && defDB.Path == p {
		return defDB, nil
	}
	if defDB != nil {
		_ = defDB.Close()
	}
	_ = os.MkdirAll(config.Dir(), 0o700)
	ownerOnly(p) // conversations/memory: 0600 even when ~/.blaze predates 0700
	defDB, defErr = Open(p)
	if defErr == nil {
		_, _ = defDB.ImportMarkdown(filepath.Join(config.Dir(), "memory.md"))
	}
	return defDB, defErr
}

// Open opens or creates a database file and brings its schema up to date.
func Open(path string) (*DB, error) {
	// The CLI opens the same file. busy_timeout waits up to 5 s for its write lock
	// instead of failing; _txlock=immediate makes every BEGIN a BEGIN IMMEDIATE, so a
	// transaction takes the write lock up front and never has to upgrade a read lock
	// mid-way (the SQLITE_BUSY that broke a first run when both apps created blaze.db).
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_txlock=immediate"
	s, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	s.SetMaxOpenConns(1) // one connection: no "database is locked" between our own goroutines
	d := &DB{sql: s, Path: path}
	if err := d.migrate(); err != nil {
		s.Close()
		return nil, fmt.Errorf("blaze.db: %w", err)
	}
	return d, nil
}

func (d *DB) Close() error { return d.sql.Close() }

// CloseDefault closes the shared database Default opened, if any; the next Default
// opens it again. Windows can't delete a file that is still open, so tests call it
// before their temporary BLAZE_HOME is removed.
func CloseDefault() {
	defMu.Lock()
	defer defMu.Unlock()
	if defDB != nil {
		_ = defDB.Close()
		defDB, defErr = nil, nil
	}
}

// ---------------- schema ----------------

var migrations = []string{
	// v1
	`CREATE TABLE memories(
		id INTEGER PRIMARY KEY,
		text TEXT NOT NULL,
		norm TEXT NOT NULL,
		category TEXT NOT NULL DEFAULT '',
		project TEXT NOT NULL DEFAULT '',
		chat TEXT NOT NULL DEFAULT '',
		created INTEGER NOT NULL,
		updated INTEGER NOT NULL,
		uses INTEGER NOT NULL DEFAULT 0,
		last_used INTEGER NOT NULL DEFAULT 0,
		pinned INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX memories_norm ON memories(norm);
	CREATE VIRTUAL TABLE memories_fts USING fts5(text, content='memories', content_rowid='id', tokenize='porter unicode61');
	CREATE TRIGGER memories_ai AFTER INSERT ON memories BEGIN
		INSERT INTO memories_fts(rowid, text) VALUES (new.id, new.text);
	END;
	CREATE TRIGGER memories_ad AFTER DELETE ON memories BEGIN
		INSERT INTO memories_fts(memories_fts, rowid, text) VALUES ('delete', old.id, old.text);
	END;
	CREATE TRIGGER memories_au AFTER UPDATE OF text ON memories BEGIN
		INSERT INTO memories_fts(memories_fts, rowid, text) VALUES ('delete', old.id, old.text);
		INSERT INTO memories_fts(rowid, text) VALUES (new.id, new.text);
	END;
	CREATE TABLE skills(name TEXT NOT NULL, desc TEXT NOT NULL DEFAULT '', path TEXT PRIMARY KEY, body TEXT NOT NULL DEFAULT '', mtime INTEGER NOT NULL DEFAULT 0);
	CREATE VIRTUAL TABLE skills_fts USING fts5(path UNINDEXED, name, desc, body, tokenize='porter unicode61');
	CREATE TABLE tools(name TEXT PRIMARY KEY, desc TEXT NOT NULL DEFAULT '', grp TEXT NOT NULL DEFAULT '');
	CREATE VIRTUAL TABLE tools_fts USING fts5(key UNINDEXED, name, desc, grp, tokenize='porter unicode61');
	CREATE TABLE active_tools(conv TEXT NOT NULL, name TEXT NOT NULL, at INTEGER NOT NULL, PRIMARY KEY(conv, name));
	CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT NOT NULL);`,
}

// ErrNewerSchema means blaze.db was written by a newer Blaze (Desktop or CLI) than
// this one. Desktop leaves the file untouched and runs without memory.
var ErrNewerSchema = errors.New("your memory was saved by a newer version of Blaze. Please update Blaze to use memory")

// SchemaVersion is the newest schema this build knows.
func SchemaVersion() int { return len(migrations) }

// migrate mirrors Blaze CLI's internal/store (v0.6.0): refuse a newer schema, run each
// migration under the write lock and re-read user_version there, then check the tables.
func (d *DB) migrate() error {
	var v int
	if err := retryBusy(func() error { return d.sql.QueryRow(`PRAGMA user_version`).Scan(&v) }); err != nil {
		return err
	}
	if v > len(migrations) {
		return fmt.Errorf("%w (schema v%d, this build knows v%d)", ErrNewerSchema, v, len(migrations))
	}
	// WAL lets Desktop read while the CLI writes. Switched on only after the version
	// check, so a newer file we refuse is left byte-for-byte as it was.
	// Changing the journal mode skips SQLite's busy handler, so another opener
	// creating the file at the same moment can fail it at once: retry it ourselves.
	if err := retryBusy(func() error { _, err := d.sql.Exec(`PRAGMA journal_mode=WAL`); return err }); err != nil {
		return err
	}
	for v < len(migrations) {
		// BEGIN IMMEDIATE (via _txlock) holds the write lock; re-read the version
		// under it in case the CLI migrated while we waited.
		var tx *sql.Tx
		if err := retryBusy(func() (e error) { tx, e = d.sql.Begin(); return }); err != nil {
			return err
		}
		if err := tx.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
			tx.Rollback()
			return err
		}
		if v >= len(migrations) {
			tx.Rollback()
			break
		}
		if _, err := tx.Exec(migrations[v]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", v+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, v+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		v++
	}
	if v > len(migrations) {
		return fmt.Errorf("%w (schema v%d, this build knows v%d)", ErrNewerSchema, v, len(migrations))
	}
	if err := retryBusy(d.checkTables); err != nil {
		return err
	}
	// Forget tool activations of chats nobody touched for a month.
	_, _ = d.sql.Exec(`DELETE FROM active_tools WHERE at < ?`, time.Now().Add(-30*24*time.Hour).Unix())
	return nil
}

// checkTables makes sure a database that claims schema v1 really has the v1
// tables, so a damaged or foreign file is refused instead of written to.
func (d *DB) checkTables() error {
	for _, t := range []string{"memories", "memories_fts", "skills", "skills_fts", "tools", "tools_fts", "active_tools", "meta"} {
		var n int
		if err := d.sql.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name=?`, t).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("schema v%d is missing table %s; not touching it", d.Version(), t)
		}
	}
	return nil
}

// Version is the schema version (PRAGMA user_version).
func (d *DB) Version() int {
	var v int
	_ = d.sql.QueryRow(`PRAGMA user_version`).Scan(&v)
	return v
}

// ---------------- meta ----------------

func (d *DB) Meta(key string) string {
	var v string
	_ = d.sql.QueryRow(`SELECT value FROM meta WHERE key=?`, key).Scan(&v)
	return v
}

func (d *DB) SetMeta(key, value string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sql.Exec(`INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// ---------------- text helpers ----------------

// Norm is the comparison form of a text: lower case, words only, single spaces.
func Norm(s string) string {
	f := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !(unicode.IsLetter(r) || unicode.IsDigit(r)) })
	return strings.Join(f, " ")
}

var stop = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a an and are as at be but by can could did do does for from had has have he her his how i if in into is it its just me my no not of on or our she so than that the their them then there these they this to too up us was we were what when where which who why will with would you your yes ok okay please thanks thank hi hello hey can't don't i'm it's let lets make want need get got use using also very really some any all about`) {
		stop[w] = true
	}
}

// ftsQuery turns free text into a safe FTS5 query: the most recent distinct
// non-stopword terms, quoted, OR-ed (BM25 ranks rows matching more of them first).
func ftsQuery(q string, max int) string {
	words := strings.Fields(Norm(q))
	seen := map[string]bool{}
	var terms []string
	for i := len(words) - 1; i >= 0 && len(terms) < max; i-- { // newest words win
		w := words[i]
		if len(w) < 2 || stop[w] || seen[w] {
			continue
		}
		seen[w] = true
		terms = append(terms, `"`+w+`"`)
	}
	return strings.Join(terms, " OR ")
}

// Tokens approximates a text's token count (≈4 characters per token).
func Tokens(s string) int { return (len(s) + 3) / 4 }

var ErrNotFound = errors.New("That memory changed or was removed. Reopen the graph.")

// retryBusy runs f again while SQLite answers SQLITE_BUSY or SQLITE_LOCKED, for up to
// about 5 s. Some statements (journal_mode, the first read of a file being created)
// return busy without waiting on busy_timeout.
func retryBusy(f func() error) error {
	deadline := time.Now().Add(5 * time.Second)
	wait := 5 * time.Millisecond
	for {
		err := f()
		if err == nil || !isBusy(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(wait)
		if wait < 200*time.Millisecond {
			wait *= 2
		}
	}
}

func isBusy(err error) bool {
	m := err.Error()
	return strings.Contains(m, "SQLITE_BUSY") || strings.Contains(m, "database is locked") || strings.Contains(m, "SQLITE_LOCKED")
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
