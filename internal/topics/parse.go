package topics

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// JudgeInput renders the judge's user message.
func JudgeInput(text string, active *Topic, ts []*Topic) string {
	var b strings.Builder
	b.WriteString("TOPICS (newest first)\n")
	for _, t := range ts {
		mark := ""
		if active != nil && t.ID == active.ID {
			mark = " (current)"
		}
		goal := ""
		if t.Note != "" {
			goal = " · " + clip(strings.SplitN(t.Note, "\n", 2)[0], 120)
		}
		fmt.Fprintf(&b, "- %d: %s%s · keywords: %s%s\n", t.ID, t.Title, mark, strings.Join(topTerms(t, 8), ", "), goal)
	}
	b.WriteString("\nNEW MESSAGE\n" + clip(text, 1200))
	return b.String()
}

// ParseJudge reads {"topic": 3|"new", "title": "..."} (prose and fences tolerated).
func ParseJudge(s string) (id int, title string, err error) {
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i < 0 || j <= i {
		return 0, "", fmt.Errorf("no JSON")
	}
	var raw struct {
		Topic json.RawMessage `json:"topic"`
		Title string          `json:"title"`
	}
	if err := json.Unmarshal([]byte(s[i:j+1]), &raw); err != nil {
		return 0, "", err
	}
	v := strings.Trim(strings.TrimSpace(string(raw.Topic)), `"`)
	if v == "" || strings.EqualFold(v, "new") || v == "0" {
		return 0, raw.Title, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, "", fmt.Errorf("bad topic %q", v)
	}
	return n, raw.Title, nil
}

// ParseCarry reads the carry pass's JSON array.
func ParseCarry(s string, topic, turn int) []Carry {
	i, j := strings.Index(s, "["), strings.LastIndex(s, "]")
	if i < 0 || j <= i {
		return nil
	}
	var raw []struct{ Kind, Text string }
	if json.Unmarshal([]byte(s[i:j+1]), &raw) != nil {
		return nil
	}
	var out []Carry
	for _, r := range raw {
		t := strings.Join(strings.Fields(r.Text), " ")
		if t == "" {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(r.Kind))
		switch k {
		case "deadline", "decision", "preference", "promise", "important":
		default:
			k = "important"
		}
		out = append(out, Carry{Topic: topic, Turn: turn, Kind: k, Text: clip(t, 220)})
	}
	return out
}
