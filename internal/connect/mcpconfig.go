package connect

// Shared MCP config (v0.8.1): ~/.blaze/mcp.json in the standard shape every MCP host
// reads, so Blaze CLI and Blaze Desktop share one list of servers:
//
//	{"mcpServers": {
//	   "files":  {"command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "~"], "env": {"K": "v"}},
//	   "linear": {"url": "https://mcp.linear.app/mcp", "headers": {"Authorization": "Bearer ${LINEAR_TOKEN}"}},
//	   "old":    {"url": "https://example.com/sse", "type": "sse", "disabled": true}}}
//
// The file is the source of truth for which servers exist and how to start them;
// connectors.json keeps what only Desktop needs (cached tools, per-tool modes, the last
// error). A token typed into Desktop stays in the system keychain, never in the file.
// Keys and fields this code doesn't know are kept as they are when it writes.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// MCPEntry is one server in mcp.json.
type MCPEntry struct {
	Command  string            `json:"command,omitempty"`
	Args     []string          `json:"args,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	URL      string            `json:"url,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Type     string            `json:"type,omitempty"` // stdio | http | streamable-http | sse
	Disabled bool              `json:"disabled,omitempty"`
}

// Transport is how Blaze talks to the server: stdio, http (Streamable HTTP) or sse.
func (e MCPEntry) Transport() string {
	t := strings.ToLower(strings.TrimSpace(e.Type))
	switch {
	case t == "sse":
		return "sse"
	case t == "stdio" || (e.URL == "" && e.Command != ""):
		return "stdio"
	case e.URL != "" && t == "" && strings.HasSuffix(strings.TrimRight(e.URL, "/"), "/sse"):
		return "sse" // the old HTTP+SSE endpoints conventionally end in /sse
	}
	return "http"
}

var idRe = regexp.MustCompile(`[^a-z0-9-]+`)

// MCPID turns a server's name in mcp.json into Blaze's id: lowercase letters, digits
// and single hyphens (never "_", so mcp__<id>__<tool> always splits one way).
func MCPID(name string) string {
	id := idRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	id = strings.Trim(regexp.MustCompile(`-+`).ReplaceAllString(id, "-"), "-")
	if len(id) > 24 {
		id = strings.Trim(id[:24], "-")
	}
	if id == "" {
		id = "server"
	}
	return id
}

// MCPConfigPath is the shared file in Blaze's config folder (~/.blaze/mcp.json).
func MCPConfigPath(blazeDir string) string { return filepath.Join(blazeDir, "mcp.json") }

// readMCPConfig reads the file as raw JSON so unknown keys survive a rewrite.
func readMCPConfig(path string) (top map[string]json.RawMessage, servers map[string]map[string]json.RawMessage, err error) {
	top = map[string]json.RawMessage{}
	servers = map[string]map[string]json.RawMessage{}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return top, servers, nil
		}
		return nil, nil, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return top, servers, nil
	}
	if err := json.Unmarshal(b, &top); err != nil {
		return nil, nil, err
	}
	if raw, ok := top["mcpServers"]; ok {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return nil, nil, err
		}
	}
	if servers == nil {
		servers = map[string]map[string]json.RawMessage{}
	}
	return top, servers, nil
}

// ReadMCPConfig returns the servers in the file by name.
func ReadMCPConfig(path string) (map[string]MCPEntry, error) {
	_, raw, err := readMCPConfig(path)
	if err != nil {
		return nil, err
	}
	out := map[string]MCPEntry{}
	for name, fields := range raw {
		b, _ := json.Marshal(fields)
		var e MCPEntry
		if json.Unmarshal(b, &e) == nil {
			out[name] = e
		}
	}
	return out, nil
}

// entryFor is how a server is written to the file.
func entryFor(s *MCPServer) MCPEntry {
	e := MCPEntry{Disabled: s.Disabled}
	if s.Transport == "stdio" {
		e.Command, e.Args, e.Env = s.Command, s.Args, s.Env
	} else {
		e.URL, e.Headers = s.URL, s.Headers
		if s.Transport == "sse" {
			e.Type = "sse"
		}
	}
	return e
}

// writeMCPConfig puts our servers in the file (keeping other keys and fields) and drops
// the names in remove.
func writeMCPConfig(path string, servers []*MCPServer, remove []string) error {
	top, raw, err := readMCPConfig(path)
	if err != nil {
		return err
	}
	for _, n := range remove {
		delete(raw, n)
	}
	known := []string{"command", "args", "env", "url", "headers", "type", "disabled"}
	for _, s := range servers {
		key := s.key()
		fields := raw[key]
		if fields == nil {
			fields = map[string]json.RawMessage{}
		}
		for _, k := range known {
			delete(fields, k)
		}
		b, _ := json.Marshal(entryFor(s))
		var mine map[string]json.RawMessage
		_ = json.Unmarshal(b, &mine)
		for k, v := range mine {
			fields[k] = v
		}
		raw[key] = fields
	}
	sb, _ := json.Marshal(raw)
	top["mcpServers"] = sb
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// key is the server's name in mcp.json.
func (s *MCPServer) key() string {
	if s.Key != "" {
		return s.Key
	}
	return s.ID
}

// ---------- the store side ----------

// SetMCPConfig points the store at the shared file and syncs from it now. Servers
// Desktop had before the file existed are written into it once.
func (s *Store) SetMCPConfig(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfgPath = path
	s.cfgMod = time.Time{}
	return s.syncLocked(true)
}

// syncLocked re-reads the file when it changed (another app may have edited it).
func (s *Store) syncLocked(force bool) error {
	if s.cfgPath == "" {
		return nil
	}
	st, err := os.Stat(s.cfgPath)
	var mod time.Time
	if err == nil {
		mod = st.ModTime()
		if !force && mod.Equal(s.cfgMod) && st.Size() == s.cfgSize {
			return nil
		}
		s.cfgSize = st.Size()
	} else if !os.IsNotExist(err) {
		return err
	}
	entries, err := ReadMCPConfig(s.cfgPath)
	if err != nil {
		s.cfgErr = "mcp.json: " + err.Error()
		s.cfgMod = mod
		return err
	}
	s.cfgErr = ""
	byKey := map[string]*MCPServer{}
	for _, m := range s.d.MCP {
		byKey[m.key()] = m
	}
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	sort.Strings(names)
	var next []*MCPServer
	used := map[string]bool{}
	for _, n := range names {
		e := entries[n]
		m := byKey[n]
		if m == nil {
			id := MCPID(n)
			for i, base := 2, id; used[id] || s.idTakenLocked(id, n); i++ {
				id = base + "-" + itoa(i)
			}
			m = &MCPServer{ID: id, Name: n}
			if id != n {
				m.Key = n
			}
		}
		used[m.ID] = true
		tr := e.Transport()
		if m.Transport != tr || m.URL != e.URL || m.Command != e.Command || strings.Join(m.Args, "\x00") != strings.Join(e.Args, "\x00") {
			m.Tools, m.Checked, m.Error = nil, time.Time{}, "" // a different server now: list its tools again
		}
		m.Transport, m.URL, m.Command, m.Args, m.Env, m.Headers, m.Disabled = tr, e.URL, e.Command, e.Args, e.Env, e.Headers, e.Disabled
		m.Synced = true
		next = append(next, m)
	}
	var migrate []*MCPServer
	for _, m := range s.d.MCP {
		if _, ok := entries[m.key()]; ok {
			continue
		}
		if m.Synced {
			_ = s.sec.Delete(mcpKey(m.ID)) // removed from the file by another app
			continue
		}
		m.Synced = true
		migrate = append(migrate, m)
		next = append(next, m)
	}
	s.d.MCP = next
	if len(migrate) > 0 {
		if err := writeMCPConfig(s.cfgPath, migrate, nil); err != nil {
			return err
		}
		if st, err := os.Stat(s.cfgPath); err == nil {
			mod, s.cfgSize = st.ModTime(), st.Size()
		}
	}
	s.cfgMod = mod
	return s.saveLocked()
}

func (s *Store) idTakenLocked(id, key string) bool {
	for _, m := range s.d.MCP {
		if m.ID == id && m.key() != key {
			return true
		}
	}
	return false
}

// writeConfigLocked mirrors a change into the shared file.
func (s *Store) writeConfigLocked(changed []*MCPServer, removed []string) error {
	if s.cfgPath == "" {
		return nil
	}
	for _, m := range changed {
		m.Synced = true
	}
	if err := writeMCPConfig(s.cfgPath, changed, removed); err != nil {
		return err
	}
	if st, err := os.Stat(s.cfgPath); err == nil {
		s.cfgMod, s.cfgSize = st.ModTime(), st.Size()
	}
	return nil
}

// MCPConfigError is the last problem reading mcp.json ("" when it's fine).
func (s *Store) MCPConfigError() string { s.mu.Lock(); defer s.mu.Unlock(); return s.cfgErr }

// MCPConfigFile is the shared file's path ("" when not set).
func (s *Store) MCPConfigFile() string { s.mu.Lock(); defer s.mu.Unlock(); return s.cfgPath }

func itoa(i int) string {
	b := []byte{}
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// expandEnv fills ${VAR} / $VAR from the environment in env values and headers, the
// way other MCP hosts do, so the file can name a secret without holding it.
func expandEnv(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if strings.Contains(v, "$") {
			v = os.ExpandEnv(v)
		}
		out[k] = v
	}
	return out
}
