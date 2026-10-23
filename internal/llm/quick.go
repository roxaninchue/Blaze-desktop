package llm

// Quick: tiny background calls (chat titles) on the cheapest healthy model.
// They never stream, never use tools, try at most two models, and stay out of the
// router's speed stats so a 6-word answer doesn't make a model look fast for real turns.

import (
	"context"
	"errors"
	"sort"
	"time"

	"blaze-desktop/internal/config"
)

// QuickTargets orders the usable models for a tiny call: free-tier ones first, then
// the router's own order (health, then speed). Resting and cooling models are skipped.
func QuickTargets(cfg *config.Config) []Target {
	all := Targets(cfg, false)
	now := time.Now()
	var out []Target
	hmu.Lock()
	for _, t := range all {
		if x := health[t.key()]; x != nil && x.CoolUntil.After(now) {
			continue
		}
		out = append(out, t)
	}
	hmu.Unlock()
	free := func(t Target) bool {
		if p, ok := config.ProviderByID(t.Provider); ok && p.Free {
			return true
		}
		return Info(t.Provider, t.Model).Free || t.Provider == "ollama"
	}
	sort.SliceStable(out, func(i, j int) bool { return free(out[i]) && !free(out[j]) })
	return out
}

// Quick asks for a short plain-text answer. It gives up after timeout.
func Quick(ctx context.Context, cfg *config.Config, msgs []Message, timeout time.Duration) (string, error) {
	if b := currentBackend(); b != nil { // v0.19 desktop: Blaze Cloud / Custom (backend.go)
		if t, err, ok := b.Quick(ctx, cfg, msgs, timeout); ok {
			return t, err
		}
	}
	return quick(ctx, cfg, msgs, timeout)
}

func quick(ctx context.Context, cfg *config.Config, msgs []Message, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	list := QuickTargets(cfg)
	tokens := Estimate(chars(msgs, nil))
	tried := 0
	var last error
	for _, t := range list {
		if tried >= 2 {
			break
		}
		if w, _ := Wait(t.key(), tokens); w > 0 {
			continue
		}
		p, _ := config.ProviderByID(t.Provider)
		keys := cfg.AllKeys(t.Provider)
		key := ""
		if len(keys) > 0 {
			key = keys[0]
		}
		tried++
		Spend(t.key(), tokens)
		r, err := Chat(ctx, p, key, t.Model, msgs, nil, nil)
		if err == nil && r != nil {
			RecordUsage(t.Provider, t.Model, chars(msgs, nil), r)
			return r.Text, nil
		}
		if r != nil {
			Headers(t.key(), r.Headers)
		}
		if IsQuota(err) {
			var he *HTTPError
			ra := 0
			if errors.As(err, &he) {
				ra = he.RetryAfter
			}
			Limited(t.key(), err.Error(), ra)
		}
		last = err
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
	}
	if tried == 0 && cfg.AllowKeyless() {
		r, err := keyless(ctx, msgs, Opts{})
		if err != nil {
			return "", err
		}
		return r.Text, nil
	}
	if last == nil {
		last = errors.New("no model free for a quick call")
	}
	return "", last
}
