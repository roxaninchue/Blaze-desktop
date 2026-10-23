package main

// Student pack (v0.15+): low-data mode, the flashcards behind the Learn view, and the
// bindings the frontend uses. Classroom tools live in internal/connect/classroom.go and
// the skills (study-planner, tutor, lecture-notes, essay-coach, group-project,
// classroom-sync) in skills/.

import (
	"encoding/json"

	"blaze-desktop/internal/flashcards"
	"blaze-desktop/internal/llm"
)

// lowDataDrop are the tools low-data mode hides: they send screenshots or images.
var lowDataDrop = map[string]bool{"see_screen": true}

// SetLowData turns low-data mode on or off (Settings › Behaviour › Data).
func (a *App) SetLowData(on bool) { a.prefs.LowData = on; a.savePrefs() }

func (a *App) lowDataLine() string {
	if !a.prefs.LowData {
		return ""
	}
	return "\nLOW-DATA MODE (the user is on limited mobile data)\n" +
		"- Text first: use read_screen, never screenshots; don't download, generate or open images, video or big files unless the user asks for that file.\n" +
		"- Prefer fetch_page on one good source over many searches; read only the part you need.\n" +
		"- Keep answers compact. Skip previews and optional extras that load data.\n"
}

func (a *App) lowDataTools(all []llm.Tool) []llm.Tool {
	if !a.prefs.LowData {
		return all
	}
	out := make([]llm.Tool, 0, len(all))
	for _, t := range all {
		if !lowDataDrop[t.Function.Name] {
			out = append(out, t)
		}
	}
	return out
}

// callBytes is roughly what one model call moved over the network: the request's
// messages as JSON plus the reply. The live card adds them up as MB per task.
func callBytes(sent []llm.Message, r *llm.Result) int {
	b, _ := json.Marshal(sent)
	n := len(b)
	if r != nil {
		n += len(r.Text)
		for _, tc := range r.ToolCalls {
			n += len(tc.Function.Name) + len(tc.Function.Arguments)
		}
	}
	return n
}

// ---------- Learn view: flashcards ----------

func (a *App) FlashDecks() []flashcards.Deck { return flashcards.Decks() }

func (a *App) FlashDue(deck string) []flashcards.Card { return flashcards.Due(deck, 50) }

func (a *App) FlashReview(id string, grade int) string {
	_, err := flashcards.Review(id, grade)
	return errStr(err)
}

func (a *App) FlashDelete(id string) string { return errStr(flashcards.Delete(id)) }
