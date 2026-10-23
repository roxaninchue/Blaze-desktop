package llm

// Private vision (v0.8.3): pictures never leave the computer. Before a turn, each image
// is read on this computer (the OS's own OCR plus a size and colour readout, package
// ocr) and cloud models get only that text. A model running on this computer (Ollama,
// or a custom service on a loopback address) may still be handed the picture itself.

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"

	"blaze-desktop/internal/config"
	"blaze-desktop/internal/ocr"
)

// IsLocal reports whether a provider runs on this computer (Ollama, or a custom
// service on a loopback address).
func IsLocal(p config.Provider) bool {
	if p.ID == "ollama" {
		return true
	}
	u, err := url.Parse(strings.TrimSpace(p.BaseURL))
	if err != nil || u.Hostname() == "" {
		return false
	}
	h := u.Hostname()
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// The reading of an image is kept by file, size and time, so each picture is read
// once however many steps resend it.
var (
	descMu   sync.Mutex
	descMemo = map[string]string{}
)

// describeImage is swapped in tests.
var describeImage = ocr.Describe

// PrepareImages marks every image the turn would send as Private and fills in what
// was read from it on this computer. Older images are dropped first (as Chat would) so
// only pictures that would travel get read.
func PrepareImages(ctx context.Context, msgs []Message, status func(string)) []Message {
	msgs = KeepRecentImages(msgs, 3)
	out := make([]Message, len(msgs))
	told := false
	for i, m := range msgs {
		out[i] = m
		if m.Role != "user" || imageCount(m) == 0 {
			continue
		}
		at := make([]Attach, len(m.Attach))
		copy(at, m.Attach)
		for j, a := range at {
			if a.Kind != "image" {
				continue
			}
			a.Private = true
			if strings.TrimSpace(a.Desc) == "" {
				p := a.Send
				if p == "" {
					p = a.Path
				}
				key := memoKey(p)
				descMu.Lock()
				d, ok := descMemo[key]
				descMu.Unlock()
				if !ok {
					if status != nil && !told {
						status("Reading the image on this computer (Private vision)")
						told = true
					}
					// Read the original when there is one: more pixels, better OCR.
					src := a.Path
					if _, err := os.Stat(src); err != nil {
						src = p
					}
					d = describeImage(ctx, src)
					descMu.Lock()
					if len(descMemo) > 256 {
						descMemo = map[string]string{}
					}
					descMemo[key] = d
					descMu.Unlock()
				}
				a.Desc = d
			}
			at[j] = a
		}
		out[i].Attach = at
	}
	return out
}

func memoKey(p string) string {
	if st, err := os.Stat(p); err == nil {
		return fmt.Sprint(p, "|", st.Size(), "|", st.ModTime().UnixNano())
	}
	return p
}
