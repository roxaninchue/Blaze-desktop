package tools

import (
	"fmt"
	"strings"

	"blaze-desktop/internal/flashcards"
)

// Flashcards for the tutor skill (v0.15): cards go to the Learn view, which schedules
// reviews with spaced repetition.
func init() {
	register("flashcards_add", "Save flashcards to a deck in the Learn view (spaced repetition). Use after turning a PDF, slides or notes into cards with the tutor skill. One card per line in cards, written 'front :: back'. Short fronts (one question or term), backs under 40 words.",
		[]prop{p("deck", "string", "Deck name, usually the course code or topic, e.g. NET103 or 'Photosynthesis'"), p("cards", "string", "One card per line: front :: back"), p("source", "string", "Optional: the file or lecture the cards came from")},
		[]string{"deck", "cards"}, func(e *Env, a args) string {
			var cards [][2]string
			for _, line := range strings.Split(a.s("cards"), "\n") {
				f, b, ok := strings.Cut(line, "::")
				if !ok {
					continue
				}
				cards = append(cards, [2]string{strings.TrimSpace(strings.TrimLeft(f, "-*0123456789. ")), strings.TrimSpace(b)})
			}
			if len(cards) == 0 {
				return "Error: no cards. Write one per line as 'front :: back'."
			}
			n, err := flashcards.Add(a.s("deck"), a.s("source"), cards)
			if err != nil {
				return "Error saving flashcards: " + err.Error()
			}
			e.status(fmt.Sprintf("Saved %d flashcards", n))
			return fmt.Sprintf("Saved %d new flashcard(s) to %q (%d skipped as duplicates or empty). The user reviews them in the Learn view (/learn); due cards come back on a spaced schedule.", n, a.s("deck"), len(cards)-n)
		})
	register("flashcards_due", "List the flashcard decks in the Learn view with how many cards are due, or the due cards of one deck (to quiz the user in chat). Record a chat quiz answer with grade 0-3 via flashcards_review.",
		[]prop{p("deck", "string", "Optional deck; empty lists every deck"), p("limit", "integer", "Max cards (default 10)")},
		nil, func(e *Env, a args) string {
			if a.s("deck") == "" {
				ds := flashcards.Decks()
				if len(ds) == 0 {
					return "No flashcards yet. Make some with the tutor skill (flashcards_add)."
				}
				var b strings.Builder
				for _, d := range ds {
					fmt.Fprintf(&b, "%s · %d cards · %d due · %d new\n", d.Name, d.Cards, d.Due, d.New)
				}
				return b.String()
			}
			cs := flashcards.Due(a.s("deck"), a.i("limit", 10))
			if len(cs) == 0 {
				return "Nothing due in " + a.s("deck") + " right now."
			}
			var b strings.Builder
			for _, c := range cs {
				fmt.Fprintf(&b, "[%s] Q: %s\n    A: %s\n", c.ID, c.Front, c.Back)
			}
			return b.String()
		})
	register("flashcards_review", "Record how the user did on one flashcard you quizzed in chat: grade 0 again (wrong), 1 hard, 2 good, 3 easy. Reschedules it with spaced repetition.",
		[]prop{p("id", "string", "Card id from flashcards_due"), p("grade", "integer", "0 again, 1 hard, 2 good, 3 easy")},
		[]string{"id", "grade"}, func(e *Env, a args) string {
			c, err := flashcards.Review(a.s("id"), a.i("grade", 2))
			if err != nil {
				return "Error: " + err.Error()
			}
			return fmt.Sprintf("Next review of %q on %s.", c.Front, c.Due.Local().Format("Mon 2 Jan"))
		})
}
