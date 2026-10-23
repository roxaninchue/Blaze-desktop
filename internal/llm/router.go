package llm

// The router (ported from the Android app's FreeRouter.kt, KeyRing.kt and the
// fallback parts of LlmClient.kt).
//
//   - Auto (free routing): the user's chain, fill-first. Cooling entries are skipped,
//     flaky ones pushed back, and among equally healthy ones the fastest goes first.
//   - A fixed default model is always tried first; if it fails and fallback is on,
//     the chain carries the turn.
//   - Each provider can hold several keys. A key that runs out of quota on a model
//     rests and the next key carries on with the same model.
//   - A model that can't call tools (Pollinations, or one the catalog or the provider
//     says can't) is never used for a turn that sends tools. Keyless is only a
//     plain-chat last resort when nothing else is set up, and only when the caller allows it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"blaze-desktop/internal/config"
)

type Health struct {
	Fails     int       `json:"fails"`
	OK        int       `json:"ok"`
	CoolUntil time.Time `json:"cool_until"`
	AvgMs     int64     `json:"avg_ms"`
	LastErr   string    `json:"last_error"`
	NoTools   bool      `json:"no_tools"`
}

type persisted struct {
	NoTools  map[string]bool      `json:"no_tools"`  // provider|model the provider said can't use tools
	KeyRests map[string]time.Time `json:"key_rests"` // provider|model|keyhash -> resting until
}

var (
	hmu    sync.Mutex
	health = map[string]*Health{}
	store  *persisted
)

func routerPath() string { return filepath.Join(config.Dir(), "desktop", "router.json") }

func loadStore() {
	if store != nil {
		return
	}
	store = &persisted{NoTools: map[string]bool{}, KeyRests: map[string]time.Time{}}
	b, _ := os.ReadFile(routerPath())
	_ = json.Unmarshal(b, store)
	if store.NoTools == nil {
		store.NoTools = map[string]bool{}
	}
	if store.KeyRests == nil {
		store.KeyRests = map[string]time.Time{}
	}
}

func saveStore() {
	now := time.Now()
	for k, t := range store.KeyRests {
		if t.Before(now) {
			delete(store.KeyRests, k)
		}
	}
	_ = os.MkdirAll(filepath.Dir(routerPath()), 0o700)
	b, _ := json.Marshal(store)
	_ = os.WriteFile(routerPath(), b, 0o600)
}

func learnedNoTools(provider, model string) bool {
	hmu.Lock()
	defer hmu.Unlock()
	loadStore()
	return store.NoTools[provider+"|"+model]
}

func markNoTools(provider, model string) {
	hmu.Lock()
	defer hmu.Unlock()
	loadStore()
	store.NoTools[provider+"|"+model] = true
	saveStore()
}

func h(k string) *Health {
	x := health[k]
	if x == nil {
		x = &Health{}
		health[k] = x
	}
	return x
}

func keyHash(key string) string {
	v := uint32(2166136261)
	for _, c := range key {
		v = (v ^ uint32(c)) * 16777619
	}
	return fmt.Sprintf("%08x", v)
}

func restKey(provider, model, key string, d time.Duration) {
	hmu.Lock()
	defer hmu.Unlock()
	loadStore()
	store.KeyRests[provider+"|"+model+"|"+keyHash(key)] = time.Now().Add(d)
	saveStore()
}

func keyResting(provider, model, key string) bool {
	hmu.Lock()
	defer hmu.Unlock()
	loadStore()
	return store.KeyRests[provider+"|"+model+"|"+keyHash(key)].After(time.Now())
}

// ClearHealth forgets cool-downs and key rests for a provider (after adding a key, say).
func ClearHealth(provider string) {
	hmu.Lock()
	loadStore()
	for k, x := range health {
		if strings.HasPrefix(k, provider+"|") {
			x.CoolUntil, x.Fails = time.Time{}, 0
		}
	}
	for k := range store.KeyRests {
		if strings.HasPrefix(k, provider+"|") {
			delete(store.KeyRests, k)
		}
	}
	saveStore()
	hmu.Unlock()
	ClearBudget(provider + "|")
}

// IsQuota is true for a rate limit or out-of-credits error.
func IsQuota(err error) bool {
	var he *HTTPError
	if !errors.As(err, &he) {
		return false
	}
	low := strings.ToLower(he.Body)
	return he.Code == 429 || he.Code == 402 || strings.Contains(low, "quota") || strings.Contains(low, "resource_exhausted") ||
		strings.Contains(low, "rate limit") || strings.Contains(low, "credits")
}

// ---------------- targets ----------------

type Target struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Name     string `json:"name"`
	Fixed    bool   `json:"fixed"` // the user's chosen default
}

func (t Target) key() string { return t.Provider + "|" + t.Model }

func usable(cfg *config.Config, provider string) bool {
	if provider == config.KeylessID || provider == "" {
		return false
	}
	p, ok := config.ProviderByID(provider)
	if !ok || p.BaseURL == "" {
		return false
	}
	return provider == "ollama" || provider == config.Custom || cfg.Key(provider) != ""
}

// Entries is the Auto chain: the saved chain, or (when it's empty) every free-tier
// provider with a key, in the default fill-first order.
func Entries(cfg *config.Config) []config.Entry {
	ch := cfg.RouteChain()
	if len(ch) > 0 {
		return ch
	}
	var out []config.Entry
	for _, id := range config.FreeOrder {
		if cfg.Key(id) != "" {
			out = append(out, config.Entry{Provider: id, Model: cfg.Model(id)})
		}
	}
	return out
}

// Targets lists who a turn would try, in order. needTools drops models that can't use tools.
func Targets(cfg *config.Config, needTools bool) []Target {
	var out []Target
	seen := map[string]bool{}
	push := func(provider, model string, fixed bool) {
		if model == "" {
			model = cfg.Model(provider)
		}
		model = ResolveModel(provider, model)
		t := Target{Provider: provider, Model: model, Name: config.Name(provider), Fixed: fixed}
		if model == "" || seen[t.key()] || !usable(cfg, provider) {
			return
		}
		if needTools && !CanUseTools(provider, model) {
			return
		}
		seen[t.key()] = true
		out = append(out, t)
	}
	auto := cfg.IsAuto()
	if !auto && cfg.DailyCap > 0 {
		if p, ok := config.ProviderByID(cfg.Default.Provider); ok && !p.Free && TodayCost() >= cfg.DailyCap {
			auto = true // past today's spend cap: free chain only
		}
	}
	if !auto {
		push(cfg.Default.Provider, cfg.Default.Model, true)
		if cfg.NoFallback {
			return out
		}
	}
	var chain []Target
	start := len(out)
	for _, e := range Entries(cfg) {
		push(e.Provider, e.Model, false)
	}
	chain = append(chain, out[start:]...)
	out = out[:start]
	hmu.Lock()
	now := time.Now()
	var ready, cooling []Target
	for _, t := range chain {
		if x := health[t.key()]; x != nil && x.CoolUntil.After(now) {
			cooling = append(cooling, t)
		} else {
			ready = append(ready, t)
		}
	}
	fails := func(t Target) int {
		if x := health[t.key()]; x != nil {
			return min(x.Fails, 3)
		}
		return 0
	}
	avg := func(t Target) int64 {
		if x := health[t.key()]; x != nil {
			return x.AvgMs
		}
		return 0
	}
	sort.SliceStable(ready, func(i, j int) bool {
		fi, fj := fails(ready[i]), fails(ready[j])
		if fi != fj {
			return fi < fj
		}
		if !cfg.NoFastest {
			// Among equally healthy ones the fastest goes first; an untried model counts as
			// fast so it gets measured.
			return avg(ready[i]) < avg(ready[j])
		}
		return false
	})
	sort.SliceStable(cooling, func(i, j int) bool {
		return health[cooling[i].key()].CoolUntil.Before(health[cooling[j].key()].CoolUntil)
	})
	hmu.Unlock()
	return append(append(out, ready...), cooling...)
}

// ---------------- complete ----------------

type Opts struct {
	OnText   func(string)
	OnStatus func(string) // "Groq is cooling down, back in 12s"
	// PlainFallback lets the keyless model answer without tools when no model that
	// can use tools is set up at all. Never set it for Design or unattended runs.
	PlainFallback bool
}

// Complete routes one turn (kept for callers that only stream text).
func Complete(ctx context.Context, cfg *config.Config, msgs []Message, tools []Tool, onText func(string)) (*Result, error) {
	return CompleteWith(ctx, cfg, msgs, tools, Opts{OnText: onText})
}

func chars(msgs []Message, tools []Tool) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content) + 20 + 4000*imageCount(m)
		for _, c := range m.ToolCalls {
			n += len(c.Function.Name) + len(c.Function.Arguments) + 30
		}
	}
	b, _ := json.Marshal(tools)
	return n + len(b)
}

func fmtWait(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= 90*time.Second:
		return fmt.Sprintf("%d min", int((d+59*time.Second)/time.Minute))
	}
	return fmt.Sprintf("%ds", int((d+999*time.Millisecond)/time.Second))
}

// CompleteWith routes one turn across the user's models.
func CompleteWith(ctx context.Context, cfg *config.Config, msgs []Message, tools []Tool, o Opts) (*Result, error) {
	if b := currentBackend(); b != nil { // v0.19 desktop: Blaze Cloud / Custom (backend.go)
		if r, err, ok := b.Complete(ctx, cfg, msgs, tools, o); ok {
			return r, err
		}
	}
	return complete(ctx, cfg, Sanitize(msgs), tools, o, false, false)
}

func complete(ctx context.Context, cfg *config.Config, msgs []Message, tools []Tool, o Opts, waited, shrunk bool) (*Result, error) {
	needTools := len(tools) > 0
	list := Targets(cfg, needTools)
	if len(list) == 0 {
		// Nothing that can do this turn. Keyless may answer plain chat, never tools.
		if (!needTools || o.PlainFallback) && cfg.AllowKeyless() {
			return keyless(ctx, msgs, o)
		}
		if needTools && len(Targets(cfg, false)) > 0 {
			return nil, &RouteError{NoTools: true}
		}
		return nil, &RouteError{NoModels: true}
	}
	if cfg.PrivateVision && HasImages(msgs) {
		// Private vision: each image is read on this computer (OCR, size, colours) and
		// cloud models get only that text; the picture never leaves.
		msgs = PrepareImages(ctx, msgs, o.OnStatus)
	} else if HasImages(msgs) {
		// Images: models that can see go first. When none can, the image travels as a path.
		var canSee bool
		if list, canSee = visionFirst(list); !canSee && o.OnStatus != nil && !waited && !shrunk {
			o.OnStatus("None of your models can see images, so Blaze sent the image as a file path")
		}
	}
	tokens := Estimate(chars(msgs, tools))
	re := &RouteError{}
	type rest struct {
		name string
		wait time.Duration
		why  string
	}
	var resting []rest
	allTooLarge := true
	for _, t := range list {
		p, _ := config.ProviderByID(t.Provider)
		keys := cfg.AllKeys(t.Provider)
		if len(keys) == 0 {
			keys = []string{""} // ollama / custom without a key
		}
		if w, why := Wait(t.key(), tokens); w > 0 {
			resting = append(resting, rest{t.Name, w, why})
			allTooLarge = allTooLarge && strings.HasPrefix(why, "request bigger")
			continue
		}
		var lastErr error
		answered := false
		anyKey := false
		for ki, key := range keys {
			if len(keys) > 1 && keyResting(t.Provider, t.Model, key) {
				continue
			}
			anyKey = true
			Spend(t.key(), tokens)
			started := false
			wrapped := func(s string) {
				started = true
				if o.OnText != nil {
					o.OnText(s)
				}
			}
			t0 := time.Now()
			r, err, note := callHealing(ctx, p, key, t.Model, msgs, tools, wrapped, &started)
			if r != nil {
				Headers(t.key(), r.Headers)
			}
			if err == nil {
				ms := time.Since(t0).Milliseconds()
				hmu.Lock()
				x := h(t.key())
				x.Fails, x.OK, x.CoolUntil, x.LastErr = 0, x.OK+1, time.Time{}, ""
				if x.AvgMs == 0 {
					x.AvgMs = ms
				} else {
					x.AvgMs = (x.AvgMs*3 + ms) / 4
				}
				hmu.Unlock()
				RecordUsage(t.Provider, t.Model, chars(msgs, tools), r)
				r.Provider, r.Model = t.Provider, t.Model
				r.Route = t.Name + " · " + t.Model
				if ki > 0 {
					note = strings.TrimPrefix(note+" · key "+fmt.Sprint(ki+1), " · ")
				}
				if note != "" {
					r.Route += " · " + note
				}
				if len(re.Failures) > 0 {
					r.Note = re.Failures[0].Name + " couldn't answer, so " + t.Name + " did."
				}
				return r, nil
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if started {
				// Half an answer already shown: don't silently restart on another model.
				return nil, &RouteError{Failures: []Failure{failure(t, err)}}
			}
			lastErr = err
			if IsQuota(err) {
				var he *HTTPError
				ra := 0
				if errors.As(err, &he) {
					ra = he.RetryAfter
				}
				d := Limited(t.key(), err.Error(), ra)
				if len(keys) > 1 {
					// This key is out for this model: rest it and carry on with the next key.
					if d < time.Hour && dailyRe.MatchString(err.Error()) {
						d = time.Hour
					}
					restKey(t.Provider, t.Model, key, d)
					ClearBudget(t.key())
					continue
				}
			}
			break
		}
		if answered {
			continue
		}
		if !anyKey {
			resting = append(resting, rest{t.Name, time.Hour, "every key used up"})
			continue
		}
		if lastErr == nil {
			continue
		}
		err := lastErr
		if !TooLarge(err) {
			allTooLarge = false
		}
		var he *HTTPError
		code := 0
		if errors.As(err, &he) {
			code = he.Code
		}
		cool := 10 * time.Second
		switch {
		case IsQuota(err):
			w, why := Wait(t.key(), 0)
			if w == 0 {
				w = time.Minute
			}
			cool = w
			resting = append(resting, rest{t.Name, w, why})
		case NoToolSupport(err):
			markNoTools(t.Provider, t.Model)
			cool = 10 * time.Minute
		case code == 401 || code == 403 || code == 404:
			cool = 30 * time.Minute
		case code >= 500 || code == 0:
			cool = 20 * time.Second
		}
		hmu.Lock()
		x := h(t.key())
		x.Fails++
		x.CoolUntil = time.Now().Add(cool)
		x.LastErr = trunc(err.Error(), 160)
		hmu.Unlock()
		if !IsQuota(err) {
			re.Failures = append(re.Failures, failure(t, err))
		}
	}
	// Every model said the request was too big: trim the conversation once and go round again.
	if allTooLarge && !shrunk && (len(re.Failures) > 0 || len(resting) > 0) {
		return complete(ctx, cfg, Shrink(msgs, 8), tools, o, waited, true)
	}
	// Everyone is resting but one is back within ~25 s: wait quietly once instead of failing.
	sort.Slice(resting, func(i, j int) bool { return resting[i].wait < resting[j].wait })
	if !waited && len(resting) > 0 && resting[0].wait <= 25*time.Second && len(re.Failures) == 0 {
		if o.OnStatus != nil {
			o.OnStatus(fmt.Sprintf("%s is cooling down, back in %s", resting[0].name, fmtWait(resting[0].wait)))
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(resting[0].wait + 300*time.Millisecond):
		}
		return complete(ctx, cfg, msgs, tools, o, true, shrunk)
	}
	seen := map[string]bool{}
	for _, r := range resting {
		if !seen[r.name] {
			seen[r.name] = true
			re.Resting = append(re.Resting, fmt.Sprintf("%s back in %s (%s)", r.name, fmtWait(r.wait), r.why))
		}
	}
	return nil, re
}

func failure(t Target, err error) Failure {
	f := Failure{Provider: t.Provider, Model: t.Model, Name: t.Name, Raw: trunc(err.Error(), 300)}
	var he *HTTPError
	if errors.As(err, &he) {
		f.Code = he.Code
		f.Message = PlainText(t.Name, he.Code, he.Body)
	} else {
		f.Message = PlainText(t.Name, 0, err.Error())
	}
	return f
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// callHealing makes one call; if the provider refuses the conversation's shape it
// retries once with the tool turns flattened to text, and once trimmed if too large.
func callHealing(ctx context.Context, p config.Provider, key, model string, msgs []Message, tools []Tool, onText func(string), started *bool) (*Result, error, string) {
	r, err := Chat(ctx, p, key, model, msgs, tools, onText)
	if err == nil || *started || ctx.Err() != nil {
		return r, err, ""
	}
	if IsShapeError(err) && !NoToolSupport(err) && !IsQuota(err) {
		r2, err2 := Chat(ctx, p, key, model, Flatten(msgs), tools, onText)
		if err2 == nil {
			return r2, nil, "healed"
		}
		return r2, err, ""
	}
	if TooLarge(err) {
		r2, err2 := Chat(ctx, p, key, model, Shrink(msgs, 10), tools, onText)
		if err2 == nil {
			return r2, nil, "trimmed"
		}
		return r2, err2, ""
	}
	return r, err, ""
}

// keyless answers plain chat on Pollinations, without tools.
func keyless(ctx context.Context, msgs []Message, o Opts) (*Result, error) {
	p, _ := config.ProviderByID(config.KeylessID)
	r, err := Chat(ctx, p, "", p.DefaultModel, Flatten(msgs), nil, o.OnText)
	if err != nil {
		t := Target{Provider: p.ID, Model: p.DefaultModel, Name: config.Name(p.ID)}
		return nil, &RouteError{Failures: []Failure{failure(t, err)}}
	}
	r.ToolCalls = nil
	r.Keyless = true
	r.Provider, r.Model = p.ID, p.DefaultModel
	r.Route = "Pollinations (keyless) · chat only, no tools"
	return r, nil
}

// ---------------- status for the UI ----------------

type TargetStatus struct {
	Target
	Health
	Cooling  int    `json:"cooling"` // seconds left
	Keys     int    `json:"keys"`
	CanTools bool   `json:"can_tools"`
	Resting  string `json:"resting,omitempty"`
}

// Status describes each model a tool turn would try, in order.
func Status(cfg *config.Config) []TargetStatus {
	var out []TargetStatus
	all := Targets(cfg, false)
	for _, t := range all {
		s := TargetStatus{Target: t, Keys: len(cfg.AllKeys(t.Provider)), CanTools: CanUseTools(t.Provider, t.Model)}
		hmu.Lock()
		if x := health[t.key()]; x != nil {
			s.Health = *x
			if d := time.Until(x.CoolUntil); d > 0 {
				s.Cooling = int(d.Seconds()) + 1
			}
		}
		hmu.Unlock()
		if w, why := Wait(t.key(), 0); w > 0 {
			s.Resting = why
			s.Cooling = max(s.Cooling, int(w.Seconds())+1)
		}
		s.NoTools = !s.CanTools
		out = append(out, s)
	}
	return out
}
