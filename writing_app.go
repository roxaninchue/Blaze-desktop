package main

// Writing tools (v0.19.10): select text, then Rewrite in a tone or Translate, and accept or reject
// the result. The text goes to the same model as chat (Blaze Cloud or the user's own), with a
// strict prompt. Long text is split on paragraphs, and every piece is checked (not empty, the
// same links, placeholders and code, actually translated) and retried once before it's offered.

import (
	"context"
	"strings"
	"time"

	"blaze-desktop/internal/llm"
	"blaze-desktop/internal/writing"
)

// WriteResult is what the Accept / Reject card shows.
type WriteResult struct {
	Text  string `json:"text"`
	Note  string `json:"note,omitempty"`  // e.g. a piece that kept a lost link by using the original
	Error string `json:"error,omitempty"` // plain words; nothing to accept
}

// WritingMenu is what the selection bar offers.
func (a *App) WritingMenu() map[string][]string {
	return map[string][]string{"tones": writing.Tones, "languages": writing.Languages}
}

// WriteAssist rewrites (mode "rewrite", arg = tone) or translates (mode "translate", arg =
// language) text and returns the result for the user to accept or reject. It never changes
// anything itself.
func (a *App) WriteAssist(text, mode, arg string) WriteResult {
	if strings.TrimSpace(text) == "" {
		return WriteResult{Error: "Select some text first."}
	}
	if len(text) > writing.MaxText {
		return WriteResult{Error: "That's too much text at once. Select a section of about 30 pages or less."}
	}
	sys, err := writing.System(mode, arg)
	if err != nil {
		return WriteResult{Error: "Pick a language to translate into."}
	}
	if why := a.capReached(); why != "" {
		return WriteResult{Error: why}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var out strings.Builder
	var notes []string
	for _, part := range writing.Split(text, writing.MaxChunk) {
		if strings.TrimSpace(part) == "" { // keep blank separators exactly
			out.WriteString(part)
			continue
		}
		res, note, err := a.writeOne(ctx, sys, mode, part)
		if err != nil {
			if ctx.Err() != nil {
				return WriteResult{Error: "That took too long. Try a shorter selection."}
			}
			return WriteResult{Error: err.Error()}
		}
		if note != "" {
			notes = append(notes, note)
		}
		out.WriteString(res)
	}
	r := WriteResult{Text: out.String()}
	if len(notes) > 0 {
		r.Note = notes[0]
	}
	return r
}

// writeOne does one piece: ask, clean, check, and retry once with the problem spelled out.
func (a *App) writeOne(ctx context.Context, sys, mode, part string) (string, string, error) {
	msgs := []llm.Message{{Role: "system", Content: sys}, {Role: "user", Content: strings.TrimSpace(part)}}
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		r, err := llm.CompleteWith(ctx, a.cfg, msgs, nil, llm.Opts{})
		if err != nil {
			last = err
			if ctx.Err() != nil {
				return "", "", err
			}
			continue
		}
		res := writing.Clean(r.Text, part)
		if cerr := writing.Check(mode, part, res); cerr != nil {
			last = cerr
			msgs = append(msgs, llm.Message{Role: "assistant", Content: r.Text},
				llm.Message{Role: "user", Content: "Not quite: " + cerr.Error() + ". Do it again, following every rule, and return only the text."})
			continue
		}
		return res, "", nil
	}
	if last != nil && strings.Contains(last.Error(), "dropped") {
		return "", "", errWrite("Blaze couldn't keep every link or placeholder in that text, so nothing was changed. Try a smaller selection.")
	}
	return "", "", errWrite("Blaze couldn't do that right now. Nothing was changed. Try again in a moment.")
}

type errWrite string

func (e errWrite) Error() string { return string(e) }
