package cloudai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// Plan asks the gateway which plan the signed-in account is on (basic, standard or pro), from
// GET /v1/billing/me: term.plan, else plan. "" with an error when signed out or unreachable.
func (g *Gateway) Plan(ctx context.Context) (string, error) {
	tok := ""
	if g.Token != nil {
		tok = g.Token()
	}
	if tok == "" {
		return "", errors.New("signed out")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	url := strings.TrimSuffix(strings.TrimRight(g.Base, "/"), "/v1") + "/v1/billing/me"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Blaze-Desktop")
	hc := g.HC
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", errors.New(resp.Status)
	}
	return planFrom(io.LimitReader(resp.Body, 256<<10))
}

func planFrom(r io.Reader) (string, error) {
	var b struct {
		Plan string `json:"plan"`
		Term *struct {
			Plan string `json:"plan"`
		} `json:"term"`
	}
	if err := json.NewDecoder(r).Decode(&b); err != nil {
		return "", err
	}
	p := b.Plan
	if b.Term != nil && b.Term.Plan != "" {
		p = b.Term.Plan
	}
	p = strings.ToLower(strings.TrimSpace(p))
	switch p {
	case "basic", "standard", "pro":
		return p, nil
	case "":
		return "basic", nil
	}
	return p, nil
}
