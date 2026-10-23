package tools

import (
	"regexp"
	"strings"
)

// sendRe spots a phone request that sends something in the user's name (a text, a
// WhatsApp message, an email, a post). Those always need the user's own yes here first.
var sendRe = regexp.MustCompile(`(?i)\b(send|text|sms|whatsapp|message|reply|forward|post|dm|email|share)\b`)

// IsPhoneSend says whether a phone_ask task sends something in the user's name.
func IsPhoneSend(task string) bool { return sendRe.MatchString(task) }

func init() {
	register("phone_ask", "Ask the user's paired phone (Blaze for Android) to do something or find something out, in plain words: send or read texts, call someone, check notifications, set an alarm, find a contact, get its location, anything on the phone. The phone's Blaze does it (asking the user there when it needs approval) and returns its answer.",
		[]prop{p("task", "string", "What the phone should do, as a complete request, e.g. \"Text Mum: running 10 minutes late\"")}, []string{"task"},
		func(e *Env, a args) string {
			t := strings.TrimSpace(a.s("task"))
			if t == "" {
				return "Error: say what the phone should do in task."
			}
			if e.Phone == nil {
				return "No phone is paired with this PC. The user can pair one in Blaze Desktop › Phone (scan the QR code from Blaze on Android › Settings › Blaze Desktop)."
			}
			if IsPhoneSend(t) {
				ok := false
				switch {
				case e.Approve != nil:
					ok = e.Approve("Send through your phone?", t)
				case e.Confirm != nil:
					ok = e.Confirm("Send through your phone?", t)
				}
				if !ok {
					return "The user didn't approve sending this through the phone. Nothing was sent. Show the draft and ask what to change."
				}
			}
			e.status("Asking your phone: " + firstLineOf(t, 60))
			return e.Phone(t)
		})
}

func firstLineOf(s string, n int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > n {
		s = s[:n] + "…"
	}
	return s
}
