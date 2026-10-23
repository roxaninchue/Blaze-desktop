package router

// Combos: named fallback chains with a routing strategy (OmniRoute's "combos").
// A client asks for a combo by its id or name as the model ("my-chain"), for the
// zero-setup "auto" combo, for one provider's model ("groq/llama-3.3-70b-versatile"),
// or for a bare model id that one of the user's providers offers.

import (
	"errors"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"

	"blaze-desktop/internal/config"
	"blaze-desktop/internal/llm"
)

// Strategies (a port of the most useful of OmniRoute's routing strategies).
const (
	StratPriority     = "priority"      // top to bottom, the next one when one fails
	StratRoundRobin   = "round-robin"   // spread requests across the list
	StratLeastLatency = "least-latency" // the one answering fastest lately first
	StratQuotaAware   = "quota-aware"   // the one with the most quota left first (least used today, not rate limited)
	StratCheapest     = "cheapest"      // free tiers first, then the lowest price
	StratRandom       = "random"
)

var Strategies = []struct {
	ID, Name, Desc string
}{
	{StratPriority, "Priority", "Top to bottom. The next one takes over when one fails or runs out."},
	{StratRoundRobin, "Round robin", "Takes turns, so no single free tier runs dry first."},
	{StratLeastLatency, "Least latency", "Whichever has been answering fastest goes first."},
	{StratQuotaAware, "Quota aware", "Whichever has the most quota left today goes first."},
	{StratCheapest, "Cheapest", "Free tiers first, then the lowest price per token."},
	{StratRandom, "Random", "A random order every request."},
}

func validStrategy(s string) bool {
	for _, x := range Strategies {
		if x.ID == s {
			return true
		}
	}
	return false
}

type Combo struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Strategy string         `json:"strategy"`
	Targets  []config.Entry `json:"targets"`
	Note     string         `json:"note,omitempty"`
}

// Target is one provider + model a request may go to.
type Target struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Name     string `json:"name"`
}

func (t Target) key() string { return t.Provider + "|" + t.Model }

// Plan is who a request will try, in order, and what it asked for.
type Plan struct {
	Asked   string   // the model the client sent
	Combo   string   // "auto", an auto/* preset, a combo id, or "" for a single model
	Targets []Target //
	Chaos   bool     // ask every target at once and keep the first answer
}

var (
	rrMu sync.Mutex
	rr   = map[string]int{}
)

// ErrNoModel is returned when the model a client asked for isn't one BlazeRouter can serve.
var ErrNoModel = errors.New("no such model")

// isAuto is true for the names that mean one of the built-in auto/* presets.
func isAuto(m string) (bool, string) {
	p, ok := autoPreset(legacyAuto(m))
	if !ok {
		return false, ""
	}
	if p.Strategy != "" {
		return true, p.Strategy
	}
	return true, StratPriority
}

// legacyAuto maps Blaze's pre-port aliases (blazerouter/fast…) onto auto/*.
func legacyAuto(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "blazerouter/fast":
		return "auto/fast"
	case "blazerouter/cheap":
		return "auto/cheap"
	case "blazerouter/quota":
		return "auto/quota"
	}
	return m
}

// plan works out the targets for a requested model.
func (r *Router) plan(asked string, needTools bool) (Plan, error) {
	cfg := r.cfg()
	pl := Plan{Asked: asked}
	m := strings.TrimSpace(asked)
	m = strings.TrimPrefix(m, "blazerouter/")
	if p, ok := autoPreset(legacyAuto(asked)); ok {
		pl.Combo = p.ID
		switch {
		case p.ID == "auto":
			pl.Targets = r.order("auto", StratPriority, r.autoTargets(cfg), needTools)
		case p.Strategy != "":
			pl.Targets = r.order(p.ID, p.Strategy, r.autoTargets(cfg), needTools)
		default:
			pl.Targets = r.orderPreset(p, r.autoTargets(cfg), needTools)
			pl.Chaos = p.Variant == "chaos" && len(pl.Targets) > 1
		}
		return pl, nil
	}
	s := r.Settings()
	if c := s.combo(strings.TrimPrefix(m, "combo/")); c != nil {
		pl.Combo = c.ID
		var ts []Target
		for _, e := range c.Targets {
			if e.Provider == config.RouterID || !llm.Usable(cfg, e.Provider) {
				continue
			}
			mod := e.Model
			if mod == "" {
				mod = cfg.Model(e.Provider)
			}
			ts = append(ts, Target{Provider: e.Provider, Model: llm.ResolveModel(e.Provider, mod), Name: config.Name(e.Provider)})
		}
		pl.Targets = r.order(c.ID, c.Strategy, ts, needTools)
		return pl, nil
	}
	// provider/model
	if i := strings.Index(m, "/"); i > 0 {
		pid, mod := m[:i], m[i+1:]
		if _, ok := config.ProviderByID(pid); ok && pid != config.RouterID && llm.Usable(cfg, pid) {
			pl.Targets = []Target{{Provider: pid, Model: mod, Name: config.Name(pid)}}
			return pl, nil
		}
	}
	// A bare model id: every usable provider that lists it (Blaze's catalog or its chosen model).
	var ts []Target
	for _, p := range routable() {
		if !llm.Usable(cfg, p.ID) {
			continue
		}
		has := cfg.Model(p.ID) == m
		for _, mi := range llm.Catalog[p.ID] {
			has = has || mi.ID == m
		}
		if has {
			ts = append(ts, Target{Provider: p.ID, Model: m, Name: config.Name(p.ID)})
		}
	}
	if len(ts) == 0 {
		return pl, ErrNoModel
	}
	pl.Targets = r.order(m, StratPriority, ts, needTools)
	return pl, nil
}

// autoTargets is the zero-setup combo: Blaze's own Auto chain (the user's chain, or
// every free tier with a key), then every other provider that has a key, so a key
// added for any catalog provider joins in without any setup.
func (r *Router) autoTargets(cfg *config.Config) []Target {
	var out []Target
	seen := map[string]bool{}
	add := func(pid, model string) {
		if pid == config.RouterID || pid == config.KeylessID || !llm.Usable(cfg, pid) {
			return
		}
		if model == "" {
			model = cfg.Model(pid)
		}
		model = llm.ResolveModel(pid, model)
		t := Target{Provider: pid, Model: model, Name: config.Name(pid)}
		if model == "" || seen[t.key()] {
			return
		}
		seen[t.key()] = true
		out = append(out, t)
	}
	for _, e := range llm.Entries(cfg) {
		add(e.Provider, e.Model)
	}
	for _, p := range routable() {
		if p.ID == "ollama" || p.ID == config.Custom {
			continue // local and custom services join Auto only when they are in the chain
		}
		if cfg.Key(p.ID) != "" {
			add(p.ID, "")
		}
	}
	return out
}

// order applies a strategy, then the circuit breaker: models resting after failures
// or rate limits go last (soonest back first), and models that can't use tools are
// dropped for a request that sends tools.
func (r *Router) order(id, strat string, ts []Target, needTools bool) []Target {
	var list []Target
	for _, t := range ts {
		if needTools && !llm.CanUseTools(t.Provider, t.Model) {
			continue
		}
		list = append(list, t)
	}
	if len(list) == 0 {
		return nil
	}
	switch strat {
	case StratRoundRobin:
		rrMu.Lock()
		n := rr[id] % len(list)
		rr[id]++
		rrMu.Unlock()
		list = append(list[n:], list[:n]...)
	case StratLeastLatency:
		sort.SliceStable(list, func(i, j int) bool {
			return llm.HealthOf(list[i].Provider, list[i].Model).AvgMs < llm.HealthOf(list[j].Provider, list[j].Model).AvgMs
		})
	case StratQuotaAware:
		used := r.log.todayByTarget()
		sort.SliceStable(list, func(i, j int) bool {
			wi, _ := llm.Wait(list[i].key(), 0)
			wj, _ := llm.Wait(list[j].key(), 0)
			if (wi > 0) != (wj > 0) {
				return wi == 0
			}
			fi, fj := quotaRoom(list[i].Provider), quotaRoom(list[j].Provider)
			if fi != fj {
				return fi > fj
			}
			return used[list[i].key()] < used[list[j].key()]
		})
	case StratCheapest:
		sort.SliceStable(list, func(i, j int) bool { return cost(list[i]) < cost(list[j]) })
	case StratRandom:
		rand.Shuffle(len(list), func(i, j int) { list[i], list[j] = list[j], list[i] })
	default: // priority; Auto also puts healthier models first among the ready ones
		if id == "auto" {
			sort.SliceStable(list, func(i, j int) bool {
				return min(llm.HealthOf(list[i].Provider, list[i].Model).Fails, 3) < min(llm.HealthOf(list[j].Provider, list[j].Model).Fails, 3)
			})
		}
	}
	var ready, cooling []Target
	for _, t := range list {
		if llm.CoolingFor(t.Provider, t.Model) > 0 {
			cooling = append(cooling, t)
		} else {
			ready = append(ready, t)
		}
	}
	sort.SliceStable(cooling, func(i, j int) bool {
		return llm.CoolingFor(cooling[i].Provider, cooling[i].Model) < llm.CoolingFor(cooling[j].Provider, cooling[j].Model)
	})
	return append(ready, cooling...)
}

// quotaRoom ranks a provider's documented free allowance: recurring capped pools by
// size, uncapped ones high, one-time credits low.
func quotaRoom(pid string) int64 {
	f, ok := Free(pid)
	if !ok {
		return 0
	}
	switch f.Type {
	case "recurring-uncapped", "local":
		return 500_000_000
	case "one-time-initial":
		return 1
	}
	return f.Monthly
}

// cost is a rough price rank: 0 for free tiers, otherwise the model's known price.
func cost(t Target) float64 {
	if strings.HasSuffix(t.Model, ":free") {
		return 0
	}
	if p, ok := config.ProviderByID(t.Provider); ok && p.Free && t.Provider != "openrouter" {
		return 0
	}
	if in, out, ok := llm.Price(t.Provider, t.Model); ok {
		return in + out
	}
	return 50 // unknown price: after the known ones
}

// SaveCombo adds or replaces a combo. An empty id makes one from the name.
func (r *Router) SaveCombo(c Combo) (Combo, error) {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return c, errors.New("give the combo a name")
	}
	if c.ID == "" {
		c.ID = slug(c.Name)
	}
	if c.ID == "auto" || strings.HasPrefix(c.ID, "auto/") {
		return c, errors.New(`"auto" is the built-in combo; pick another name`)
	}
	if !validStrategy(c.Strategy) {
		c.Strategy = StratPriority
	}
	var ts []config.Entry
	for _, e := range c.Targets {
		if e.Provider != "" && e.Provider != config.RouterID {
			ts = append(ts, config.Entry{Provider: e.Provider, Model: strings.TrimSpace(e.Model)})
		}
	}
	c.Targets = ts
	r.mu.Lock()
	s := r.set
	found := false
	for i := range s.Combos {
		if s.Combos[i].ID == c.ID {
			s.Combos[i] = c
			found = true
		}
	}
	if !found {
		s.Combos = append(s.Combos, c)
	}
	r.mu.Unlock()
	return c, s.save()
}

// DeleteCombo removes a combo.
func (r *Router) DeleteCombo(id string) error {
	r.mu.Lock()
	s := r.set
	var keep []Combo
	for _, c := range s.Combos {
		if c.ID != id {
			keep = append(keep, c)
		}
	}
	s.Combos = keep
	r.mu.Unlock()
	return s.save()
}

func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, c := range strings.ToLower(s) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			b.WriteRune(c)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if out == "" {
		out = "combo"
	}
	return out
}
