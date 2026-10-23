package llm

// Model catalog: which models each provider offers, what they can do (tools,
// vision), how big their context is, and the live list from the provider's own
// API merged on top (ported from the Android app's ModelCatalog.kt and
// Providers.kt). The built-in table only describes models; whether your key can
// actually use one comes from the live list and, on request, a one-word probe.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"blaze-desktop/internal/config"
)

// Cap is a tri-state capability: yes, no, or not known (allowed, learned at run time).
type Cap string

const (
	Yes     Cap = "yes"
	No      Cap = "no"
	Unknown Cap = "unknown"
)

type ModelInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Tools   Cap    `json:"tools"`
	Vision  bool   `json:"vision"`
	Context int    `json:"context"` // tokens, 0 = unknown
	Free    bool   `json:"free"`    // on the provider's free tier
	Note    string `json:"note,omitempty"`
	Live    bool   `json:"live"`  // seen in the provider's live list
	Known   bool   `json:"known"` // in Blaze's built-in table
}

func m(id, name string, tools Cap, vision bool, ctx int, free bool, note string) ModelInfo {
	return ModelInfo{ID: id, Name: name, Tools: tools, Vision: vision, Context: ctx, Free: free, Note: note, Known: true}
}

// Catalog is Blaze's built-in knowledge of each provider's main chat models.
var Catalog = map[string][]ModelInfo{
	"gemini": {
		m("gemini-3.8-flash", "Gemini 3.8 Flash", Yes, true, 1048576, true, "Blaze's default"),
		m("gemini-2.5-pro", "Gemini 2.5 Pro", Yes, true, 1048576, true, "strongest, low free limits"),
		m("gemini-2.5-flash", "Gemini 2.5 Flash", Yes, true, 1048576, true, ""),
		m("gemini-2.5-flash-lite", "Gemini 2.5 Flash-Lite", Yes, true, 1048576, true, "fastest"),
		m("gemini-flash-latest", "Gemini Flash (latest)", Yes, true, 1048576, true, "always the newest Flash"),
	},
	"groq": {
		m("llama-3.3-70b-versatile", "Llama 3.3 70B", Yes, false, 131072, true, ""),
		m("openai/gpt-oss-120b", "GPT-OSS 120B", Yes, false, 131072, true, ""),
		m("openai/gpt-oss-20b", "GPT-OSS 20B", Yes, false, 131072, true, "fast"),
		m("moonshotai/kimi-k2-instruct-0905", "Kimi K2", Yes, false, 262144, true, ""),
		m("qwen/qwen3-32b", "Qwen3 32B", Yes, false, 131072, true, ""),
		m("meta-llama/llama-4-scout-17b-16e-instruct", "Llama 4 Scout", Yes, true, 131072, true, ""),
		m("llama-3.1-8b-instant", "Llama 3.1 8B Instant", Yes, false, 131072, true, "small, chat only recommended"),
	},
	"cerebras": {
		m("gpt-oss-120b", "GPT-OSS 120B", Yes, false, 131072, true, ""),
		m("llama-3.3-70b", "Llama 3.3 70B", Yes, false, 65536, true, ""),
		m("qwen-3-32b", "Qwen3 32B", Yes, false, 65536, true, ""),
	},
	"openrouter": {
		m("openrouter/free", "OpenRouter free (auto pick)", Unknown, false, 0, true, "Blaze swaps in today's best free tool model"),
	},
	"mistral": {
		m("mistral-small-latest", "Mistral Small", Yes, true, 131072, true, ""),
		m("mistral-medium-latest", "Mistral Medium", Yes, true, 131072, true, ""),
		m("mistral-large-latest", "Mistral Large", Yes, false, 131072, true, ""),
		m("codestral-latest", "Codestral", Yes, false, 262144, true, "code"),
	},
	"nvidia": {
		m("meta/llama-3.3-70b-instruct", "Llama 3.3 70B", Yes, false, 131072, true, ""),
		m("openai/gpt-oss-120b", "GPT-OSS 120B", Yes, false, 131072, true, ""),
		m("deepseek-ai/deepseek-v3.1", "DeepSeek V3.1", Yes, false, 131072, true, ""),
		m("moonshotai/kimi-k2-instruct", "Kimi K2", Yes, false, 131072, true, ""),
		m("qwen/qwen3-235b-a22b", "Qwen3 235B", Unknown, false, 131072, true, ""),
	},
	"deepseek": {
		m("deepseek-chat", "DeepSeek Chat", Yes, false, 131072, false, ""),
		m("deepseek-reasoner", "DeepSeek Reasoner", Unknown, false, 131072, false, "thinks first, slower"),
	},
	"moonshot": {
		m("kimi-k2-0905-preview", "Kimi K2", Yes, false, 262144, false, ""),
		m("kimi-k2-turbo-preview", "Kimi K2 Turbo", Yes, false, 262144, false, "faster"),
		m("moonshot-v1-128k", "Moonshot v1 128K", Yes, false, 131072, false, ""),
	},
	"openai": {
		m("gpt-5", "GPT-5", Yes, true, 400000, false, ""),
		m("gpt-5-mini", "GPT-5 mini", Yes, true, 400000, false, ""),
		m("gpt-5-nano", "GPT-5 nano", Yes, true, 400000, false, "cheapest"),
		m("gpt-4.1", "GPT-4.1", Yes, true, 1047576, false, ""),
		m("gpt-4.1-mini", "GPT-4.1 mini", Yes, true, 1047576, false, ""),
		m("gpt-4o-mini", "GPT-4o mini", Yes, true, 128000, false, ""),
	},
	"anthropic": {
		m("claude-sonnet-4-5", "Claude Sonnet 4.5", Yes, true, 200000, false, ""),
		m("claude-opus-4-1", "Claude Opus 4.1", Yes, true, 200000, false, "most capable, priciest"),
		m("claude-haiku-4-5", "Claude Haiku 4.5", Yes, true, 200000, false, "fast"),
	},
	"xai": {
		m("grok-4", "Grok 4", Yes, true, 256000, false, ""),
		m("grok-4-fast", "Grok 4 Fast", Yes, true, 2000000, false, ""),
		m("grok-code-fast-1", "Grok Code Fast", Yes, false, 256000, false, "code"),
		m("grok-3-mini", "Grok 3 mini", Yes, false, 131072, false, ""),
	},
	config.KeylessID: {
		m("openai-fast", "Pollinations (keyless)", No, false, 0, true, "plain chat only, can't use tools"),
	},
}

var (
	nonChat = []string{"tts", "image", "imagen", "veo", "lyria", "embedding", "embed", "transcribe", "robotics", "native-audio",
		"audio", "computer-use", "omni", "banana", "aqa", "deep-research", "antigravity", "learnlm", "whisper", "dall-e",
		"moderation", "davinci", "babbage", "realtime", "sora", "rerank", "guard", "nemoretriever", "ocr", "speech", "-live", "live-"}
	noToolsRe = regexp.MustCompile(`(?i)(gemma|deepseek-r1|-r1\b|r1-distill|o1-mini|\bo1-preview|-base\b|instruct-v0\.1|search-preview|pollinations)`)
	visionRe  = regexp.MustCompile(`(?i)(vision|-vl\b|-vl-|gemini|gpt-4o|gpt-4\.1|gpt-5|claude|pixtral|llava|llama-4|grok-4|mistral-small|mistral-medium)`)
)

// IsChat is false for embedding, speech, image and other non-chat models.
func IsChat(id string) bool {
	low := strings.ToLower(id)
	for _, s := range nonChat {
		if strings.Contains(low, s) {
			return false
		}
	}
	return true
}

// Info describes a model: the built-in entry when there is one, otherwise a guess from its name.
func Info(provider, model string) ModelInfo {
	for _, mi := range Catalog[provider] {
		if mi.ID == model {
			return mi
		}
	}
	mi := ModelInfo{ID: model, Name: model, Tools: Unknown}
	if provider == config.KeylessID {
		mi.Tools = No
	} else if noToolsRe.MatchString(model) {
		mi.Tools = No
	} else if provider != "ollama" && provider != config.Custom && IsChat(model) {
		mi.Tools = Unknown
	}
	mi.Vision = visionRe.MatchString(model)
	if provider == "openrouter" {
		mi.Free = strings.HasSuffix(model, ":free") || model == "openrouter/free"
	} else if p, ok := config.ProviderByID(provider); ok {
		mi.Free = p.Free
	}
	return mi
}

// CanUseTools is false only when the model is known (or learned) not to support tool calls.
func CanUseTools(provider, model string) bool {
	if provider == config.KeylessID {
		return false
	}
	if learnedNoTools(provider, model) {
		return false
	}
	return Info(provider, model).Tools != No
}

// ---------------- live list ----------------

type cacheEntry struct {
	At     time.Time   `json:"at"`
	Tag    string      `json:"tag"`
	Plan   string      `json:"plan,omitempty"`
	Models []ModelInfo `json:"models"`
}

var (
	cacheMu sync.Mutex
	cache   map[string]cacheEntry
)

func cachePath() string { return filepath.Join(config.Dir(), "desktop", "models-cache.json") }

func loadCache() {
	if cache != nil {
		return
	}
	cache = map[string]cacheEntry{}
	b, _ := os.ReadFile(cachePath())
	_ = json.Unmarshal(b, &cache)
}

func saveCache() {
	_ = os.MkdirAll(filepath.Dir(cachePath()), 0o700)
	b, _ := json.Marshal(cache)
	_ = os.WriteFile(cachePath(), b, 0o600)
}

// keyTag tells cached lists apart per key without storing the key.
func keyTag(p config.Provider, key string) string {
	h := uint32(2166136261)
	for _, c := range key + "|" + p.BaseURL {
		h = (h ^ uint32(c)) * 16777619
	}
	return p.ID + ":" + itoa(int(h))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// ModelList is what Settings and the model picker show for one provider.
type ModelList struct {
	Provider string      `json:"provider"`
	Models   []ModelInfo `json:"models"`
	Plan     string      `json:"plan,omitempty"`
	Error    string      `json:"error,omitempty"`
	Fetched  string      `json:"fetched,omitempty"` // when the live list was fetched
}

// Options merges the built-in catalog with the provider's live list (cached for 12 h,
// or fetched now when refresh is set). Non-chat models are left out.
func Options(ctx context.Context, p config.Provider, key string, refresh bool) ModelList {
	out := ModelList{Provider: p.ID}
	var live []ModelInfo
	cacheMu.Lock()
	loadCache()
	ce, ok := cache[p.ID]
	cacheMu.Unlock()
	tag := keyTag(p, key)
	fresh := ok && ce.Tag == tag && time.Since(ce.At) < 12*time.Hour
	needKey := !config.Keyless(p.ID) && p.ID != "openrouter"
	if fresh && !refresh {
		live, out.Plan = ce.Models, ce.Plan
		out.Fetched = ce.At.Format("Jan 2 15:04")
	} else if p.ID != config.KeylessID && (key != "" || !needKey) && p.BaseURL != "" {
		ls, err := ListModels(ctx, p, key)
		if err != nil {
			out.Error = err.Error()
			if ok && ce.Tag == tag {
				live = ce.Models
				out.Fetched = ce.At.Format("Jan 2 15:04")
			}
		} else {
			live = ls
			cacheMu.Lock()
			cache[p.ID] = cacheEntry{At: time.Now(), Tag: tag, Plan: ce.Plan, Models: ls}
			saveCache()
			cacheMu.Unlock()
			out.Fetched = time.Now().Format("Jan 2 15:04")
		}
	}
	seen := map[string]int{}
	for _, mi := range Catalog[p.ID] {
		seen[mi.ID] = len(out.Models)
		out.Models = append(out.Models, mi)
	}
	for _, l := range live {
		if i, ok := seen[l.ID]; ok {
			k := out.Models[i]
			k.Live = true
			if l.Context > 0 {
				k.Context = l.Context
			}
			if l.Tools != Unknown && k.Tools == Unknown {
				k.Tools = l.Tools
			}
			if l.Note != "" {
				k.Note = l.Note
			}
			out.Models[i] = k
			continue
		}
		seen[l.ID] = len(out.Models)
		out.Models = append(out.Models, l)
	}
	for i := range out.Models {
		if learnedNoTools(p.ID, out.Models[i].ID) {
			out.Models[i].Tools = No
			out.Models[i].Note = strings.TrimPrefix(out.Models[i].Note+" · refused tools", " · ")
		}
	}
	// Built-in entries first, then live ones (stable before preview, newest first).
	sort.SliceStable(out.Models, func(i, j int) bool {
		a, b := out.Models[i], out.Models[j]
		if a.Known != b.Known {
			return a.Known
		}
		if a.Known {
			return false
		}
		if ra, rb := rank(a.ID), rank(b.ID); ra != rb {
			return ra < rb
		}
		return a.ID < b.ID
	})
	return out
}

func rank(id string) int {
	switch {
	case strings.Contains(id, "latest"):
		return 2
	case strings.Contains(id, "preview") || strings.Contains(id, "exp"):
		return 1
	}
	return 0
}

// ListModels asks the provider which models the key can see (chat models only).
func ListModels(ctx context.Context, p config.Provider, key string) ([]ModelInfo, error) {
	if p.ID == "gemini" {
		return listGemini(ctx, key)
	}
	u := strings.TrimRight(p.BaseURL, "/") + "/models"
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	for k, v := range AuthHeaders(p.ID, key) {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 25 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode >= 300 {
		return nil, &HTTPError{Code: resp.StatusCode, Body: cleanErr(string(raw))}
	}
	var r struct {
		Data []struct {
			ID                  string   `json:"id"`
			Name                string   `json:"name"`
			DisplayName         string   `json:"display_name"`
			ContextLength       int      `json:"context_length"`
			ContextWindow       int      `json:"context_window"`
			MaxModelLen         int      `json:"max_model_len"`
			SupportedParameters []string `json:"supported_parameters"`
			Architecture        struct {
				InputModalities []string `json:"input_modalities"`
			} `json:"architecture"`
			Capabilities struct {
				FunctionCalling *bool `json:"function_calling"`
				Vision          *bool `json:"vision"`
			} `json:"capabilities"`
			Active *bool `json:"active"`
		} `json:"data"`
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	_ = json.Unmarshal(raw, &r)
	var out []ModelInfo
	for _, d := range r.Data {
		id := strings.TrimPrefix(d.ID, "models/")
		if id == "" || !IsChat(id) || (d.Active != nil && !*d.Active) {
			continue
		}
		mi := Info(p.ID, id)
		mi.Known = false
		mi.Live = true
		if n := firstNonEmpty(d.DisplayName, d.Name); n != "" {
			mi.Name = n
		}
		mi.Context = max(d.ContextLength, d.ContextWindow, d.MaxModelLen)
		if p.ID == "openrouter" {
			mi.Tools = No
			for _, s := range d.SupportedParameters {
				if s == "tools" {
					mi.Tools = Yes
				}
			}
			for _, s := range d.Architecture.InputModalities {
				if s == "image" {
					mi.Vision = true
				}
			}
		}
		if d.Capabilities.FunctionCalling != nil { // Mistral
			if *d.Capabilities.FunctionCalling {
				mi.Tools = Yes
			} else {
				mi.Tools = No
			}
		}
		if d.Capabilities.Vision != nil {
			mi.Vision = *d.Capabilities.Vision
		}
		out = append(out, mi)
	}
	for _, d := range r.Models { // Ollama's native shape, just in case
		id := firstNonEmpty(d.Model, d.Name)
		if id != "" && IsChat(id) {
			mi := Info(p.ID, id)
			mi.Live = true
			out = append(out, mi)
		}
	}
	if p.ID == "openrouter" {
		// OpenRouter lists hundreds; keep the free ones that can use tools first, then the rest of the free ones.
		var free []ModelInfo
		for _, mi := range out {
			if mi.Free {
				free = append(free, mi)
			}
		}
		sort.SliceStable(free, func(i, j int) bool { return free[i].Tools == Yes && free[j].Tools != Yes })
		out = free
	}
	return dedupe(out), nil
}

func listGemini(ctx context.Context, key string) ([]ModelInfo, error) {
	var out []ModelInfo
	token := ""
	for page := 0; page < 10; page++ {
		u := "https://generativelanguage.googleapis.com/v1beta/models?pageSize=1000"
		if token != "" {
			u += "&pageToken=" + url.QueryEscape(token)
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("x-goog-api-key", key)
		resp, err := (&http.Client{Timeout: 25 * time.Second}).Do(req)
		if err != nil {
			return nil, err
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			return nil, &HTTPError{Code: resp.StatusCode, Body: cleanErr(string(raw))}
		}
		var r struct {
			Models []struct {
				Name             string   `json:"name"`
				DisplayName      string   `json:"displayName"`
				InputTokenLimit  int      `json:"inputTokenLimit"`
				SupportedMethods []string `json:"supportedGenerationMethods"`
			} `json:"models"`
			Next string `json:"nextPageToken"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, err
		}
		for _, g := range r.Models {
			id := strings.TrimPrefix(g.Name, "models/")
			gen := false
			for _, s := range g.SupportedMethods {
				if s == "generateContent" {
					gen = true
				}
			}
			if !strings.HasPrefix(id, "gemini") || !gen || !IsChat(id) {
				continue
			}
			mi := Info("gemini", id)
			mi.Known, mi.Live = false, true
			mi.Name = firstNonEmpty(g.DisplayName, id)
			mi.Context = g.InputTokenLimit
			if mi.Tools == Unknown {
				mi.Tools = Yes // every Gemini chat model takes function declarations
			}
			mi.Vision, mi.Free = true, true
			out = append(out, mi)
		}
		token = r.Next
		if token == "" {
			break
		}
	}
	return dedupe(out), nil
}

func dedupe(in []ModelInfo) []ModelInfo {
	seen := map[string]bool{}
	var out []ModelInfo
	for _, mi := range in {
		if !seen[mi.ID] {
			seen[mi.ID] = true
			out = append(out, mi)
		}
	}
	return out
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

// ---------------- plan detection ----------------

// Detect probes each listed model with a one-word request and keeps the ones the
// key's plan allows (404 / 403 / a quota of 0 drop a model; a 429 keeps it as busy).
// Big catalogues (over 20 models) are not probed, to save quota.
func Detect(ctx context.Context, p config.Provider, key string) ModelList {
	ls, err := ListModels(ctx, p, key)
	out := ModelList{Provider: p.ID, Fetched: time.Now().Format("Jan 2 15:04")}
	if err != nil {
		out.Error = err.Error()
		return out
	}
	if len(ls) == 0 {
		out.Error = "This key can't see any chat models."
		return out
	}
	if len(ls) > 20 {
		out.Plan = "Key verified"
		if p.ID == "openrouter" {
			if info := OpenRouterKey(ctx, key); info != nil {
				if f, _ := info["is_free_tier"].(bool); f {
					out.Plan = "Free tier"
				} else {
					out.Plan = "Paid credits"
				}
			}
		}
		out.Models = ls
	} else {
		type res struct {
			i      int
			status string
			free   bool
		}
		ch := make(chan res, len(ls))
		gate := make(chan struct{}, 4)
		for i, mi := range ls {
			go func(i int, id string) {
				gate <- struct{}{}
				defer func() { <-gate }()
				st, free := probe(ctx, p, key, id)
				ch <- res{i, st, free}
			}(i, mi.ID)
		}
		sawFree, proOK, okN := false, false, 0
		keep := make([]bool, len(ls))
		for range ls {
			r := <-ch
			if r.free {
				sawFree = true
			}
			switch r.status {
			case "ok":
				keep[r.i] = true
				okN++
				if strings.Contains(ls[r.i].ID, "pro") {
					proOK = true
				}
			case "busy":
				keep[r.i] = true
				ls[r.i].Note = "rate limited right now"
				okN++
			}
		}
		for i, mi := range ls {
			if keep[i] {
				out.Models = append(out.Models, mi)
			}
		}
		switch {
		case p.ID != "gemini":
			if okN == len(ls) {
				out.Plan = "All models allowed"
			} else {
				out.Plan = "Limited (" + itoa(okN) + " of " + itoa(len(ls)) + ")"
			}
		case sawFree && !proOK:
			out.Plan = "Free tier"
		case proOK || (!sawFree && okN == len(ls)):
			out.Plan = "Paid (billing on)"
		default:
			out.Plan = "Free tier"
		}
		if len(out.Models) == 0 {
			out.Error = "Your key's plan doesn't allow any chat model right now."
		}
	}
	cacheMu.Lock()
	loadCache()
	if len(out.Models) > 0 {
		cache[p.ID] = cacheEntry{At: time.Now(), Tag: keyTag(p, key), Plan: out.Plan, Models: out.Models}
		saveCache()
	}
	cacheMu.Unlock()
	return out
}

func probe(ctx context.Context, p config.Provider, key, id string) (string, bool) {
	body, _ := json.Marshal(map[string]any{"model": id, "max_tokens": 16, "messages": []Message{{Role: "user", Content: "Say ok"}}})
	req, _ := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(p.BaseURL, "/")+"/chat/completions", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range AuthHeaders(p.ID, key) {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 45 * time.Second}).Do(req)
	if err != nil {
		return "blocked", false
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	low := strings.ToLower(string(raw))
	free := strings.Contains(low, "free_tier")
	switch c := resp.StatusCode; {
	case c < 300:
		return "ok", free
	case c == 429 && (strings.Contains(low, "limit: 0") || strings.Contains(low, `"limit": 0`)):
		return "blocked", free
	case c == 429:
		return "busy", free
	case c == 400 && !strings.Contains(low, "not found") && !strings.Contains(low, "not supported") && !strings.Contains(low, "not available"):
		return "ok", free
	}
	return "blocked", free
}

// ---------------- OpenRouter: today's best free tool models ----------------

var (
	orWeak   = regexp.MustCompile(`(?i)(code|coder|codestral|devstral|mini|nano|tiny|small|lite|micro|guard|embed|vision|-vl|ocr|reward|math|\b[0-9]b\b|\b(1\.5|0\.5|1|2|3|4|7|8|9|12|14)b\b|e2b|e4b|uncensored|roleplay|rp\b)`)
	orStrong = []string{"deepseek", "qwen3", "kimi", "glm", "gpt-oss", "llama-4", "gemini", "mistral-large", "nemotron-ultra", "hermes", "maverick", "grok"}
	orMu     sync.Mutex
	orPicks  []string
	orAt     time.Time
)

// OpenRouterPicks returns the strongest free OpenRouter models that support tools,
// refreshed in the background at most once a day ("openrouter/free" otherwise hands
// a turn to a random, sometimes tiny, model).
func OpenRouterPicks() []string {
	orMu.Lock()
	defer orMu.Unlock()
	if time.Since(orAt) > 24*time.Hour {
		orAt = time.Now()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			p, _ := config.ProviderByID("openrouter")
			ls, err := ListModels(ctx, p, "")
			if err != nil {
				return
			}
			var c []ModelInfo
			for _, mi := range ls {
				if mi.Free && mi.Tools == Yes && !orWeak.MatchString(mi.ID) && strings.HasSuffix(mi.ID, ":free") {
					c = append(c, mi)
				}
			}
			score := func(id string) int {
				for i, s := range orStrong {
					if strings.Contains(strings.ToLower(id), s) {
						return len(orStrong) - i
					}
				}
				return 0
			}
			sort.SliceStable(c, func(i, j int) bool {
				si, sj := score(c[i].ID), score(c[j].ID)
				if si != sj {
					return si > sj
				}
				return c[i].Context > c[j].Context
			})
			var top []string
			for i := 0; i < len(c) && i < 3; i++ {
				top = append(top, c[i].ID)
			}
			if len(top) > 0 {
				orMu.Lock()
				orPicks = top
				orMu.Unlock()
			}
		}()
	}
	return append([]string(nil), orPicks...)
}

// ResolveModel swaps "openrouter/free" (and tiny OpenRouter models) for today's best free pick.
func ResolveModel(provider, model string) string {
	if provider == "openrouter" && (model == "" || model == "openrouter/free" || (strings.HasSuffix(model, ":free") && orWeak.MatchString(model))) {
		if p := OpenRouterPicks(); len(p) > 0 {
			return p[0]
		}
		if model == "" {
			return "openrouter/free"
		}
	}
	return model
}

// PickModel chooses a sensible default from a provider's list: the catalog's default
// when listed, else the strongest tool-capable chat model.
func PickModel(provider string, ids []string) string {
	if p, ok := config.ProviderByID(provider); ok && p.DefaultModel != "" {
		for _, id := range ids {
			if id == p.DefaultModel {
				return id
			}
		}
		if len(ids) == 0 {
			return p.DefaultModel
		}
	}
	for _, mi := range Catalog[provider] {
		for _, id := range ids {
			if id == mi.ID && mi.Tools == Yes {
				return id
			}
		}
	}
	best, bestScore := "", -1
	weak := regexp.MustCompile(`(?i)(embed|guard|vision|-vl|ocr|reward|whisper|tts|audio|image|moderation|rerank|\b[0-9]b\b|\b(0\.5|1|1\.5|2|3|4|7|8)b\b|mini|nano|tiny|lite)`)
	strong := []string{"gpt-oss-120b", "kimi", "qwen3", "deepseek", "llama-4", "llama-3.3-70b", "glm", "gemini", "mistral-large", "mistral-medium", "nemotron", "grok", "claude", "gpt-5", "gpt-4.1"}
	for _, id := range ids {
		if weak.MatchString(id) || !CanUseTools(provider, id) || !IsChat(id) {
			continue
		}
		score := 0
		for i, s := range strong {
			if strings.Contains(strings.ToLower(id), s) {
				score = len(strong) - i
				break
			}
		}
		if score > bestScore {
			best, bestScore = id, score
		}
	}
	if best == "" && len(ids) > 0 {
		best = ids[0]
	}
	return best
}
