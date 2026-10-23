// Package bgruntest is a fake Blaze gateway runs API for tests (no network).
package bgruntest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"blaze-desktop/internal/bgrun"
)

// Fake plays scripted views per run. A needs_device view repeats until tool results arrive.
type Fake struct {
	mu sync.Mutex
	// AckFor is the ack for a create (nil: no ack).
	AckFor func(req bgrun.CreateReq) *bgrun.Ack
	// ScriptFor is the views a new run plays (the last one repeats).
	ScriptFor func(req bgrun.CreateReq) []bgrun.View
	CreateErr error

	Created []bgrun.CreateReq
	Results map[string][][]bgrun.ToolResult
	Deleted []string
	Gets    int
	// Extra are runs GET /v1/runs lists besides the created ones (another device's, or ones
	// created "before a restart" with Seed).
	Extra []bgrun.Summary

	runs  map[string]*run
	order []string
	n     int
}

type run struct {
	cmid  string
	views []bgrun.View
	i     int
}

func (f *Fake) init() {
	if f.runs == nil {
		f.runs = map[string]*run{}
		f.Results = map[string][][]bgrun.ToolResult{}
	}
}

// Seed adds a run that exists on the gateway already (made before a restart).
func (f *Fake) Seed(id, clientMsgID string, views ...bgrun.View) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.init()
	f.runs[id] = &run{cmid: clientMsgID, views: views}
	f.order = append(f.order, id)
}

// Create implements bgrun.API.
func (f *Fake) Create(_ context.Context, req bgrun.CreateReq) (*bgrun.Created, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.init()
	if f.CreateErr != nil {
		return nil, f.CreateErr
	}
	// Idempotent on client_msg_id, as the gateway is: the same id is the same run (existing).
	for _, id := range f.order {
		if r := f.runs[id]; r != nil && r.cmid != "" && r.cmid == req.ClientMsgID {
			return &bgrun.Created{RunID: id, ClientMsgID: req.ClientMsgID, Status: bgrun.StQueued, Existing: true}, nil
		}
	}
	f.Created = append(f.Created, req)
	f.n++
	id := fmt.Sprintf("run_fake%d", f.n)
	var views []bgrun.View
	if f.ScriptFor != nil {
		views = f.ScriptFor(req)
	}
	if len(views) == 0 {
		views = []bgrun.View{Done("ok", "", "")}
	}
	f.runs[id] = &run{cmid: req.ClientMsgID, views: views}
	f.order = append(f.order, id)
	var ack *bgrun.Ack
	if f.AckFor != nil {
		ack = f.AckFor(req)
	}
	return &bgrun.Created{RunID: id, ClientMsgID: req.ClientMsgID, Status: bgrun.StQueued, Ack: ack}, nil
}

// Get implements bgrun.API.
func (f *Fake) Get(_ context.Context, id string, after int) (*bgrun.View, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.init()
	f.Gets++
	r := f.runs[id]
	if r == nil {
		return nil, &bgrun.HTTPError{Code: 404, Body: "not_found"}
	}
	v := r.views[r.i]
	if v.Status != bgrun.StNeedsDevice && r.i < len(r.views)-1 {
		r.i++
	}
	out := v
	out.ID, out.ClientMsgID = id, r.cmid
	out.Events = nil
	for _, e := range v.Events {
		if e.Seq > after {
			out.Events = append(out.Events, e)
		}
	}
	return &out, nil
}

// Runs is every run id the fake holds, in order.
func (f *Fake) Runs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.order...)
}

// List implements bgrun.API.
func (f *Fake) List(_ context.Context) ([]bgrun.Summary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.init()
	var out []bgrun.Summary
	for _, id := range f.order {
		r := f.runs[id]
		out = append(out, bgrun.Summary{ID: id, ClientMsgID: r.cmid, Status: r.views[r.i].Status})
	}
	return append(out, f.Extra...), nil
}

// ToolResults implements bgrun.API.
func (f *Fake) ToolResults(_ context.Context, id string, res []bgrun.ToolResult) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.init()
	r := f.runs[id]
	if r == nil {
		return &bgrun.HTTPError{Code: 404, Body: "not_found"}
	}
	if r.views[r.i].Status != bgrun.StNeedsDevice {
		return &bgrun.HTTPError{Code: 409, Body: "not_waiting"}
	}
	f.Results[id] = append(f.Results[id], res)
	if r.i < len(r.views)-1 {
		r.i++
	}
	return nil
}

// Delete implements bgrun.API.
func (f *Fake) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Deleted = append(f.Deleted, id)
	return nil
}

// Snapshot returns copies of what the fake saw.
func (f *Fake) Snapshot() (created []bgrun.CreateReq, results map[string][][]bgrun.ToolResult, deleted []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	res := map[string][][]bgrun.ToolResult{}
	for k, v := range f.Results {
		res[k] = append([][]bgrun.ToolResult(nil), v...)
	}
	return append([]bgrun.CreateReq(nil), f.Created...), res, append([]string(nil), f.Deleted...)
}

// ---------------- views ----------------

// Running is a run still working.
func Running() bgrun.View {
	return bgrun.View{Status: bgrun.StRunning, Events: []bgrun.Event{{Seq: 3, Type: "status", Status: "running"}}, NextAfter: 3}
}

// NeedsDevice waits for this computer to run calls (name, JSON args pairs).
func NeedsDevice(id, name, args string) bgrun.View {
	c := bgrun.ToolCall{ID: id, Type: "function"}
	c.Function.Name, c.Function.Arguments = name, args
	return bgrun.View{Status: bgrun.StNeedsDevice, Pending: []bgrun.ToolCall{c},
		Events: []bgrun.Event{{Seq: 4, Type: "tool_calls"}, {Seq: 5, Type: "status", Status: "needs_device"}}, NextAfter: 5}
}

// Done is a finished run with its done event (gateway 0.8.38 shape).
func Done(reply, summary, intent string) bgrun.View {
	var rc *bgrun.Receipt
	if summary != "" || intent != "" {
		rc = &bgrun.Receipt{Intent: intent, Summary: summary}
	}
	return bgrun.View{Status: bgrun.StDone, Reply: reply, NextAfter: 9, Events: []bgrun.Event{
		{Seq: 8, Type: "done", Reply: reply, Receipt: rc}, {Seq: 9, Type: "status", Status: "done"}}}
}

// Failed is a failed run with its failed event.
func Failed(code, reason, try string) bgrun.View {
	return bgrun.View{Status: bgrun.StFailed, Error: &bgrun.RunError{Code: code, Message: reason}, NextAfter: 9, Events: []bgrun.Event{
		{Seq: 7, Type: "failed", Code: code, Reason: reason, Try: try}, {Seq: 8, Type: "error", Code: code, Message: json.RawMessage(strconvQuote(reason))},
		{Seq: 9, Type: "status", Status: "failed"}}}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
