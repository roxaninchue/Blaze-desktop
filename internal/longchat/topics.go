package longchat

// Topic state (v0.19, internal/topics): one JSON row per chat in a NEW table of
// longchat.db. Additive only: older builds and Blaze CLI never read it, and a chat with
// no row simply starts a fresh topic state (its turns are re-read lexically, never with
// model calls). The FTS index (chat_fts) is untouched: every turn stays searchable.

import (
	"encoding/json"
	"time"

	"blaze-desktop/internal/topics"
)

// topicSchema is created with the v0.18 schema in Open (all IF NOT EXISTS).
const topicSchema = `
CREATE TABLE IF NOT EXISTS chat_topics(chat_id TEXT PRIMARY KEY, state TEXT NOT NULL DEFAULT '{}', updated INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS longchat_meta(key TEXT PRIMARY KEY, value TEXT NOT NULL);`

// Topics loads a chat's topic state (a fresh one when there is none or it can't be read).
func (d *DB) Topics(chat string) *topics.State {
	st := topics.New()
	var raw string
	if d.sql.QueryRow(`SELECT state FROM chat_topics WHERE chat_id=?`, chat).Scan(&raw) != nil {
		return st
	}
	if json.Unmarshal([]byte(raw), st) != nil {
		return topics.New()
	}
	if st.TurnTopic == nil {
		st.TurnTopic = map[int]int{}
	}
	return st
}

// SetTopics saves a chat's topic state.
func (d *DB) SetTopics(chat string, st *topics.State) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err = d.sql.Exec(`INSERT INTO chat_topics(chat_id, state, updated) VALUES (?,?,?) ON CONFLICT(chat_id) DO UPDATE SET state=excluded.state, updated=excluded.updated`, chat, string(b), time.Now().Unix())
	return err
}

// ForgetTopics drops a deleted chat's topic state.
func (d *DB) ForgetTopics(chat string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, _ = d.sql.Exec(`DELETE FROM chat_topics WHERE chat_id=?`, chat)
}

// MetaValue / SetMetaValue: small desktop settings kept with the long-chat data (the
// topic view's on/off switch) so app.go's prefs struct stays untouched.
func (d *DB) MetaValue(k string) string {
	var v string
	_ = d.sql.QueryRow(`SELECT value FROM longchat_meta WHERE key=?`, k).Scan(&v)
	return v
}

func (d *DB) SetMetaValue(k, v string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sql.Exec(`INSERT INTO longchat_meta(key, value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v)
	return err
}
