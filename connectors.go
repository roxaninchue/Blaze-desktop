package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"blaze-desktop/internal/config"
	"blaze-desktop/internal/connect"
	"blaze-desktop/internal/llm"
)

// Connectors: the user's own accounts and MCP servers as agent tools.
// The UI calls these; the agent loop calls a.conn directly.

func (a *App) startConnectors() {
	a.conn = connect.New(filepath.Join(config.Dir(), "desktop"), connect.Keychain(), connect.Host{
		Emit:    func(ev string, d any) { wr.EventsEmit(a.ctx, ev, d) },
		OpenURL: func(u string) { wr.BrowserOpenURL(a.ctx, u) },
		Confirm: a.confirmAlways,
		Yolo:    func() bool { return a.approval() == "autopilot" }, // Autopilot runs Action tools; Write tools still ask
		Focus: func() {
			wr.WindowUnminimise(a.ctx)
			wr.WindowShow(a.ctx)
		},
		Dir:     func() string { return a.prefs.Folder },
		Version: version,
	})
	// MCP servers are shared with Blaze CLI through ~/.blaze/mcp.json (v0.8.1).
	if err := a.conn.SetMCPConfig(connect.MCPConfigPath(config.Dir())); err != nil {
		fmt.Fprintln(os.Stderr, "blaze: mcp.json:", err)
	}
	// Launched by a blaze:// link (Windows and Linux pass it as an argument).
	for _, arg := range os.Args[1:] {
		a.routeURL(arg)
	}
}

// routeURL handles a blaze:// URL from the OS, a second launch or macOS.
func (a *App) routeURL(raw string) {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "blaze://") || a.conn == nil {
		return
	}
	go func() {
		if !a.conn.HandleURL(raw) {
			wr.EventsEmit(a.ctx, "toast", map[string]string{"text": "Blaze doesn't know the link " + raw, "kind": "error"})
		}
	}()
}

// Connectors is everything the Connectors page shows.
func (a *App) Connectors() connect.View { a.waitReady(); return a.conn.Views() }

func errText(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

// ConnectorSetField saves a client ID, client secret or token. "" means saved.
func (a *App) ConnectorSetField(id, key, value string) string {
	return errText(a.conn.SetField(id, key, value))
}

func (a *App) ConnectorConnect(id string) connect.ConnectResult { return a.conn.Connect(id) }
func (a *App) ConnectorCancel(id string)                        { a.conn.CancelConnect(id) }
func (a *App) ConnectorDisconnect(id string)                    { a.conn.Disconnect(id) }
func (a *App) ConnectorSetEnabled(id string, on bool) string {
	return errText(a.conn.SetEnabled(id, on))
}
func (a *App) ConnectorSetRedirect(id, mode string) string {
	return errText(a.conn.SetRedirect(id, mode))
}

// ConnectorToolMode sets ask / always / off for one tool. owner is a connector id or mcp:<server>.
func (a *App) ConnectorToolMode(owner, tool, mode string) string {
	return errText(a.conn.SetToolMode(owner, tool, mode))
}

// ConnectorGrant asks the provider for the extra permissions a tool needs.
func (a *App) ConnectorGrant(id, tool string) connect.ConnectResult {
	return a.conn.GrantScopes(id, tool)
}

// ConnectorPasteCallback finishes a sign-in from a redirect URL the user copied
// out of the browser (when the loopback port or blaze:// can't be reached).
func (a *App) ConnectorPasteCallback(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(raw), "blaze://") {
		if a.conn.HandleURL(raw) {
			return ""
		}
		return "That isn't a Blaze sign-in link."
	}
	_, msg, ok := a.conn.HandleCallback(raw)
	if ok {
		return ""
	}
	return msg
}

type MCPResult struct {
	Server connect.MCPView `json:"server"`
	Error  string          `json:"error,omitempty"`
}

func (a *App) MCPAdd(spec connect.MCPSpec) MCPResult {
	v, err := a.conn.AddMCP(spec)
	if err == nil && v.Error != "" {
		return MCPResult{Server: v, Error: v.Error}
	}
	return MCPResult{Server: v, Error: errText(err)}
}

func (a *App) MCPRefresh(id string) MCPResult {
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	v, err := a.conn.RefreshMCP(ctx, id)
	return MCPResult{Server: v, Error: errText(err)}
}

func (a *App) MCPRemove(id string) string { return errText(a.conn.RemoveMCP(id)) }
func (a *App) MCPSetEnabled(id string, on bool) string {
	return errText(a.conn.SetMCPEnabled(id, on))
}

// noteHistory tells the connectors which tool groups a reopened chat had loaded.
func (a *App) noteHistory(h []llm.Message) {
	if a.conn == nil {
		return
	}
	a.conn.ResetLoaded()
	for _, m := range h {
		for _, tc := range m.ToolCalls {
			a.conn.NoteUsed(tc.Function.Name, tc.Function.Arguments)
		}
	}
}

// confirmAlways is the approval card with Allow once / Always allow / Don't allow.
func (a *App) confirmAlways(title, detail string, allowAlways bool) (bool, bool) {
	v := a.ask(title, detail, allowAlways)
	return v > 0, v == 2
}

// ask shows a confirm card and waits: 0 no, 1 yes, 2 always.
func (a *App) ask(title, detail string, allowAlways bool) int {
	id := newID()
	ch := make(chan int, 1)
	a.mu.Lock()
	a.pending[id] = ch
	a.mu.Unlock()
	payload := map[string]any{"id": id, "title": title, "detail": detail}
	if allowAlways {
		payload["always"] = true
	}
	if why := permReason(title, detail); why != "" { // v0.19.14: one line on why Blaze asks
		payload["why"] = why
		payload["declined"] = permDeclined(title)
	}
	emit(a.ctx, "confirm", payload)
	defer func() { a.mu.Lock(); delete(a.pending, id); a.mu.Unlock() }()
	select {
	case v := <-ch:
		return v
	case <-a.ctx.Done():
		return 0
	}
}

// AnswerChoice is called by a confirm card: "once", "always" or "no".
func (a *App) AnswerChoice(id, choice string) {
	v := 0
	switch choice {
	case "once", "yes":
		v = 1
	case "always":
		v = 2
	}
	a.mu.Lock()
	ch := a.pending[id]
	a.mu.Unlock()
	if ch != nil {
		select {
		case ch <- v:
		default:
		}
	}
}
