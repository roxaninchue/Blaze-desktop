package main

// v0.19 Blaze Cloud + Custom models (Settings › Models, the model chip).
//
// By default every model call goes through Blaze Cloud (internal/cloudai): the Blaze gateway,
// signed in with the Blaze account from Settings › Account (account.go), model blaze-auto plus a
// task hint per request. No provider keys. Custom is the user's own providers and keys (the
// existing provider router over ~/.blaze/config.json and the keystore), with optional per-task
// models and an opt-in "use Blaze Cloud as a backup".

import (
	"context"
	"blaze-desktop/internal/persona"
	"strings"
	"sync"
	"time"

	"blaze-desktop/internal/cloudai"
	"blaze-desktop/internal/config"
	"blaze-desktop/internal/llm"
)

// cloudModels routes every llm.CompleteWith / llm.Quick in this app (nil until startup).
var cloudModels *cloudai.Router

// cloudGateway is Blaze Cloud's gateway (the plan badge asks it for the account's plan).
var cloudGateway *cloudai.Gateway

func (a *App) startCloudModels() {
	// A fixed model keeps Custom, except BlazeRouter's own presets: those move to Blaze Cloud
	// (v0.19.1), the way Blaze Mobile answers.
	viaRouter := a.cfg.Default != nil && a.cfg.Default.Provider == config.RouterID
	choice := cloudai.LoadChoice(config.Dir(), !a.cfg.IsAuto() && !viaRouter)
	if choice.Mode == cloudai.ModeCustom && viaRouter && len(choice.Overrides) == 0 && !choice.RouterMoved {
		choice.Mode, choice.RouterMoved = cloudai.ModeCloud, true
		_ = choice.Save()
	}
	gw := cloudai.NewGateway(cloudai.BaseFrom(choice.GatewayURL), a.blazeToken)
	cloudGateway = gw
	cloudModels = cloudai.NewRouter(choice, gw)
	llm.SetBackend(cloudModels)
	go a.CloudPrewarm() // v0.19.1: the first message doesn't wait for DNS + TLS
	// v0.19.8: the shared persona, cached on disk and refreshed at most daily.
	persona.Load(config.Dir())
	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			_ = persona.Refresh(ctx, config.Dir(), nil)
			cancel()
			time.Sleep(6 * time.Hour)
		}
	}()
}

// CloudPrewarm opens the connection to Blaze Cloud ahead of Send (v0.19.1): called at start-up,
// on the first key press in the composer and when the window comes back to the front. At most
// once a minute; a GET of the gateway's public /health, no account token, no model call.
func (a *App) CloudPrewarm() bool {
	if cloudModels == nil || cloudModels.GW == nil || !cloudModelsOn() {
		return false
	}
	return cloudModels.GW.Prewarm(context.Background())
}

// blazeToken is the signed-in Blaze account's session token ("" when signed out).
func (a *App) blazeToken() string {
	if a.cloud == nil || a.cloud.sec == nil {
		return ""
	}
	t, _ := a.cloud.sec.Get(sessionKey)
	return strings.TrimSpace(t)
}

// cloudModelsOn: Blaze Cloud answers by default.
func cloudModelsOn() bool {
	if cloudModels == nil {
		return false
	}
	c := cloudModels.Choice()
	return c.Cloud()
}

// useCustomModels switches to Custom (picking a model of your own in the menu means Custom).
func useCustomModels() {
	if cloudModels != nil && cloudModelsOn() {
		_ = cloudModels.Update(func(c *cloudai.Choice) { c.Mode = cloudai.ModeCustom })
	}
}

// ---------------- bound to the frontend ----------------

// OverrideView is one per-task model of Custom.
type OverrideView struct {
	Task     string `json:"task"` // chat | code | vision
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Label    string `json:"label,omitempty"` // "Groq · qwen-coder"
}

// ModelsChoiceView is Settings › Models' top and the model menu's first rows.
type ModelsChoiceView struct {
	Mode      string         `json:"mode"` // cloud | custom
	Fallback  bool           `json:"fallback"`
	Overrides []OverrideView `json:"overrides"`
	SignedIn  bool           `json:"signed_in"`
	Email     string         `json:"email,omitempty"`
	Gateway   string         `json:"gateway"`
	Custom    string         `json:"custom"` // the Custom default's label ("Auto (free routing)", "Groq · llama…")
	Label     string         `json:"label"`  // what the chip shows before a turn answers
}

func (a *App) modelsChoice() ModelsChoiceView {
	v := ModelsChoiceView{Mode: cloudai.ModeCloud, Custom: a.selection().Label}
	if cloudModels == nil {
		return v
	}
	c := cloudModels.Choice()
	v.Mode, v.Fallback, v.Gateway = c.Mode, c.FallbackToCloud, cloudModels.GW.Base
	for _, t := range cloudai.OverrideTasks {
		ov := OverrideView{Task: t}
		if p, ok := c.Overrides[t]; ok {
			ov.Provider, ov.Model, ov.Label = p.Provider, p.Model, config.Name(p.Provider)+" · "+p.Model
		}
		v.Overrides = append(v.Overrides, ov)
	}
	if a.cloud != nil {
		a.cloud.mu.Lock()
		if u := a.cloud.user; u != nil {
			v.SignedIn, v.Email = true, u.Email
		}
		a.cloud.mu.Unlock()
	}
	v.Label = a.modelName()
	return v
}

// ModelsChoice is the current choice.
func (a *App) ModelsChoice() ModelsChoiceView {
	a.waitReady()
	return a.modelsChoice()
}

// SetModelsMode chooses Blaze Cloud ("cloud") or Custom ("custom").
func (a *App) SetModelsMode(mode string) ModelsChoiceView {
	a.waitReady()
	if cloudModels != nil && (mode == cloudai.ModeCloud || mode == cloudai.ModeCustom) {
		_ = cloudModels.Update(func(c *cloudai.Choice) { c.Mode = mode })
		a.emitModel()
	}
	return a.modelsChoice()
}

// SetCloudFallback: when every Custom model fails, Blaze Cloud answers instead (and says so).
func (a *App) SetCloudFallback(on bool) ModelsChoiceView {
	a.waitReady()
	if cloudModels != nil {
		_ = cloudModels.Update(func(c *cloudai.Choice) { c.FallbackToCloud = on })
	}
	return a.modelsChoice()
}

// SetTaskModel pins a task (chat | code | vision) to one model in Custom; an empty
// provider clears it. Chat and code models must be able to call tools (they do the work).
// Returns "" or a plain reason.
func (a *App) SetTaskModel(task, provider, model string) string {
	a.waitReady()
	if cloudModels == nil {
		return "Blaze is still starting."
	}
	provider, model = strings.TrimSpace(provider), strings.TrimSpace(model)
	if provider != "" {
		if _, ok := config.ProviderByID(provider); !ok {
			return "Unknown provider."
		}
		if model == "" {
			return "Pick a model."
		}
		if (task == "chat" || task == "code") && !llm.CanUseTools(provider, model) {
			return model + " can't call tools, so Blaze can't act with it. Pick one marked Tools."
		}
	}
	ok := true
	_ = cloudModels.Update(func(c *cloudai.Choice) { ok = c.SetOverride(task, provider, model) })
	if !ok {
		return "Blaze can pin chat, code or vision."
	}
	return ""
}

// CloudStatus asks Blaze Cloud whether this account can use it (signed out, waiting list…).
func (a *App) CloudStatus() cloudai.Status {
	a.waitReady()
	if cloudModels == nil {
		return cloudai.Status{Line: cloudai.SignedOut}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	return cloudModels.GW.Check(ctx)
}

var planMu sync.Mutex
var planVal string
var planAt time.Time

// AccountPlan is the signed-in account's plan for the badge under the avatar: basic, standard
// or pro, "" when signed out or Blaze Cloud can't be reached. Cached for 10 minutes; force
// asks again (after an upgrade).
func (a *App) AccountPlan(force bool) string {
	planMu.Lock()
	if !force && !planAt.IsZero() && time.Since(planAt) < 10*time.Minute {
		defer planMu.Unlock()
		return planVal
	}
	planMu.Unlock()
	p := ""
	if cloudGateway != nil {
		p, _ = cloudGateway.Plan(context.Background())
	}
	planMu.Lock()
	planVal, planAt = p, time.Now()
	planMu.Unlock()
	return p
}

// firstNameLine tells the model the signed-in user's first name (the persona uses it only when
// the user greets first).
func (a *App) firstNameLine() string {
	if a.cloud == nil {
		return ""
	}
	a.cloud.mu.Lock()
	u := a.cloud.user
	a.cloud.mu.Unlock()
	if u == nil {
		return ""
	}
	f := strings.Fields(strings.TrimSpace(u.Name))
	if len(f) == 0 {
		return ""
	}
	return "\nUser's first name: " + f[0] + ".\n"
}
