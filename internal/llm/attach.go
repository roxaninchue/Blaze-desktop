package llm

// Attachments on a user message (v0.5 composer): images go to vision models as
// image parts; files and folders travel as text in the message itself (see the
// desktop's attach.go) and only keep a reference here so saved chats can show them.

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"sync"
)

// Attach is one thing the user attached with +, drag-and-drop or paste.
type Attach struct {
	Kind  string `json:"kind"` // image, file, folder
	Path  string `json:"path"` // the original on disk
	Name  string `json:"name"`
	Size  int64  `json:"size,omitempty"`
	Count int    `json:"count,omitempty"` // items in a folder
	Mime  string `json:"mime,omitempty"`
	Send  string `json:"send,omitempty"`  // a downscaled copy that is sent instead of Path
	Thumb string `json:"thumb,omitempty"` // small data URL for the chat chip
	Note  string `json:"note,omitempty"`  // e.g. "inlined", "path only"
	// Private vision: the image stays on this computer. Desc is what was read from it
	// there (OCR text, size, colours); cloud models get Desc instead of the picture.
	Private bool   `json:"private,omitempty"`
	Desc    string `json:"desc,omitempty"`
}

// wireMsg is a Message as providers see it (no attachment bookkeeping).
type wireMsg struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// HasImages reports whether any message carries an image.
func HasImages(msgs []Message) bool {
	for _, m := range msgs {
		for _, a := range m.Attach {
			if a.Kind == "image" {
				return true
			}
		}
	}
	return false
}

func imageCount(m Message) int {
	n := 0
	for _, a := range m.Attach {
		if a.Kind == "image" {
			n++
		}
	}
	return n
}

// KeepRecentImages drops image parts from all but the last `keep` user messages that
// carry images, so a long chat doesn't resend every old picture on every step.
func KeepRecentImages(msgs []Message, keep int) []Message {
	out := append([]Message(nil), msgs...)
	seen := 0
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Role != "user" || imageCount(out[i]) == 0 {
			continue
		}
		seen++
		if seen <= keep {
			continue
		}
		var rest []Attach
		var gone []string
		for _, a := range out[i].Attach {
			if a.Kind == "image" {
				gone = append(gone, a.Path)
				continue
			}
			rest = append(rest, a)
		}
		m := out[i]
		m.Attach = rest
		m.Content += "\n[Earlier image(s), not resent: " + strings.Join(gone, ", ") + "]"
		out[i] = m
	}
	return out
}

type imgCache struct {
	key  string
	data string
}

var (
	imgMu   sync.Mutex
	imgMemo = map[string]imgCache{}
)

// imageURL returns a data: URL for an attached image (cached by path, size and mtime).
func imageURL(a Attach) (string, error) {
	p := a.Send
	if p == "" {
		p = a.Path
	}
	st, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if st.Size() > 12<<20 {
		return "", fmt.Errorf("image too big (%d MB)", st.Size()>>20)
	}
	key := fmt.Sprint(st.Size(), st.ModTime().UnixNano())
	imgMu.Lock()
	if c, ok := imgMemo[p]; ok && c.key == key {
		imgMu.Unlock()
		return c.data, nil
	}
	imgMu.Unlock()
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	mime := a.Mime
	if a.Send != "" || mime == "" {
		mime = MimeOf(p)
	}
	u := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
	imgMu.Lock()
	if len(imgMemo) > 64 {
		imgMemo = map[string]imgCache{}
	}
	imgMemo[p] = imgCache{key, u}
	imgMu.Unlock()
	return u, nil
}

// MimeOf guesses an image MIME type from the file name.
func MimeOf(p string) string {
	l := strings.ToLower(p)
	switch {
	case strings.HasSuffix(l, ".png"):
		return "image/png"
	case strings.HasSuffix(l, ".gif"):
		return "image/gif"
	case strings.HasSuffix(l, ".webp"):
		return "image/webp"
	case strings.HasSuffix(l, ".bmp"):
		return "image/bmp"
	}
	return "image/jpeg"
}

// wire turns messages into what a provider receives. Images become image_url parts
// for models that can see; for the rest they become a one-line note with the path.
// local says the provider runs on this computer: a Private image's pixels only ever
// go to a local model, and every other model gets the local description instead.
func wire(msgs []Message, vision, local bool) []wireMsg {
	out := make([]wireMsg, 0, len(msgs))
	for _, m := range msgs {
		w := wireMsg{Role: m.Role, Content: m.Content, ToolCalls: m.ToolCalls, ToolCallID: m.ToolCallID, Name: m.Name}
		if m.Role == "user" && imageCount(m) > 0 {
			var notes []string
			var parts []map[string]any
			for _, a := range m.Attach {
				if a.Kind != "image" {
					continue
				}
				if a.Private && !(vision && local) {
					desc := strings.TrimSpace(a.Desc)
					if desc == "" {
						desc = "(no description)"
					}
					notes = append(notes, "[Image attached: "+a.Path+". Private vision is on, so the picture stayed on the user's computer and you can't see it. What was read from it there:\n"+desc+"\nWork from this text; if the task needs the picture itself, say so and suggest turning Private vision off for it.]")
					continue
				}
				if !vision {
					notes = append(notes, "[Image attached: "+a.Path+" — this model can't see images; tell the user to switch to a vision model (e.g. Gemini) if the picture matters]")
					continue
				}
				u, err := imageURL(a)
				if err != nil {
					notes = append(notes, "[Image "+a.Path+" couldn't be read: "+err.Error()+"]")
					continue
				}
				notes = append(notes, "[Image attached: "+a.Path+"]")
				parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": u}})
			}
			text := strings.TrimSpace(m.Content + "\n" + strings.Join(notes, "\n"))
			if len(parts) > 0 {
				w.Content = append([]map[string]any{{"type": "text", "text": text}}, parts...)
			} else {
				w.Content = text
			}
		}
		out = append(out, w)
	}
	return out
}

// visionFirst moves the models that can see images to the front of the turn's order.
func visionFirst(list []Target) ([]Target, bool) {
	var yes, no []Target
	for _, t := range list {
		if Info(t.Provider, t.Model).Vision {
			yes = append(yes, t)
		} else {
			no = append(no, t)
		}
	}
	return append(yes, no...), len(yes) > 0
}

// VisionModel names the first model that would take a turn with images ("" = none can).
func VisionModel(targets []Target) string {
	for _, t := range targets {
		if Info(t.Provider, t.Model).Vision {
			return t.Name + " · " + t.Model
		}
	}
	return ""
}
