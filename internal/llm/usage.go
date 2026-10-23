package llm

// Token usage per day and model, with a cost estimate where the price is known
// (ported from the Android app's Usage.kt). Stored in ~/.blaze/desktop/usage.json.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"blaze-desktop/internal/config"
)

type usageRow struct {
	In     int64   `json:"in"`
	Out    int64   `json:"out"`
	Cost   float64 `json:"cost"`
	Calls  int     `json:"calls"`
	Priced bool    `json:"priced"`
}

var (
	umu   sync.Mutex
	udata map[string]map[string]*usageRow // day -> "provider|model" -> row
)

func usagePath() string { return filepath.Join(config.Dir(), "desktop", "usage.json") }

func loadUsage() {
	if udata != nil {
		return
	}
	udata = map[string]map[string]*usageRow{}
	b, _ := os.ReadFile(usagePath())
	_ = json.Unmarshal(b, &udata)
}

func saveUsage() {
	days := make([]string, 0, len(udata))
	for d := range udata {
		days = append(days, d)
	}
	sort.Strings(days)
	for len(days) > 62 { // keep about two months
		delete(udata, days[0])
		days = days[1:]
	}
	_ = os.MkdirAll(filepath.Dir(usagePath()), 0o700)
	b, _ := json.Marshal(udata)
	_ = os.WriteFile(usagePath(), b, 0o600)
}

// price is USD per million tokens (input, output), matched by model-name substring.
func price(provider, model string) (float64, float64, bool) {
	m := strings.ToLower(model)
	if strings.HasSuffix(m, ":free") {
		return 0, 0, true
	}
	if p, ok := config.ProviderByID(provider); ok && p.Free && provider != "openrouter" && provider != "mistral" && provider != "deepseek" {
		return 0, 0, true // free tiers (Gemini, Groq, Cerebras, NVIDIA, local) count as 0
	}
	table := []struct {
		k       string
		in, out float64
	}{
		{"gpt-4o-mini", 0.15, 0.6}, {"gpt-4.1-nano", 0.1, 0.4}, {"gpt-4.1-mini", 0.4, 1.6}, {"gpt-4.1", 2, 8}, {"gpt-4o", 2.5, 10},
		{"gpt-5-nano", 0.05, 0.4}, {"gpt-5-mini", 0.25, 2}, {"gpt-5", 1.25, 10}, {"o4-mini", 1.1, 4.4},
		{"claude-3-5-haiku", 0.8, 4}, {"haiku", 1, 5}, {"sonnet", 3, 15}, {"opus", 15, 75},
		{"deepseek-chat", 0.27, 1.1}, {"deepseek-reasoner", 0.55, 2.19}, {"mistral-small", 0.1, 0.3}, {"mistral-large", 2, 6},
		{"grok-4-fast", 0.2, 0.5}, {"grok-4", 3, 15}, {"grok-3-mini", 0.3, 0.5}, {"kimi-k2", 0.6, 2.5},
	}
	for _, t := range table {
		if strings.Contains(m, t.k) {
			return t.in, t.out, true
		}
	}
	return 0, 0, false
}

func today() string { return time.Now().Format("2006-01-02") }

// RecordUsage adds one model call. Uses the provider's counts when sent, else estimates.
func RecordUsage(provider, model string, promptChars int, reply *Result) {
	if provider == config.RouterID {
		return // BlazeRouter records the real provider and model itself
	}
	in, out := int64(Estimate(promptChars)), int64(0)
	if reply != nil {
		out = int64(len(reply.Text)/4 + 1)
		for _, c := range reply.ToolCalls {
			out += int64(len(c.Function.Arguments)/4 + 10)
		}
		if reply.Usage != nil && reply.Usage.Prompt > 0 {
			in = int64(reply.Usage.Prompt)
		}
		if reply.Usage != nil && reply.Usage.Completion > 0 {
			out = int64(reply.Usage.Completion)
		}
	}
	pi, po, priced := price(provider, model)
	cost := (float64(in)*pi + float64(out)*po) / 1e6
	umu.Lock()
	defer umu.Unlock()
	loadUsage()
	d := udata[today()]
	if d == nil {
		d = map[string]*usageRow{}
		udata[today()] = d
	}
	k := provider + "|" + model
	r := d[k]
	if r == nil {
		r = &usageRow{Priced: true}
		d[k] = r
	}
	r.In += in
	r.Out += out
	r.Cost += cost
	r.Calls++
	r.Priced = r.Priced && priced
	saveUsage()
}

type UsageTotals struct {
	In     int64   `json:"in"`
	Out    int64   `json:"out"`
	Cost   float64 `json:"cost"`
	Calls  int     `json:"calls"`
	Priced bool    `json:"priced"` // false when some calls had no known price
}

type UsageModel struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	UsageTotals
}

type UsageDay struct {
	Day    string  `json:"day"`
	Tokens int64   `json:"tokens"`
	Cost   float64 `json:"cost"`
	Calls  int     `json:"calls"`
}

type UsageReport struct {
	Today  UsageTotals  `json:"today"`
	Month  UsageTotals  `json:"month"`
	Models []UsageModel `json:"models"` // this month, busiest first
	Days   []UsageDay   `json:"days"`   // last 14 days, oldest first
}

func totals(prefix string) (UsageTotals, map[string]*UsageTotals) {
	t := UsageTotals{Priced: true}
	per := map[string]*UsageTotals{}
	any := false
	for day, d := range udata {
		if !strings.HasPrefix(day, prefix) {
			continue
		}
		for k, r := range d {
			any = true
			t.In += r.In
			t.Out += r.Out
			t.Cost += r.Cost
			t.Calls += r.Calls
			t.Priced = t.Priced && r.Priced
			p := per[k]
			if p == nil {
				p = &UsageTotals{Priced: true}
				per[k] = p
			}
			p.In += r.In
			p.Out += r.Out
			p.Cost += r.Cost
			p.Calls += r.Calls
			p.Priced = p.Priced && r.Priced
		}
	}
	t.Priced = t.Priced && any
	return t, per
}

// Report returns today's and this month's totals, per-model rows and a 14-day series.
func Report() UsageReport {
	umu.Lock()
	defer umu.Unlock()
	loadUsage()
	var r UsageReport
	r.Today, _ = totals(today())
	var per map[string]*UsageTotals
	r.Month, per = totals(today()[:7])
	for k, t := range per {
		pm := strings.SplitN(k, "|", 2)
		um := UsageModel{Provider: pm[0], UsageTotals: *t}
		if len(pm) > 1 {
			um.Model = pm[1]
		}
		r.Models = append(r.Models, um)
	}
	sort.Slice(r.Models, func(i, j int) bool { return r.Models[i].Calls > r.Models[j].Calls })
	for i := 13; i >= 0; i-- {
		day := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		t, _ := totals(day)
		r.Days = append(r.Days, UsageDay{Day: day, Tokens: t.In + t.Out, Cost: t.Cost, Calls: t.Calls})
	}
	return r
}

// TodayCost is today's estimated spend in USD.
func TodayCost() float64 {
	umu.Lock()
	defer umu.Unlock()
	loadUsage()
	t, _ := totals(today())
	return t.Cost
}

func ResetUsage() {
	umu.Lock()
	defer umu.Unlock()
	udata = map[string]map[string]*usageRow{}
	saveUsage()
}
