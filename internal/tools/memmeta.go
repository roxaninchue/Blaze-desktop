package tools

// Memory metadata for the Memory screen. Since v0.5.1 everything lives in the
// memories table of blaze.db: created time, project and chat a fact was saved in,
// the category a model gave it, and use counts. The old memory.md and
// memory.meta.json are imported once (store.ImportMarkdown) and kept as
// *.migrated backups.

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

var memCtx struct {
	sync.Mutex
	project, chat string
}

// SetMemoryContext tells remember which project and chat the current turn belongs to.
func SetMemoryContext(project, chat string) {
	memCtx.Lock()
	memCtx.project, memCtx.chat = project, chat
	memCtx.Unlock()
}

// A Fact is one saved memory as the Memory screen sees it.
type Fact struct {
	ID       string // "m<row id>": stable across edits
	Text     string
	Date     time.Time // when it was saved
	Category string    // from the model or the user; "" = use the local guess
	Project  string
	Chat     string
	Pinned   bool
	Uses     int
}

// FactID is the UI id of a memory row.
func FactID(id int64) string { return "m" + strconv.FormatInt(id, 10) }

func rowID(id string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimPrefix(id, "m"), 10, 64)
	return n, err == nil && strings.HasPrefix(id, "m")
}

// Facts returns every memory, oldest first.
func Facts() []Fact {
	d := DB()
	if d == nil {
		return nil
	}
	var out []Fact
	for _, m := range d.Memories() {
		out = append(out, Fact{ID: FactID(m.ID), Text: m.Text, Date: m.Created, Category: m.Category, Project: m.Project, Chat: m.Chat, Pinned: m.Pinned, Uses: m.Uses})
	}
	return out
}

func withRow(id string, fn func(n int64) error) error {
	d := DB()
	if d == nil {
		return errors.New(strings.TrimPrefix(noDBMsg(), "Error: "))
	}
	n, ok := rowID(id)
	if !ok {
		return errors.New("That memory changed or was removed. Reopen the graph.")
	}
	return fn(n)
}

// EditFact replaces one fact's text, keeping its id and metadata. Returns the id.
func EditFact(id, text string) (string, error) {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "", errors.New("A memory can't be empty. Use Forget to remove it.")
	}
	return id, withRow(id, func(n int64) error { return DB().Edit(n, text) })
}

// ForgetFact removes one fact by id.
func ForgetFact(id string) error {
	return withRow(id, func(n int64) error { return DB().Delete(n) })
}

// SetFactCategory sets a fact's category ("" goes back to the local guess).
func SetFactCategory(id, category string) error {
	return withRow(id, func(n int64) error { return DB().SetCategory(n, category) })
}

// PinFact pins a fact so it goes into every prompt.
func PinFact(id string, on bool) error {
	return withRow(id, func(n int64) error { return DB().SetPinned(n, on) })
}

// MemSortedAt is when the model last sorted memories into categories.
func MemSortedAt() time.Time {
	d := DB()
	if d == nil {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, d.Meta("mem_sorted_at"))
	return t
}

// SetMemSortedAt records a model sort attempt.
func SetMemSortedAt(t time.Time) {
	if d := DB(); d != nil {
		_ = d.SetMeta("mem_sorted_at", t.Format(time.RFC3339))
	}
}
