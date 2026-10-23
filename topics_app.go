package main

// Topic-aware infinite chat (v0.19, internal/topics): each turn sends the system prompt
// (with the pinned memory core), the ACTIVE topic's state note and recent turns, the
// lines carried over from other topics and a one-line index of the rest. Turns about
// other topics stay out of the prompt and in the search index. Falls back to the
// v0.18 rolling-summary path when it can't run (or is switched off).

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"blaze-desktop/internal/llm"
	"blaze-desktop/internal/longchat"
	"blaze-desktop/internal/mind"
	"blaze-desktop/internal/topics"
)

// topicMu guards load → change → save of topic state (the turn and the background
// carry-over pass both write it).
var topicMu sync.Mutex

const topicsKey = "topics" // longchat_meta: "off" switches the topic view off

// TopicMemoryOn reports whether the topic view is on (default on).
func (a *App) TopicMemoryOn() bool {
	d, err := longchat.Default()
	return err == nil && d.MetaValue(topicsKey) != "off"
}

// SetTopicMemory switches the topic view on or off (off = the v0.18 rolling summary).
func (a *App) SetTopicMemory(on bool) {
	if d, err := longchat.Default(); err == nil {
		v := "on"
		if !on {
			v = "off"
		}
		_ = d.SetMetaValue(topicsKey, v)
	}
}

// ChatTopics is the chip's data for the chat on screen: its current topic and all topics.
func (a *App) ChatTopics() topics.View {
	a.mu.Lock()
	id := ""
	if a.cur != nil {
		id = a.cur.id
	}
	a.mu.Unlock()
	d, err := longchat.Default()
	if err != nil || id == "" || !a.TopicMemoryOn() {
		return topics.View{Topics: []topics.Info{}}
	}
	return d.Topics(id).View()
}

// topicClean is what segmentation reads of a user message: no attached files, and
// continuations ([Continue], an agent's result, a design edit) stay in the active topic.
func topicClean(m llm.Message) (string, bool) {
	c := m.Content
	if isAgentResult(m) || strings.HasPrefix(c, continueTag) || strings.HasPrefix(c, "[Design edit]") {
		return c, true
	}
	return stripAttached(c), false
}

// topicContext picks the messages for this step and the topic system text. ok=false
// means the caller uses the v0.18 path.
func (a *App) topicContext(ctx context.Context, s *session, hist []llm.Message, from int) ([]llm.Message, string, bool) {
	if s == nil || s.id == "" || len(hist) == 0 || !a.TopicMemoryOn() {
		return nil, "", false
	}
	d, err := longchat.Default()
	if err != nil {
		return nil, "", false
	}
	topicMu.Lock()
	defer topicMu.Unlock()
	st := d.Topics(s.id)
	before := st.Active
	known := map[string]bool{}
	for _, c := range st.Carry {
		known[strings.ToLower(c.Text)] = true
	}
	lastUser := 0
	for i := len(hist) - 1; i >= 0; i-- {
		if hist[i].Role == "user" {
			lastUser = i
			break
		}
	}
	// A background run's request also skips the topic judge (a small model call, ~1 s): the ack
	// waits on this request (live 0.19.14 N1 run 1: ack 1.4-1.6 s on a new subject, 0.2 s after);
	// the lexical placement is used instead.
	judge := a.topicJudge(ctx)
	if skipFold(ctx) {
		judge = nil
	}
	evs := st.Segment(hist, topics.SegOpts{Judge: judge, JudgeFrom: lastUser, Clean: topicClean})
	budget := longchat.Budget(a.contextWindow())
	// Fold the active topic's older turns into its note (in batches, never redone).
	// A background run's request skips the fold (a model call, ~3-4 s): the ack waits on this
	// request, so the next classic turn folds instead (live 0.19.14 N3 run 5: ack at 3.8 s).
	if chunk, start, upto, ok := st.FoldChunk(hist, st.Active, budget); ok && !skipFold(ctx) {
		a.temit(s, "status", "Updating the topic note")
		t := st.Get(st.Active)
		note, err := a.topicNote(ctx, t.Title, t.Note, chunk, start)
		if err != nil {
			note = topics.HeuristicNote(t.Note, chunk, start)
		}
		st.SetNote(st.Active, note, upto)
	}
	// What the topics just left carried over (heuristic pass, already in st.Carry).
	var fresh []topics.Carry
	for _, c := range st.Carry {
		if !known[strings.ToLower(c.Text)] {
			fresh = append(fresh, c)
		}
	}
	if err := d.SetTopics(s.id, st); err != nil {
		return nil, "", false
	}
	for _, ev := range evs {
		if ev.Closed != 0 {
			a.topicCarryPass(s.id, ev.Closed, hist[:ev.Turn])
		}
	}
	a.carryToMind(st, fresh)
	if st.Active != before || len(evs) > 0 {
		a.temit(s, "topic", st.View())
	}
	out, extra := st.Assemble(hist, topics.Opts{Budget: budget, From: from})
	if len(out) == 0 {
		return nil, "", false
	}
	return out, extra, true
}

// topicJudge is the one small model call for an ambiguous message (15 s at most; on
// any failure the lexical fallback decides).
func (a *App) topicJudge(ctx context.Context) topics.Judge {
	if a.cfg == nil {
		return nil
	}
	return func(text string, act *topics.Topic, ts []*topics.Topic) (int, string, error) {
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		out, err := llmQuick(c, a, []llm.Message{{Role: "system", Content: topics.JudgePrompt}, {Role: "user", Content: topics.JudgeInput(text, act, ts)}})
		if err != nil {
			return 0, "", err
		}
		return topics.ParseJudge(out)
	}
}

// topicNote folds a chunk of one topic's turns into its note with the chat model.
func (a *App) topicNote(ctx context.Context, title, prev string, chunk []llm.Message, start int) (string, error) {
	if a.cfg == nil {
		return "", fmt.Errorf("no model")
	}
	msgs := []llm.Message{{Role: "system", Content: topics.NotePrompt},
		{Role: "user", Content: "TOPIC: " + title + "\n\nCURRENT NOTE:\n" + firstNonEmpty(prev, "(none yet)") + "\n\nNEW TURNS OF THIS TOPIC:\n" + topics.Transcript(chunk, start)}}
	r, err := llm.CompleteWith(ctx, a.cfg, msgs, nil, llm.Opts{})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(r.Text) == "" {
		return "", fmt.Errorf("empty note")
	}
	return r.Text, nil
}

// topicCarryPass asks the cheap model, in the background, what of a topic being left
// must still matter elsewhere, and adds it to the chat's carry-over list.
func (a *App) topicCarryPass(chat string, id int, hist []llm.Message) {
	if a.cfg == nil || !a.learnOn() {
		return
	}
	go func() {
		d, err := longchat.Default()
		if err != nil {
			return
		}
		topicMu.Lock()
		st := d.Topics(chat)
		topicMu.Unlock()
		var turns []llm.Message
		start, last := -1, 0
		for _, p := range topics.Spans(hist) {
			if st.TurnTopic[p[0]] != id {
				continue
			}
			if start < 0 {
				start = p[0]
			}
			last = p[0]
			turns = append(turns, hist[p[0]:p[1]]...)
		}
		if len(turns) == 0 {
			return
		}
		tr := topics.Transcript(turns, start)
		if len(tr) > 6000 {
			tr = tr[len(tr)-6000:]
		}
		out, err := llmQuick(context.Background(), a, []llm.Message{{Role: "system", Content: topics.CarryPrompt}, {Role: "user", Content: tr}})
		if err != nil {
			return
		}
		cs := topics.ParseCarry(out, id, last)
		if len(cs) == 0 {
			return
		}
		topicMu.Lock()
		st = d.Topics(chat)
		added := st.AddCarry(cs)
		_ = d.SetTopics(chat, st)
		topicMu.Unlock()
		a.carryToMind(st, added)
	}()
}

// carryToMind keeps carried-over lines in the memory table too, so they surface in
// every chat (and in Blaze CLI through the blaze.db mirror). mindSave dedupes, so a line
// the after-turn pass already saved is never kept twice. "important" lines are too loose
// to keep forever: they stay in the chat's carry list only.
func (a *App) carryToMind(st *topics.State, cs []topics.Carry) {
	if len(cs) == 0 || !a.learnOn() {
		return
	}
	kind := map[string]string{"deadline": "fact", "decision": "decision", "preference": "preference", "promise": "fact"}
	var ms []mind.Memory
	for _, c := range cs {
		k, ok := kind[c.Kind]
		if !ok {
			continue
		}
		subj := "chat"
		if t := st.Get(c.Topic); t != nil {
			subj = t.Title
		}
		ms = append(ms, mind.Memory{Kind: k, Subject: subj, Text: c.Text})
	}
	if len(ms) == 0 {
		return
	}
	go func() {
		d := mindDB()
		if d == nil {
			return
		}
		for _, m := range ms {
			a.mindSave(d, m)
		}
	}()
}

type skipFoldKey struct{}

// withSkipFold marks ctx as a request that must not wait on the topic-note fold.
func withSkipFold(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipFoldKey{}, true)
}

func skipFold(ctx context.Context) bool { v, _ := ctx.Value(skipFoldKey{}).(bool); return v }
