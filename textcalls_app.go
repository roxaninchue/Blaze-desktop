package main

import (
	"strings"

	"blaze-desktop/internal/llm"
)

// completeTurn is App.run's port to the models (llm.CompleteWith); tests script it.
var completeTurn = llm.CompleteWith

// eventTap, when set, sees every chat event App.run emits (tests only).
var eventTap func(ev string, d any)

// errTextCalls ends a turn whose model kept writing tool calls as text that could not
// be run, instead of showing that syntax as the answer (v0.19.13, from CLI 0.14.14).
const errTextCalls = "The model wrote its tool call as text and it couldn't be run, twice. Try again, or pick another model."

// recoverTextCalls turns tool calls a model wrote into its text into real calls.
// recovered: the result now carries them (and only the prose around them).
// junk: the text is tool syntax that could not be turned into a call.
func recoverTextCalls(r *llm.Result, known []string) (_ *llm.Result, recovered, junk bool) {
	if r == nil || len(r.ToolCalls) > 0 || len(known) == 0 || strings.TrimSpace(r.Text) == "" {
		return r, false, false
	}
	rest, calls := llm.ExtractTextCalls(r.Text, known)
	if len(calls) > 0 {
		c := *r
		c.Text = strings.TrimSpace(rest)
		c.ToolCalls = calls
		return &c, true, false
	}
	return r, false, llm.LooksLikeToolText(r.Text, known)
}

// primaryArgs maps each tool to its main argument: its only or first required
// parameter, so a text call with a bare string argument can still run.
func primaryArgs(all []llm.Tool) func(string) string {
	m := map[string]string{}
	for _, t := range all {
		p := t.Function.Parameters
		props, _ := p["properties"].(map[string]any)
		var key string
		switch req := p["required"].(type) {
		case []string:
			if len(req) > 0 {
				key = req[0]
			}
		case []any:
			if len(req) > 0 {
				key, _ = req[0].(string)
			}
		}
		if key == "" && len(props) == 1 {
			for k := range props {
				key = k
			}
		}
		if key != "" {
			m[t.Function.Name] = key
		}
	}
	return func(name string) string { return m[name] }
}
