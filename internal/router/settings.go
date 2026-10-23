package router

// BlazeRouter's own settings, in ~/.blaze/desktop/blazerouter.json (mode 0600).
// Keys are NOT here: they stay in Blaze's shared key store (the OS keychain or
// ~/.blaze/keys.json), which Blaze CLI reads too. The custom provider's address and
// each provider's chosen model stay in config.json, also shared with the CLI.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"blaze-desktop/internal/config"
)

const DefaultPort = 20128

// Compression levels (compress.go).
const (
	CompressOff        = "off"
	CompressLight      = "light"
	CompressStandard   = "standard"
	CompressAggressive = "aggressive"
)

type Settings struct {
	Port        int               `json:"port"`
	Enabled     *bool             `json:"enabled,omitempty"`     // server on (nil = off since v0.19.1: Blaze Cloud answers)
	Autostart   *bool             `json:"autostart,omitempty"`   // starts with Blaze (nil = yes)
	Compression string            `json:"compression,omitempty"` // off, light (default), standard, aggressive
	APIKey      string            `json:"api_key"`               // the local key clients send
	Combos      []Combo           `json:"combos,omitempty"`
	Bases       map[string]string `json:"bases,omitempty"` // provider -> address (Cloudflare with your account id)
	Migrated    bool              `json:"migrated,omitempty"`
}

func (s *Settings) On() bool        { return s.Enabled != nil && *s.Enabled }
func (s *Settings) AutoStart() bool { return s.Autostart == nil || *s.Autostart }

var smu sync.Mutex

func settingsPath() string { return filepath.Join(config.Dir(), "desktop", "blazerouter.json") }

// NewKey makes a local API key: "br-" and 40 hex characters.
func NewKey() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return "br-" + hex.EncodeToString(b)
}

func loadSettings() *Settings {
	s := &Settings{}
	b, _ := os.ReadFile(settingsPath())
	_ = json.Unmarshal(b, s)
	if s.Port <= 0 || s.Port > 65535 {
		s.Port = DefaultPort
	}
	switch s.Compression {
	case CompressOff, CompressLight, CompressStandard, CompressAggressive:
	default:
		s.Compression = CompressLight
	}
	if s.Bases == nil {
		s.Bases = map[string]string{}
	}
	return s
}

func (s *Settings) save() error {
	smu.Lock()
	defer smu.Unlock()
	if err := os.MkdirAll(filepath.Dir(settingsPath()), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	tmp := settingsPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, settingsPath())
}

// migrate runs once: everything Blaze's old Settings › Models held becomes BlazeRouter's.
// Nothing is moved or rewritten in the shared files, so Blaze CLI keeps working as before:
//   - keys stay in the shared key store; BlazeRouter reads them from there.
//   - the custom provider's address and name stay in config.json.
//   - the Auto chain (with each entry's model) becomes the combo "My chain", so a
//     hand-ordered chain and any custom model ids in it are kept.
//
// Returns true when it changed the settings.
func (s *Settings) migrate(cfg *config.Config) bool {
	if s.Migrated {
		return false
	}
	s.Migrated = true
	if s.APIKey == "" {
		s.APIKey = NewKey()
	}
	var chain []config.Entry
	for _, e := range cfg.RouteChain() {
		if e.Provider == config.RouterID {
			continue
		}
		if e.Model == "" {
			e.Model = cfg.Model(e.Provider)
		}
		chain = append(chain, e)
	}
	if len(chain) > 0 && s.combo("my-chain") == nil {
		s.Combos = append(s.Combos, Combo{ID: "my-chain", Name: "My chain", Strategy: StratPriority, Targets: chain,
			Note: "Your Auto chain from Blaze's old Settings › Models"})
	}
	// A fixed default model with a custom id is kept in config.json (ModelFor) as it was.
	return true
}

func (s Settings) combo(id string) *Combo {
	id = strings.ToLower(strings.TrimSpace(id))
	for i := range s.Combos {
		if strings.ToLower(s.Combos[i].ID) == id || strings.ToLower(s.Combos[i].Name) == id {
			return &s.Combos[i]
		}
	}
	return nil
}
