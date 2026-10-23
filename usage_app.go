package main

// The usage meter (v0.19.9): Blaze Cloud's allowance for today, the same numbers the CLI shows
// (gateway 0.7.0 headers on every reply, GET /v1/usage on demand), plus a daily cap the user sets
// so Blaze never quietly uses more than they want.

import (
	"context"
	"fmt"
	"time"

	"blaze-desktop/internal/cloudai"
)

// UsageView is the meter in the top bar.
type UsageView struct {
	Cloud    bool           `json:"cloud"`     // Blaze Cloud is answering (the meter only applies then)
	SignedIn bool           `json:"signed_in"` // the gateway needs the account
	Quota    *cloudai.Quota `json:"quota"`     // nil until the first reply or check
	Cap      int            `json:"cap"`       // the user's own daily message cap, 0 = none
	Error    string         `json:"error,omitempty"`
}

func (a *App) cloudOn() bool {
	return cloudModels != nil && cloudModels.Choice().Mode == cloudai.ModeCloud
}

// CloudUsage is the meter's state; refresh asks GET /v1/usage (free) instead of the last reply's
// numbers when those are older than a minute.
func (a *App) CloudUsage(refresh bool) UsageView {
	v := UsageView{Cloud: a.cloudOn(), SignedIn: a.blazeToken() != "", Cap: a.prefs.CloudDailyCap}
	if cloudGateway == nil || !v.SignedIn {
		return v
	}
	v.Quota = cloudGateway.LastQuota()
	if refresh && (v.Quota == nil || time.Since(v.Quota.At) > time.Minute) {
		if q, err := cloudGateway.Usage(context.Background()); err == nil {
			v.Quota = q
		} else if v.Quota == nil {
			v.Error = "Couldn't reach Blaze Cloud for your usage."
		}
	}
	return v
}

// SetCloudDailyCap sets the most Blaze Cloud messages Blaze may send in a day (0 = no cap of your own).
func (a *App) SetCloudDailyCap(n int) UsageView {
	if n < 0 {
		n = 0
	}
	a.prefs.CloudDailyCap = n
	a.savePrefs()
	return a.CloudUsage(false)
}

// capReached is the message to show when the user's own cap stops a turn ("" = go ahead).
func (a *App) capReached() string {
	c := a.prefs.CloudDailyCap
	if c <= 0 || !a.cloudOn() || cloudGateway == nil {
		return ""
	}
	q := cloudGateway.LastQuota()
	if q == nil {
		if fresh, err := cloudGateway.Usage(context.Background()); err == nil {
			q = fresh
		}
	}
	if q == nil || q.RequestsUsed < c {
		return ""
	}
	return fmt.Sprintf("You've used %d Blaze Cloud messages today, your own daily cap. Raise it in the usage meter (top bar), or it resets at midnight UTC.", q.RequestsUsed)
}
