package router

// The request log: the last requests through BlazeRouter, kept in memory and in
// ~/.blaze/desktop/blazerouter-log.jsonl (trimmed to the newest maxLog on start).
// Only metadata is kept (who answered, tokens, time, errors), never prompts or replies.

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"blaze-desktop/internal/config"
)

const maxLog = 1000

type Entry struct {
	Time      time.Time `json:"time"`
	Asked     string    `json:"asked"`           // the model the client sent
	Combo     string    `json:"combo,omitempty"` // auto, a combo id, or "" for one model
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	Status    int       `json:"status"`
	Ms        int64     `json:"ms"`
	In        int       `json:"in"`
	Out       int       `json:"out"`
	Stream    bool      `json:"stream"`
	Client    string    `json:"client,omitempty"`    // from the User-Agent: Cursor, Claude Code, Blaze…
	Format    string    `json:"format,omitempty"`    // openai, anthropic, gemini, embeddings
	Saved     int       `json:"saved,omitempty"`     // characters compression removed
	Fallbacks int       `json:"fallbacks,omitempty"` // targets skipped or failed before this one
	Error     string    `json:"error,omitempty"`
	Route     []Attempt `json:"route,omitempty"`
}

type reqLog struct {
	mu   sync.Mutex
	list []Entry
	path string
}

func logPath() string { return filepath.Join(config.Dir(), "desktop", "blazerouter-log.jsonl") }

func openLog() *reqLog {
	l := &reqLog{path: logPath()}
	f, err := os.Open(l.path)
	if err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			var e Entry
			if json.Unmarshal(sc.Bytes(), &e) == nil {
				l.list = append(l.list, e)
			}
		}
		f.Close()
	}
	if len(l.list) > maxLog {
		l.list = l.list[len(l.list)-maxLog:]
		l.rewrite()
	}
	return l
}

func (l *reqLog) rewrite() {
	_ = os.MkdirAll(filepath.Dir(l.path), 0o700)
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	for _, e := range l.list {
		b, _ := json.Marshal(e)
		_, _ = f.Write(append(b, '\n'))
	}
}

func (l *reqLog) add(e Entry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.list = append(l.list, e)
	if len(l.list) > maxLog*2 {
		l.list = l.list[len(l.list)-maxLog:]
		l.rewrite()
		return
	}
	_ = os.MkdirAll(filepath.Dir(l.path), 0o700)
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	b, _ := json.Marshal(e)
	_, _ = f.Write(append(b, '\n'))
	f.Close()
}

// recent is the newest n entries, newest first.
func (l *reqLog) recent(n int) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Entry
	for i := len(l.list) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, l.list[i])
	}
	return out
}

func (l *reqLog) clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.list = nil
	l.rewrite()
}

// todayByTarget counts today's requests per provider|model (for quota-aware routing).
func (l *reqLog) todayByTarget() map[string]int {
	l.mu.Lock()
	defer l.mu.Unlock()
	day := time.Now().Format("2006-01-02")
	out := map[string]int{}
	for _, e := range l.list {
		if e.Time.Local().Format("2006-01-02") == day && e.Provider != "" {
			out[e.Provider+"|"+e.Model]++
		}
	}
	return out
}

// Stat is one provider's (or model's) traffic.
type Stat struct {
	Provider string  `json:"provider"`
	Model    string  `json:"model,omitempty"`
	Requests int     `json:"requests"`
	Errors   int     `json:"errors"`
	In       int64   `json:"in"`
	Out      int64   `json:"out"`
	AvgMs    int64   `json:"avg_ms"`
	Saved    int64   `json:"saved"`
	Share    float64 `json:"share"` // of all requests in the window
}

// Stats is traffic since a time, per provider and per provider+model, busiest first.
func (l *reqLog) stats(since time.Time) (total Stat, byProvider, byModel []Stat) {
	l.mu.Lock()
	defer l.mu.Unlock()
	bp, bm := map[string]*Stat{}, map[string]*Stat{}
	var ms int64
	for _, e := range l.list {
		if e.Time.Before(since) {
			continue
		}
		total.Requests++
		failed := e.Status >= 400 || e.Error != ""
		if failed {
			total.Errors++
		}
		total.In += int64(e.In)
		total.Out += int64(e.Out)
		total.Saved += int64(e.Saved)
		ms += e.Ms
		if e.Provider == "" {
			continue
		}
		for _, x := range []struct {
			m map[string]*Stat
			k string
			s Stat
		}{{bp, e.Provider, Stat{Provider: e.Provider}}, {bm, e.Provider + "|" + e.Model, Stat{Provider: e.Provider, Model: e.Model}}} {
			s := x.m[x.k]
			if s == nil {
				c := x.s
				s = &c
				x.m[x.k] = s
			}
			s.AvgMs = (s.AvgMs*int64(s.Requests) + e.Ms) / int64(s.Requests+1)
			s.Requests++
			if failed {
				s.Errors++
			}
			s.In += int64(e.In)
			s.Out += int64(e.Out)
			s.Saved += int64(e.Saved)
		}
	}
	if total.Requests > 0 {
		total.AvgMs = ms / int64(total.Requests)
	}
	flat := func(m map[string]*Stat) []Stat {
		var out []Stat
		for _, s := range m {
			if total.Requests > 0 {
				s.Share = float64(s.Requests) / float64(total.Requests)
			}
			out = append(out, *s)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Requests > out[j].Requests })
		return out
	}
	return total, flat(bp), flat(bm)
}

// Hourly is requests per hour over the last 24 hours, oldest first.
func (l *reqLog) hourly() []int {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]int, 24)
	now := time.Now().Truncate(time.Hour)
	for _, e := range l.list {
		h := int(now.Sub(e.Time.Truncate(time.Hour)) / time.Hour)
		if h >= 0 && h < 24 {
			out[23-h]++
		}
	}
	return out
}
