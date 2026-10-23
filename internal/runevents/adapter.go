// Package runevents is the ONE place that reads a run's events for the chat's working pill and
// the delivery status under the user's message (0.19.16).
//
// Everything that knows the gateway's wire format is in this file. It speaks the Site contract
// STEPS_DELIVERY_CONTRACT.md (gateway 0.8.45, final 2026-10-23 11:08) and falls back to a stub for
// an older gateway that has no delivery fields:
//   - Sent:  the 2xx of POST /v1/runs (its `received`), or the message_received event.
//   - Read:  the message_read event, or `read != null` in a create/retry answer. Never from a timer.
//     Stub (older gateway, no `received` in the 202): the first run event after the 202.
//   - step:  {type: step, kind: step|specialty, text}; specialty_loaded / specialty_updated
//     (always the generic line, never which); a device tool call's plain phrase (local intent).
//
// The rest of the app (runs_app.go, the frontend's runEvents.js) only sees Ev.
package runevents

import (
	"strings"

	"blaze-desktop/internal/bgrun"
)

// UI event types (the chat's vocabulary; the frontend reads only these).
const (
	Sending          = "message_sending"
	Received         = "message_received"
	Read             = "message_read"
	Step             = "step"
	SpecialtyLoaded  = "specialty_loaded"
	SpecialtyUpdated = "specialty_updated"
)

// Last-resort specialty lines, only when an event and the request say nothing usable (HelpLine
// builds the real line from the event's detail). Never which one.
const (
	SpecialtyLoadedText   = "Bringing in a specialist"
	SpecialtyUpdatedText  = "Updating a specialist"
	SpecialistLoadedText  = "Bringing in a specialist"
	SpecialistUpdatedText = "Updating a specialist"
)

// Ev is one event for the chat (the frontend's "run_event").
type Ev struct {
	Type        string `json:"type"`
	ClientMsgID string `json:"client_msg_id,omitempty"`
	RunID       string `json:"run_id,omitempty"`
	Conv        string `json:"conv,omitempty"`
	Text        string `json:"text,omitempty"`    // step / specialty: the pill's line, already sanitized
	Attempt     int    `json:"attempt,omitempty"` // sending: which try (1 = first)
	Offline     bool   `json:"offline,omitempty"` // sending: the last try could not reach Blaze
}

// Sent is the event for a create attempt (attempt 1 = the first try).
func Sent(cmid string, attempt int, offline bool) Ev {
	return Ev{Type: Sending, ClientMsgID: cmid, Attempt: attempt, Offline: offline}
}

// Delivered is what a create answer says about delivery: Sent always (a 2xx), plus Read when
// the gateway already picked the message up (`read != null`, e.g. on a retry).
func Delivered(cr *bgrun.Created, cmid string) []Ev {
	out := []Ev{Accepted(cr, cmid)}
	if cr != nil && cr.Read != nil {
		e := out[0]
		e.Type = Read
		out = append(out, e)
	}
	return out
}

// Accepted is POST /v1/runs answered (202, or 200 existing for a retried client_msg_id).
func Accepted(cr *bgrun.Created, cmid string) Ev {
	e := Ev{Type: Received, ClientMsgID: cmid}
	if cr != nil {
		e.RunID = cr.RunID
		if cr.ClientMsgID != "" {
			e.ClientMsgID = cr.ClientMsgID
		}
	}
	return e
}

// Tracker turns one run's events into Evs. Not safe for concurrent use (one follower each).
type Tracker struct {
	RunID, ClientMsgID string
	// Contract: the gateway sends message_read itself (its 202 had `received`), so Read comes only
	// from that event. false = an older gateway: the stub reads the first run event as Read.
	Contract bool
	// Request is the user's message the run answers: what a specialty is for when its event
	// carries no detail of its own.
	Request string
	read    bool
	last    string
}

// For is the tracker of a run created with answer cr.
func For(cr *bgrun.Created, cmid string) *Tracker {
	t := &Tracker{ClientMsgID: cmid}
	if cr != nil {
		t.RunID = cr.RunID
		t.Contract = cr.Received != nil
		t.read = cr.Read != nil
	}
	return t
}

func (t *Tracker) base(typ string) Ev {
	return Ev{Type: typ, RunID: t.RunID, ClientMsgID: t.ClientMsgID}
}

// readOnce is message_read the first time anything comes back for the run (stub only: with the
// contract, Read is the message_read event alone).
func (t *Tracker) readOnce(out []Ev) []Ev {
	if t.read || t.Contract {
		return out
	}
	return t.markRead(out)
}

func (t *Tracker) markRead(out []Ev) []Ev {
	if t.read {
		return out
	}
	t.read = true
	return append(out, t.base(Read))
}

// step adds a pill line (nothing when it is empty or the same as the last one).
func (t *Tracker) step(out []Ev, typ, text string) []Ev {
	text = Sanitize(text)
	if text == "" || text == t.last {
		return out
	}
	t.last = text
	e := t.base(typ)
	e.Text = text
	return append(out, e)
}

// Ack is the 202's ack line being shown (stub: Blaze has read the message; with the contract the
// ack is a template computed before pickup, so it is not Read).
func (t *Tracker) Ack() []Ev { return t.readOnce(nil) }

// FromRun maps one gateway run event. The wire names (type, text, name) are the contract's.
func (t *Tracker) FromRun(e bgrun.Event) []Ev {
	var out []Ev
	switch e.Type {
	case "message_received":
		t.Contract = true // always seq 1 on a contract gateway: from here Read is message_read alone
		return []Ev{t.base(Received)}
	case "message_read":
		return t.markRead(nil)
	}
	out = t.readOnce(out)
	switch e.Type {
	case "step":
		if e.Kind == "specialty" {
			// sent as both a step and specialty_loaded/updated: the same line, shown once
			upd := strings.Contains(strings.ToLower(e.Text), "updat")
			typ := SpecialtyLoaded
			if upd {
				typ = SpecialtyUpdated
			}
			return t.step(out, typ, t.help(upd, e))
		}
		out = t.step(out, Step, e.Text)
	case "specialty_loaded":
		out = t.step(out, SpecialtyLoaded, t.help(false, e))
	case "specialty_updated":
		out = t.step(out, SpecialtyUpdated, t.help(true, e))
	}
	return out
}

// help is a specialty event's pill line: its own detail (detail, task, or a text that is more than
// the contract's generic label), else the request, else the general line.
func (t *Tracker) help(updated bool, e bgrun.Event) string {
	d := e.Detail
	if d == "" {
		d = e.Task
	}
	if d == "" {
		d = e.Text // the generic "Using a specialty" cleans to "" and falls through
	}
	return HelpLine(updated, d, t.Request, e.Name, e.OwnerName)
}

// FromToolCall is a device tool call the gateway asked this computer to run; phrase is the app's
// own plain words for it (friendlyTool, which says what a specialist is for), never the call's JSON.
func (t *Tracker) FromToolCall(name, phrase string) []Ev {
	out := t.readOnce(nil)
	switch {
	case strings.Contains(name, "specialist") && (strings.HasPrefix(name, "dispatch") || strings.HasPrefix(name, "load")):
		// friendlyTool's help line is kept; anything else (a name, a bare label) is rebuilt
		return t.step(out, SpecialtyLoaded, HelpLine(false, phrase, t.Request))
	case strings.Contains(name, "specialist") && (strings.HasPrefix(name, "apply") || strings.HasPrefix(name, "update")):
		if Sanitize(phrase) == "" {
			phrase = SpecialistUpdatedText
		}
		return t.step(out, SpecialtyUpdated, phrase)
	}
	return t.step(out, Step, phrase)
}
