package router

// The built-in auto/* presets, ported from OmniRoute (src/app/api/combos/auto/route.ts,
// open-sse/services/autoCombo/{builtinCatalog,autoPrefix,suffixComposition,
// virtualFactory,modePacks}.ts and src/domain/assessment/types.ts).
//
// Every preset is a zero-setup "virtual combo" over the same pool as Auto (every
// provider the user has a key for). What differs is how the pool is filtered and
// ranked:
//   - a variant picks a weight pack (OmniRoute's mode packs: quality-first for
//     coding and smart, ship-fast for fast, cost-saver for cheap, offline-friendly
//     (quota first) for offline, chaos-mode for chaos, the default weights otherwise);
//   - a category filters or biases the pool (vision and multimodal keep only models
//     that can see; coding and reasoning raise models known for it);
//   - a tier refines it (free keeps free-tier models only, pro prefers premium ones,
//     fast/cheap/reliable swap the weight pack, subscription keeps recurring
//     free allowances, thrifty leans cheap).
// Like OmniRoute, every scored preset also sticks with the last known good model
// (LKGP) while it stays healthy, and chaos fans a request out to the top five
// models from different providers at once and keeps the first answer.

import (
	"math"
	"sort"
	"strings"
	"sync"

	"blaze-desktop/internal/config"
	"blaze-desktop/internal/llm"
)

// AutoPreset is one built-in auto/* model.
type AutoPreset struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Desc     string `json:"desc"`
	Variant  string `json:"variant,omitempty"`  // coding, fast, cheap, offline, smart, lkgp, chaos ("" = default weights)
	Category string `json:"category,omitempty"` // coding, reasoning, vision, chat, multimodal
	Tier     string `json:"tier,omitempty"`     // fast, cheap, floor, free, reliable, pro, subscription, thrifty
	Strategy string `json:"-"`                  // legacy presets that map straight onto a combo strategy
}

// AutoPresets lists every built-in auto/* id, the same names OmniRoute uses.
var AutoPresets = []AutoPreset{
	{ID: "auto", Name: "Auto", Desc: "Your Blaze chain, healthiest first, then every provider with a key"},
	{ID: "auto/best-free", Name: "Best Free", Desc: "Free-tier models only, cheapest and healthiest first", Variant: "cheap", Tier: "free"},
	{ID: "auto/best-chat", Name: "Best Chat", Desc: "Balanced everyday chat"},
	{ID: "auto/best-coding", Name: "Best Coding", Desc: "Models known for code, quality first", Variant: "coding", Category: "coding"},
	{ID: "auto/best-coding-fast", Name: "Best Coding Fast", Desc: "Coding models, fastest first", Variant: "fast", Category: "coding"},
	{ID: "auto/best-fast", Name: "Best Fast", Desc: "Lowest latency first", Variant: "fast"},
	{ID: "auto/best-reasoning", Name: "Best Reasoning", Desc: "Thinking models, quality first", Variant: "smart", Category: "reasoning"},
	{ID: "auto/best-vision", Name: "Best Vision", Desc: "Only models that can see images", Category: "vision"},
	{ID: "auto/best-chaos", Name: "Best Chaos", Desc: "Asks the five steadiest models at once and keeps the first answer", Variant: "chaos"},
	{ID: "auto/chaos", Name: "Chaos", Desc: "Parallel fan-out to the top five models from different providers", Variant: "chaos"},
	{ID: "auto/chat", Name: "Chat", Desc: "Everyday chat, default weights"},
	{ID: "auto/cheap", Name: "Cheap", Desc: "Free tiers first, then the lowest price", Variant: "cheap"},
	{ID: "auto/claude-opus", Name: "Claude Opus", Desc: "Strongest reasoning and code you have (Opus-class)", Variant: "smart", Category: "reasoning"},
	{ID: "auto/claude-sonnet", Name: "Claude Sonnet", Desc: "Strong coding and chat (Sonnet-class)", Variant: "coding", Category: "coding"},
	{ID: "auto/claude-haiku", Name: "Claude Haiku", Desc: "Quick and light (Haiku-class)", Variant: "fast"},
	{ID: "auto/coding", Name: "Coding", Desc: "Models known for code", Variant: "coding", Category: "coding"},
	{ID: "auto/fast", Name: "Fast", Desc: "Fastest first", Variant: "fast"},
	{ID: "auto/lkgp", Name: "Last Known Good", Desc: "Sticks with the model that last answered", Variant: "lkgp"},
	{ID: "auto/multimodal", Name: "Multimodal", Desc: "Models that take images and text", Category: "multimodal"},
	{ID: "auto/offline", Name: "Offline", Desc: "Most quota left first, so it keeps answering longest", Variant: "offline"},
	{ID: "auto/pro-chat", Name: "Pro Chat", Desc: "Premium models first for chat", Category: "chat", Tier: "pro"},
	{ID: "auto/pro-coding", Name: "Pro Coding", Desc: "Premium coding models first", Variant: "coding", Category: "coding", Tier: "pro"},
	{ID: "auto/pro-fast", Name: "Pro Fast", Desc: "Premium models, fastest first", Variant: "fast", Tier: "pro"},
	{ID: "auto/pro-reasoning", Name: "Pro Reasoning", Desc: "Premium thinking models first", Variant: "smart", Category: "reasoning", Tier: "pro"},
	{ID: "auto/pro-vision", Name: "Pro Vision", Desc: "Premium models that can see images", Category: "vision", Tier: "pro"},
	{ID: "auto/quality", Name: "Quality", Desc: "Best task fit and stability first", Variant: "smart"},
	{ID: "auto/reasoning", Name: "Reasoning", Desc: "Thinking models first", Category: "reasoning"},
	{ID: "auto/smart", Name: "Smart", Desc: "Quality first, with a little exploration", Variant: "smart"},
	{ID: "auto/subscription", Name: "Subscription", Desc: "Only providers with a recurring free allowance", Tier: "subscription"},
	{ID: "auto/thrifty", Name: "Thrifty", Desc: "Recurring free allowances first, then the cheapest", Variant: "cheap", Tier: "thrifty"},
	{ID: "auto/vision", Name: "Vision", Desc: "Only models that can see images", Category: "vision"},
	{ID: "auto/coding:fast", Name: "Coding Fast", Desc: "Coding models, fastest first", Category: "coding", Tier: "fast"},
	{ID: "auto/coding:cheap", Name: "Coding Cheap", Desc: "Coding models, cheapest first", Category: "coding", Tier: "cheap"},
	{ID: "auto/coding:free", Name: "Coding Free", Desc: "Free-tier coding models only", Category: "coding", Tier: "free"},
	{ID: "auto/coding:pro", Name: "Coding Pro", Desc: "Premium coding models first", Category: "coding", Tier: "pro"},
	{ID: "auto/coding:reliable", Name: "Coding Reliable", Desc: "Coding models, most reliable first", Category: "coding", Tier: "reliable"},
	{ID: "auto/reasoning:pro", Name: "Reasoning Pro", Desc: "Premium thinking models first", Category: "reasoning", Tier: "pro"},
	// Blaze's own presets from before the port (kept: clients may already use them).
	{ID: "auto/free", Name: "Free", Desc: "Same as Auto", Strategy: StratPriority},
	{ID: "auto/quota", Name: "Quota", Desc: "Most quota left today first", Strategy: StratQuotaAware},
}

var autoCategories = map[string]bool{"coding": true, "reasoning": true, "vision": true, "chat": true, "multimodal": true}
var autoTiers = map[string]bool{"fast": true, "cheap": true, "floor": true, "free": true, "reliable": true, "pro": true, "subscription": true, "thrifty": true}
var autoVariants = map[string]bool{"coding": true, "fast": true, "cheap": true, "offline": true, "smart": true, "lkgp": true, "chaos": true}

// autoPreset resolves a model name to its preset. Any auto/<category>[:<tier>] and
// any auto/<variant> resolve on demand, like OmniRoute, even when not listed.
func autoPreset(m string) (AutoPreset, bool) {
	m = strings.ToLower(strings.TrimSpace(m))
	m = strings.TrimPrefix(m, "blazerouter/")
	switch m {
	case "", "auto", "default", "blazerouter", "auto/":
		return AutoPresets[0], true
	}
	if !strings.HasPrefix(m, "auto/") {
		return AutoPreset{}, false
	}
	for _, p := range AutoPresets {
		if p.ID == m {
			return p, true
		}
	}
	suf := strings.TrimPrefix(m, "auto/")
	if autoVariants[suf] {
		return AutoPreset{ID: m, Name: titleWord(suf), Variant: suf}, true
	}
	cat, tier, _ := strings.Cut(suf, ":")
	if autoCategories[cat] && (tier == "" || autoTiers[tier]) {
		return AutoPreset{ID: m, Name: strings.TrimSpace(titleWord(cat) + " " + titleWord(tier)), Category: cat, Tier: tier}, true
	}
	return AutoPreset{}, false
}

func titleWord(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// weights are the scoring weights of one mode pack (OmniRoute's ScoringWeights,
// reduced to the signals Blaze measures).
type weights struct{ quota, health, cost, latency, fit, stability, tier float64 }

var (
	wDefault     = weights{0.1429, 0.1605, 0.1429, 0.1143, 0.0762, 0.0476 + 0.03, 0.0476}
	wShipFast    = weights{0.1133, 0.2667, 0.0276, 0.3048, 0.0952, 0.03, 0.0376}
	wCostSaver   = weights{0.1133, 0.181, 0.3324, 0.0476, 0.0952, 0.0476 + 0.03, 0.0376}
	wQuality     = weights{0.0752, 0.1714, 0.0276, 0.0476, 0.3524, 0.1429 + 0.03, 0.0276}
	wOffline     = weights{0.3324, 0.2667, 0.0752, 0.0476, 0, 0.0952 + 0.03, 0.0376}
	wReliability = weights{0.1133, 0.3524, 0.0181, 0.0476, 0.0952, 0.1905 + 0.04, 0.0276}
	wChaos       = weights{0.0376, 0.4, 0.014, 0.0186, 0.1905, 0.1714 + 0.03, 0.004}
)

func (p AutoPreset) weights() weights {
	w := wDefault
	switch p.Variant {
	case "coding", "smart":
		w = wQuality
	case "fast":
		w = wShipFast
	case "cheap":
		w = wCostSaver
	case "offline":
		w = wOffline
	case "chaos":
		w = wChaos
	}
	if p.Category != "" && p.Category != "chat" && p.Variant == "" {
		w = wQuality
	}
	switch p.Tier {
	case "fast":
		w = wShipFast
	case "cheap", "floor", "thrifty":
		w = wCostSaver
	case "reliable":
		w = wReliability
	}
	return w
}

// lkgp remembers the last model that answered for each preset.
var (
	lkgpMu sync.Mutex
	lkgp   = map[string]string{}
)

func noteGood(combo string, t Target) {
	if combo == "" {
		return
	}
	lkgpMu.Lock()
	lkgp[combo] = t.key()
	lkgpMu.Unlock()
}

func lastGood(combo string) string {
	lkgpMu.Lock()
	defer lkgpMu.Unlock()
	return lkgp[combo]
}

// Model hints for task fit, by category (lower-case substrings of the model id).
var fitHints = map[string][]string{
	"coding":    {"coder", "code", "devstral", "codestral", "qwen3", "deepseek", "kimi", "glm", "gpt-oss", "gpt-4.1", "gpt-5", "claude", "sonnet", "opus", "gemini-2.5-pro", "gemini-3", "minimax", "grok-code"},
	"reasoning": {"r1", "reason", "think", "qwq", "o3", "o4", "magistral", "pro", "gpt-oss", "deepseek-v", "kimi-k2", "opus", "nemotron-ultra", "phi-4-reasoning"},
	"fast":      {"flash", "lite", "mini", "nano", "8b", "7b", "instant", "small", "haiku", "turbo", "scout", "fast"},
	"premium":   {"pro", "opus", "sonnet", "gpt-5", "gpt-4.1", "gpt-4o", "o3", "o4", "405b", "235b", "480b", "671b", "large", "ultra", "max", "k2", "r1", "v3", "glm-4.6", "glm-5"},
}

func hits(model string, cat string) bool {
	low := strings.ToLower(model)
	for _, h := range fitHints[cat] {
		if strings.Contains(low, h) {
			return true
		}
	}
	return false
}

func canSee(t Target) bool {
	if llm.Info(t.Provider, t.Model).Vision {
		return true
	}
	low := strings.ToLower(t.Model)
	for _, h := range []string{"vision", "-vl", "vl-", "gemini", "gpt-4o", "gpt-4.1", "gpt-5", "llama-4", "pixtral", "claude", "gemma-3", "qwen2.5-vl", "qwen3-vl", "kimi-vl", "llava"} {
		if strings.Contains(low, h) {
			return true
		}
	}
	return false
}

func isFree(t Target) bool {
	if strings.HasSuffix(t.Model, ":free") || llm.Info(t.Provider, t.Model).Free {
		return true
	}
	return cost(t) == 0
}

func premium(t Target) bool {
	return hits(t.Model, "premium") || llm.Info(t.Provider, t.Model).Context >= 400000
}

func recurring(pid string) bool {
	if f, ok := Free(pid); ok {
		return strings.HasPrefix(f.Type, "recurring") || f.Type == "local"
	}
	p, ok := config.ProviderByID(pid)
	return ok && p.Free
}

// filterPool applies the category and tier filters. Like OmniRoute, subscription
// fails closed (no fallback to other capacity); pro and free fall back to the whole
// pool rather than leave a free-tier user with nothing.
func (p AutoPreset) filterPool(ts []Target) []Target {
	keep := func(ok func(Target) bool) []Target {
		var out []Target
		for _, t := range ts {
			if ok(t) {
				out = append(out, t)
			}
		}
		return out
	}
	if p.Category == "vision" || p.Category == "multimodal" {
		ts = keep(canSee)
	}
	switch p.Tier {
	case "free":
		if f := keep(isFree); len(f) > 0 {
			ts = f
		}
	case "subscription":
		ts = keep(func(t Target) bool { return recurring(t.Provider) })
	}
	return ts
}

// score ranks one target for a preset (0..1, higher first).
func (p AutoPreset) score(t Target, w weights, used map[string]int, maxRoom float64) float64 {
	h := llm.HealthOf(t.Provider, t.Model)
	health := 1 - float64(min(h.Fails, 3))/3
	if llm.CoolingFor(t.Provider, t.Model) > 0 {
		health = 0
	}
	lat := 0.5
	if h.AvgMs > 0 {
		lat = 1 / (1 + float64(h.AvgMs)/1500)
	}
	costInv := 1 / (1 + cost(t))
	room := 0.0
	if maxRoom > 0 {
		room = math.Log1p(float64(quotaRoom(t.Provider))) / math.Log1p(maxRoom)
	}
	quota := room / (1 + float64(used[t.key()])/100)
	if wq, _ := llm.Wait(t.key(), 0); wq > 0 {
		quota = 0
	}
	stab := 0.5
	if h.OK+h.Fails > 0 {
		stab = float64(h.OK) / float64(h.OK+h.Fails)
	}
	fit := 0.5
	switch {
	case p.Category == "coding" || p.Variant == "coding":
		fit = b2f(hits(t.Model, "coding"), 1, 0.3)
		if llm.CanUseTools(t.Provider, t.Model) {
			fit = math.Min(1, fit+0.2)
		}
	case p.Category == "reasoning" || p.Variant == "smart":
		fit = b2f(hits(t.Model, "reasoning") || premium(t), 1, 0.3)
	case p.Category == "vision" || p.Category == "multimodal":
		fit = b2f(canSee(t), 1, 0)
	case p.Variant == "fast" || p.Tier == "fast":
		fit = b2f(hits(t.Model, "fast"), 1, 0.4)
	}
	tier := b2f(isFree(t), 1, 0.5)
	switch p.Tier {
	case "pro":
		tier = b2f(premium(t), 1, 0)
		fit = math.Max(fit, tier) // premium is the point of a pro preset
	case "thrifty":
		tier = b2f(recurring(t.Provider), 1, 0.3)
	}
	s := w.quota*quota + w.health*health + w.cost*costInv + w.latency*lat + w.fit*fit + w.stability*stab + w.tier*tier
	if p.Tier == "pro" && premium(t) {
		s += 1 // premium models first, the rest after
	}
	return s
}

func b2f(b bool, yes, no float64) float64 {
	if b {
		return yes
	}
	return no
}

// chaosPanel is how many models a chaos preset asks at once (OmniRoute's default).
const chaosPanel = 5

// orderPreset filters and ranks the Auto pool for a scored preset. Resting models
// go last, as everywhere else.
func (r *Router) orderPreset(p AutoPreset, pool []Target, needTools bool) []Target {
	var list []Target
	for _, t := range p.filterPool(pool) {
		if needTools && !llm.CanUseTools(t.Provider, t.Model) {
			continue
		}
		list = append(list, t)
	}
	if len(list) == 0 {
		return nil
	}
	w := p.weights()
	used := r.log.todayByTarget()
	maxRoom := 0.0
	for _, t := range list {
		maxRoom = math.Max(maxRoom, float64(quotaRoom(t.Provider)))
	}
	sc := map[string]float64{}
	for _, t := range list {
		sc[t.key()] = p.score(t, w, used, maxRoom)
	}
	sort.SliceStable(list, func(i, j int) bool { return sc[list[i].key()] > sc[list[j].key()] })
	if p.Variant != "chaos" {
		if lg := lastGood(p.ID); lg != "" {
			for i, t := range list {
				if t.key() == lg && llm.CoolingFor(t.Provider, t.Model) == 0 {
					list = append([]Target{t}, append(list[:i:i], list[i+1:]...)...)
					break
				}
			}
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
	list = append(ready, cooling...)
	if p.Variant == "chaos" {
		// One model per provider, the top five.
		seen := map[string]bool{}
		var panel []Target
		for _, t := range list {
			if seen[t.Provider] {
				continue
			}
			seen[t.Provider] = true
			panel = append(panel, t)
			if len(panel) == chaosPanel {
				break
			}
		}
		if len(panel) > 0 {
			list = panel
		}
	}
	return list
}
