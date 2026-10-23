package main

// Blaze self-knowledge (v0.12+): Blaze knows what it is. The ABOUT YOU block in every
// system prompt carries identity, version, the apps and how they connect, BlazeRouter and
// Blaze Link status and the OS; the about_blaze tool returns the full live report. The
// formatting lives in internal/selfknow (shared with Blaze CLI and named like Blaze
// Android's agent/SelfKnowledge.kt); this file reads Desktop's live state into it.

import (
	"os"
	"runtime"
	"runtime/debug"
	"strings"

	"blaze-desktop/internal/config"
	"blaze-desktop/internal/router"
	"blaze-desktop/internal/selfknow"
	"blaze-desktop/internal/store"
	"blaze-desktop/internal/tools"
)

// linkInBuild says whether this build has Blaze Link's PC side (the pairing server and
// Phone view). It does since v0.14.0 (pair.go, ported from PR #3), so ABOUT YOU and
// about_blaze give the real pairing steps and the live paired-phone status.
const linkInBuild = true

// buildLine is the Go version and, when the binary carries it, the git commit.
func buildLine() string {
	parts := []string{runtime.Version(), runtime.GOOS + "/" + runtime.GOARCH}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				parts = append(parts, "commit "+s.Value[:7])
			}
		}
	}
	return strings.Join(parts, " · ")
}

// selfFacts reads Blaze Desktop's live state. full adds the slower parts (connectors,
// MCP, skills, memory, registries) that only the about_blaze tool needs.
func (a *App) selfFacts(full bool) selfknow.Facts {
	f := selfknow.Facts{App: "Blaze Desktop", Version: version, Build: buildLine(), DataDir: config.Dir(), PhonePrev: true}
	f.Account.Service = selfknow.Worker
	f.Account.SignIn = "/account or Settings › Account (email, Google or GitHub). Blaze CLI on this computer shares the sign-in."
	if cs := a.cloud; cs != nil {
		cs.mu.Lock()
		f.Account.Online = cs.online
		if cs.client != nil && cs.client.Base != "" {
			f.Account.Service = cs.client.Base
		}
		if u := cs.user; u != nil {
			f.Account.SignedIn, f.Account.Email, f.Account.Name = true, u.Email, u.Name
		}
		cs.mu.Unlock()
	}
	f.Account.KeysSync = config.Keys().LastSync()

	f.Router.Present = true
	if a.router != nil {
		v := a.routerView()
		f.Router.Running, f.Router.Enabled, f.Router.InUse = v.Running, v.Enabled, v.InUse
		f.Router.URL, f.Router.Port, f.Router.Ready, f.Router.Models, f.Router.Note = v.URL, v.Port, v.Ready, v.Models, v.Note
	}
	if f.Router.URL == "" {
		f.Router.URL = "http://127.0.0.1:20128"
	}

	f.Link.InBuild = linkInBuild
	f.Link.Devices = selfknow.Devices(config.Dir())
	if a.pair != nil {
		f.Link.Online = a.pair.connectedPhone()
	}

	in := a.info
	f.System = selfknow.System{OSName: in.OSName, OS: in.OS, Arch: in.Arch, CPU: in.CPU, Cores: in.Cores, RAMGB: in.RAMGB, GPU: in.GPU, Shell: in.Shell, Host: in.Hostname}
	if runtime.GOOS == "linux" {
		f.System.Desktop, f.System.Session = selfknow.DesktopEnv(os.Getenv)
	}
	if a.cfg != nil {
		f.Model = a.modelName()
	}
	if !full {
		return f
	}
	for _, p := range router.AutoPresets {
		f.Router.Presets = append(f.Router.Presets, selfknow.Entry{Name: p.ID, Desc: p.Desc})
	}
	f.Views = selfknow.Views(assets, "frontend/dist")
	f.Commands = selfknow.Commands(assets, "frontend/dist")
	if a.conn != nil {
		v := a.conn.Views()
		for _, c := range v.Connectors {
			f.Connectors = append(f.Connectors, selfknow.Conn{Name: c.Name, Status: c.Status, Account: c.Account})
		}
		for _, m := range v.MCP {
			f.MCP = append(f.MCP, selfknow.MCP{Name: m.Name, Status: m.Status, Tools: len(m.Tools)})
		}
		f.MCPFile = v.MCPFile
	}
	for _, s := range tools.SkillStatus() {
		if s.Enabled {
			f.Skills = append(f.Skills, selfknow.Entry{Name: s.Name, Desc: s.Description})
		} else {
			f.SkillsOff++
		}
	}
	if db, err := store.Default(); err == nil {
		f.Memories, f.MemoryDB = db.CountMemories(), db.Path
	}
	return f
}

// aboutYou is the ABOUT YOU block for the system prompt.
func (a *App) aboutYou() string { return selfknow.Brief(a.selfFacts(false)) }

func init() {
	tools.Register(selfknow.ToolName, selfknow.ToolDesc,
		[][3]string{{"section", "string", "Optional: one of " + strings.Join(selfknow.Sections, ", ") + " (empty = everything)"}},
		nil, func(e *tools.Env, args map[string]any) string {
			a := theApp
			if a == nil {
				return "Error: Blaze Desktop is still starting."
			}
			return selfknow.Report(a.selfFacts(true), tools.ArgS(args, "section"))
		})
}
