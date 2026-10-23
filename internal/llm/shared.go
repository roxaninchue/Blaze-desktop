package llm

// Shared state for BlazeRouter (internal/router): the same health, cool-downs, key
// rests and rate-limit budget Blaze's own chats use, so a model that is resting for
// Blaze is resting for every tool that goes through the local gateway too, and the
// other way round.

import (
	"errors"
	"time"

	"blaze-desktop/internal/config"
)

// HealthOf is a copy of a provider|model's health (zero when never tried).
func HealthOf(provider, model string) Health {
	hmu.Lock()
	defer hmu.Unlock()
	if x := health[provider+"|"+model]; x != nil {
		return *x
	}
	return Health{}
}

// CoolingFor is how long provider|model still rests after failures (0 = ready).
func CoolingFor(provider, model string) time.Duration {
	hmu.Lock()
	defer hmu.Unlock()
	if x := health[provider+"|"+model]; x != nil {
		if d := time.Until(x.CoolUntil); d > 0 {
			return d
		}
	}
	return 0
}

// ReportOK records a successful call that took ms milliseconds.
func ReportOK(provider, model string, ms int64) {
	hmu.Lock()
	defer hmu.Unlock()
	x := h(provider + "|" + model)
	x.Fails, x.OK, x.CoolUntil, x.LastErr = 0, x.OK+1, time.Time{}, ""
	if x.AvgMs == 0 {
		x.AvgMs = ms
	} else {
		x.AvgMs = (x.AvgMs*3 + ms) / 4
	}
}

// ReportFail records a failed call and cools the model down the way Blaze's own
// router does: rate limits for as long as the provider says, auth and missing models
// for 30 minutes, outages for 20 seconds. Returns the cool-down it chose.
func ReportFail(provider, model string, err error) time.Duration {
	key := provider + "|" + model
	var he *HTTPError
	code := 0
	if errors.As(err, &he) {
		code = he.Code
	}
	cool := 10 * time.Second
	switch {
	case IsQuota(err):
		ra := 0
		if he != nil {
			ra = he.RetryAfter
		}
		cool = Limited(key, err.Error(), ra)
		if cool <= 0 {
			cool = time.Minute
		}
	case NoToolSupport(err):
		markNoTools(provider, model)
		cool = 10 * time.Minute
	case code == 401 || code == 403 || code == 404:
		cool = 30 * time.Minute
	case code >= 500 || code == 0:
		cool = 20 * time.Second
	}
	hmu.Lock()
	x := h(key)
	x.Fails++
	x.CoolUntil = time.Now().Add(cool)
	x.LastErr = trunc(err.Error(), 160)
	hmu.Unlock()
	return cool
}

// KeyResting is true while a key rests for provider|model after running out of quota.
func KeyResting(provider, model, key string) bool { return keyResting(provider, model, key) }

// RestKey rests one key for provider|model (the next key carries on).
func RestKey(provider, model, key string, d time.Duration) { restKey(provider, model, key, d) }

// IsDaily is true for a per-day quota message (the key should rest at least an hour).
func IsDaily(msg string) bool { return dailyRe.MatchString(msg) }

// Usable is true when a provider can take a call now: it has an address, and a key
// unless it is local (Ollama) or the user's own service.
func Usable(cfg *config.Config, provider string) bool { return usable(cfg, provider) }

// Price is USD per million tokens (input, output) when Blaze knows the model's price.
func Price(provider, model string) (float64, float64, bool) { return price(provider, model) }
