package cloudsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Client talks to the gateway's /v1/sync routes with a Blaze account session.
type Client struct {
	Base  string // gateway root, e.g. https://blaze-gateway.blaze-app.workers.dev (no /v1)
	Token string // Blaze account session (bz_…)
	HTTP  *http.Client
	// UserAgent defaults to "Blaze-Desktop" (Blaze CLI passes "Blaze-CLI").
	UserAgent string
}

// Meta is GET /v1/sync/meta.
type Meta struct {
	SetUp  bool    `json:"set_up"`
	Salt   string  `json:"salt"`
	KDF    string  `json:"kdf"`
	Check  string  `json:"check"`
	Epoch  string  `json:"epoch"`
	Seq    int64   `json:"seq"`
	Bytes  int64   `json:"bytes"`
	Limits *Limits `json:"limits,omitempty"`
}

// Limits is the "limits" block of GET /v1/sync/meta (gateway src/sync.ts SYNC_LIMITS).
type Limits struct {
	AccountBytes int64 `json:"account_bytes"`
	ItemBytes    int   `json:"item_bytes"`
	PushItems    int   `json:"push_items"`
	PullItems    int   `json:"pull_items"`
}

// Remote is one item as the gateway returns it.
type Remote struct {
	Kind      string  `json:"kind"`
	ID        string  `json:"id"`
	Seq       int64   `json:"seq"`
	Rev       int64   `json:"rev"`
	Deleted   bool    `json:"deleted"`
	Blob      *string `json:"blob"`
	UpdatedAt int64   `json:"updated_at"`
}

// Page is GET /v1/sync/pull.
type Page struct {
	Items []Remote `json:"items"`
	Next  int64    `json:"next"`
	More  bool     `json:"more"`
	Epoch string   `json:"epoch"`
}

// Out is one item to push; Blob "" pushes a delete.
type Out struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	BaseRev int64  `json:"base_rev"`
	Blob    string `json:"blob,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
}

// PushResult is POST /v1/sync/push.
type PushResult struct {
	Accepted []struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
		Rev  int64  `json:"rev"`
		Seq  int64  `json:"seq"`
	} `json:"accepted"`
	Conflicts []Remote `json:"conflicts"`
	Rejected  []struct {
		Kind  string `json:"kind"`
		ID    string `json:"id"`
		Error string `json:"error"`
	} `json:"rejected"`
	Seq   int64  `json:"seq"`
	Bytes int64  `json:"bytes"`
	Epoch string `json:"epoch"`
}

// HTTPError is a refusal from the gateway with its error code.
type HTTPError struct {
	Status int
	Code   string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("sync: http %d %s", e.Status, e.Code) }

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	if c.Token == "" {
		return &HTTPError{401, "signed_out"}
	}
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Base, "/")+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	ua := c.UserAgent
	if ua == "" {
		ua = "Blaze-Desktop"
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 45 * time.Second}
	}
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 16<<20))
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
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (c *Client) Meta(ctx context.Context) (Meta, error) {
	var m Meta
	err := c.do(ctx, "GET", "/v1/sync/meta", nil, &m)
	return m, err
}

// SetUp writes the account's salt, kdf and check (first device only; 409 already_set_up otherwise).
func (c *Client) SetUp(ctx context.Context, salt, kdf, check string) (Meta, error) {
	var m Meta
	err := c.do(ctx, "PUT", "/v1/sync/meta", map[string]any{"salt": salt, "kdf": kdf, "check": check}, &m)
	return m, err
}

func (c *Client) Pull(ctx context.Context, since int64, limit int) (Page, error) {
	var p Page
	err := c.do(ctx, "GET", fmt.Sprintf("/v1/sync/pull?since=%d&limit=%d", since, limit), nil, &p)
	return p, err
}

func (c *Client) Push(ctx context.Context, items []Out) (PushResult, error) {
	var r PushResult
	for i := range items {
		if items[i].Blob == "" {
			items[i].Deleted = true
		}
	}
	err := c.do(ctx, "POST", "/v1/sync/push", map[string]any{"items": items}, &r)
	var he *HTTPError
	if errors.As(err, &he) && he.Status == 409 && he.Code == "busy" {
		return r, ErrBusy
	}
	return r, err
}

// Wipe deletes the account's whole cloud copy (DELETE /v1/sync).
func (c *Client) Wipe(ctx context.Context) error { return c.do(ctx, "DELETE", "/v1/sync", nil, nil) }

// Unlock derives the key for an account that is already set up and checks it.
func Unlock(m Meta, passphrase string) ([]byte, error) {
	key, err := DeriveKey(passphrase, m.Salt, IterationsOf(m.KDF))
	if err != nil {
		return nil, err
	}
	if !Checks(key, m.Check) {
		return nil, ErrBadPassphrase
	}
	return key, nil
}

// ErrBusy is 409 busy: another device was writing the account's copy at that moment; retry shortly.
var ErrBusy = errors.New("sync: another device is syncing right now")

// Enable turns sync on for this account with passphrase and returns the derived key (to keep in
// the OS keystore; never the passphrase). The first device sets the account up (salt, kdf,
// check); later devices check the passphrase against the account's check blob. Slow (600k
// PBKDF2 rounds). iterations 0 = Iterations (tests pass a low count).
func Enable(ctx context.Context, c *Client, passphrase string, iterations int) ([]byte, error) {
	pass := strings.TrimSpace(passphrase)
	if len(pass) < 8 {
		return nil, errors.New("use a passphrase of at least 8 characters")
	}
	m, err := c.Meta(ctx)
	if err != nil {
		return nil, err
	}
	if m.SetUp {
		return Unlock(m, pass)
	}
	if iterations <= 0 {
		iterations = Iterations
	}
	salt := NewSalt()
	key, err := DeriveKey(pass, salt, iterations)
	if err != nil {
		return nil, err
	}
	check, err := MakeCheck(key)
	if err != nil {
		return nil, err
	}
	kdf := "pbkdf2-sha256:" + strconv.Itoa(iterations)
	if _, err := c.SetUp(ctx, salt, kdf, check); err != nil {
		return nil, err
	}
	return key, nil
}
