package cloudsync

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

// Chats sync as small items, as on the phone since Android 0.45.0 (connect/ChatItems.kt):
//
//   - kind "chat" (ChatHeadAdapter): the chat's own fields (title, updated, /clear time). Desktop
//     sends it with no messages ("split":1); messages carried in one from another device (older
//     phones send whole chats, newer ones their newest 20) are merged in by gid.
//   - kind "msg" (MessageAdapter): one message, id "<chat>:<gid>". A new message is one small
//     push. Never pushed as a delete: a message that left this device's file (a trim) must never
//     delete it elsewhere. A deleted chat is a "chat" tombstone.
//
// Roles travel as the phone names them: "USER" and "BLAZE". Desktop syncs user messages and
// assistant replies with text; tool calls and tool results stay on this device, and the phone's
// "STEP"/"ERROR" bubbles are not applied here.

const (
	RoleUser  = "USER"
	RoleBlaze = "BLAZE"
)

// ChatMsg is one synced message. At is ms (0 = unknown, a message saved before times).
type ChatMsg struct {
	GID, Role, Text string
	At              int64
}

// ChatHead is a chat's own fields. Updated / ClearedAt are ms.
type ChatHead struct {
	ID, Title          string
	Updated, ClearedAt int64
}

// ChatStore is where chats live on this device.
type ChatStore interface {
	// Chats are the ids of the chats that sync (never private ones).
	Chats() []string
	Head(id string) (ChatHead, bool)
	// Messages are the chat's syncable messages, each with its gid.
	Messages(id string) ([]ChatMsg, bool)
	// PutHead creates the chat if missing, else updates its fields (messages untouched).
	PutHead(h ChatHead) error
	// PutMsg inserts the message, or replaces the copy with the same gid (creating the chat).
	PutMsg(chat string, m ChatMsg) error
	RemoveMsg(chat, gid string) error
	// Delete removes a whole chat (a tombstone from another device).
	Delete(id string) error
}

// ---------------- gids (agent/Gid.kt) ----------------

// LegacyGID is the id a message without one gets: the same on every device and every load
// (Gid.legacy: "l" + 16 hex of sha256(role \0 at \0 text)).
func LegacyGID(role string, at int64, text string) string {
	d := sha256.Sum256([]byte(role + "\x00" + strconv.FormatInt(at, 10) + "\x00" + text))
	return "l" + hex.EncodeToString(d[:8])
}

// ValidGID: [A-Za-z0-9._-], at most 64 characters (Gid.valid).
func ValidGID(g string) bool {
	if g == "" || len(g) > 64 {
		return false
	}
	for _, c := range g {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// MsgItemID is "<chat>:<gid>" when it is a valid item id.
func MsgItemID(chat, gid string) (string, bool) {
	id := chat + ":" + gid
	return id, IDRe.MatchString(id) && ValidGID(gid)
}

// SplitMsgItemID is (chat, gid) of a "msg" item id.
func SplitMsgItemID(id string) (string, string, bool) {
	i := strings.LastIndexByte(id, ':')
	if i <= 0 || i == len(id)-1 {
		return "", "", false
	}
	return id[:i], id[i+1:], true
}

// SyncableChat: a valid item id and not a private chat.
func SyncableChat(id string) bool { return IDRe.MatchString(id) && !strings.HasPrefix(id, "private") }

func msgData(m ChatMsg) map[string]any {
	d := map[string]any{"gid": m.GID, "role": m.Role, "text": m.Text}
	if m.At > 0 {
		d["at"] = m.At
	}
	return d
}

func msgFromData(gid string, d map[string]any) ChatMsg {
	return ChatMsg{GID: gid, Role: optString(d, "role"), Text: optString(d, "text"), At: optLong(d, "at")}
}

// ---------------- kind "chat" ----------------

// ChatHeadAdapter is kind "chat".
type ChatHeadAdapter struct{ Store ChatStore }

func (c *ChatHeadAdapter) Kind() string { return KindChat }

func headStamp(h ChatHead) string {
	return shortHash(canon(map[string]any{"title": h.Title, "cleared_at": h.ClearedAt}), 12)
}

func (c *ChatHeadAdapter) Stamps() map[string]string {
	out := map[string]string{}
	for _, id := range c.Store.Chats() {
		if h, ok := c.Store.Head(id); ok {
			out[id] = headStamp(h)
		}
	}
	return out
}

func (c *ChatHeadAdapter) Stamp(id string) (string, bool) {
	if !SyncableChat(id) {
		return "", false
	}
	h, ok := c.Store.Head(id)
	if !ok {
		return "", false
	}
	return headStamp(h), true
}

func (c *ChatHeadAdapter) Read(id string) (Item, bool) {
	if !SyncableChat(id) {
		return Item{}, false
	}
	h, ok := c.Store.Head(id)
	if !ok {
		return Item{}, false
	}
	d := map[string]any{"id": id, "title": h.Title, "updated": h.Updated, "messages": []any{}, "split": 1}
	if h.ClearedAt > 0 {
		d["cleared_at"] = h.ClearedAt
	}
	return Item{Kind: KindChat, ID: id, T: h.Updated, Data: d}, true
}

func (c *ChatHeadAdapter) Apply(it Item) error {
	if !SyncableChat(it.ID) {
		return nil
	}
	if it.Deleted() {
		return c.Store.Delete(it.ID)
	}
	h, _ := c.Store.Head(it.ID)
	h.ID = it.ID
	if t := strings.TrimSpace(optString(it.Data, "title")); t != "" {
		h.Title = t
	}
	if u := optLong(it.Data, "updated"); u > h.Updated {
		h.Updated = u
	}
	if cl := optLong(it.Data, "cleared_at"); cl > h.ClearedAt {
		h.ClearedAt = cl
	}
	if err := c.Store.PutHead(h); err != nil {
		return err
	}
	// Messages carried in the chat item (whole chats from older phones, the newest 20 from newer).
	arr, _ := it.Data["messages"].([]any)
	seen := map[string]bool{}
	for _, x := range arr {
		m, ok := x.(map[string]any)
		if !ok {
			continue
		}
		role := optString(m, "role")
		if role != RoleUser && role != RoleBlaze || optString(m, "text") == "" {
			continue
		}
		g := optString(m, "gid")
		if !ValidGID(g) {
			base := LegacyGID(role, optLong(m, "at"), optString(m, "text"))
			g = base
			for n := 2; seen[g]; n++ {
				g = base + "-" + strconv.Itoa(n)
			}
		}
		seen[g] = true
		if cl := h.ClearedAt; cl > 0 {
			if at := optLong(m, "at"); at > 0 && at <= cl {
				continue
			}
		}
		if err := putIfBetter(c.Store, it.ID, msgFromData(g, m)); err != nil {
			return err
		}
	}
	return nil
}

func (c *ChatHeadAdapter) Shrink(it Item, fits func(Item) bool) (Item, bool) {
	d := map[string]any{}
	for k, v := range it.Data {
		d[k] = v
	}
	d["messages"] = []any{}
	small := Item{Kind: it.Kind, ID: it.ID, T: it.T, Data: d}
	return small, fits(small)
}
func (c *ChatHeadAdapter) PushDeletes() bool { return true }
func (c *ChatHeadAdapter) Flush() error      { return nil }

// putIfBetter writes m unless the copy here is the same or fuller.
func putIfBetter(s ChatStore, chat string, m ChatMsg) error {
	if ms, ok := s.Messages(chat); ok {
		for _, h := range ms {
			if h.GID != m.GID {
				continue
			}
			hd, md := msgData(h), msgData(m)
			if canon(hd) == canon(md) || better(hd, md) {
				return nil
			}
			break
		}
	}
	return s.PutMsg(chat, m)
}

// ---------------- kind "msg" ----------------

// MessageAdapter is kind "msg". DeletedHere(chat) is true when the chat was deleted on this
// device (a late message must not bring it back).
type MessageAdapter struct {
	Store       ChatStore
	DeletedHere func(chat string) bool

	ranks map[string]int // item id → age in its chat (0 = newest), from the last Stamps
}

func (m *MessageAdapter) Kind() string { return KindMessage }

func msgStamp(c ChatMsg) string { return shortHash(canon(msgData(c)), 12) }

func (m *MessageAdapter) Stamps() map[string]string {
	out := map[string]string{}
	m.ranks = map[string]int{}
	for _, chat := range m.Store.Chats() {
		ms, _ := m.Store.Messages(chat)
		for i, c := range ms {
			if id, ok := MsgItemID(chat, c.GID); ok {
				out[id] = msgStamp(c)
				m.ranks[id] = len(ms) - 1 - i
			}
		}
	}
	return out
}

// Rank is how many newer messages the item's chat holds (0 = the newest), as of the last
// Stamps; an unknown id ranks 0. The engine backfills high ranks slowly (Engine.Window).
func (m *MessageAdapter) Rank(id string) int { return m.ranks[id] }

func (m *MessageAdapter) find(id string) (string, ChatMsg, bool) {
	chat, gid, ok := SplitMsgItemID(id)
	if !ok || !SyncableChat(chat) {
		return "", ChatMsg{}, false
	}
	ms, _ := m.Store.Messages(chat)
	for _, c := range ms {
		if c.GID == gid {
			return chat, c, true
		}
	}
	return chat, ChatMsg{}, false
}

func (m *MessageAdapter) Stamp(id string) (string, bool) {
	_, c, ok := m.find(id)
	if !ok {
		return "", false
	}
	return msgStamp(c), true
}

func (m *MessageAdapter) Read(id string) (Item, bool) {
	_, c, ok := m.find(id)
	if !ok {
		return Item{}, false
	}
	t := c.At
	if t <= 0 {
		t = 1
	}
	return Item{Kind: KindMessage, ID: id, T: t, Data: msgData(c)}, true
}

func (m *MessageAdapter) Apply(it Item) error {
	chat, gid, ok := SplitMsgItemID(it.ID)
	if !ok || !SyncableChat(chat) || !ValidGID(gid) {
		return nil
	}
	if it.Deleted() {
		return m.Store.RemoveMsg(chat, gid)
	}
	if m.DeletedHere != nil && m.DeletedHere(chat) {
		return nil
	}
	c := msgFromData(gid, it.Data)
	if c.Role != RoleUser && c.Role != RoleBlaze || c.Text == "" {
		return nil
	}
	if h, ok := m.Store.Head(chat); ok && h.ClearedAt > 0 && c.At > 0 && c.At <= h.ClearedAt {
		return nil
	}
	return m.Store.PutMsg(chat, c)
}

func (m *MessageAdapter) Shrink(it Item, fits func(Item) bool) (Item, bool) {
	text := optString(it.Data, "text")
	r := []rune(text)
	for n := len(r) / 2; n > 1000; n /= 2 {
		d := map[string]any{}
		for k, v := range it.Data {
			d[k] = v
		}
		d["text"] = string(r[:n]) + "…"
		small := Item{Kind: it.Kind, ID: it.ID, T: it.T, Data: d}
		if fits(small) {
			return small, true
		}
	}
	return Item{}, false
}
func (m *MessageAdapter) PushDeletes() bool { return false }
func (m *MessageAdapter) Flush() error      { return nil }

// ChatAdapters are the chat and message adapters over one store, with the phone's
// "deleted here" rule wired to the sync state.
func ChatAdapters(s ChatStore, st *State) []Adapter {
	head := &ChatHeadAdapter{Store: s}
	msg := &MessageAdapter{Store: s, DeletedHere: func(chat string) bool {
		e, ok := st.Get(KindChat, chat)
		if !ok {
			return false
		}
		_, here := s.Head(chat)
		return e.Deleted || !here
	}}
	return []Adapter{head, msg}
}

// sortedKeys is a helper for deterministic output.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
