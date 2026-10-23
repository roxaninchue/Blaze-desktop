package llm

// Token meter for the free chain (ported from the Android app's Budget.kt). Free
// tiers cap tokens and requests per minute and per day, so instead of sending and
// getting a 429 we remember what each provider+model told us (rate-limit headers,
// 429 retry hints) and what we sent in the last minute, and skip a model that has
// no room for this request before calling it.

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type sent struct {
	at     time.Time
	tokens int
}

type meter struct {
	sent         []sent
	tpm, rpm     int
	remTokens    int
	remTokensAt  time.Time
	tokensReset  time.Duration
	remReq       int
	remReqAt     time.Time
	reqReset     time.Duration
	blockedUntil time.Time
	why          string
}

var (
	bmu    sync.Mutex
	meters = map[string]*meter{}
)

func mt(key string) *meter {
	m := meters[key]
	if m == nil {
		m = &meter{remTokens: -1, remReq: -1}
		meters[key] = m
	}
	return m
}

// Estimate is a rough token count: about 4 characters per token.
func Estimate(chars int) int { return chars/4 + 50 }

func (m *meter) trim() {
	cut := time.Now().Add(-time.Minute)
	i := 0
	for i < len(m.sent) && m.sent[i].at.Before(cut) {
		i++
	}
	m.sent = m.sent[i:]
}

// Spend remembers a request that went out.
func Spend(key string, tokens int) {
	bmu.Lock()
	defer bmu.Unlock()
	m := mt(key)
	m.sent = append(m.sent, sent{time.Now(), tokens})
	m.trim()
}

// Headers reads x-ratelimit-* headers (Groq, OpenRouter, Cerebras, Mistral, OpenAI style).
func Headers(key string, h map[string]string) {
	if len(h) == 0 {
		return
	}
	bmu.Lock()
	defer bmu.Unlock()
	m := mt(key)
	now := time.Now()
	num := func(names ...string) (int, bool) {
		for _, n := range names {
			if v, ok := h[n]; ok {
				if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					return int(f), true
				}
			}
		}
		return 0, false
	}
	dur := func(names ...string) (time.Duration, bool) {
		for _, n := range names {
			if v, ok := h[n]; ok {
				if d, ok := ParseDuration(v); ok {
					return d, true
				}
			}
		}
		return 0, false
	}
	if v, ok := num("x-ratelimit-limit-tokens", "x-ratelimit-limit-tokens-minute", "x-ratelimit-tokens-limit"); ok {
		m.tpm = v
	}
	if v, ok := num("x-ratelimit-limit-requests", "x-ratelimit-limit-requests-minute"); ok {
		m.rpm = v
	}
	if v, ok := num("x-ratelimit-remaining-tokens", "x-ratelimit-remaining-tokens-minute", "x-ratelimit-tokens-remaining"); ok {
		m.remTokens, m.remTokensAt = v, now
	}
	if v, ok := num("x-ratelimit-remaining-requests", "x-ratelimit-remaining-requests-minute", "x-ratelimit-remaining"); ok {
		m.remReq, m.remReqAt = v, now
	}
	if d, ok := dur("x-ratelimit-reset-tokens", "x-ratelimit-reset-tokens-minute"); ok {
		m.tokensReset = d
	}
	if d, ok := dur("x-ratelimit-reset-requests", "x-ratelimit-reset-requests-minute", "x-ratelimit-reset"); ok {
		m.reqReset = d
	}
}

var dailyRe = regexp.MustCompile(`(?i)(per ?day|daily|RPD|TPD|quota exceeded|exceeded your current quota|free_tier_requests)`)

// Limited records a 429 or quota error: block until the provider's own hint, or a sensible default.
func Limited(key, message string, retryAfterSec int) time.Duration {
	var d time.Duration
	if retryAfterSec > 0 {
		d = time.Duration(retryAfterSec) * time.Second
	} else if h, ok := HintFrom(message); ok {
		d = h
	}
	daily := d == 0 && dailyRe.MatchString(message)
	if d == 0 {
		if daily {
			d = 30 * time.Minute
		} else {
			d = time.Minute
		}
	}
	if d < 3*time.Second {
		d = 3 * time.Second
	}
	if d > 6*time.Hour {
		d = 6 * time.Hour
	}
	bmu.Lock()
	defer bmu.Unlock()
	m := mt(key)
	m.blockedUntil = time.Now().Add(d)
	m.why = "rate limit"
	if daily {
		m.why = "daily quota"
	}
	return d
}

var (
	hintDelay = regexp.MustCompile(`(?i)retry ?delay"?\s*:\s*"?([0-9.]+)s`)
	hintAgain = regexp.MustCompile(`(?i)(?:try again|retry|reset|available) (?:in|after) ((?:[0-9.]+\s*(?:ms|h|m|s|seconds?|minutes?|hours?)\s*)+)`)
	durPart   = regexp.MustCompile(`([0-9.]+)\s*(ms|milliseconds?|hours?|h|minutes?|min|m|seconds?|sec|s)`)
)

// HintFrom finds "try again in 12.5s", "retryDelay": "40s", "Please retry in 1m3s".
func HintFrom(msg string) (time.Duration, bool) {
	if m := hintDelay.FindStringSubmatch(msg); m != nil {
		f, _ := strconv.ParseFloat(m[1], 64)
		return time.Duration(f * float64(time.Second)), true
	}
	if m := hintAgain.FindStringSubmatch(msg); m != nil {
		return ParseDuration(m[1])
	}
	return 0, false
}

// ParseDuration reads "1m2.5s", "7.66s", "120ms", "40 seconds", "2 minutes" or plain seconds.
func ParseDuration(s string) (time.Duration, bool) {
	t := strings.ToLower(strings.TrimSpace(s))
	if f, err := strconv.ParseFloat(t, 64); err == nil {
		return time.Duration(f * float64(time.Second)), true
	}
	var total float64
	any := false
	for _, g := range durPart.FindAllStringSubmatch(t, -1) {
		v, err := strconv.ParseFloat(g[1], 64)
		if err != nil {
			continue
		}
		any = true
		switch g[2] {
		case "ms", "millisecond", "milliseconds":
			total += v
		case "h", "hour", "hours":
			total += v * 3600_000
		case "m", "min", "minute", "minutes":
			total += v * 60_000
		default:
			total += v * 1000
		}
	}
	return time.Duration(total) * time.Millisecond, any
}

// Wait is 0 when key can take a request of tokens now; otherwise how long until it can, and why.
func Wait(key string, tokens int) (time.Duration, string) {
	bmu.Lock()
	defer bmu.Unlock()
	m := meters[key]
	if m == nil {
		return 0, ""
	}
	now := time.Now()
	if m.blockedUntil.After(now) {
		return m.blockedUntil.Sub(now), m.why
	}
	m.trim()
	if m.remTokens >= 0 && m.tokensReset > 0 && now.Before(m.remTokensAt.Add(m.tokensReset)) {
		used := 0
		for _, s := range m.sent {
			if s.at.After(m.remTokensAt) {
				used += s.tokens
			}
		}
		if m.remTokens-used < tokens {
			return m.remTokensAt.Add(m.tokensReset).Sub(now), "token limit"
		}
	}
	if m.remReq == 0 && m.reqReset > 0 && now.Before(m.remReqAt.Add(m.reqReset)) {
		return m.remReqAt.Add(m.reqReset).Sub(now), "request limit"
	}
	if m.tpm > 0 && tokens > m.tpm {
		return time.Minute, "request bigger than its per-minute limit"
	}
	sum := 0
	for _, s := range m.sent {
		sum += s.tokens
	}
	if m.tpm > 0 && len(m.sent) > 0 && sum+tokens > m.tpm {
		return max(m.sent[0].at.Add(time.Minute).Sub(now), time.Second), "token limit"
	}
	if m.rpm > 0 && len(m.sent) > 0 && len(m.sent) >= m.rpm {
		return max(m.sent[0].at.Add(time.Minute).Sub(now), time.Second), "request limit"
	}
	return 0, ""
}

// ClearBudget lifts a block (after the user adds a key, for example).
func ClearBudget(prefix string) {
	bmu.Lock()
	defer bmu.Unlock()
	for k, m := range meters {
		if strings.HasPrefix(k, prefix) {
			m.blockedUntil = time.Time{}
		}
	}
}
