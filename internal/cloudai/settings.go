package cloudai

// The desktop's model choice (Settings › Models): Blaze Cloud (recommended) or Custom.
// Saved in <blaze dir>/desktop/models.json, a desktop-only file: Blaze CLI never reads it
// and keeps routing with its provider config in config.json, exactly as before.
// Custom uses that same shared config (providers, keys in the keystore, the default model
// and Auto chain), plus optional per-task overrides kept here.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const (
	ModeCloud  = "cloud"
	ModeCustom = "custom"
)

// OverrideTasks are the tasks a Custom user can pin to a model.
var OverrideTasks = []string{"chat", "code", "vision"}

// Pick is one provider + model.
type Pick struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// Choice is models.json. Unknown keys in the file are kept on save.
type Choice struct {
	Mode string `json:"mode"` // cloud | custom
	// RouterMoved: a Custom choice that only pointed at BlazeRouter was moved to Blaze
	// Cloud once (v0.19.1), so it isn't moved again if the user picks it back.
	RouterMoved bool `json:"router_moved,omitempty"`
	// FallbackToCloud: when every Custom model fails, Blaze Cloud answers (and says so).
	// Off unless the user switches it on.
	FallbackToCloud bool            `json:"fallback_to_cloud,omitempty"`
	Overrides       map[string]Pick `json:"overrides,omitempty"` // chat | code | vision
	GatewayURL      string          `json:"gateway_url,omitempty"`
	path            string
}

// Path is where the choice lives under dir (~/.blaze).
func Path(dir string) string { return filepath.Join(dir, "desktop", "models.json") }

// LoadChoice reads the saved choice. With no file yet, legacy decides: a person who
// already picked one fixed model of their own (config.json "default") keeps it as Custom;
// everyone else starts on Blaze Cloud.
func LoadChoice(dir string, legacyFixedModel bool) *Choice {
	c := &Choice{path: Path(dir)}
	b, err := os.ReadFile(c.path)
	if err != nil {
		c.Mode = ModeCloud
		if legacyFixedModel {
			c.Mode = ModeCustom
		}
		return c
	}
	_ = json.Unmarshal(b, c)
	c.normalize()
	return c
}

func (c *Choice) normalize() {
	if c.Mode != ModeCustom {
		c.Mode = ModeCloud
	}
	for k, p := range c.Overrides {
		if !validTask(k) || strings.TrimSpace(p.Provider) == "" || strings.TrimSpace(p.Model) == "" {
			delete(c.Overrides, k)
		}
	}
}

func validTask(t string) bool {
	for _, x := range OverrideTasks {
		if x == t {
			return true
		}
	}
	return false
}

// Cloud is true when Blaze Cloud answers by default.
func (c *Choice) Cloud() bool { return c == nil || c.Mode != ModeCustom }

// SetOverride pins a task to a model; an empty provider or model clears it.
func (c *Choice) SetOverride(task, provider, model string) bool {
	if !validTask(task) {
		return false
	}
	provider, model = strings.TrimSpace(provider), strings.TrimSpace(model)
	if provider == "" || model == "" {
		delete(c.Overrides, task)
		return true
	}
	if c.Overrides == nil {
		c.Overrides = map[string]Pick{}
	}
	c.Overrides[task] = Pick{Provider: provider, Model: model}
	return true
}

// Save writes models.json atomically (0600), keeping keys this version doesn't know.
func (c *Choice) Save() error {
	c.normalize()
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	raw := map[string]json.RawMessage{}
	if b, err := os.ReadFile(c.path); err == nil {
		_ = json.Unmarshal(b, &raw)
	}
	for _, k := range []string{"mode", "fallback_to_cloud", "overrides", "gateway_url"} {
		delete(raw, k)
	}
	mine := map[string]json.RawMessage{}
	b, _ := json.Marshal(c)
	_ = json.Unmarshal(b, &mine)
	for k, v := range mine {
		raw[k] = v
	}
	out, _ := json.MarshalIndent(raw, "", "  ")
	tmp, err := os.CreateTemp(filepath.Dir(c.path), ".models.json.*")
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

// OverrideFor is the pinned model for a Custom task (chat | code | vision; see CustomTask).
func (c *Choice) OverrideFor(task string) (Pick, bool) {
	if c == nil || c.Overrides == nil {
		return Pick{}, false
	}
	p, ok := c.Overrides[task]
	return p, ok
}
