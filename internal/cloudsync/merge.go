package cloudsync

import (
	"math"
	"sort"
)

// The merge rules, ported from Blaze Android connect/SyncMerge.kt (keep in step):
//
//   - Every item: last writer wins by the item's own change time (Item.T, ms). A tie goes to the
//     copy whose content sorts higher, so two devices always settle on the same one.
//   - A delete is an item too (a tombstone): it wins over an older copy and loses to a newer one.
//   - One message ("msg") edited on two devices: the fuller copy wins (a reply that finished
//     streaming beats its first half).
//   - A chat item ("chat"): fields from the copy with the newer "updated", a field only the older
//     copy has is kept; messages carried in it (older phones send whole chats) are a union by gid.

// Outcome of resolving one item changed on both sides since the last sync.
type Outcome int

const (
	KeepLocal  Outcome = iota // keep what is here (and push it)
	TakeRemote                // take the other copy as it is
	Merged                    // apply the merge here and push it
)

func weight(i Item) string {
	if i.Data == nil {
		return ""
	}
	return canon(i.Data)
}

// Resolve picks the winner of local vs remote (Merged returns the merged item).
func Resolve(local, remote Item) (Outcome, Item) {
	if local.Kind == KindMessage && !local.Deleted() && !remote.Deleted() {
		if canon(local.Data) == canon(remote.Data) {
			return TakeRemote, remote
		}
		if better(local.Data, remote.Data) {
			return KeepLocal, local
		}
		return TakeRemote, remote
	}
	if local.Kind == KindChat && !local.Deleted() && !remote.Deleted() {
		m := mergeChatHead(local.Data, remote.Data)
		t := local.T
		if remote.T > t {
			t = remote.T
		}
		switch canon(m) {
		case canon(remote.Data):
			return TakeRemote, remote
		case canon(local.Data):
			return KeepLocal, local
		}
		return Merged, Item{Kind: KindChat, ID: local.ID, T: t, Data: m}
	}
	switch {
	case local.T > remote.T:
		return KeepLocal, local
	case remote.T > local.T:
		return TakeRemote, remote
	case weight(local) > weight(remote):
		return KeepLocal, local
	}
	return TakeRemote, remote
}

// better is true when a is the fuller copy of one message (SyncMerge.better): longer text,
// then longer JSON, then content order.
func better(a, b map[string]any) bool {
	la, lb := jlen(optString(a, "text")), jlen(optString(b, "text"))
	if la != lb {
		return la > lb
	}
	ca, cb := canon(a), canon(b)
	if jlen(ca) != jlen(cb) {
		return jlen(ca) > jlen(cb)
	}
	return ca >= cb
}

// mergeChatHead merges two copies of a chat item: the newer copy's fields over the older's,
// messages a union by gid (role@at for ones without), ordered by time.
func mergeChatHead(a, b map[string]any) map[string]any {
	newer, older := a, b
	if optLong(b, "updated") > optLong(a, "updated") {
		newer, older = b, a
	}
	out := map[string]any{}
	for k, v := range older {
		out[k] = v
	}
	for k, v := range newer {
		out[k] = v
	}
	cleared := optLong(a, "cleared_at")
	if c := optLong(b, "cleared_at"); c > cleared {
		cleared = c
	}
	byKey := map[string]map[string]any{}
	var order []string
	for _, src := range []map[string]any{older, newer} {
		arr, _ := src["messages"].([]any)
		for _, x := range arr {
			m, ok := x.(map[string]any)
			if !ok {
				continue
			}
			k := msgKey(m)
			if prev, ok := byKey[k]; ok {
				if better(m, prev) {
					byKey[k] = m
				}
				continue
			}
			byKey[k] = m
			order = append(order, k)
		}
	}
	var msgs []any
	for _, k := range order {
		m := byKey[k]
		if at := optLong(m, "at"); cleared > 0 && at > 0 && at <= cleared {
			continue
		}
		msgs = append(msgs, m)
	}
	sortByAt(msgs)
	if msgs == nil {
		msgs = []any{}
	}
	out["messages"] = msgs
	if cleared > 0 {
		out["cleared_at"] = cleared
	}
	u := optLong(a, "updated")
	if x := optLong(b, "updated"); x > u {
		u = x
	}
	out["updated"] = u
	return out
}

func msgKey(m map[string]any) string {
	if g := optString(m, "gid"); g != "" {
		return "g:" + g
	}
	return optString(m, "role") + "@" + optString(m, "at") + "#" + optString(m, "text")
}

// sortByAt is a stable sort by "at" (no time = after the timed ones, in their own order).
func sortByAt(ms []any) {
	key := func(x any) int64 {
		m, _ := x.(map[string]any)
		if t := optLong(m, "at"); t > 0 {
			return t
		}
		return math.MaxInt64
	}
	sort.SliceStable(ms, func(i, j int) bool { return key(ms[i]) < key(ms[j]) })
}
