package cloudai

// Router is the desktop's llm.Backend: each request goes to Blaze Cloud or to the user's
// Custom models, by the saved Choice.
//
//   - Blaze Cloud: the gateway (client.go). No provider keys involved.
//   - Custom: the classic provider router over the user's providers and keys (config.json
//     + keystore). A per-task pin (chat / code / vision) answers with exactly that model.
//     When Custom fails Blaze says so plainly; it only asks Blaze Cloud instead when the
//     user switched "Use Blaze Cloud as a backup" on, and then it says that too.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"blaze-desktop/internal/config"
	"blaze-desktop/internal/llm"
)

// Router routes requests between Blaze Cloud and Custom.
type Router struct {
	mu     sync.RWMutex
	choice *Choice
	GW     *Gateway
	// Direct runs the classic router (llm.CompleteDirect); tests may replace it.
	Direct func(ctx context.Context, cfg *config.Config, msgs []llm.Message, tools []llm.Tool, o llm.Opts) (*llm.Result, error)
	// QuickDirect runs the classic quick call (llm.QuickDirect); tests may replace it.
	QuickDirect func(ctx context.Context, cfg *config.Config, msgs []llm.Message, timeout time.Duration) (string, error)
}

// NewRouter routes by choice through gw.
func NewRouter(choice *Choice, gw *Gateway) *Router {
	return &Router{choice: choice, GW: gw, Direct: llm.CompleteDirect, QuickDirect: llm.QuickDirect}
}

// Choice is a copy of the current choice.
func (r *Router) Choice() Choice {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c := *r.choice
	c.Overrides = map[string]Pick{}
	for k, v := range r.choice.Overrides {
		c.Overrides[k] = v
	}
	return c
}

// Update changes the choice under the lock and saves it.
func (r *Router) Update(f func(c *Choice)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	f(r.choice)
	return r.choice.Save()
}

// CustomTask is which pin a Custom request uses: vision (an image the model must see),
// code (code in the last user message), else chat. Tools don't change it.
func CustomTask(msgs []llm.Message) string {
	for _, m := range msgs {
		for _, a := range m.Attach {
			if a.Kind == "image" && !a.Private {
				return "vision"
			}
		}
	}
	if codeCue.MatchString(lastUserText(msgs)) {
		return "code"
	}
	return "chat"
}

// Pinned is cfg narrowed to one model with no fallback, for a per-task pin.
func Pinned(cfg *config.Config, p Pick) *config.Config {
	c := *cfg
	c.Default = &config.Entry{Provider: p.Provider, Model: p.Model}
	c.NoFallback = true
	return &c
}

// CustomFailed is a Custom failure, said plainly. It wraps the router's error so the
// chat's Details still list every model that failed.
type CustomFailed struct {
	Line string
	Err  error
}

func (e *CustomFailed) Error() string { return e.Line }

// PlainLine is the line the chat shows (llm.Plainer).
func (e *CustomFailed) PlainLine() string { return e.Line }
func (e *CustomFailed) Unwrap() error     { return e.Err }

// customLine is the plain line for a Custom failure.
func customLine(err error, fallbackOn bool) string {
	var re *llm.RouteError
	if errors.As(err, &re) && (re.NoModels || re.NoTools) {
		return re.Error() // already says what to set up
	}
	line := "Your custom model couldn't answer: " + strings.TrimSuffix(llm.Plain(err), ".") + "."
	if !fallbackOn {
		line += " Blaze Cloud wasn't asked, because using it as a backup is off (Settings › Models)."
	}
	return line
}

// Complete implements llm.Backend.
func (r *Router) Complete(ctx context.Context, cfg *config.Config, msgs []llm.Message, tools []llm.Tool, o llm.Opts) (*llm.Result, error, bool) {
	ch := r.Choice()
	if ch.Cloud() {
		res, err := r.GW.Complete(ctx, msgs, tools, o.OnText, false)
		return res, err, true
	}
	use := cfg
	if p, ok := ch.OverrideFor(CustomTask(msgs)); ok {
		use = Pinned(cfg, p)
	}
	started := false
	o2 := o
	o2.OnText = func(s string) {
		started = true
		if o.OnText != nil {
			o.OnText(s)
		}
	}
	res, err := r.Direct(ctx, use, msgs, tools, o2)
	if err == nil || ctx.Err() != nil {
		return res, err, true
	}
	if !ch.FallbackToCloud || started {
		return nil, &CustomFailed{Line: customLine(err, ch.FallbackToCloud), Err: err}, true
	}
	if o.OnStatus != nil {
		o.OnStatus("Your custom model couldn't answer, so Blaze Cloud is answering")
	}
	cres, cerr := r.GW.Complete(ctx, msgs, tools, o.OnText, false)
	if cerr != nil {
		line := cerr.Error()
		var ce *Error
		if errors.As(cerr, &ce) {
			line = ce.Line
		}
		return nil, &CustomFailed{Line: "Your custom model couldn't answer (" + strings.TrimSuffix(llm.Plain(err), ".") + "), and neither could Blaze Cloud: " + line, Err: err}, true
	}
	cres.Note = "Your custom model couldn't answer, so Blaze Cloud did."
	return cres, nil, true
}

// Quick implements llm.Backend (titles, learning, summaries).
func (r *Router) Quick(ctx context.Context, cfg *config.Config, msgs []llm.Message, timeout time.Duration) (string, error, bool) {
	ch := r.Choice()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if ch.Cloud() {
		res, err := r.GW.Complete(ctx, msgs, nil, nil, true)
		if err != nil {
			return "", err, true
		}
		return res.Text, nil, true
	}
	if p, ok := ch.OverrideFor("chat"); ok {
		res, err := r.Direct(ctx, Pinned(cfg, p), msgs, nil, llm.Opts{})
		if err == nil {
			return res.Text, nil, true
		}
		return r.quickFallback(ctx, ch, msgs, err)
	}
	t, err := r.QuickDirect(ctx, cfg, msgs, timeout)
	if err == nil {
		return t, nil, true
	}
	return r.quickFallback(ctx, ch, msgs, err)
}

func (r *Router) quickFallback(ctx context.Context, ch Choice, msgs []llm.Message, err error) (string, error, bool) {
	if !ch.FallbackToCloud || ctx.Err() != nil {
		return "", &CustomFailed{Line: customLine(err, ch.FallbackToCloud), Err: err}, true
	}
	res, cerr := r.GW.Complete(ctx, msgs, nil, nil, true)
	if cerr != nil {
		return "", &CustomFailed{Line: customLine(err, true), Err: err}, true
	}
	return res.Text, nil, true
}
