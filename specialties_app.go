package main

// Specialties (gateway 0.8.45): the account's playbooks of steps plus the user's own rules, kept on
// the Blaze account and shared by every device. Settings › Account › Specialties lists them and
// lets the user open one, delete it, delete a single rule, or clear the general rules
// (frontend/dist/specialties.js). Nothing is cached here; every call goes to the gateway.

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"blaze-desktop/internal/specialties"
)

// specialtiesAPIOverride lets tests point the client at a fake gateway.
var specialtiesAPIOverride func(a *App) *specialties.Client

// SpecialtiesView is what Settings shows.
type SpecialtiesView struct {
	SignedIn     bool                   `json:"signedIn"`
	Items        []specialties.Summary  `json:"items"`
	GeneralRules []string               `json:"generalRules"`
	Open         *specialties.Specialty `json:"open,omitempty"`
	Error        string                 `json:"error,omitempty"`
}

func (a *App) specialtiesClient() *specialties.Client {
	if specialtiesAPIOverride != nil {
		return specialtiesAPIOverride(a)
	}
	return &specialties.Client{Base: syncBase(), Token: a.blazeToken(), UserAgent: "Blaze-Desktop/" + strings.TrimSpace(version)}
}

func specialtiesErrText(err error) string {
	if err == nil {
		return ""
	}
	var he *specialties.HTTPError
	var ne net.Error
	switch {
	case errors.Is(err, specialties.ErrBadID):
		return "That specialty isn't on your account."
	case errors.As(err, &he) && (he.Status == 401 || he.Status == 403):
		return "Sign in to your Blaze account to see your specialties."
	case errors.As(err, &he) && he.Status == 404:
		return "That's already gone. The list is up to date now."
	case errors.As(err, &he) && he.Status == 429:
		return "Too many requests right now. Try again in a minute."
	case errors.As(err, &ne), errors.Is(err, context.DeadlineExceeded):
		return "Couldn't reach Blaze. Check your connection and try again."
	}
	return "Couldn't load your specialties right now. Try again in a moment."
}

func (a *App) specialtiesList(ctx context.Context) SpecialtiesView {
	v := SpecialtiesView{Items: []specialties.Summary{}, GeneralRules: []string{}}
	if a.blazeToken() == "" && specialtiesAPIOverride == nil {
		v.Error = "Sign in to your Blaze account to see your specialties."
		return v
	}
	v.SignedIn = true
	l, err := a.specialtiesClient().List(ctx)
	if err != nil {
		v.Error = specialtiesErrText(err)
		var he *specialties.HTTPError
		if errors.As(err, &he) && (he.Status == 401 || he.Status == 403) {
			v.SignedIn = false
		}
		return v
	}
	v.Items, v.GeneralRules = l.Data, l.GeneralRules
	return v
}

func specialtiesCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 20*time.Second)
}

// SpecialtiesList returns the account's specialties and general rules.
func (a *App) SpecialtiesList() SpecialtiesView {
	ctx, cancel := specialtiesCtx()
	defer cancel()
	return a.specialtiesList(ctx)
}

// SpecialtyGet returns the list with one specialty (or "general") opened.
func (a *App) SpecialtyGet(id string) SpecialtiesView {
	ctx, cancel := specialtiesCtx()
	defer cancel()
	v := a.specialtiesList(ctx)
	if v.Error != "" {
		return v
	}
	s, err := a.specialtiesClient().Get(ctx, id)
	if err != nil {
		v.Error = specialtiesErrText(err)
		return v
	}
	v.Open = &s
	return v
}

// SpecialtyDelete deletes a specialty (or, for "general", every general rule) and returns the new list.
func (a *App) SpecialtyDelete(id string) SpecialtiesView {
	ctx, cancel := specialtiesCtx()
	defer cancel()
	err := a.specialtiesClient().Delete(ctx, id)
	v := a.specialtiesList(ctx)
	var he *specialties.HTTPError
	if err != nil && !(errors.As(err, &he) && he.Status == 404) && v.Error == "" {
		v.Error = specialtiesErrText(err)
	}
	return v
}

// SpecialtyRuleDelete deletes rule n of a specialty (or of the general rules) and returns the list
// with that specialty still open, so the user sees the rules that are left.
func (a *App) SpecialtyRuleDelete(id string, n int) SpecialtiesView {
	ctx, cancel := specialtiesCtx()
	defer cancel()
	err := a.specialtiesClient().DeleteRule(ctx, id, n)
	v := a.specialtiesList(ctx)
	if v.Error != "" {
		return v
	}
	if s, gerr := a.specialtiesClient().Get(ctx, id); gerr == nil {
		v.Open = &s
	}
	if err != nil {
		v.Error = specialtiesErrText(err)
	}
	return v
}
