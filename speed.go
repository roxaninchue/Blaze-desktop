package main

// v0.19.1 speed (Blaze Mobile 0.45.1 PromptParts.kt and ToolPick.keepOrder).
//
// AI services reuse their work for the longest identical start of a request, so a reused start
// answers sooner. The chat's system prompt is now put together stable first:
//
//	1. stable: rules, about Blaze, personality, design rules, project, PC profile, connected
//	   accounts, the skills picked for this request, learning rules;
//	2. constant blocks: delegation, orchestration, long-chat rules (these used to come after the
//	   live lines, so every step re-read them);
//	3. live: current folder, memory block, clock;
//	4. per step: MORE TOOLS (changes when find_tools turns tools on), long-chat summary, the note
//	   about the last reply.
//
// No wording changed. Tools keep their place across a turn's steps: one that joins mid-turn goes
// after the ones already sent instead of in the middle (the same tools are sent, nothing added
// or dropped).

import (
	"strings"

	"blaze-desktop/internal/llm"
)

// promptOrder joins the system prompt's tiers, stable first. note is "ABOUT YOUR LAST REPLY".
func promptOrder(stable, constant, live, moreTools, longExtra, note string) string {
	var b strings.Builder
	b.Grow(len(stable) + len(constant) + len(live) + len(moreTools) + len(longExtra) + len(note) + 32)
	b.WriteString(stable)
	b.WriteString(constant)
	b.WriteString(live)
	b.WriteString(moreTools)
	b.WriteString(longExtra)
	if note != "" {
		b.WriteString("\nABOUT YOUR LAST REPLY\n" + note + "\n")
	}
	return b.String()
}

// keepToolOrder returns tools ordered like the step before (prev: names in the order they were
// sent), with tools new to this step appended in their own order, and the new order to keep.
// The set of tools is exactly the input's.
func keepToolOrder(prev []string, tools []llm.Tool) ([]llm.Tool, []string) {
	byName := make(map[string]int, len(tools))
	for i, t := range tools {
		if _, dup := byName[t.Function.Name]; !dup {
			byName[t.Function.Name] = i
		}
	}
	out := make([]llm.Tool, 0, len(tools))
	used := make([]bool, len(tools))
	for _, n := range prev {
		if i, ok := byName[n]; ok && !used[i] {
			out = append(out, tools[i])
			used[i] = true
		}
	}
	for i, t := range tools {
		if !used[i] {
			out = append(out, t)
			used[i] = true
		}
	}
	order := make([]string, len(out))
	for i, t := range out {
		order[i] = t.Function.Name
	}
	return out, order
}

// commonPrefix is the length of the shared start of two prompts (tests and the speed check).
func commonPrefix(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}
