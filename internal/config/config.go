// Package config keeps Blaze's settings in ~/.blaze/config.json and API keys in
// the shared key store (internal/keystore, the same code as Blaze CLI's): the OS
// keychain (Windows Credential Manager, macOS Keychain, Linux Secret Service),
// falling back to ~/.blaze/keys.json (mode 0600) when no keychain exists. A key saved
// in Blaze CLI works here and the other way round.
//
// The file is shared with Blaze CLI. Every field added since v0.2 is optional
// (omitempty) so an older config.json loads unchanged and an older CLI simply
// ignores what it doesn't know. Save keeps fields it doesn't know (and legacy keys an
// older Blaze still reads) instead of dropping them.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"blaze-desktop/internal/keystore"
)

type Provider struct {
	ID, Name, BaseURL, DefaultModel, KeyHint, KeyPage, Env string
	Free                                                   bool
	QuotaPage                                              string
}

// Custom is the id of the user's own OpenAI-compatible service (base URL in Config.CustomBase).
const Custom = "custom"

// Keyless is the keyless public endpoint: plain chat only, it can't call tools.
const KeylessID = "pollinations"

// Providers mirrors the Android app's list (all OpenAI-compatible).
var Providers = []Provider{
	{"gemini", "Google Gemini", "https://generativelanguage.googleapis.com/v1beta/openai/", "gemini-3.8-flash", "AIza…", "https://aistudio.google.com/apikey", "GEMINI_API_KEY", true, "https://aistudio.google.com/rate-limit?timeRange=last-28-days"},
	{"groq", "Groq", "https://api.groq.com/openai/v1/", "llama-3.3-70b-versatile", "gsk_…", "https://console.groq.com/keys", "GROQ_API_KEY", true, "https://console.groq.com/settings/limits"},
	{"cerebras", "Cerebras", "https://api.cerebras.ai/v1/", "gpt-oss-120b", "csk-…", "https://cloud.cerebras.ai/platform", "CEREBRAS_API_KEY", true, "https://cloud.cerebras.ai/platform"},
	{"openrouter", "OpenRouter", "https://openrouter.ai/api/v1/", "", "sk-or-…", "https://openrouter.ai/settings/keys", "OPENROUTER_API_KEY", true, "https://openrouter.ai/settings/credits"},
	{"mistral", "Mistral", "https://api.mistral.ai/v1/", "mistral-small-latest", "…", "https://console.mistral.ai/api-keys", "MISTRAL_API_KEY", true, "https://admin.mistral.ai/plateforme/limits"},
	{"nvidia", "NVIDIA NIM", "https://integrate.api.nvidia.com/v1/", "meta/llama-3.3-70b-instruct", "nvapi-…", "https://build.nvidia.com/settings/api-keys", "NVIDIA_API_KEY", true, "https://build.nvidia.com/"},
	{"deepseek", "DeepSeek", "https://api.deepseek.com/v1/", "deepseek-chat", "sk-…", "https://platform.deepseek.com/api_keys", "DEEPSEEK_API_KEY", false, "https://platform.deepseek.com/usage"},
	{"moonshot", "Kimi (Moonshot)", "https://api.moonshot.ai/v1/", "", "sk-…", "https://platform.moonshot.ai/console/api-keys", "MOONSHOT_API_KEY", false, "https://platform.moonshot.ai/console/limits"},
	{"openai", "ChatGPT (OpenAI)", "https://api.openai.com/v1/", "", "sk-…", "https://platform.openai.com/api-keys", "OPENAI_API_KEY", false, "https://platform.openai.com/settings/organization/limits"},
	{"anthropic", "Claude (Anthropic)", "https://api.anthropic.com/v1/", "", "sk-ant-…", "https://console.anthropic.com/settings/keys", "ANTHROPIC_API_KEY", false, "https://console.anthropic.com/settings/limits"},
	{"xai", "Grok (xAI)", "https://api.x.ai/v1/", "", "xai-…", "https://console.x.ai/", "XAI_API_KEY", false, "https://console.x.ai/"},
	{"ollama", "Ollama (local)", "http://127.0.0.1:11434/v1/", "", "(no key)", "https://ollama.com/download", "", true, ""},
	{Custom, "Custom (OpenAI-compatible)", "", "", "Your key", "", "", false, ""},
	{KeylessID, "Pollinations (keyless)", "https://text.pollinations.ai/openai/", "openai-fast", "(no key)", "https://pollinations.ai", "", true, ""},
}

// FreeOrder is the default fill-first order for Auto (providers with a real free tier).
var FreeOrder = []string{"gemini", "groq", "cerebras", "nvidia", "openrouter", "mistral", "deepseek", "moonshot"}

var (
	cmu        sync.RWMutex
	customBase string
	bases      = map[string]string{} // per-provider address overrides (BlazeRouter's own, a Cloudflare account)
	extra      []Provider            // providers added at run time (BlazeRouter's catalog)
)

// RouterID is BlazeRouter, the local OpenAI-compatible gateway built into Blaze Desktop.
// Its address is only set while its server runs (SetBase), so a stopped router is never usable.
const RouterID = "blazerouter"

// Register adds providers at run time (BlazeRouter's free-tier catalog). Known ids are skipped.
func Register(ps ...Provider) {
	cmu.Lock()
	defer cmu.Unlock()
	for _, p := range ps {
		dup := false
		for _, q := range Providers {
			dup = dup || q.ID == p.ID
		}
		for _, q := range extra {
			dup = dup || q.ID == p.ID
		}
		if !dup {
			extra = append(extra, p)
		}
	}
}

// All is every provider: the built-in list, then the registered ones.
func All() []Provider {
	cmu.RLock()
	defer cmu.RUnlock()
	return append(append([]Provider(nil), Providers...), extra...)
}

// SetBase overrides a provider's service address ("" removes the override).
func SetBase(id, base string) {
	cmu.Lock()
	defer cmu.Unlock()
	if strings.TrimSpace(base) == "" {
		delete(bases, id)
	} else {
		bases[id] = strings.TrimSpace(base)
	}
}

func ProviderByID(id string) (Provider, bool) {
	for _, p := range All() {
		if p.ID == id {
			cmu.RLock()
			if id == Custom {
				p.BaseURL = customBase
			} else if b, ok := bases[id]; ok {
				p.BaseURL = b
			} else if strings.Contains(p.BaseURL, "{") {
				p.BaseURL = "" // a template (Cloudflare's account id) that isn't filled in yet
			}
			cmu.RUnlock()
			return p, true
		}
	}
	return Provider{}, false
}

// Name is the provider's display name without the bracketed note ("Kimi (Moonshot)" -> "Kimi").
func Name(id string) string {
	if p, ok := ProviderByID(id); ok {
		if i := strings.Index(p.Name, " ("); i > 0 {
			return p.Name[:i]
		}
		return p.Name
	}
	return id
}

type Entry struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type Config struct {
	Chain       []Entry `json:"chain"`        // tried in order; first healthy one answers
	AutoApprove bool    `json:"auto_approve"` // --yolo default
	SetupDone   bool    `json:"setup_done"`
	MaxSteps    int     `json:"max_steps"`
	Persona     string  `json:"persona,omitempty"`
	NoKeyless   bool    `json:"no_keyless,omitempty"` // don't fall back to the keyless model

	// v0.3 (desktop): model choice. Default nil or provider "auto" = Auto (free routing over Chain).
	Default    *Entry            `json:"default,omitempty"`
	ModelFor   map[string]string `json:"model_for,omitempty"`   // chosen model per provider
	NoFallback bool              `json:"no_fallback,omitempty"` // a fixed model doesn't fall back to the chain
	NoFastest  bool              `json:"no_fastest,omitempty"`  // Auto keeps strict chain order
	DailyCap   float64           `json:"daily_cap,omitempty"`   // USD; past it a paid default switches to Auto
	CustomBase string            `json:"custom_base,omitempty"` // base URL of the custom provider
	CustomName string            `json:"custom_name,omitempty"` // label for it

	// Private vision (desktop v0.8.3, shared with Blaze CLI): pictures never leave the
	// computer. The OS's own OCR and a local colour/size readout turn each image into
	// text, and only that text goes to cloud models.
	PrivateVision bool `json:"private_vision,omitempty"`
	path          string
}

// Auto is the provider id that means "free routing".
const Auto = "auto"

func Dir() string {
	if d := os.Getenv("BLAZE_HOME"); d != "" {
		return d
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".blaze")
}

func Load() *Config {
	c := &Config{MaxSteps: 30, path: filepath.Join(Dir(), "config.json")}
	if b, err := os.ReadFile(c.path); err == nil {
		_ = json.Unmarshal(b, c)
	}
	if c.MaxSteps <= 0 {
		c.MaxSteps = 30
	}
	c.applyCustom()
	return c
}

func (c *Config) applyCustom() {
	cmu.Lock()
	customBase = strings.TrimSpace(c.CustomBase)
	cmu.Unlock()
}

func (c *Config) Save() error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	c.applyCustom()
	raw := map[string]json.RawMessage{}
	if b, err := os.ReadFile(c.path); err == nil {
		_ = json.Unmarshal(b, &raw)
	}
	for _, n := range jsonNames(reflect.TypeOf(*c)) {
		delete(raw, n) // cleared omitempty fields must go, not linger
	}
	mine := map[string]json.RawMessage{}
	b, _ := json.Marshal(c)
	_ = json.Unmarshal(b, &mine)
	for k, v := range mine {
		raw[k] = v
	}
	out, _ := json.MarshalIndent(raw, "", "  ")
	tmp, err := os.CreateTemp(filepath.Dir(c.path), ".config.json.*")
	if err != nil {
		return err
	}
	_ = tmp.Chmod(0o600)
	_, werr := tmp.Write(out)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(tmp.Name())
		return werr
	}
	if err := os.Rename(tmp.Name(), c.path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// jsonNames are the config.json field names a struct owns.
func jsonNames(t reflect.Type) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		n, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if n == "-" {
			continue
		}
		if n == "" {
			n = f.Name
		}
		out = append(out, n)
	}
	return out
}

// IsAuto is true when no fixed default model is chosen.
func (c *Config) IsAuto() bool {
	return c.Default == nil || c.Default.Provider == "" || c.Default.Provider == Auto
}

// Model is the model Blaze uses for a provider: the user's pick, then its chain entry, then the built-in default.
func (c *Config) Model(provider string) string {
	if m := c.ModelFor[provider]; m != "" {
		return m
	}
	for _, e := range c.Chain {
		if e.Provider == provider && e.Model != "" {
			return e.Model
		}
	}
	if p, ok := ProviderByID(provider); ok {
		return p.DefaultModel
	}
	return ""
}

func (c *Config) SetModelFor(provider, model string) {
	if c.ModelFor == nil {
		c.ModelFor = map[string]string{}
	}
	c.ModelFor[provider] = model
}

// ---------------- keys ----------------

// Keys is the shared key store (same as Blaze CLI's).
func Keys() *keystore.Store { return keystore.Open(Dir()) }

// KeysChanged, when set, runs after a provider's keys change (the account syncs them).
var KeysChanged func(provider string)

func keysChanged(provider string) {
	if KeysChanged != nil && keystore.IsSynced(provider) {
		KeysChanged(provider)
	}
}

// KeyHook, when set, supplies keys that don't live in the key store (BlazeRouter's local key).
var KeyHook func(provider string) string

// Key returns the provider's main key: env var first, then the shared key store.
func (c *Config) Key(provider string) string {
	if KeyHook != nil {
		if k := KeyHook(provider); k != "" {
			return k
		}
	}
	if p, ok := ProviderByID(provider); ok && p.Env != "" {
		if v := strings.TrimSpace(os.Getenv(p.Env)); v != "" {
			return v
		}
	}
	return Keys().Main(provider)
}

// SetKey stores a provider's main key. Returns where it went.
func (c *Config) SetKey(provider, key string) (string, error) {
	where, err := Keys().Set(provider, key)
	if err == nil {
		keysChanged(provider)
	}
	return where, err
}

// DeleteKey removes every key of a provider (main and extras).
func (c *Config) DeleteKey(provider string) {
	_ = Keys().SetAll(provider, nil)
	keysChanged(provider)
}

// ExtraKeys are the provider's additional keys (the key ring).
func (c *Config) ExtraKeys(provider string) []string { return Keys().Extras(provider) }

// AllKeys is the key ring: the main key (env var first) then the extras, without duplicates.
func (c *Config) AllKeys(provider string) []string {
	var out []string
	seen := map[string]bool{}
	for _, k := range append([]string{c.Key(provider)}, c.ExtraKeys(provider)...) {
		k = strings.TrimSpace(k)
		if k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// AddKey adds a key to the ring: it becomes the main key when there is none yet.
// Returns where it was stored, or "" when it was already saved.
func (c *Config) AddKey(provider, key string) (string, error) {
	key = strings.TrimSpace(key)
	for _, k := range c.AllKeys(provider) {
		if k == key {
			return "", nil
		}
	}
	ks := Keys()
	if ks.Main(provider) == "" {
		return c.SetKey(provider, key)
	}
	where, err := ks.SetExtras(provider, append(ks.Extras(provider), key))
	if err == nil {
		keysChanged(provider)
	}
	return where, err
}

// RemoveKey removes one key from the ring. Removing the main key promotes the first extra.
func (c *Config) RemoveKey(provider, key string) error {
	ks := Keys()
	var keep []string
	for _, k := range ks.All(provider) {
		if k != key {
			keep = append(keep, k)
		}
	}
	if err := ks.SetAll(provider, keep); err != nil {
		return err
	}
	keysChanged(provider)
	return nil
}

// ---------------- routing helpers ----------------

// HasAnyModel is true when at least one chain entry has what it needs.
func (c *Config) HasAnyModel() bool {
	for _, e := range c.Chain {
		if (Keyless(e.Provider) && e.Provider != KeylessID) || c.Key(e.Provider) != "" {
			return true
		}
	}
	if !c.IsAuto() && (c.Key(c.Default.Provider) != "" || c.Default.Provider == "ollama") {
		return true
	}
	return false
}

// Keyless providers need no API key.
func Keyless(id string) bool { return id == "ollama" || id == KeylessID }

// RouteChain is the chain without the keyless model; keyless is only ever a plain-chat
// last resort and is added by the router itself, never as a chain entry.
func (c *Config) RouteChain() []Entry {
	var ch []Entry
	for _, e := range c.Chain {
		if e.Provider != KeylessID && e.Provider != "" {
			ch = append(ch, e)
		}
	}
	return ch
}

// AllowKeyless is whether the keyless model may answer plain chat when nothing else can.
func (c *Config) AllowKeyless() bool { return !c.NoKeyless }
