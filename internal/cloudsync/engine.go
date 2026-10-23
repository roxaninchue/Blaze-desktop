package cloudsync

import (
	"context"
	"errors"
	"sort"
	"time"
)

// One sync round, ported from Blaze Android connect/SyncEngine.kt (keep in step).
//
// A round: read the account's sync meta → pull every page after the saved cursor and apply what
// changed elsewhere (merging where this device changed the same item) → push what changed here
// since the last round, rebasing on conflicts → save the state. Changes are found by comparing
// each item's cheap local stamp with the stamp saved after the last good sync, so no store needs
// a change log; an item the state knows but the store no longer has was deleted here and is
// pushed as a tombstone (once).

// Api is the gateway (the /v1/sync routes). *Client implements it.
type Api interface {
	Meta(ctx context.Context) (Meta, error)
	Pull(ctx context.Context, since int64, limit int) (Page, error)
	Push(ctx context.Context, items []Out) (PushResult, error)
}

// Adapter is one kind of thing on this device (memory, chats, messages).
type Adapter interface {
	Kind() string
	// Stamps is every item here: id → a cheap fingerprint that changes when the item does.
	Stamps() map[string]string
	// Stamp is one item's fingerprint (false when it isn't here).
	Stamp(id string) (string, bool)
	// Read is the item as it is now (false when gone).
	Read(id string) (Item, bool)
	// Apply stores a copy from another device; for a tombstone, deletes it here.
	Apply(it Item) error
	// Shrink makes an item that doesn't fit the per-item limit smaller (false = skip it).
	Shrink(it Item, fits func(Item) bool) (Item, bool)
	// PushDeletes false: an item that left this device is NOT pushed as a delete (messages).
	PushDeletes() bool
	// Flush writes buffered applies (after the pull, after conflicts, at the end).
	Flush() error
}

// Gateway limits (gateway src/sync.ts SYNC_LIMITS).
const (
	ItemLimit = 512 * 1024      // base64 characters per item
	PushItems = 200             // items per push
	PushBytes = 3 * 1024 * 1024 // keep a push under ~3 MB
	PullLimit = 200
	rounds    = 3
)

var (
	// ErrNotSetUp: the account has no cloud copy (deleted from another device): turn sync off here.
	ErrNotSetUp = errors.New("sync: the account's cloud copy is not set up")
	// ErrWrongKey: the cloud copy was reset with another passphrase: ask for it again.
	ErrWrongKey = errors.New("sync: the cloud copy uses another passphrase")
)

// Report is what one round did.
type Report struct {
	Pulled, Pushed, Merged, FailedOpen int
	Skipped                            []string
	Full                               bool // account copy is over quota (25 MB)
	// Backlog is how many older items (beyond each chat's newest Window messages) are still
	// waiting for a later round's backfill.
	Backlog int
}

// Ranker is an adapter whose items have an age rank inside their group (0 = newest message of
// its chat). The engine pushes items ranked below Engine.Window at once and the older ones as a
// slow backfill, so a first sync of a long history doesn't burn the gateway's daily D1 writes.
// Rank is valid after Stamps.
type Ranker interface {
	Rank(id string) int
}

// Engine runs rounds for one device.
type Engine struct {
	API      Api
	Adapters []Adapter
	Key      []byte
	State    *State
	Now      func() time.Time // nil = time.Now
	Sleep    func(time.Duration)

	// Window: items of a Ranker adapter ranked >= Window (older than each chat's newest Window
	// messages) are backfill. 0 = no window, everything pushes at once.
	Window int
	// BackfillPushes is the most pushes of backfill items in one round (newest first). 0 = none
	// this round (the runner allows backfill on some rounds only).
	BackfillPushes int

	byKind map[string]Adapter
	rep    Report
}

func (e *Engine) now() int64 {
	if e.Now != nil {
		return e.Now().UnixMilli()
	}
	return time.Now().UnixMilli()
}

func (e *Engine) seal(i Item) (string, error) { return Seal(e.Key, i.Kind, i.ID, i.Envelope()) }

func (e *Engine) openRemote(r Remote) (Item, bool) {
	if r.Deleted || r.Blob == nil || *r.Blob == "" {
		return Item{Kind: r.Kind, ID: r.ID, T: r.UpdatedAt}, true
	}
	plain, err := Open(e.Key, r.Kind, r.ID, *r.Blob)
	if err != nil {
		e.rep.FailedOpen++
		return Item{}, false
	}
	it, err := ParseItem(r.Kind, r.ID, plain)
	if err != nil {
		e.rep.FailedOpen++
		return Item{}, false
	}
	return it, true
}

// Run is one full round. ErrNotSetUp / ErrWrongKey as on the phone.
func (e *Engine) Run(ctx context.Context) (Report, error) {
	e.rep = Report{}
	e.byKind = map[string]Adapter{}
	for _, a := range e.Adapters {
		e.byKind[a.Kind()] = a
	}
	if len(e.Key) != 32 {
		return e.rep, errors.New("sync: no key")
	}
	m, err := e.API.Meta(ctx)
	if err != nil {
		return e.rep, err
	}
	if !m.SetUp {
		return e.rep, ErrNotSetUp
	}
	if m.Epoch != e.State.Epoch {
		// A new cloud copy (first round, or another device reset it): check the key, then start over.
		if m.Check != "" && !Checks(e.Key, m.Check) {
			return e.rep, ErrWrongKey
		}
		e.State.ClearItems()
		e.State.Cursor = 0
		e.State.Epoch = m.Epoch
	}
	if err := e.pullAll(ctx); err != nil {
		return e.rep, err
	}
	if err := e.flushAll(); err != nil {
		return e.rep, err
	}
	perr := e.pushAll(ctx)
	ferr := e.flushAll()
	if perr != nil {
		return e.rep, perr
	}
	if ferr != nil {
		return e.rep, ferr
	}
	return e.rep, e.State.Save()
}

func (e *Engine) flushAll() error {
	var first error
	for _, a := range e.Adapters {
		if err := a.Flush(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// ---------------- pull ----------------

func (e *Engine) pullAll(ctx context.Context) error {
	since := e.State.Cursor
	for guard := 0; guard < 10_000; guard++ {
		p, err := e.API.Pull(ctx, since, PullLimit)
		if err != nil {
			return err
		}
		for _, r := range p.Items {
			if err := e.take(r); err != nil {
				return err
			}
		}
		since = p.Next
		e.State.Cursor = since
		if !p.More || len(p.Items) == 0 {
			break
		}
	}
	return nil
}

func (e *Engine) stampOf(a Adapter, id string) string {
	s, _ := a.Stamp(id)
	return s
}

// take handles one copy from the server (pulled, or the server's side of a push conflict).
func (e *Engine) take(r Remote) error {
	a := e.byKind[r.Kind]
	if a == nil {
		return nil // a kind this device doesn't sync (settings, designs, a newer Blaze's)
	}
	st, have := e.State.Get(r.Kind, r.ID)
	if have && st.Rev >= r.Rev && !st.Skipped {
		return nil // already have it (our own push coming back)
	}
	remote, ok := e.openRemote(r)
	if !ok {
		return nil
	}
	stampNow, here := a.Stamp(r.ID)
	var changedHere bool
	switch {
	case here && have:
		changedHere = stampNow != st.Stamp
	case here:
		changedHere = true
	case have:
		changedHere = !st.Deleted
	}
	if !changedHere {
		if !(remote.Deleted() && !here) {
			if err := a.Apply(remote); err != nil {
				return err
			}
		}
		e.rep.Pulled++
		e.State.Put(r.Kind, r.ID, Entry{Rev: r.Rev, Stamp: e.stampOf(a, r.ID), T: remote.T, Deleted: remote.Deleted()})
		return nil
	}
	// Changed on both sides since the last sync.
	local, ok := a.Read(r.ID)
	if !ok {
		t := st.T
		if t <= 0 {
			t = e.now()
		}
		local = Item{Kind: r.Kind, ID: r.ID, T: t}
	}
	if sameContent(local, remote) {
		// Both sides already hold the same thing (a lost state file, or the same edit made twice):
		// nothing to apply or push.
		e.State.Put(r.Kind, r.ID, Entry{Rev: r.Rev, Stamp: stampNow, T: remote.T, Deleted: remote.Deleted()})
		return nil
	}
	out, merged := Resolve(local, remote)
	switch out {
	case TakeRemote:
		if err := a.Apply(remote); err != nil {
			return err
		}
		e.rep.Pulled++
		e.State.Put(r.Kind, r.ID, Entry{Rev: r.Rev, Stamp: e.stampOf(a, r.ID), T: remote.T, Deleted: remote.Deleted()})
	case KeepLocal:
		// Push ours on top of the server's rev: the old stamp stays, so it still counts as changed.
		e.State.Put(r.Kind, r.ID, Entry{Rev: r.Rev, Stamp: st.Stamp, T: st.T, Deleted: st.Deleted})
	case Merged:
		if err := a.Apply(merged); err != nil {
			return err
		}
		e.rep.Merged++
		// Applied here, still to be pushed: a stamp that can't match makes the push pick it up.
		e.State.Put(r.Kind, r.ID, Entry{Rev: r.Rev, Stamp: "merged", T: merged.T})
	}
	return nil
}

// ---------------- push ----------------

type outItem struct {
	out   Out
	stamp string
}

func (e *Engine) fits(i Item) bool {
	b, err := e.seal(i)
	return err == nil && len(b) <= ItemLimit
}

type rankedItem struct {
	a     Adapter
	id    string
	stamp string
	rev   int64
	rank  int
}

// prepare seals one changed item for a push (shrunk when it is over the item limit; false when
// it can't be sent, which marks it skipped).
func (e *Engine) prepare(a Adapter, id, stamp string, rev int64) (outItem, bool, error) {
	item, ok := a.Read(id)
	if !ok {
		return outItem{}, false, nil
	}
	if item.T <= 0 {
		item.T = e.now()
	}
	blob, err := e.seal(item)
	if err != nil {
		return outItem{}, false, err
	}
	if len(blob) > ItemLimit {
		small, ok := a.Shrink(item, e.fits)
		if !ok {
			e.rep.Skipped = append(e.rep.Skipped, a.Kind()+"/"+id)
			e.State.Put(a.Kind(), id, Entry{Rev: rev, Stamp: stamp, T: item.T, Skipped: true})
			return outItem{}, false, nil
		}
		if blob, err = e.seal(small); err != nil {
			return outItem{}, false, err
		}
	}
	return outItem{Out{Kind: a.Kind(), ID: id, BaseRev: rev, Blob: blob}, stamp}, true, nil
}

func (e *Engine) pushAll(ctx context.Context) error {
	backfill := e.BackfillPushes
	for round := 0; round < rounds; round++ {
		var out []outItem
		var older []rankedItem
		for _, a := range e.Adapters {
			ranker, _ := a.(Ranker)
			if e.Window <= 0 {
				ranker = nil
			}
			stamps := a.Stamps()
			ids := make([]string, 0, len(stamps))
			for id := range stamps {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				stamp := stamps[id]
				st, have := e.State.Get(a.Kind(), id)
				if have && st.Stamp == stamp && !st.Deleted {
					continue
				}
				if have && st.Skipped && st.Stamp == stamp {
					continue
				}
				if ranker != nil {
					if r := ranker.Rank(id); r >= e.Window {
						older = append(older, rankedItem{a, id, stamp, st.Rev, r})
						continue
					}
				}
				o, ok, err := e.prepare(a, id, stamp, st.Rev)
				if err != nil {
					return err
				}
				if ok {
					out = append(out, o)
				}
			}
			// Gone here since the last sync: a tombstone (once).
			if a.PushDeletes() {
				for _, id := range e.State.IDs(a.Kind()) {
					if _, ok := stamps[id]; ok {
						continue
					}
					st, _ := e.State.Get(a.Kind(), id)
					if st.Deleted {
						continue
					}
					out = append(out, outItem{Out{Kind: a.Kind(), ID: id, BaseRev: st.Rev, Deleted: true}, ""})
				}
			}
		}
		all := batches(out)
		e.rep.Backlog = 0
		if len(older) > 0 {
			// Backfill: newest first across chats, at most `backfill` pushes over the whole round.
			// Only the items that can go this round are sealed.
			sort.SliceStable(older, func(i, j int) bool {
				if older[i].rank != older[j].rank {
					return older[i].rank < older[j].rank
				}
				return older[i].id < older[j].id
			})
			var ol []outItem
			taken := 0
			for _, r := range older {
				if len(ol) >= backfill*PushItems {
					break
				}
				taken++
				o, ok, err := e.prepare(r.a, r.id, r.stamp, r.rev)
				if err != nil {
					return err
				}
				if ok {
					ol = append(ol, o)
				}
			}
			ob := batches(ol)
			n := backfill
			if n > len(ob) {
				n = len(ob)
			}
			if n < 0 {
				n = 0
			}
			e.rep.Backlog = len(older) - taken
			for _, b := range ob[n:] {
				e.rep.Backlog += len(b)
			}
			all = append(all, ob[:n]...)
			backfill -= n
		}
		if len(all) == 0 {
			return nil
		}
		conflicts := 0
		for _, batch := range all {
			items := make([]Out, len(batch))
			stampOf := map[string]outItem{}
			for i, b := range batch {
				items[i] = b.out
				stampOf[skey(b.out.Kind, b.out.ID)] = b
			}
			res, err := e.pushWithRetry(ctx, items)
			if err != nil {
				return err
			}
			for _, ac := range res.Accepted {
				b := stampOf[skey(ac.Kind, ac.ID)]
				e.rep.Pushed++
				e.State.Put(ac.Kind, ac.ID, Entry{Rev: ac.Rev, Stamp: b.stamp, T: e.now(), Deleted: b.out.Blob == ""})
			}
			for _, rj := range res.Rejected {
				if rj.Error == "quota_exceeded" {
					e.rep.Full = true
				} else {
					e.rep.Skipped = append(e.rep.Skipped, rj.Kind+"/"+rj.ID)
				}
				st, _ := e.State.Get(rj.Kind, rj.ID)
				// Don't send it again every round; it goes again when it changes (or after RetrySkipped).
				e.State.Put(rj.Kind, rj.ID, Entry{Rev: st.Rev, Stamp: stampOf[skey(rj.Kind, rj.ID)].stamp, T: st.T, Skipped: true})
			}
			for _, c := range res.Conflicts {
				conflicts++
				if err := e.take(c); err != nil {
					return err
				}
			}
		}
		if conflicts == 0 {
			return nil
		}
	}
	return nil
}

func batches(out []outItem) [][]outItem {
	var res [][]outItem
	var cur []outItem
	bytes := 0
	for _, o := range out {
		n := len(o.out.Blob)
		if len(cur) > 0 && (len(cur) >= PushItems || bytes+n > PushBytes) {
			res = append(res, cur)
			cur, bytes = nil, 0
		}
		cur = append(cur, o)
		bytes += n
	}
	if len(cur) > 0 {
		res = append(res, cur)
	}
	return res
}

func (e *Engine) pushWithRetry(ctx context.Context, items []Out) (PushResult, error) {
	sleep := e.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	var last error
	for i := 0; i < 3; i++ {
		res, err := e.API.Push(ctx, append([]Out(nil), items...))
		if err == nil {
			return res, nil
		}
		if !errors.Is(err, ErrBusy) {
			return res, err
		}
		last = err
		sleep(time.Duration(300*(i+1)) * time.Millisecond)
	}
	return PushResult{}, last
}

// sameContent: both copies say the same (both deleted, or equal data; for a chat item only the
// fields its stamp covers, since "updated" moves on every save).
func sameContent(a, b Item) bool {
	if a.Deleted() || b.Deleted() {
		return a.Deleted() && b.Deleted()
	}
	if a.Kind == KindChat {
		return optString(a.Data, "title") == optString(b.Data, "title") && optLong(a.Data, "cleared_at") == optLong(b.Data, "cleared_at") &&
			len(asList(b.Data["messages"])) == 0
	}
	return canon(a.Data) == canon(b.Data)
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}
