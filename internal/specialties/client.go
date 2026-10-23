// Package specialties is the desktop side of the account's specialties (gateway 0.8.45,
// src/specialties.ts): the playbooks of steps plus the user's own rules that Blaze keeps per
// account and shares across phone, desktop and CLI. The user can list them, open one, and delete
// a specialty, a single rule, or the general rules:
//
//	GET    /v1/specialties                     list (+ general_rules)
//	GET    /v1/specialties/:id                 one specialty ("general" = general rules)
//	DELETE /v1/specialties/:id                 delete it (and its repeat counter and rules)
//	DELETE /v1/specialties/:id/rules/:n        delete rule n (0-based)
//
// Everything is per Blaze account session; nothing is cached on this computer.
package specialties

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// General is the id of the account's general rules (rules that fit no kind of task).
const General = "general"

// Client calls the gateway with a Blaze account session.
type Client struct {
	Base      string // gateway root without /v1
	Token     string // Blaze account session
	HTTP      *http.Client
	UserAgent string
}

// Summary is one row of GET /v1/specialties.
type Summary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Version   int    `json:"version"`
	Uses      int    `json:"uses"`
	Steps     int    `json:"steps"`
	Rules     int    `json:"rules"`
	UpdatedAt string `json:"updated_at"`
}

// List is GET /v1/specialties.
type List struct {
	Data         []Summary `json:"data"`
	GeneralRules []string  `json:"general_rules"`
}

// Specialty is GET /v1/specialties/:id (general rules come back with only id, name and rules).
type Specialty struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Kind      string   `json:"kind,omitempty"`
	Trigger   string   `json:"trigger,omitempty"`
	Steps     []string `json:"steps"`
	Rules     []string `json:"rules"`
	Version   int      `json:"version,omitempty"`
	Uses      int      `json:"uses,omitempty"`
	CreatedAt string   `json:"created_at,omitempty"`
	UpdatedAt string   `json:"updated_at,omitempty"`
}

// HTTPError is a non-2xx answer.
type HTTPError struct {
	Status int
	Code   string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("specialties: http %d %s", e.Status, e.Code) }

// ErrSignedOut: there is no Blaze account session on this computer.
var ErrSignedOut = &HTTPError{401, "signed_out"}

// ErrBadID: the id is neither "general" nor a specialty id, so it is never sent.
var ErrBadID = errors.New("specialties: not a specialty id")

var idRe = regexp.MustCompile(`^sp_[0-9a-f]{20}$`)

// ValidID reports whether id can be sent to the gateway.
func ValidID(id string) bool { return id == General || idRe.MatchString(id) }

func (c *Client) do(ctx context.Context, method, path string, out any) error {
	if c.Token == "" {
		return ErrSignedOut
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Base, "/")+path, nil)
	if err != nil {
		return err
	}
	ua := c.UserAgent
	if ua == "" {
		ua = "Blaze-Desktop"
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Authorization", "Bearer "+c.Token)
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode >= 400 {
		var e struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		code := e.Error.Code
		if code == "" {
			code = fmt.Sprintf("http_%d", res.StatusCode)
		}
		return &HTTPError{res.StatusCode, code}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(bytes.NewReader(raw)).Decode(out)
}

// List returns the account's specialties (newest first) and its general rules.
func (c *Client) List(ctx context.Context) (List, error) {
	var l List
	err := c.do(ctx, "GET", "/v1/specialties", &l)
	if l.Data == nil {
		l.Data = []Summary{}
	}
	if l.GeneralRules == nil {
		l.GeneralRules = []string{}
	}
	return l, err
}

// Get returns one specialty, or the general rules for id "general".
func (c *Client) Get(ctx context.Context, id string) (Specialty, error) {
	var s Specialty
	if !ValidID(id) {
		return s, ErrBadID
	}
	err := c.do(ctx, "GET", "/v1/specialties/"+url.PathEscape(id), &s)
	if s.Steps == nil {
		s.Steps = []string{}
	}
	if s.Rules == nil {
		s.Rules = []string{}
	}
	return s, err
}

// Delete removes a specialty (or, for "general", all general rules).
func (c *Client) Delete(ctx context.Context, id string) error {
	if !ValidID(id) {
		return ErrBadID
	}
	return c.do(ctx, "DELETE", "/v1/specialties/"+url.PathEscape(id), nil)
}

// DeleteRule removes rule n (0-based, in the order Get returned them).
func (c *Client) DeleteRule(ctx context.Context, id string, n int) error {
	if !ValidID(id) {
		return ErrBadID
	}
	if n < 0 {
		return &HTTPError{404, "not_found"}
	}
	return c.do(ctx, "DELETE", fmt.Sprintf("/v1/specialties/%s/rules/%d", url.PathEscape(id), n), nil)
}
