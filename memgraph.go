package main

// Memory screen (v0.5): Blaze's memories, skills and projects as a graph.
// Links are inferred locally and cheaply (shared rare words and names, same chat,
// mentions of a project); categories come from a keyword guess, optionally replaced
// by a model's sort that runs lazily in the background and is cached per fact.

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"blaze-desktop/internal/llm"
	"blaze-desktop/internal/tools"
)

type MemNode struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"` // memory, skill, project
	Label     string `json:"label"`
	Text      string `json:"text"`
	Cat       int    `json:"cat"`
	Created   int64  `json:"created"` // unix ms
	Project   string `json:"project,omitempty"`
	ProjName  string `json:"project_name,omitempty"`
	Chat      string `json:"chat,omitempty"`
	ChatTitle string `json:"chat_title,omitempty"`
	Path      string `json:"path,omitempty"`
	AI        bool   `json:"ai,omitempty"` // category came from the model
	Pinned    bool   `json:"pinned,omitempty"`
	Uses      int    `json:"uses,omitempty"` // times it was put in a prompt or recalled
}

type MemEdge struct {
	A   int     `json:"a"`
	B   int     `json:"b"`
	W   float64 `json:"w"`
	Why string  `json:"why"`
}

type MemCat struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type MemGraph struct {
	Nodes    []MemNode `json:"nodes"`
	Edges    []MemEdge `json:"edges"`
	Cats     []MemCat  `json:"cats"`
	AISort   bool      `json:"ai_sort"`
	Sorting  bool      `json:"sorting"`
	Unsorted int       `json:"unsorted"`
	CanSort  bool      `json:"can_sort"`
	Notice   string    `json:"notice,omitempty"` // "please update Blaze" when blaze.db is newer than this build
}

var memSort struct {
	sync.Mutex
	running bool
}

func (a *App) memAISort() bool { return a.prefs.MemoryAISort == nil || *a.prefs.MemoryAISort }

// SetMemoryAISort turns the background model sort of memories on or off.
func (a *App) SetMemoryAISort(on bool) { a.prefs.MemoryAISort = &on; a.savePrefs() }

// tagMemoryTurn gives the turn a chat id up front so memories saved in it link to
// their chat. Called from turn() with a.mu held.
func (a *App) tagMemoryTurn(s *session) {
	a.nameSession(s)
	tools.SetMemoryContext(a.prefs.Project, s.id)
	a.conv.Store(s.id)
}

// convKey names the current chat for tools turned on with find_tools. It never
// takes a.mu, so tools can ask for it from anywhere.
func (a *App) convKey() string {
	s, _ := a.conv.Load().(string)
	return "chat:" + s
}

// ---------------- local categories ----------------

var catWords = []struct {
	name  string
	words []string
}{
	{"Preferences", []string{"prefer", "prefers", "like", "likes", "wants", "want", "always", "never", "style", "tone", "favourite", "favorite", "hate", "hates", "dislike", "dislikes", "rather", "concise", "short", "dark", "light", "mode", "language", "british", "american", "emoji"}},
	{"Setup & tools", []string{"pnpm", "npm", "yarn", "node", "python", "pip", "git", "docker", "linux", "arch", "omarchy", "hyprland", "wayland", "windows", "macos", "mac", "shell", "bash", "zsh", "fish", "terminal", "editor", "vscode", "neovim", "vim", "install", "installed", "gpu", "cpu", "ram", "ssd", "disk", "laptop", "pc", "monitor", "keyboard", "driver", "ollama", "model", "key", "keys", "wsl", "brew", "pacman"}},
	{"Projects & code", []string{"project", "repo", "repository", "code", "app", "build", "deploy", "deploys", "test", "tests", "branch", "commit", "shopify", "site", "website", "component", "folder", "src", "api", "backend", "frontend", "database", "postgres", "react", "vite", "go", "wails", "release", "ci", "liquid", "theme", "css", "typescript"}},
	{"People", []string{"friend", "mom", "mum", "dad", "brother", "sister", "wife", "husband", "partner", "girlfriend", "boyfriend", "colleague", "cofounder", "co-founder", "boss", "manager", "team", "teacher", "cousin", "uncle", "aunt", "son", "daughter", "family", "named", "called"}},
	{"Work & business", []string{"business", "client", "clients", "customer", "customers", "store", "shop", "sales", "price", "prices", "pricing", "revenue", "marketing", "ads", "invoice", "order", "orders", "supplier", "suppliers", "product", "products", "brand", "dropshipping", "zendrop", "upwork", "fiverr", "freelance", "margin", "profit", "budget", "kwacha", "usd", "launch"}},
	{"Personal", []string{"birthday", "born", "lives", "live", "home", "health", "food", "diet", "allergic", "allergy", "sleep", "gym", "school", "study", "exam", "university", "church", "music", "football", "movie", "games", "gaming", "travel", "lusaka", "zambia", "city", "timezone", "wakes", "morning", "night"}},
}

func localCategory(tokens []string) string {
	best, score := "General", 0
	for _, c := range catWords {
		n := 0
		for _, t := range tokens {
			for _, w := range c.words {
				if t == w {
					n++
				}
			}
		}
		if n > score {
			best, score = c.name, n
		}
	}
	return best
}

// ---------------- tokens ----------------

var stop = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`the and for are but not you all any can had her was one our out has him his how its may new now old see two way who did get got let put say she too use uses used using with that this from they them then than when what will your into over also just only very more most some such like want wants user users blaze about after before their there these those which while where would could should been being have does doing done each make makes made keep keeps kept always never every much many here because via per it's i'm don't doesn't`) {
		stop[w] = true
	}
}

func words(s string) []string {
	var out []string
	f := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' || r == '_' || r == '+' || r == '#' || r == '/')
	})
	for _, w := range f {
		w = strings.Trim(w, ".-_/")
		if strings.Contains(w, "/") { // a path: keep its last part
			w = strings.Trim(filepath.Base(w), ".-_")
		}
		if len(w) < 3 && !(len(w) == 2 && (w == "go" || w == "ci" || w == "ai" || w == "pc")) {
			continue
		}
		out = append(out, w)
	}
	return out
}

func stem(w string) string {
	switch {
	case len(w) > 6 && strings.HasSuffix(w, "ing"):
		return w[:len(w)-3]
	case len(w) > 5 && strings.HasSuffix(w, "ies"):
		return w[:len(w)-3] + "y"
	case len(w) > 4 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss"):
		return w[:len(w)-1]
	}
	return w
}

func linkTokens(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range words(s) {
		if stop[w] {
			continue
		}
		w = stem(w)
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

func shortLabel(s string) string {
	s = strings.TrimSpace(s)
	for _, p := range []string{"User ", "The user "} {
		if strings.HasPrefix(s, p) && len(s) > len(p)+8 {
			s = strings.ToUpper(s[len(p):len(p)+1]) + s[len(p)+1:]
		}
	}
	if i := strings.IndexAny(s, ".;:—"); i > 12 {
		s = s[:i]
	}
	f := strings.Fields(s)
	out := ""
	for _, w := range f {
		if len(out)+len(w)+1 > 34 {
			return strings.TrimRight(out, ",") + "…"
		}
		if out != "" {
			out += " "
		}
		out += w
	}
	return strings.TrimRight(out, ",")
}

// ---------------- graph ----------------

// MemoryGraph returns every memory, skill and project with inferred links.
func (a *App) MemoryGraph() MemGraph {
	facts := tools.Facts()
	g := MemGraph{AISort: a.memAISort(), CanSort: len(llm.Targets(a.cfg, false)) > 0, Notice: tools.UpdateNotice()}

	projects := loadProjects()
	projName := map[string]string{}
	for _, p := range projects {
		projName[p.ID] = p.Name
	}
	chatTitle := map[string]string{}
	needChats := false
	for _, f := range facts {
		if f.Chat != "" {
			needChats = true
		}
	}
	if needChats {
		for _, c := range a.allConvs() {
			chatTitle[c.ID] = c.Title
		}
	}

	catIdx := map[string]int{}
	cat := func(name string) int {
		if i, ok := catIdx[name]; ok {
			g.Cats[i].Count++
			return i
		}
		catIdx[name] = len(g.Cats)
		g.Cats = append(g.Cats, MemCat{Name: name, Count: 1})
		return catIdx[name]
	}

	var toks [][]string
	// One mind: the memories table is the source of truth. Its lines come first; a
	// blaze.db line it mirrors (kept for Blaze CLI) isn't shown twice.
	mirrored := map[string]bool{}
	if md := mindDB(); md != nil {
		for _, m := range md.All() {
			if m.Mirror != 0 {
				mirrored[tools.FactID(m.Mirror)] = true
			}
			n := MemNode{ID: mindNodeID(m.ID), Kind: "memory", Label: shortLabel(m.Subject + ": " + m.Text), Text: m.Text, Cat: cat(kindCat(m.Kind)), AI: true,
				Created: m.Created * 1000}
			g.Nodes = append(g.Nodes, n)
			toks = append(toks, linkTokens(m.Subject+" "+m.Related+" "+m.Text))
		}
	}
	for _, f := range facts {
		if mirrored[f.ID] {
			continue
		}
		t := linkTokens(f.Text)
		name, ai := localCategory(t), false
		if f.Category != "" {
			name, ai = f.Category, true
		} else {
			g.Unsorted++
		}
		n := MemNode{ID: f.ID, Kind: "memory", Label: shortLabel(f.Text), Text: f.Text, Cat: cat(name), AI: ai,
			Created: f.Date.UnixMilli(), Project: f.Project, Chat: f.Chat, ProjName: projName[f.Project], ChatTitle: chatTitle[f.Chat],
			Pinned: f.Pinned, Uses: f.Uses}
		g.Nodes = append(g.Nodes, n)
		toks = append(toks, t)
	}
	nMem := len(g.Nodes)
	for _, s := range tools.Skills() {
		created := int64(0)
		if st, err := osStat(s.Path); err == nil {
			created = st.UnixMilli()
		}
		g.Nodes = append(g.Nodes, MemNode{ID: "skill:" + filepath.Base(filepath.Dir(s.Path)), Kind: "skill", Label: s.Name, Text: s.Desc, Cat: cat("Skills"), Created: created, Path: s.Path})
		toks = append(toks, linkTokens(s.Name+" "+strings.ReplaceAll(s.Name, "-", " ")+" "+s.Desc))
	}
	projStart := len(g.Nodes)
	for _, p := range projects {
		g.Nodes = append(g.Nodes, MemNode{ID: "project:" + p.ID, Kind: "project", Label: p.Name, Text: p.Instructions, Cat: cat("Projects"), Created: p.Created.UnixMilli(), Project: p.ID, ProjName: p.Name, Path: p.Folder})
		toks = append(toks, nil)
	}

	g.Edges = inferEdges(g.Nodes[:projStart], toks[:projStart], nMem)

	// Project links: saved in that project, or mentions its name or folder.
	for pi, p := range projects {
		pn := projStart + pi
		keys := map[string]bool{}
		for _, k := range linkTokens(p.Name + " " + filepath.Base(p.Folder)) {
			keys[k] = true
		}
		joined := strings.ToLower(strings.ReplaceAll(p.Name, " ", ""))
		for i := 0; i < projStart; i++ {
			n := g.Nodes[i]
			why := ""
			if n.Project == p.ID {
				why = "saved in " + p.Name
			} else {
				for _, t := range toks[i] {
					if keys[t] || (len(joined) > 3 && t == joined) {
						why = "mentions " + p.Name
						break
					}
				}
			}
			if why != "" {
				g.Edges = append(g.Edges, MemEdge{A: i, B: pn, W: 1.5, Why: why})
			}
		}
	}

	// Lazy model sort: only new, unsorted memories, at most every 6 hours, in the background.
	if g.AISort && g.CanSort && g.Unsorted >= 3 && time.Since(tools.MemSortedAt()) > 6*time.Hour {
		go a.sortMemories(false)
	}
	memSort.Lock()
	g.Sorting = memSort.running
	memSort.Unlock()
	return g
}

// inferEdges links nodes that share rare words/names or were saved in the same chat.
// An inverted index keeps it near-linear; each node keeps its strongest few links.
func inferEdges(nodes []MemNode, toks [][]string, nMem int) []MemEdge {
	N := len(nodes)
	if N < 2 {
		return nil
	}
	df := map[string][]int{}
	for i, ts := range toks {
		for _, t := range ts {
			df[t] = append(df[t], i)
		}
	}
	maxDF := int(math.Max(3, math.Min(60, float64(N)*0.12)))
	type acc struct {
		w    float64
		best string
		bw   float64
	}
	pairs := map[[2]int]*acc{}
	add := func(i, j int, w float64, why string) {
		if i > j {
			i, j = j, i
		}
		k := [2]int{i, j}
		p := pairs[k]
		if p == nil {
			p = &acc{}
			pairs[k] = p
		}
		p.w += w
		if w > p.bw {
			p.bw, p.best = w, why
		}
	}
	for t, ids := range df {
		if len(ids) < 2 || len(ids) > maxDF {
			continue
		}
		w := math.Log(float64(N+1) / float64(len(ids)))
		for x := 0; x < len(ids); x++ {
			for y := x + 1; y < len(ids); y++ {
				add(ids[x], ids[y], w, t)
			}
		}
	}
	byChat := map[string][]int{}
	for i, n := range nodes[:nMem] {
		if n.Chat != "" {
			byChat[n.Chat] = append(byChat[n.Chat], i)
		}
	}
	for _, ids := range byChat {
		for x := 0; x < len(ids) && x < 40; x++ {
			for y := x + 1; y < len(ids) && y < 40; y++ {
				add(ids[x], ids[y], 2.2, "same chat")
			}
		}
	}
	type cand struct {
		k   [2]int
		w   float64
		why string
	}
	var cs []cand
	thr := 2.0
	if N < 12 {
		thr = 1.0
	}
	for k, p := range pairs {
		if p.w >= thr {
			why := p.best
			if why != "same chat" {
				why = "both mention “" + why + "”"
			}
			cs = append(cs, cand{k, p.w, why})
		}
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].w != cs[j].w {
			return cs[i].w > cs[j].w
		}
		return cs[i].k[0] < cs[j].k[0] || (cs[i].k[0] == cs[j].k[0] && cs[i].k[1] < cs[j].k[1])
	})
	deg := make([]int, N)
	const maxDeg = 5
	var out []MemEdge
	for _, c := range cs {
		i, j := c.k[0], c.k[1]
		if deg[i] >= maxDeg && deg[j] >= maxDeg {
			continue
		}
		deg[i]++
		deg[j]++
		out = append(out, MemEdge{A: i, B: j, W: math.Round(c.w*100) / 100, Why: c.why})
	}
	return out
}

// ---------------- edit / forget ----------------

type MemEdit struct {
	ID    string `json:"id"`
	Error string `json:"error,omitempty"`
}

// EditMemory changes one memory's text; the id changes with the text.
// mindNodeID is a memories-table line's id in the graph ("k<id>").
func mindNodeID(id int64) string { return "k" + strconv.FormatInt(id, 10) }

func mindIDOf(nodeID string) (int64, bool) {
	if !strings.HasPrefix(nodeID, "k") {
		return 0, false
	}
	n, err := strconv.ParseInt(nodeID[1:], 10, 64)
	return n, err == nil
}

// kindCat names a memory kind as a graph category.
func kindCat(k string) string {
	return map[string]string{"profile": "Profile", "preference": "Preferences", "person": "People", "project": "Projects & plans", "decision": "Decisions", "fact": "Facts"}[k]
}

func (a *App) EditMemory(id, text string) MemEdit {
	if mid, ok := mindIDOf(id); ok {
		md := mindDB()
		if md == nil {
			return MemEdit{Error: "memory isn't available"}
		}
		if err := md.Update(mid, text, "", ""); err != nil {
			return MemEdit{Error: err.Error()}
		}
		if m, ok := md.Get(mid); ok && m.Mirror != 0 {
			_, _ = tools.EditFact(tools.FactID(m.Mirror), m.Text)
		}
		return MemEdit{ID: id}
	}
	nid, err := tools.EditFact(id, text)
	if err != nil {
		return MemEdit{Error: err.Error()}
	}
	return MemEdit{ID: nid}
}

// ForgetMemory removes one memory. Returns "" or a plain error.
func (a *App) ForgetMemory(id string) string {
	if mid, ok := mindIDOf(id); ok {
		md := mindDB()
		if md == nil {
			return "memory isn't available"
		}
		if m, ok := md.Get(mid); ok && m.Mirror != 0 {
			_ = tools.ForgetFact(tools.FactID(m.Mirror))
		}
		if err := md.Delete(mid); err != nil {
			return err.Error()
		}
		return ""
	}
	if err := tools.ForgetFact(id); err != nil {
		return err.Error()
	}
	return ""
}

// SetMemoryCategory pins a memory to a category by hand.
func (a *App) SetMemoryCategory(id, category string) { _ = tools.SetFactCategory(id, category) }

// PinMemory pins a memory so it goes into every prompt (or unpins it). Returns "" or a plain error.
func (a *App) PinMemory(id string, on bool) string {
	if err := tools.PinFact(id, on); err != nil {
		return err.Error()
	}
	return ""
}

// SearchMemory returns the ids of the memories matching q, best first (the Memory screen's search box).
func (a *App) SearchMemory(q string) []string {
	d := tools.DB()
	if d == nil {
		return nil
	}
	var out []string
	if md := mindDB(); md != nil {
		for _, m := range md.Search(q, "", 40) {
			out = append(out, mindNodeID(m.ID))
		}
	}
	for _, m := range d.Search(q, 200) {
		out = append(out, tools.FactID(m.ID))
	}
	return out
}

// SortMemories asks the model to sort memories into categories now (all of them).
func (a *App) SortMemories() string {
	if len(llm.Targets(a.cfg, false)) == 0 {
		return "Add a model first (Settings › Models)."
	}
	go a.sortMemories(true)
	return ""
}

func (a *App) sortMemories(all bool) {
	memSort.Lock()
	if memSort.running {
		memSort.Unlock()
		return
	}
	memSort.running = true
	memSort.Unlock()
	defer func() {
		memSort.Lock()
		memSort.running = false
		memSort.Unlock()
		if a.ctx != nil {
			wr.EventsEmit(a.ctx, "memory", "sorted")
		}
	}()
	tools.SetMemSortedAt(time.Now())
	existing := map[string]bool{}
	var todo []tools.Fact
	for _, f := range tools.Facts() {
		if f.Category != "" {
			existing[f.Category] = true
			if !all {
				continue
			}
		}
		todo = append(todo, f)
	}
	if len(todo) == 0 {
		return
	}
	var names []string
	for k := range existing {
		names = append(names, k)
	}
	sort.Strings(names)
	for start := 0; start < len(todo); start += 120 {
		batch := todo[start:min(start+120, len(todo))]
		var b strings.Builder
		b.WriteString("Sort these facts an assistant saved about its user into 4 to 8 broad categories. Category names: 1-3 words, Title Case")
		if len(names) > 0 && !all {
			b.WriteString(", reusing these where they fit: " + strings.Join(names, ", "))
		}
		b.WriteString(".\nReply with one JSON object only, mapping each id to its category, like {\"a1b2\": \"Preferences\"}.\n\n")
		for _, f := range batch {
			t := f.Text
			if len(t) > 200 {
				t = t[:200]
			}
			b.WriteString(f.ID + ": " + t + "\n")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		r, err := llm.Complete(ctx, a.cfg, []llm.Message{{Role: "user", Content: b.String()}}, nil, nil)
		cancel()
		if err != nil || r == nil {
			return
		}
		txt := r.Text
		if i := strings.Index(txt, "{"); i >= 0 {
			txt = txt[i:]
		}
		res, ok := llm.ParseArgs(txt)
		if !ok {
			return
		}
		for _, f := range batch {
			c, _ := res[f.ID].(string)
			c = strings.TrimSpace(c)
			if c == "" || len(c) > 32 {
				continue
			}
			_ = tools.SetFactCategory(f.ID, c)
		}
	}
}

func osStat(p string) (time.Time, error) {
	st, err := os.Stat(p)
	if err != nil {
		return time.Time{}, err
	}
	return st.ModTime(), nil
}
