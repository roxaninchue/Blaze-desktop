package router

// The dashboard's JSON API (/api/*). Every call needs the dashboard's session token
// (Blaze opens the dashboard with it) or the local API key. Keys are only ever shown
// masked; a key goes into Blaze's shared key store, never into a BlazeRouter file.

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"blaze-desktop/internal/config"
	"blaze-desktop/internal/llm"
)

//go:embed web
var webFS embed.FS

// frameAncestors lets Blaze's webview (wails:// on macOS/Linux, http(s)://wails.localhost
// on Windows) show the dashboard in its BlazeRouter view, and nobody else.
const frameAncestors = "frame-ancestors 'self' wails: wails://wails http://wails.localhost https://wails.localhost"

func dashboardFiles() http.Handler {
	sub, _ := fs.Sub(webFS, "web")
	fsrv := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		// Only Blaze's own window (the Wails webview) and the dashboard itself may frame it.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; "+frameAncestors)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		fsrv.ServeHTTP(w, req)
	})
}

// ProviderView is one provider on the dashboard.
type ProviderView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Base      string    `json:"base"`
	KeyHint   string    `json:"key_hint"`
	KeyPage   string    `json:"key_page"`
	QuotaPage string    `json:"quota_page"`
	Free      bool      `json:"free"`
	FreeInfo  *FreeInfo `json:"free_info,omitempty"`
	Keys      []KeyView `json:"keys"`
	Ready     bool      `json:"ready"`
	Model     string    `json:"model"`
	InAuto    bool      `json:"in_auto"`
	Local     bool      `json:"local"`  // runs on this computer (no key)
	Custom    bool      `json:"custom"` // the user's own OpenAI-compatible service
	NeedsBase bool      `json:"needs_base"`
	BaseHint  string    `json:"base_hint,omitempty"`
	Builtin   bool      `json:"builtin"` // one of Blaze's original providers
	Health    HealthV   `json:"health"`
	Requests  int       `json:"requests"` // last 24 hours
	Errors    int       `json:"errors"`
	Env       string    `json:"env,omitempty"`
}

type KeyView struct {
	Masked string `json:"masked"`
	Main   bool   `json:"main"`
	Env    bool   `json:"env"`
}

type HealthV struct {
	OK        int    `json:"ok"`
	Fails     int    `json:"fails"`
	AvgMs     int64  `json:"avg_ms"`
	Cooling   int    `json:"cooling"` // seconds
	LastError string `json:"last_error,omitempty"`
	Resting   string `json:"resting,omitempty"`
}

func mask(k string) string {
	if len(k) <= 10 {
		return strings.Repeat("•", 6)
	}
	return k[:4] + "••••" + k[len(k)-4:]
}

func builtin(id string) bool {
	for _, p := range config.Providers {
		if p.ID == id {
			return true
		}
	}
	return false
}

func healthOf(provider, model string) HealthV {
	h := llm.HealthOf(provider, model)
	v := HealthV{OK: h.OK, Fails: h.Fails, AvgMs: h.AvgMs, LastError: h.LastErr}
	if d := llm.CoolingFor(provider, model); d > 0 {
		v.Cooling = int(d.Seconds()) + 1
	}
	if w, why := llm.Wait(provider+"|"+model, 0); w > 0 {
		v.Resting = why
		v.Cooling = max(v.Cooling, int(w.Seconds())+1)
	}
	return v
}

// Providers lists every provider BlazeRouter can use, set-up ones first.
func (r *Router) Providers() []ProviderView {
	cfg := r.cfg()
	auto := map[string]bool{}
	for _, t := range r.autoTargets(cfg) {
		auto[t.Provider] = true
	}
	_, byP, _ := r.log.stats(time.Now().Add(-24 * time.Hour))
	stat := map[string]Stat{}
	for _, s := range byP {
		stat[s.Provider] = s
	}
	var out []ProviderView
	for _, p := range routable() {
		pp, _ := config.ProviderByID(p.ID)
		v := ProviderView{ID: p.ID, Name: p.Name, Base: pp.BaseURL, KeyHint: p.KeyHint, KeyPage: p.KeyPage, QuotaPage: p.QuotaPage, Free: p.Free,
			Model: cfg.Model(p.ID), InAuto: auto[p.ID], Local: p.ID == "ollama", Custom: p.ID == config.Custom, Builtin: builtin(p.ID), Env: p.Env}
		if f, ok := Free(p.ID); ok {
			f := f
			v.FreeInfo = &f
		}
		envKey := ""
		if p.Env != "" {
			envKey = strings.TrimSpace(os.Getenv(p.Env))
		}
		for i, k := range cfg.AllKeys(p.ID) {
			v.Keys = append(v.Keys, KeyView{Masked: mask(k), Main: i == 0, Env: k == envKey})
		}
		if v.Keys == nil {
			v.Keys = []KeyView{}
		}
		if Templated(p.ID) {
			v.NeedsBase = pp.BaseURL == ""
			v.BaseHint = rawBase(p.ID)
		}
		if v.Custom {
			v.Name = firstOf(cfg.CustomName, "Custom (OpenAI-compatible)")
			v.NeedsBase = cfg.CustomBase == ""
			v.BaseHint = "https://my-service.example.com/v1"
		}
		v.Ready = llm.Usable(cfg, p.ID) && (len(v.Keys) > 0 || v.Local && inChain(cfg, "ollama") || v.Custom)
		v.Health = healthOf(p.ID, llm.ResolveModel(p.ID, v.Model))
		s := stat[p.ID]
		v.Requests, v.Errors = s.Requests, s.Errors
		out = append(out, v)
	}
	// Set-up first, then Blaze's originals, then the long tail (catalog order).
	rank := func(v ProviderView) int {
		switch {
		case v.Ready:
			return 0
		case v.Builtin:
			return 1
		}
		return 2
	}
	stable(out, func(a, b ProviderView) bool { return rank(a) < rank(b) })
	return out
}

func stable(s []ProviderView, less func(a, b ProviderView) bool) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && less(s[j], s[j-1]); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// ComboView is a combo with each target's state.
type ComboView struct {
	Combo
	Builtin bool         `json:"builtin"`
	Rows    []ComboRowV  `json:"rows"`
	Usable  int          `json:"usable"`
	Order   []TargetView `json:"order"` // the order the next request would try
}

type ComboRowV struct {
	Provider string  `json:"provider"`
	Name     string  `json:"name"`
	Model    string  `json:"model"`
	Ready    bool    `json:"ready"`
	Health   HealthV `json:"health"`
}

type TargetView struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
	Model    string `json:"model"`
	Cooling  int    `json:"cooling"`
}

func (r *Router) combos() []ComboView {
	cfg := r.cfg()
	auto := Combo{ID: "auto", Name: "Auto", Strategy: StratPriority, Note: "Works with zero setup: every provider you have a key for, healthiest and fastest first."}
	for _, t := range r.autoTargets(cfg) {
		auto.Targets = append(auto.Targets, config.Entry{Provider: t.Provider, Model: t.Model})
	}
	all := []ComboView{{Combo: auto, Builtin: true}}
	for _, c := range r.Settings().Combos {
		all = append(all, ComboView{Combo: c})
	}
	for i := range all {
		c := &all[i]
		for _, e := range c.Targets {
			m := e.Model
			if m == "" {
				m = cfg.Model(e.Provider)
			}
			ok := llm.Usable(cfg, e.Provider)
			if ok {
				c.Usable++
			}
			c.Rows = append(c.Rows, ComboRowV{Provider: e.Provider, Name: config.Name(e.Provider), Model: m, Ready: ok, Health: healthOf(e.Provider, m)})
		}
		if pl, err := r.plan(c.ID, false); err == nil {
			for _, t := range pl.Targets {
				c.Order = append(c.Order, TargetView{t.Provider, t.Name, t.Model, int(llm.CoolingFor(t.Provider, t.Model).Seconds())})
			}
		}
		if c.Rows == nil {
			c.Rows = []ComboRowV{}
		}
	}
	return all
}

func (r *Router) state() map[string]any {
	cfg := r.cfg()
	s := r.Settings()
	total, byP, _ := r.log.stats(time.Now().Add(-24 * time.Hour))
	blaze := "auto"
	if !cfg.IsAuto() {
		blaze = cfg.Default.Provider + "/" + cfg.Default.Model
		if cfg.Default.Provider == config.RouterID {
			blaze = "blazerouter/" + cfg.Default.Model
		}
	}
	return map[string]any{
		"version": r.opt.Version, "status": r.Status(), "url": r.URL(), "key": s.APIKey,
		"settings":   map[string]any{"port": s.Port, "compression": s.Compression, "autostart": s.AutoStart(), "enabled": s.On()},
		"strategies": Strategies, "combos": r.combos(), "providers": r.Providers(),
		"recent": r.log.recent(12), "today": total, "by_provider": byP, "hourly": r.log.hourly(),
		"blaze_model": blaze, "home": config.Dir(),
	}
}

type apiReq struct {
	Provider    string `json:"provider"`
	Key         string `json:"key"`
	Model       string `json:"model"`
	Index       int    `json:"index"`
	Base        string `json:"base"`
	Name        string `json:"name"`
	ID          string `json:"id"`
	Combo       *Combo `json:"combo"`
	Port        int    `json:"port"`
	Compression string `json:"compression"`
	Autostart   *bool  `json:"autostart"`
	Enabled     *bool  `json:"enabled"`
}

func (r *Router) handleAPI(w http.ResponseWriter, req *http.Request) {
	if !r.authorized(req) {
		writeJSON(w, 401, map[string]any{"error": "Open the dashboard from Blaze (the BlazeRouter button in the top bar), or paste your BlazeRouter key."})
		return
	}
	var a apiReq
	if req.Method == http.MethodPost {
		_ = json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&a)
	}
	q := req.URL.Query()
	cfg := r.cfg()
	ok := func(v any) { writeJSON(w, 200, v) }
	fail := func(code int, msg string) { writeJSON(w, code, map[string]any{"error": msg}) }
	path := strings.TrimPrefix(req.URL.Path, "/api/")
	switch path {
	case "state":
		ok(r.state())
	case "providers":
		ok(r.Providers())
	case "models":
		p, found := config.ProviderByID(q.Get("provider"))
		if !found {
			fail(404, "Unknown provider.")
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
		defer cancel()
		key := ""
		if ks := cfg.AllKeys(p.ID); len(ks) > 0 {
			key = ks[0]
		}
		l := llm.Options(ctx, p, key, q.Get("refresh") == "1")
		if l.Error != "" {
			l.Error = llm.PlainText(config.Name(p.ID), 0, l.Error)
		}
		ok(l)
	case "providers/key":
		model, note, err := r.addKey(cfg, a.Provider, strings.TrimSpace(a.Key))
		if err != nil {
			fail(400, err.Error())
			return
		}
		r.changed()
		ok(map[string]any{"ok": true, "model": model, "note": note})
	case "providers/test":
		ok(r.test(req.Context(), cfg, a.Provider, a.Model))
	case "providers/remove-key":
		ks := cfg.AllKeys(a.Provider)
		if a.Index < 0 || a.Index >= len(ks) {
			fail(400, "No such key.")
			return
		}
		if err := cfg.RemoveKey(a.Provider, ks[a.Index]); err != nil {
			fail(500, err.Error())
			return
		}
		llm.ClearHealth(a.Provider)
		r.changed()
		ok(map[string]any{"ok": true})
	case "providers/remove":
		cfg.DeleteKey(a.Provider)
		var ch []config.Entry
		for _, e := range cfg.Chain {
			if e.Provider != a.Provider {
				ch = append(ch, e)
			}
		}
		cfg.Chain = ch
		if !cfg.IsAuto() && cfg.Default.Provider == a.Provider {
			cfg.Default = nil
		}
		_ = cfg.Save()
		r.changed()
		ok(map[string]any{"ok": true})
	case "providers/model":
		if a.Provider == "" || a.Model == "" {
			fail(400, "Pick a provider and a model.")
			return
		}
		cfg.SetModelFor(a.Provider, a.Model)
		for i, e := range cfg.Chain {
			if e.Provider == a.Provider {
				cfg.Chain[i].Model = a.Model
			}
		}
		if !cfg.IsAuto() && cfg.Default.Provider == a.Provider {
			cfg.Default.Model = a.Model
		}
		_ = cfg.Save()
		r.changed()
		ok(map[string]any{"ok": true})
	case "providers/base":
		if err := r.setBase(cfg, a.Provider, a.Base, a.Name); err != nil {
			fail(400, err.Error())
			return
		}
		r.changed()
		ok(map[string]any{"ok": true})
	case "providers/connect":
		if a.Provider != "openrouter" || r.opt.ConnectOpenRouter == nil {
			fail(400, "One-click sign-in is only available for OpenRouter.")
			return
		}
		model, err := r.opt.ConnectOpenRouter()
		if err != nil {
			fail(400, err.Error())
			return
		}
		r.changed()
		ok(map[string]any{"ok": true, "model": model})
	case "quota":
		p, found := config.ProviderByID(q.Get("provider"))
		if !found {
			fail(404, "Unknown provider.")
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
		defer cancel()
		key := ""
		if ks := cfg.AllKeys(p.ID); len(ks) > 0 {
			key = ks[0]
		}
		ok(llm.Quota(ctx, p, key, cfg.Model(p.ID)))
	case "combos":
		if a.Combo == nil {
			ok(r.combos())
			return
		}
		c, err := r.SaveCombo(*a.Combo)
		if err != nil {
			fail(400, err.Error())
			return
		}
		r.changed()
		ok(c)
	case "combos/delete":
		_ = r.DeleteCombo(a.ID)
		r.changed()
		ok(map[string]any{"ok": true})
	case "usage":
		total24, byP, byM := r.log.stats(time.Now().Add(-24 * time.Hour))
		total7, _, byM7 := r.log.stats(time.Now().Add(-7 * 24 * time.Hour))
		ok(map[string]any{"report": llm.Report(), "today": total24, "week": total7, "by_provider": byP, "by_model": byM, "by_model_week": byM7, "hourly": r.log.hourly()})
	case "logs":
		n, _ := strconv.Atoi(q.Get("n"))
		if n <= 0 || n > maxLog {
			n = 200
		}
		ok(r.log.recent(n))
	case "logs/clear":
		r.log.clear()
		ok(map[string]any{"ok": true})
	case "settings":
		if err := r.applySettings(a); err != nil {
			fail(400, err.Error())
			return
		}
		r.changed()
		ok(r.state())
	case "key/rotate":
		r.mu.Lock()
		r.set.APIKey = NewKey()
		s := r.set
		r.mu.Unlock()
		_ = s.save()
		r.changed()
		ok(map[string]any{"key": s.APIKey})
	case "blaze/use":
		// Blaze's own chats go through BlazeRouter with this combo ("auto" or a combo id).
		m := strings.TrimSpace(a.Model)
		if m == "" {
			m = "auto"
		}
		cfg.Default = &config.Entry{Provider: config.RouterID, Model: m}
		_ = cfg.Save()
		r.changed()
		ok(map[string]any{"ok": true})
	default:
		fail(404, "Unknown call: "+path)
	}
}

func (r *Router) applySettings(a apiReq) error {
	r.mu.Lock()
	s := r.set
	restart := false
	if a.Port != 0 {
		if a.Port < 1024 || a.Port > 65535 {
			r.mu.Unlock()
			return errors.New("pick a port between 1024 and 65535")
		}
		restart = a.Port != s.Port
		s.Port = a.Port
	}
	switch a.Compression {
	case "":
	case CompressOff, CompressLight, CompressStandard, CompressAggressive:
		s.Compression = a.Compression
	default:
		r.mu.Unlock()
		return errors.New("unknown compression level")
	}
	if a.Autostart != nil {
		s.Autostart = a.Autostart
	}
	r.mu.Unlock()
	if err := s.save(); err != nil {
		return err
	}
	if a.Enabled != nil {
		return r.SetEnabled(*a.Enabled)
	}
	if restart && r.Running() {
		// The new port takes effect after the dashboard reconnects; restart a moment later
		// so this answer still reaches it.
		go func() {
			time.Sleep(300 * time.Millisecond)
			r.Stop()
			_ = r.Start()
			r.changed()
		}()
	}
	return nil
}

// setBase saves a provider's address: the custom service (config.json, shared with
// Blaze CLI) or a templated one like Cloudflare's account id (BlazeRouter's settings).
func (r *Router) setBase(cfg *config.Config, provider, base, name string) error {
	base = strings.TrimSpace(base)
	if provider == config.Custom {
		cfg.CustomBase, cfg.CustomName = base, strings.TrimSpace(name)
		return cfg.Save()
	}
	raw := rawBase(provider)
	if !strings.Contains(raw, "{") {
		return errors.New("this provider's address is fixed")
	}
	if base != "" && !strings.HasPrefix(base, "http") {
		// Just the value for the placeholder (an account id).
		i, j := strings.Index(raw, "{"), strings.Index(raw, "}")
		base = raw[:i] + base + raw[j+1:]
	}
	r.mu.Lock()
	if base == "" {
		delete(r.set.Bases, provider)
	} else {
		r.set.Bases[provider] = base
	}
	s := r.set
	r.mu.Unlock()
	config.SetBase(provider, base)
	return s.save()
}

// addKey tests and saves a key (Blaze's own AddProvider when the app provides it).
func (r *Router) addKey(cfg *config.Config, provider, key string) (string, string, error) {
	p, ok := config.ProviderByID(provider)
	if !ok {
		return "", "", errors.New("unknown provider")
	}
	if p.BaseURL == "" {
		return "", "", errors.New("set " + p.Name + "'s address first")
	}
	if key == "" && provider != "ollama" && provider != config.Custom {
		return "", "", errors.New("paste a key first")
	}
	if r.opt.AddKey != nil {
		return r.opt.AddKey(provider, key)
	}
	res := r.test(context.Background(), cfg, provider, "", key)
	if !res.OK {
		return "", "", errors.New(res.Detail)
	}
	if _, err := cfg.AddKey(provider, key); err != nil {
		return "", "", err
	}
	llm.ClearHealth(provider)
	return cfg.Model(provider), "", nil
}

// TestResult is a provider check.
type TestResult struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Ms     int64  `json:"ms"`
	Model  string `json:"model"`
}

// test sends a one-word request to a provider (with the saved key, or the one given).
func (r *Router) test(ctx context.Context, cfg *config.Config, provider, model string, key ...string) TestResult {
	p, ok := config.ProviderByID(provider)
	if !ok {
		return TestResult{Detail: "Unknown provider."}
	}
	k := ""
	if len(key) > 0 && key[0] != "" {
		k = key[0]
	} else if ks := cfg.AllKeys(provider); len(ks) > 0 {
		k = ks[0]
	}
	if model == "" {
		model = llm.ResolveModel(provider, cfg.Model(provider))
	}
	if model == "" {
		ctx2, cancel := context.WithTimeout(ctx, 20*time.Second)
		l := llm.Options(ctx2, p, k, false)
		cancel()
		for _, m := range l.Models {
			if m.Tools != llm.No {
				model = m.ID
				break
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	t0 := time.Now()
	_, err := llm.Chat(ctx, p, k, model, []llm.Message{{Role: "user", Content: "Reply with the single word: ok"}}, nil, nil)
	ms := time.Since(t0).Milliseconds()
	if err != nil {
		code := 0
		var he *llm.HTTPError
		if errors.As(err, &he) {
			code = he.Code
		}
		return TestResult{Detail: llm.PlainText(config.Name(provider), code, err.Error()), Ms: ms, Model: model}
	}
	llm.ReportOK(provider, model, ms)
	return TestResult{OK: true, Detail: config.Name(provider) + " answered with " + model + " in " + strconv.FormatInt(ms, 10) + " ms", Ms: ms, Model: model}
}
