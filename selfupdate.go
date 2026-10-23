package main

// Blaze updates itself from the chat: the /update command and the update_blaze tool
// (so "update yourself" in plain words works too). The frontend (selfupdate.js) shows
// the check, the download and the install as one live line, then Blaze restarts.
// Background agents that were running pick up again after the restart.

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"blaze-desktop/internal/agents"
	"blaze-desktop/internal/config"
	"blaze-desktop/internal/llm"
)

// resumeMarker is written just before an update restarts Blaze while agents run.
func resumeMarker() string { return filepath.Join(config.Dir(), "desktop", "resume-after-update") }

// updateBlazeTool is the main agent's tool for updating the app itself.
func updateBlazeTool() llm.Tool {
	var t llm.Tool
	t.Type = "function"
	t.Function.Name = "update_blaze"
	t.Function.Description = "Update Blaze (this app) to the newest release. Use when the user asks you to update Blaze or yourself. It checks the Blaze downloads site, and if there's a new version Blaze downloads and installs it in the chat and restarts by itself. Never use a shell command to update Blaze."
	t.Function.Parameters = map[string]any{"type": "object", "properties": map[string]any{}}
	return t
}

// selfUpdate runs update_blaze: check, then hand the install to the chat's update line.
func (a *App) selfUpdate() string {
	i := a.CheckUpdate()
	switch {
	case i.Error != "":
		return "Couldn't check for a Blaze update: " + i.Error + " Tell the user in one line."
	case !i.Available:
		return "Blaze is already on the newest version, " + i.Current + ". Tell the user in one line."
	case !i.CanAuto:
		return fmt.Sprintf("Blaze %s is out (the user has %s), but there's no automatic install for this computer. Give them the release page: %s", i.Latest, i.Current, i.Page)
	}
	a.emit("selfupdate", map[string]any{"latest": i.Latest})
	return fmt.Sprintf("Updating to %s has started. Blaze shows the progress in the chat and restarts by itself once it's installed. Tell the user in one short line and call no more tools.", i.Latest)
}

// markResume remembers that agents were running when an update restarted Blaze.
func (a *App) markResume() {
	if a.agents == nil {
		return
	}
	if r, q := a.agents.Running(); r+q > 0 {
		_ = os.MkdirAll(filepath.Dir(resumeMarker()), 0o755)
		_ = os.WriteFile(resumeMarker(), []byte(time.Now().Format(time.RFC3339)), 0o644)
	}
}

// resumeAfterUpdate restarts the agents an update interrupted (within the last hour).
func (a *App) resumeAfterUpdate() int {
	st, err := os.Stat(resumeMarker())
	if err != nil {
		return 0
	}
	_ = os.Remove(resumeMarker())
	if time.Since(st.ModTime()) > time.Hour {
		return 0
	}
	n := 0
	for _, v := range a.agents.List("") {
		if v.Status == agents.Interrupted && a.agents.Resume(v.Handle) == nil {
			n++
		}
	}
	return n
}
