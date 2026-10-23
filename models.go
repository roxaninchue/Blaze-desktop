package main

// Models and providers: everything Settings › Models, the model chip and /model use.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"blaze-desktop/internal/privacy"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"blaze-desktop/internal/cloudai"
	"blaze-desktop/internal/config"
	"blaze-desktop/internal/llm"
)

type ProviderView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Short     string `json:"short"`
	KeyHint   string `json:"key_hint"`
	KeyPage   string `json:"key_page"`
	QuotaPage string `json:"quota_page"`
	Free      bool   `json:"free"`
	HasKey    bool   `json:"has_key"`
	Keys      int    `json:"keys"`
	Keyless   bool   `json:"keyless"`
	Model     string `json:"model"`    // the model Blaze uses for it
	InChain   bool   `json:"in_chain"` // part of the Auto chain
	Ready     bool   `json:"ready"`    // usable now (key, or keyless/local)
	Base      string `json:"base,omitempty"`
}

// Selection is the default model: Auto, or one provider + model.
type Selection struct {
	Auto     bool   `json:"auto"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Label    string `json:"label"` // "Auto (free routing)" or "Groq · llama-3.3-70b-versatile"
}

type RoutingView struct {
	Fallback     bool    `json:"fallback"`      // a fixed model falls back to the chain
	FastestFirst bool    `json:"fastest_first"` // Auto puts the fastest healthy model first
	Keyless      bool    `json:"keyless"`       // keyless plain-chat last resort
	DailyCap     float64 `json:"daily_cap"`
	TodayCost    float64 `json:"today_cost"`
}

func (a *App) providerViews() []ProviderView {
	inChain := map[string]bool{}
	for _, e := range a.cfg.Chain {
		inChain[e.Provider] = true
	}
	var out []ProviderView
	for _, p := range config.Providers {
		keys := a.cfg.AllKeys(p.ID)
		v := ProviderView{ID: p.ID, Name: p.Name, Short: config.Name(p.ID), KeyHint: p.KeyHint, KeyPage: p.KeyPage, QuotaPage: p.QuotaPage,
			Free: p.Free, HasKey: len(keys) > 0, Keys: len(keys), Keyless: config.Keyless(p.ID), Model: a.cfg.Model(p.ID), InChain: inChain[p.ID]}
		if p.ID == config.Custom {
			v.Base = a.cfg.CustomBase
			if a.cfg.CustomName != "" {
				v.Short = a.cfg.CustomName
			}
		}
		v.Ready = v.HasKey || p.ID == "ollama" && v.InChain || p.ID == config.Custom && a.cfg.CustomBase != ""
		out = append(out, v)
	}
	return out
}

func (a *App) selection() Selection {
	if a.cfg.IsAuto() {
		return Selection{Auto: true, Provider: config.Auto, Label: "Auto (free routing)"}
	}
	d := a.cfg.Default
	m := d.Model
	if m == "" {
		m = a.cfg.Model(d.Provider)
	}
	return Selection{Provider: d.Provider, Model: m, Label: config.Name(d.Provider) + " · " + m}
}

func (a *App) routing() RoutingView {
	return RoutingView{Fallback: !a.cfg.NoFallback, FastestFirst: !a.cfg.NoFastest, Keyless: a.cfg.AllowKeyless(), DailyCap: a.cfg.DailyCap, TodayCost: llm.TodayCost()}
}

// modelName is what the model chip shows before a turn has answered.
func (a *App) modelName() string {
	if cloudModelsOn() {
		return cloudai.Name // v0.19 (cloudmodels_app.go)
	}
	if !a.cfg.IsAuto() {
		return a.selection().Label
	}
	if t := llm.Targets(a.cfg, true); len(t) > 0 {
		return "Auto · " + t[0].Name + " · " + t[0].Model
	}
	return ""
}

// ---------------- choosing ----------------

// ModelOptions lists one provider's models: Blaze's catalog plus the live list from the
// provider (cached; refresh fetches it now), with tools / vision / context for each.
func (a *App) ModelOptions(provider string, refresh bool) llm.ModelList {
	a.waitReady()
	p, ok := config.ProviderByID(provider)
	if !ok {
		return llm.ModelList{Provider: provider, Error: "Unknown provider."}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	key := ""
	if keys := a.cfg.AllKeys(provider); len(keys) > 0 {
		key = keys[0]
	}
	l := llm.Options(ctx, p, key, refresh)
	if l.Error != "" {
		l.Error = llm.PlainText(config.Name(provider), 0, l.Error)
	}
	return l
}

// DetectModels probes which models the key's plan really allows (uses a little quota).
func (a *App) DetectModels(provider string) llm.ModelList {
	p, ok := config.ProviderByID(provider)
	if !ok {
		return llm.ModelList{Provider: provider, Error: "Unknown provider."}
	}
	keys := a.cfg.AllKeys(provider)
	if len(keys) == 0 && !config.Keyless(provider) {
		return llm.ModelList{Provider: provider, Error: "Add a key first."}
	}
	key := ""
	if len(keys) > 0 {
		key = keys[0]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	l := llm.Detect(ctx, p, key)
	if l.Error != "" {
		l.Error = llm.PlainText(config.Name(provider), 0, l.Error)
	}
	return l
}

// SelectModel makes a model the default. provider "auto" switches to Auto (free routing).
// An empty model uses the provider's current model. Returns the chip label.
func (a *App) SelectModel(provider, model string) string {
	a.waitReady()
	if provider == cloudai.ProviderID { // v0.19.13: the Blaze Cloud row (blaze-auto) of the model menu
		a.SetModelsMode(cloudai.ModeCloud)
		return a.modelName()
	}
	useCustomModels() // picking a model of your own means Custom (cloudmodels_app.go)
	if provider == "" || provider == config.Auto {
		a.cfg.Default = nil
		_ = a.cfg.Save()
		a.emitModel()
		return a.modelName()
	}
	if provider == config.RouterID {
		return a.selectRouter(model) // never added to the chain (router_app.go)
	}
	if _, ok := config.ProviderByID(provider); !ok {
		return "Unknown provider."
	}
	if provider == config.KeylessID {
		return "Pollinations can't use tools, so it can't be the default. It only answers plain chat when nothing else is set up."
	}
	if model == "" {
		model = a.cfg.Model(provider)
	}
	if !llm.CanUseTools(provider, model) {
		return a.modelName() // a model that can't call tools is never the default
	}
	a.cfg.Default = &config.Entry{Provider: provider, Model: model}
	a.cfg.SetModelFor(provider, model)
	// Keep the chain entry for this provider on the same model, or add one.
	found := false
	for i, e := range a.cfg.Chain {
		if e.Provider == provider {
			a.cfg.Chain[i].Model = model
			found = true
			break
		}
	}
	if !found && provider != "ollama" {
		a.cfg.Chain = append(a.cfg.Chain, config.Entry{Provider: provider, Model: model})
	}
	_ = a.cfg.Save()
	a.emitModel()
	return a.modelName()
}

// SetProviderModel sets which model a provider uses (in the chain and as default if it is the default).
func (a *App) SetProviderModel(provider, model string) {
	a.cfg.SetModelFor(provider, model)
	for i, e := range a.cfg.Chain {
		if e.Provider == provider {
			a.cfg.Chain[i].Model = model
		}
	}
	if !a.cfg.IsAuto() && a.cfg.Default.Provider == provider {
		a.cfg.Default.Model = model
	}
	_ = a.cfg.Save()
	a.emitModel()
}

func (a *App) emitModel() {
	if a.ctx != nil {
		wr.EventsEmit(a.ctx, "model", map[string]any{"label": privacy.ModelLabel(a.modelName()), "selection": a.selection()})
	}
}

type ModelItem struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
	Model    string `json:"model"`
	Tools    bool   `json:"tools"`
	Current  bool   `json:"current"`
	Ready    bool   `json:"ready"`
}

// Models lists what the /model menu offers (v0.19.13). Blaze Cloud is always the first row and
// shows only blaze-auto: in Cloud mode it is the one default and nothing else from Blaze is
// offered (no provider or model names). Other models come only from the user's own providers
// (a saved key, or a local server they set up); picking one switches to Custom. In Custom mode
// Auto and BlazeRouter, which route over those same providers, are offered as before.
// Keyless is never offered.
func (a *App) Models() []ModelItem {
	a.waitReady()
	cloud := cloudModelsOn()
	sel := a.selection()
	out := []ModelItem{{Provider: cloudai.ProviderID, Name: cloudai.Name, Model: cloudai.Model, Tools: true, Current: cloud, Ready: true}}
	if !cloud {
		out = append(out, ModelItem{Provider: config.RouterID, Name: "BlazeRouter", Model: "auto/best-free and every auto/* preset", Tools: true, Current: !sel.Auto && sel.Provider == config.RouterID, Ready: true},
			ModelItem{Provider: config.Auto, Name: "Auto (free routing)", Model: "fastest healthy model in your chain", Tools: true, Current: sel.Auto, Ready: true})
	}
	for _, p := range a.providerViews() {
		if p.ID == config.KeylessID || p.ID == config.RouterID || !p.Ready {
			continue
		}
		if cloud && !p.HasKey && p.ID != "ollama" && p.ID != config.Custom {
			continue // only the user's own providers (a saved key, or their local/custom server)
		}
		out = append(out, ModelItem{Provider: p.ID, Name: p.Short, Model: p.Model, Tools: llm.CanUseTools(p.ID, p.Model), Current: !cloud && !sel.Auto && sel.Provider == p.ID, Ready: true})
	}
	return out
}

// UseModel makes a provider (with its current model) the default. Kept for older callers.
func (a *App) UseModel(provider string) string { return a.SelectModel(provider, "") }

// ---------------- providers & keys ----------------

type KeyResult struct {
	OK     bool   `json:"ok"`
	Model  string `json:"model"`
	Where  string `json:"where"`
	Error  string `json:"error"`
	Note   string `json:"note"`
	Models int    `json:"models"`
}

// AddProvider tests a key (empty for keyless/local or to re-test the saved one), saves it in
// the keychain (a second, different key joins the key ring) and adds the provider to the chain.
func (a *App) AddProvider(id, key string) KeyResult {
	a.waitReady()
	p, ok := config.ProviderByID(id)
	if !ok {
		return KeyResult{Error: "Unknown provider."}
	}
	if id == config.KeylessID {
		return KeyResult{Error: "Pollinations can't use tools, so Blaze doesn't add it as a model. Turn on the keyless fallback in Settings › Models to let it answer plain chat when nothing else is set up."}
	}
	if id == config.Custom && strings.TrimSpace(a.cfg.CustomBase) == "" {
		return KeyResult{Error: "Enter the service address first (it ends in /v1)."}
	}
	key = strings.TrimSpace(key)
	if owner := llm.KeyOwner(key); owner != "" && owner != id && id != config.Custom {
		return KeyResult{Error: fmt.Sprintf("That looks like a %s key, not a %s one. Add it under %s.", config.Name(owner), config.Name(id), config.Name(owner))}
	}
	if key == "" {
		key = a.cfg.Key(id)
	}
	if key == "" && !config.Keyless(id) && id != config.Custom {
		return KeyResult{Error: "Paste a key first."}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ls, err := llm.ListModels(ctx, p, key)
	if err != nil {
		if id == "ollama" {
			return KeyResult{Error: "Ollama isn't running. Start it, then try again."}
		}
		code := 0
		if e, ok := err.(*llm.HTTPError); ok {
			code = e.Code
		}
		// A custom service may not list models; a key it doesn't reject is still worth saving.
		if id != config.Custom || code == 401 || code == 403 {
			if code == 401 || code == 403 {
				return KeyResult{Error: "That key didn't work: " + llm.PlainText(config.Name(id), code, err.Error())}
			}
			return KeyResult{Error: "Couldn't check the key: " + llm.PlainText(config.Name(id), code, err.Error())}
		}
	}
	var ids []string
	for _, m := range ls {
		ids = append(ids, m.ID)
	}
	model := a.cfg.ModelFor[id]
	if model == "" {
		model = llm.PickModel(id, ids)
	}
	if model == "" {
		model = p.DefaultModel
	}
	if model == "" {
		return KeyResult{Error: "No usable model found on " + p.Name + ". Pick one under the provider once its list loads."}
	}
	where := "no key needed"
	note := ""
	if key != "" && !config.Keyless(id) {
		before := len(a.cfg.AllKeys(id))
		w, err := a.cfg.AddKey(id, key)
		if err != nil {
			return KeyResult{Error: "Couldn't save the key: " + err.Error()}
		}
		if w == "" {
			where = "already saved"
		} else {
			where = w
			if before > 0 {
				note = fmt.Sprintf("Added as key %d for %s. When one runs out, Blaze switches to the next.", before+1, config.Name(id))
			}
		}
	}
	a.cfg.SetModelFor(id, model)
	found := false
	for i, e := range a.cfg.Chain {
		if e.Provider == id {
			found = true
			if e.Model == "" {
				a.cfg.Chain[i].Model = model
			}
		}
	}
	if !found {
		a.cfg.Chain = append(a.cfg.Chain, config.Entry{Provider: id, Model: model})
	}
	a.cfg.SetupDone = true
	_ = a.cfg.Save()
	llm.ClearHealth(id)
	a.emitModel()
	return KeyResult{OK: true, Model: model, Where: where, Note: note, Models: len(ls)}
}

type KeyView struct {
	Masked string `json:"masked"`
	Main   bool   `json:"main"`
	Source string `json:"source"` // "keychain" or "environment"
}

func mask(k string) string {
	if len(k) <= 10 {
		return strings.Repeat("•", len(k))
	}
	return k[:4] + "…" + k[len(k)-4:]
}

// Keys lists a provider's key ring, masked.
func (a *App) Keys(provider string) []KeyView {
	var out []KeyView
	env := ""
	if p, ok := config.ProviderByID(provider); ok && p.Env != "" {
		env = p.Env
	}
	for i, k := range a.cfg.AllKeys(provider) {
		src := "saved"
		if i == 0 && env != "" && strings.TrimSpace(os.Getenv(env)) == k {
			src = "from " + env
		}
		out = append(out, KeyView{Masked: mask(k), Main: i == 0, Source: src})
	}
	return out
}

// RemoveKey removes one key of the ring by its position.
func (a *App) RemoveKey(provider string, index int) string {
	keys := a.cfg.AllKeys(provider)
	if index < 0 || index >= len(keys) {
		return "No such key."
	}
	if err := a.cfg.RemoveKey(provider, keys[index]); err != nil {
		return err.Error()
	}
	if len(a.cfg.AllKeys(provider)) == 0 {
		a.RemoveProvider(provider)
	}
	a.emitModel()
	return "ok"
}

// RemoveProvider removes the provider from the chain, forgets its keys, and drops it as default.
func (a *App) RemoveProvider(id string) {
	var chain []config.Entry
	for _, e := range a.cfg.Chain {
		if e.Provider != id {
			chain = append(chain, e)
		}
	}
	a.cfg.Chain = chain
	if !a.cfg.IsAuto() && a.cfg.Default.Provider == id {
		a.cfg.Default = nil
	}
	a.cfg.DeleteKey(id)
	_ = a.cfg.Save()
	a.emitModel()
}

// SetCustomProvider sets the custom OpenAI-compatible service.
func (a *App) SetCustomProvider(base, name string) {
	base = strings.TrimSpace(base)
	if base != "" && !strings.HasPrefix(base, "http") {
		base = "https://" + base
	}
	a.cfg.CustomBase, a.cfg.CustomName = base, strings.TrimSpace(name)
	_ = a.cfg.Save()
}

// SetChain reorders the Auto chain (the first healthy one answers).
func (a *App) SetChain(chain []config.Entry) {
	var clean []config.Entry
	for _, e := range chain {
		if e.Provider != "" && e.Provider != config.KeylessID {
			if e.Model == "" {
				e.Model = a.cfg.Model(e.Provider)
			}
			clean = append(clean, e)
		}
	}
	a.cfg.Chain = clean
	_ = a.cfg.Save()
	a.emitModel()
}

// UseFreeStack rebuilds the chain from every free-tier provider with a key, in the default order.
func (a *App) UseFreeStack() []config.Entry {
	var ch []config.Entry
	for _, id := range config.FreeOrder {
		if a.cfg.Key(id) != "" {
			ch = append(ch, config.Entry{Provider: id, Model: a.cfg.Model(id)})
		}
	}
	for _, e := range a.cfg.Chain { // keep paid/local entries the user added, at the end
		free := false
		for _, id := range config.FreeOrder {
			free = free || id == e.Provider
		}
		if !free && e.Provider != config.KeylessID {
			ch = append(ch, e)
		}
	}
	a.cfg.Chain = ch
	_ = a.cfg.Save()
	a.emitModel()
	return ch
}

// SetRouting saves the routing switches.
func (a *App) SetRouting(r RoutingView) {
	a.cfg.NoFallback = !r.Fallback
	a.cfg.NoFastest = !r.FastestFirst
	a.cfg.NoKeyless = !r.Keyless
	if r.DailyCap < 0 {
		r.DailyCap = 0
	}
	a.cfg.DailyCap = r.DailyCap
	_ = a.cfg.Save()
}

func (a *App) SetKeyless(v bool) { a.cfg.NoKeyless = !v; _ = a.cfg.Save() }

// RouterStatus is the live health of each model a turn would try, in order.
func (a *App) RouterStatus() []llm.TargetStatus {
	a.waitReady()
	return llm.Status(a.cfg)
}

func (a *App) Usage() llm.UsageReport { return llm.Report() }
func (a *App) ResetUsage()            { llm.ResetUsage() }

// Quota checks plan, balance and rate limits for a provider.
func (a *App) Quota(provider string) llm.QuotaReport {
	p, ok := config.ProviderByID(provider)
	if !ok {
		return llm.QuotaReport{Note: "Unknown provider."}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	key := ""
	if keys := a.cfg.AllKeys(provider); len(keys) > 0 {
		key = keys[0]
	}
	return llm.Quota(ctx, p, key, llm.ResolveModel(provider, a.cfg.Model(provider)))
}

// TestModel sends a one-line request to one provider/model and says how it went.
func (a *App) TestModel(provider, model string) Check {
	p, ok := config.ProviderByID(provider)
	if !ok {
		return Check{Name: provider, Detail: "Unknown provider."}
	}
	if model == "" {
		model = a.cfg.Model(provider)
	}
	model = llm.ResolveModel(provider, model)
	key := ""
	if keys := a.cfg.AllKeys(provider); len(keys) > 0 {
		key = keys[0]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	t0 := time.Now()
	_, err := llm.Chat(ctx, p, key, model, []llm.Message{{Role: "user", Content: "Reply with OK."}}, nil, nil)
	if err != nil {
		code := 0
		if he, ok := err.(*llm.HTTPError); ok {
			code = he.Code
		}
		return Check{Name: config.Name(provider), Detail: model + ": " + llm.PlainText(config.Name(provider), code, err.Error())}
	}
	d := fmt.Sprintf("%s answered in %.1fs", model, time.Since(t0).Seconds())
	if !llm.CanUseTools(provider, model) {
		d += " · can't use tools, chat only"
	}
	return Check{Name: config.Name(provider), OK: true, Detail: d}
}

// ---------------- OpenRouter one-click sign-in (OAuth PKCE) ----------------

// ConnectOpenRouter opens OpenRouter's sign-in in the browser; OpenRouter hands Blaze a key
// on a local callback, which is saved like a pasted one.
func (a *App) ConnectOpenRouter() KeyResult {
	vb := make([]byte, 48)
	_, _ = rand.Read(vb)
	verifier := base64.RawURLEncoding.EncodeToString(vb)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return KeyResult{Error: "Couldn't open a local port for the sign-in: " + err.Error()}
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	cb := fmt.Sprintf("http://localhost:%d/callback", port)
	codeCh := make(chan string, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := r.URL.Query().Get("code")
		msg := "Sign-in didn't finish. You can close this tab."
		if c != "" {
			msg = "Blaze is connected to OpenRouter. You can close this tab."
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<html><body style="font-family:system-ui;padding:48px"><h2>%s</h2></body></html>`, msg)
		select {
		case codeCh <- c:
		default:
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	u := "https://openrouter.ai/auth?callback_url=" + url.QueryEscape(cb) + "&code_challenge=" + challenge + "&code_challenge_method=S256"
	wr.BrowserOpenURL(a.ctx, u)
	var code string
	select {
	case code = <-codeCh:
	case <-time.After(10 * time.Minute):
	}
	if code == "" {
		return KeyResult{Error: "Sign-in didn't finish."}
	}
	body, _ := json.Marshal(map[string]string{"code": code, "code_verifier": verifier, "code_challenge_method": "S256"})
	resp, err := http.Post("https://openrouter.ai/api/v1/auth/keys", "application/json", strings.NewReader(string(body)))
	if err != nil {
		return KeyResult{Error: "Couldn't reach OpenRouter: " + err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var r struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(raw, &r)
	if r.Key == "" {
		return KeyResult{Error: fmt.Sprintf("OpenRouter didn't hand over a key (%d). Try again.", resp.StatusCode)}
	}
	return a.AddProvider("openrouter", r.Key)
}
