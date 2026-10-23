package cloudsync

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ConvFiles is a ChatStore over Desktop's chat files (~/.blaze/desktop/chats/<id>.json, the
// app's Conv: id, project, title, folder, updated (RFC 3339), messages [llm.Message]). Unknown
// fields are kept as they are.
//
// Desktop messages have no gid or time yet, and the app rewrites a chat file from llm.Message
// (role, content, tool calls…), so anything extra written into a message is lost on its next
// save. So a message's gid is derived the phone's way (LegacyGID of role, time, text), and the
// gid + time of a message that came from another device are also kept in an alias file
// (AliasPath, outside the chats folder) keyed by chat, LegacyGID(role, 0, text) and occurrence:
// after the app re-saves the chat the message still has its original gid, so it is never sent
// back as a new message.
//
// Not safe against the running app holding the same chat in memory: the wiring must apply
// through the App (reload the live session) or pause saves while a round runs.
type ConvFiles struct {
	Dir       string
	AliasPath string

	mu    sync.Mutex
	alias map[string][]aliasEnt // chat \0 LegacyGID(role,0,text) → gids in occurrence order
	gen   int                   // bumps on every alias change (invalidates the message cache)
	cache map[string]msgCache   // chat → its derived messages, while the file is unchanged
}

// msgCache is one chat's derived messages for one version of its file. A round asks for every
// message of every chat several times; long chats would otherwise be re-read each time.
type msgCache struct {
	mod  time.Time
	size int64
	gen  int
	msgs []ChatMsg
}

type aliasEnt struct {
	GID string `json:"g"`
	At  int64  `json:"a,omitempty"`
}

const mainChat = "main"

func (c *ConvFiles) path(id string) string { return filepath.Join(c.Dir, id+".json") }

func (c *ConvFiles) loadAlias() {
	if c.alias != nil {
		return
	}
	c.alias = map[string][]aliasEnt{}
	if c.AliasPath == "" {
		return
	}
	if b, err := os.ReadFile(c.AliasPath); err == nil {
		_ = json.Unmarshal(b, &c.alias)
	}
}

func (c *ConvFiles) saveAlias() error {
	if c.AliasPath == "" {
		return nil
	}
	b, err := json.Marshal(c.alias)
	if err != nil {
		return err
	}
	return writeAtomic(c.AliasPath, b)
}

func writeAtomic(p string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".sync.tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// conv is one chat file: its raw fields and its messages.
type conv struct {
	raw  map[string]json.RawMessage
	msgs []map[string]any
}

func (c *ConvFiles) read(id string) (*conv, bool) {
	b, err := os.ReadFile(c.path(id))
	if err != nil {
		return nil, false
	}
	v := &conv{}
	if decodeObj(b, &v.raw) != nil {
		return nil, false
	}
	if m, ok := v.raw["messages"]; ok && string(m) != "null" {
		if decodeObj(m, &v.msgs) != nil {
			return nil, false
		}
	}
	return v, true
}

func (c *ConvFiles) write(id string, v *conv) error {
	delete(c.cache, id) // same mtime granularity on some file systems: never trust a cache across our own write
	if v.msgs == nil {
		v.msgs = []map[string]any{}
	}
	v.raw["messages"] = json.RawMessage(marshal(v.msgs))
	return writeAtomic(c.path(id), []byte(marshal(v.raw)))
}

func rawString(v *conv, k string) string {
	var s string
	_ = json.Unmarshal(v.raw[k], &s)
	return s
}

// mobileRole maps a Desktop message onto the phone's role ("" = stays on this device).
func mobileRole(m map[string]any) string {
	switch optString(m, "role") {
	case "user":
		if optString(m, "content") != "" {
			return RoleUser
		}
	case "assistant":
		if tc, ok := m["tool_calls"].([]any); ok && len(tc) > 0 {
			return ""
		}
		if optString(m, "content") != "" {
			return RoleBlaze
		}
	}
	return ""
}

func desktopRole(r string) string {
	if r == RoleUser {
		return "user"
	}
	return "assistant"
}

// derive gives every syncable message of a chat its gid: its own, else the alias of a message
// that came from another device, else LegacyGID (duplicates get -2, -3… as Gid.fill does).
// Returns the messages and, per message, its index in v.msgs.
func (c *ConvFiles) derive(id string, v *conv) ([]ChatMsg, []int) {
	var out []ChatMsg
	var idx []int
	seen := map[string]bool{}
	occ := map[string]int{}
	for i, m := range v.msgs {
		role := mobileRole(m)
		if role == "" {
			continue
		}
		text := optString(m, "content")
		at := optLong(m, "at")
		base0 := LegacyGID(role, 0, text)
		k := id + "\x00" + base0
		n := occ[k]
		occ[k] = n + 1
		g := optString(m, "gid")
		if !ValidGID(g) {
			g = ""
			if as := c.alias[k]; n < len(as) {
				g = as[n].GID
				if at == 0 {
					at = as[n].At
				}
			}
		}
		if g == "" || seen[g] {
			base := LegacyGID(role, at, text)
			g = base
			for j := 2; seen[g]; j++ {
				g = base + "-" + strconv.Itoa(j)
			}
		}
		seen[g] = true
		out = append(out, ChatMsg{GID: g, Role: role, Text: text, At: at})
		idx = append(idx, i)
	}
	return out, idx
}

func (c *ConvFiles) Chats() []string {
	ents, _ := os.ReadDir(c.Dir)
	var out []string
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".json") {
			continue
		}
		id := strings.TrimSuffix(n, ".json")
		if SyncableChat(id) {
			out = append(out, id)
		}
	}
	return out
}

func parseUpdated(v *conv) int64 {
	var t time.Time
	if json.Unmarshal(v.raw["updated"], &t) == nil && !t.IsZero() {
		return t.UnixMilli()
	}
	return 0
}

func (c *ConvFiles) Head(id string) (ChatHead, bool) {
	if !SyncableChat(id) {
		return ChatHead{}, false
	}
	v, ok := c.read(id)
	if !ok {
		return ChatHead{}, false
	}
	return ChatHead{ID: id, Title: rawString(v, "title"), Updated: parseUpdated(v)}, true
}

func (c *ConvFiles) Messages(id string) ([]ChatMsg, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loadAlias()
	fi, err := os.Stat(c.path(id))
	if err != nil {
		return nil, false
	}
	if mc, ok := c.cache[id]; ok && mc.mod.Equal(fi.ModTime()) && mc.size == fi.Size() && mc.gen == c.gen {
		return mc.msgs, true
	}
	v, ok := c.read(id)
	if !ok {
		return nil, false
	}
	ms, _ := c.derive(id, v)
	if c.cache == nil {
		c.cache = map[string]msgCache{}
	}
	c.cache[id] = msgCache{mod: fi.ModTime(), size: fi.Size(), gen: c.gen, msgs: ms}
	return ms, true
}

func (c *ConvFiles) newConv(id string) *conv {
	return &conv{raw: map[string]json.RawMessage{
		"id": json.RawMessage(marshal(id)), "project": json.RawMessage(`""`), "title": json.RawMessage(`""`),
		"folder": json.RawMessage(`""`), "updated": json.RawMessage(marshal(time.Now().UTC().Format(time.RFC3339Nano))),
	}}
}

func setUpdated(v *conv, ms int64) {
	if ms > parseUpdated(v) {
		v.raw["updated"] = json.RawMessage(marshal(time.UnixMilli(ms).UTC().Format(time.RFC3339Nano)))
	}
}

func (c *ConvFiles) PutHead(h ChatHead) error {
	if !SyncableChat(h.ID) {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.read(h.ID)
	if !ok {
		v = c.newConv(h.ID)
	}
	if h.Title != "" {
		v.raw["title"] = json.RawMessage(marshal(h.Title))
	}
	setUpdated(v, h.Updated)
	return c.write(h.ID, v)
}

func (c *ConvFiles) PutMsg(chat string, m ChatMsg) error {
	if !SyncableChat(chat) || !ValidGID(m.GID) {
		return errors.New("cloudsync: bad chat or gid")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loadAlias()
	v, ok := c.read(chat)
	if !ok {
		v = c.newConv(chat)
	}
	ms, idx := c.derive(chat, v)
	nm := map[string]any{"role": desktopRole(m.Role), "content": m.Text, "gid": m.GID}
	if m.At > 0 {
		nm["at"] = m.At
	}
	replaced := false
	for i, h := range ms {
		if h.GID == m.GID {
			v.msgs[idx[i]] = nm
			replaced = true
			break
		}
	}
	if !replaced {
		pos := len(v.msgs)
		if m.At > 0 {
			for i, x := range v.msgs {
				if t := optLong(x, "at"); t > m.At {
					pos = i
					break
				}
			}
		}
		v.msgs = append(v.msgs[:pos], append([]map[string]any{nm}, v.msgs[pos:]...)...)
	}
	setUpdated(v, m.At)
	if err := c.write(chat, v); err != nil {
		return err
	}
	// Remember the gid for when the app re-saves this chat without it: recompute the occurrence
	// of this message's base among the chat's messages as they are now.
	base0 := chat + "\x00" + LegacyGID(m.Role, 0, m.Text)
	n := 0
	for _, x := range v.msgs {
		if mobileRole(x) == "" || chat+"\x00"+LegacyGID(mobileRole(x), 0, optString(x, "content")) != base0 {
			continue
		}
		if optString(x, "gid") == m.GID {
			break
		}
		n++
	}
	as := c.alias[base0]
	for len(as) <= n {
		as = append(as, aliasEnt{})
	}
	as[n] = aliasEnt{GID: m.GID, At: m.At}
	c.alias[base0] = as
	c.gen++
	delete(c.cache, chat)
	return c.saveAlias()
}

func (c *ConvFiles) RemoveMsg(chat, gid string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loadAlias()
	v, ok := c.read(chat)
	if !ok {
		return nil
	}
	ms, idx := c.derive(chat, v)
	for i, h := range ms {
		if h.GID == gid {
			j := idx[i]
			v.msgs = append(v.msgs[:j], v.msgs[j+1:]...)
			return c.write(chat, v)
		}
	}
	return nil
}

// Delete removes a chat file. The one main conversation is never deleted by sync.
func (c *ConvFiles) Delete(id string) error {
	if id == mainChat || !SyncableChat(id) {
		return nil
	}
	err := os.Remove(c.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
