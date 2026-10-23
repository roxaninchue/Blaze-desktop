package cloudsync

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Kinds Blaze Android syncs (connect/SyncAdapters.kt SyncKinds, ChatItems.kt).
const (
	KindChat     = "chat"     // a chat's own fields (title, updated…), ChatHeadAdapter
	KindMessage  = "msg"      // one message, id "<chat>:<gid>", MessageAdapter (never pushed as a delete)
	KindReaction = "react"    // one reaction, id "<chat>:<gid>:<by>:<emoji code points>"
	KindMemory   = "memory"   // one memory line, id MemoryID(text)
	KindSettings = "settings" // one synced pref per item (phone-only keys; Desktop ignores them)
	KindDesign   = "design"   // pages / posters (phone-only for now)
)

// IDRe is the gateway's (and the phone's) item id rule.
var IDRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// Item is one synced thing; Data is nil for a tombstone.
type Item struct {
	Kind string
	ID   string
	T    int64 // the item's own change time, ms (last writer wins on it)
	Data map[string]any
}

// Deleted is true for a tombstone.
func (i Item) Deleted() bool { return i.Data == nil }

// marshal is json.Marshal without HTML escaping (Android's org.json doesn't escape <>&).
func marshal(v any) string {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(v)
	return strings.TrimSuffix(b.String(), "\n")
}

// Envelope is the plaintext that goes into the blob (SyncMerge.Item.envelope):
// {"v":1,"kind":…,"id":…,"t":…,"data":{…}} or {…,"del":true}. Field order follows the phone.
func (i Item) Envelope() string {
	var b strings.Builder
	b.WriteString(`{"v":1,"kind":`)
	b.WriteString(marshal(i.Kind))
	b.WriteString(`,"id":`)
	b.WriteString(marshal(i.ID))
	b.WriteString(`,"t":`)
	b.WriteString(strconv.FormatInt(i.T, 10))
	if i.Data == nil {
		b.WriteString(`,"del":true}`)
	} else {
		b.WriteString(`,"data":`)
		b.WriteString(marshal(i.Data))
		b.WriteString(`}`)
	}
	return b.String()
}

// decodeObj decodes JSON keeping numbers exact (ms times are past 2^53 only in theory, but ids
// and counters must round-trip untouched).
func decodeObj(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(out)
}

// ParseItem reads an envelope; it refuses one made by a newer Blaze or one whose
// kind / id don't match the item it came in (SyncMerge.Item.parse).
func ParseItem(kind, id, plain string) (Item, error) {
	var e struct {
		V    *json.Number    `json:"v"`
		Kind *string         `json:"kind"`
		ID   *string         `json:"id"`
		T    json.Number     `json:"t"`
		Data json.RawMessage `json:"data"`
		Del  bool            `json:"del"`
	}
	if err := decodeObj([]byte(plain), &e); err != nil {
		return Item{}, err
	}
	if e.V != nil {
		if v, err := e.V.Int64(); err == nil && v > 1 {
			return Item{}, errors.New("made by a newer Blaze")
		}
	}
	if (e.Kind != nil && *e.Kind != kind) || (e.ID != nil && *e.ID != id) {
		return Item{}, errors.New("item mismatch")
	}
	t, _ := e.T.Int64()
	it := Item{Kind: kind, ID: id, T: t}
	if !e.Del {
		it.Data = map[string]any{}
		if len(e.Data) > 0 && string(e.Data) != "null" {
			if err := decodeObj(e.Data, &it.Data); err != nil {
				return Item{}, err
			}
		}
	}
	return it, nil
}

// ---------------- small JSON helpers (org.json optString / optLong) ----------------

func optString(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return v
	case nil:
		return ""
	case json.Number:
		return v.String()
	case bool:
		return strconv.FormatBool(v)
	default:
		return marshal(v)
	}
}

func optLong(m map[string]any, k string) int64 {
	switch v := m[k].(type) {
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n
		}
		if f, err := v.Float64(); err == nil {
			return int64(f)
		}
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return n
	}
	return 0
}

// canon is a stable string for a JSON value: object keys sorted (SyncMerge.canon), used for
// "same content" checks and tie-breaks.
func canon(v any) string {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(marshal(k))
			b.WriteByte(':')
			b.WriteString(canon(x[k]))
		}
		b.WriteByte('}')
		return b.String()
	case []any:
		var b strings.Builder
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(canon(e))
		}
		b.WriteByte(']')
		return b.String()
	case nil:
		return ""
	default:
		return marshal(x)
	}
}

// jlen is Kotlin String.length (UTF-16 code units), so "the fuller copy" picks the same side
// on the phone and here.
func jlen(s string) int { return len(utf16.Encode([]rune(s))) }

func shortHash(s string, n int) string {
	d := sha256.Sum256([]byte(s))
	return hex.EncodeToString(d[:n])
}

// ---------------- memory ids ----------------

var nonWord = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// MemoryNorm is MemoryAdapter.norm: lower case, runs of anything but letters and digits become one space.
func MemoryNorm(text string) string {
	return strings.TrimSpace(nonWord.ReplaceAllString(strings.ToLower(text), " "))
}

// MemoryID is MemoryAdapter.idOf: the same fact on two devices is one item.
func MemoryID(text string) string {
	d := sha256.Sum256([]byte(MemoryNorm(text)))
	return "m" + hex.EncodeToString(d[:16])
}

// MemoryItem is one saved memory line as the phone syncs it.
func MemoryItem(kind, subject, text, related string, updatedMs int64) Item {
	return Item{Kind: KindMemory, ID: MemoryID(text), T: updatedMs, Data: map[string]any{
		"kind": kind, "subject": subject, "text": text, "related": related,
	}}
}
