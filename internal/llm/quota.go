package llm

// Plan & quota as far as each provider exposes it to an API key (ported from the
// Android app's Quota.kt): balance endpoints where they exist, and the rate-limit
// headers of a one-token test call.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"blaze-desktop/internal/config"
)

type QuotaRow struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type QuotaReport struct {
	Rows []QuotaRow `json:"rows"`
	Note string     `json:"note,omitempty"`
	Page string     `json:"page,omitempty"` // the provider's own usage page
}

func getJSON(ctx context.Context, url string, h map[string]string) (map[string]any, int) {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	for k, v := range h {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, 0
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m, resp.StatusCode
}

// OpenRouterKey returns OpenRouter's info about a key (tier, usage, limit).
func OpenRouterKey(ctx context.Context, key string) map[string]any {
	m, c := getJSON(ctx, "https://openrouter.ai/api/v1/key", AuthHeaders("openrouter", key))
	if c >= 200 && c < 300 {
		if d, ok := m["data"].(map[string]any); ok {
			return d
		}
	}
	return nil
}

var quotaIDRe = regexp.MustCompile(`quotaId["\s:]+([A-Za-z0-9_-]+)`)

// Quota checks plan, balance and rate limits for one provider key and model.
func Quota(ctx context.Context, p config.Provider, key, model string) QuotaReport {
	r := QuotaReport{Page: p.QuotaPage}
	if key == "" && !config.Keyless(p.ID) {
		r.Note = "Add a key first."
		return r
	}
	add := func(l, v string) { r.Rows = append(r.Rows, QuotaRow{l, v}) }
	switch p.ID {
	case "openrouter":
		if d := OpenRouterKey(ctx, key); d != nil {
			if f, _ := d["is_free_tier"].(bool); f {
				add("Tier", "Free")
			} else {
				add("Tier", "Paid")
			}
			if u, ok := d["usage"].(float64); ok {
				add("Used", fmt.Sprintf("$%.4f", u))
			}
			if l, ok := d["limit"].(float64); ok {
				add("Limit", fmt.Sprintf("$%.2f", l))
			} else {
				add("Limit", "No limit")
			}
			if l, ok := d["limit_remaining"].(float64); ok {
				add("Remaining", fmt.Sprintf("$%.4f", l))
			}
		}
	case "moonshot":
		if m, c := getJSON(ctx, "https://api.moonshot.ai/v1/users/me/balance", AuthHeaders(p.ID, key)); c < 300 {
			if d, ok := m["data"].(map[string]any); ok {
				add("Balance", fmt.Sprint(d["available_balance"]))
				add("Vouchers", fmt.Sprint(d["voucher_balance"]))
			}
		}
	case "deepseek":
		if m, c := getJSON(ctx, "https://api.deepseek.com/user/balance", AuthHeaders(p.ID, key)); c < 300 && m != nil {
			if a, _ := m["is_available"].(bool); a {
				add("Available", "Yes")
			} else {
				add("Available", "No, top up")
			}
			if arr, ok := m["balance_infos"].([]any); ok {
				for _, x := range arr {
					if b, ok := x.(map[string]any); ok {
						add("Balance", fmt.Sprint(b["total_balance"], " ", b["currency"]))
					}
				}
			}
		}
	}
	// Rate limits: a one-token call; the reply's headers carry the limits.
	if model != "" && p.BaseURL != "" {
		body, _ := json.Marshal(map[string]any{"model": model, "max_tokens": 1, "messages": []Message{{Role: "user", Content: "hi"}}})
		req, _ := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(p.BaseURL, "/")+"/chat/completions", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range AuthHeaders(p.ID, key) {
			req.Header.Set(k, v)
		}
		if resp, err := (&http.Client{Timeout: 40 * time.Second}).Do(req); err == nil {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			h := rateHeaders(resp.Header)
			Headers(p.ID+"|"+model, h)
			pick := func(names ...string) string {
				for k, v := range h {
					for _, n := range names {
						if strings.HasSuffix(k, n) {
							return v
						}
					}
				}
				return ""
			}
			if lim := pick("limit-requests", "requests-limit"); lim != "" {
				add("Requests", firstNonEmpty(pick("remaining-requests", "requests-remaining"), "?")+" left of "+lim)
			}
			if lim := pick("limit-tokens", "tokens-limit"); lim != "" {
				add("Tokens", firstNonEmpty(pick("remaining-tokens", "tokens-remaining"), "?")+" left of "+lim)
			}
			if rs := pick("reset-requests", "requests-reset"); rs != "" {
				add("Resets", rs)
			}
			switch {
			case resp.StatusCode == 429:
				s := "Limit reached"
				if m := quotaIDRe.FindStringSubmatch(string(raw)); m != nil {
					s += " (" + m[1] + ")"
				}
				add("Status", s)
			case resp.StatusCode < 300:
				add("Status", "Working · "+model)
			default:
				add("Status", PlainText(config.Name(p.ID), resp.StatusCode, cleanErr(string(raw))))
			}
		}
	}
	switch {
	case len(r.Rows) == 0:
		r.Note = "This provider doesn't report quota to API keys. Its own page has the exact numbers."
	case p.ID == "gemini":
		r.Note = "Google shows exact per-model limits only in AI Studio."
	}
	return r
}
