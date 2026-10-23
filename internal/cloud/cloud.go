// Package cloud talks to blaze-auth, Blaze's small Cloudflare Worker (cloud/ in this
// repo). It does two things:
//
//   - one-click connectors: the Worker holds each provider's client secret, so the
//     user just presses Connect. The browser signs in, the Worker swaps the code for
//     tokens, and hands them back to a 127.0.0.1 loopback here through a one-time
//     code bound to our PKCE verifier.
//   - Blaze accounts: email + password, or Sign in with Google / GitHub.
//
// No secret lives in the desktop binary. Tokens end up in the OS keychain.
package cloud

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultBase is the deployed Worker.
const DefaultBase = "https://blaze-auth.blaze-app.workers.dev"

// BaseURL is BLAZE_CLOUD_URL, else "cloud_url" in <dir>/cloud.json, else DefaultBase.
func BaseURL(dir string) string {
	if v := strings.TrimSpace(os.Getenv("BLAZE_CLOUD_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	var f struct {
		CloudURL string `json:"cloud_url"`
	}
	if b, err := os.ReadFile(filepath.Join(dir, "cloud.json")); err == nil && json.Unmarshal(b, &f) == nil {
		if u, err := url.Parse(strings.TrimSpace(f.CloudURL)); err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
			return strings.TrimRight(u.String(), "/")
		}
	}
	return DefaultBase
}

// Client is one Worker base URL.
type Client struct {
	Base string
	HC   *http.Client
	// Timeout is how long the user gets to finish in the browser.
	Timeout time.Duration
}

func New(base string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), HC: &http.Client{Timeout: 30 * time.Second}, Timeout: 10 * time.Minute}
}

// ---------------- errors ----------------

// ErrNotConfigured: the Worker has no client secret for that provider yet.
var ErrNotConfigured = errors.New("not_configured")

// ErrCancelled: the sign-in was cancelled or timed out before the browser came back.
var ErrCancelled = errors.New("cancelled")

// ErrBadAnswer: the service answered 2xx with a body that isn't the JSON Blaze expects
// (a captive portal, a proxy page, a half-deployed Worker).
var ErrBadAnswer = errors.New("bad_answer")

// APIError is a refusal from the Worker ({"error": "..."}).
type APIError struct {
	Status int
	Code   string
	Retry  int
	// mfa_required (blaze-auth 0.9+): the sign-in waits for a second factor.
	MFAToken  string
	Methods   []string // "app", "email", "recovery"
	EmailHint string   // l**e@example.com
	TriesLeft int      // invalid_code
}

func (e *APIError) Error() string { return e.Code }

// Unreachable wraps a network failure (offline, DNS, TLS).
type Unreachable struct{ Err error }

func (e *Unreachable) Error() string {
	return "couldn't reach Blaze's sign-in service: " + e.Err.Error()
}
func (e *Unreachable) Unwrap() error { return e.Err }

// Message is a plain sentence for an error.
func Message(err error) string {
	var ae *APIError
	var un *Unreachable
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrNotConfigured):
		return "One-click sign-in isn't switched on for this yet. Use your own key instead."
	case errors.Is(err, ErrCancelled):
		return "Sign-in was cancelled."
	case errors.As(err, &un):
		return "Couldn't reach Blaze's sign-in service. Check your internet connection."
	case errors.Is(err, ErrBadAnswer):
		return "Blaze's sign-in service sent an answer Blaze couldn't read. Try again in a moment."
	case errors.As(err, &ae):
		switch ae.Code {
		case "too_large":
			return "That picture is too big. Pick a smaller one."
		case "unsupported_type":
			return "Use a PNG, JPEG or WebP picture."
		case "invalid_credentials":
			return "That email and password don't match."
		case "email_taken":
			return "There's already a Blaze account with that email. Sign in instead."
		case "invalid_email":
			return "That doesn't look like an email address."
		case "invalid_password":
			return "Use a password of at least 8 characters."
		case "rate_limited":
			return "Too many tries. Wait a few minutes and try again."
		case "unauthorized":
			return "You're signed out. Sign in again."
		case "access_denied":
			return "You declined access in the browser. Nothing changed."
		case "mfa_required":
			return "Enter your two-factor code to finish signing in."
		case "invalid_code":
			if ae.TriesLeft > 0 {
				return fmt.Sprintf("That code isn't right. %d tries left.", ae.TriesLeft)
			}
			return "That code isn't right."
		case "invalid_mfa_token":
			return "This sign-in expired. Start again with your email and password."
		case "too_many_codes":
			return "That's the most codes for one sign-in. Start again in a few minutes."
		case "mail_failed":
			return "Blaze couldn't send the email. Try again in a moment."
		}
		if ae.Status >= 500 && strings.HasPrefix(ae.Code, "http_") {
			return "Blaze's sign-in service is having trouble right now. Try again in a moment."
		}
		return "Sign-in failed (" + ae.Code + ")."
	}
	return err.Error()
}

// ---------------- types ----------------

type Providers struct {
	Login      map[string]bool `json:"login"`
	Connectors map[string]bool `json:"connectors"`
}

type Tokens struct {
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token,omitempty"`
	TokenType    string  `json:"token_type,omitempty"`
	Scope        string  `json:"scope,omitempty"`
	ExpiresIn    float64 `json:"expires_in,omitempty"`
	Account      string  `json:"account,omitempty"`
}

type User struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	Name          string `json:"name"`
	AvatarURL     string `json:"avatar_url"`
	EmailVerified bool   `json:"email_verified"`
	HasPassword   bool   `json:"has_password"`
	TwoFactor     bool   `json:"two_factor"`
	// TwoFactorMethods: an authenticator app and/or emailed codes (blaze-auth 0.9.1).
	TwoFactorMethods struct {
		App   bool `json:"app"`
		Email bool `json:"email"`
	} `json:"two_factor_methods"`
	Providers []string `json:"providers"`
	CreatedAt int64    `json:"created_at"`
}

// Redeemed is what /oauth/redeem returns: provider tokens, or a Blaze session.
type Redeemed struct {
	Kind     string   `json:"kind"` // tokens | session
	Provider string   `json:"provider"`
	Tokens   *Tokens  `json:"tokens,omitempty"`
	Scopes   []string `json:"scopes,omitempty"`
	Token    string   `json:"token,omitempty"`
	User     *User    `json:"user,omitempty"`
}

// Session is a signed-in Blaze account.
type Session struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

// ---------------- HTTP ----------------

func (c *Client) do(ctx context.Context, method, path, bearer string, body, out any) error {
	var rd io.Reader
	ctype := "application/json"
	if rb, ok := body.(rawBody); ok {
		rd, ctype = bytes.NewReader(rb.data), rb.ctype
	} else if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Blaze-Desktop")
	if body != nil {
		req.Header.Set("Content-Type", ctype)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.HC.Do(req)
	if err != nil {
		// Only a real cancel (Cancel pressed, window closed) is "cancelled"; a context
		// deadline is a slow or dead network and reads as unreachable.
		if errors.Is(ctx.Err(), context.Canceled) {
			return ErrCancelled
		}
		return &Unreachable{err}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		var e struct {
			Error      string   `json:"error"`
			RetryAfter int      `json:"retry_after"`
			MFAToken   string   `json:"mfa_token"`
			Methods    []string `json:"methods"`
			EmailHint  string   `json:"email_hint"`
			TriesLeft  int      `json:"tries_left"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Error == "" {
			e.Error = fmt.Sprintf("http_%d", resp.StatusCode)
		}
		if e.Error == "not_configured" {
			return ErrNotConfigured
		}
		return &APIError{Status: resp.StatusCode, Code: e.Error, Retry: e.RetryAfter, MFAToken: e.MFAToken, Methods: e.Methods, EmailHint: e.EmailHint, TriesLeft: e.TriesLeft}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%w: unexpected answer from the sign-in service: %v", ErrBadAnswer, err)
		}
	}
	return nil
}

// Providers says which sign-ins and connectors the Worker has secrets for.
func (c *Client) Providers(ctx context.Context) (*Providers, error) {
	var p Providers
	if err := c.do(ctx, "GET", "/v1/providers", "", nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (c *Client) SignUp(ctx context.Context, name, email, password string) (*Session, error) {
	var s Session
	err := c.do(ctx, "POST", "/v1/auth/signup", "", map[string]string{"name": name, "email": email, "password": password, "client": "Blaze Desktop"}, &s)
	return &s, err
}

func (c *Client) SignIn(ctx context.Context, email, password string) (*Session, error) {
	var s Session
	err := c.do(ctx, "POST", "/v1/auth/login", "", map[string]string{"email": email, "password": password, "client": "Blaze Desktop"}, &s)
	return &s, err
}

// ---------------- two-factor (blaze-auth 0.9.0 app codes, 0.9.1 emailed codes) ----------------

// MFAVerify finishes a sign-in that answered mfa_required, with an app, emailed or recovery code.
func (c *Client) MFAVerify(ctx context.Context, mfaToken, code string) (*Session, error) {
	var s Session
	err := c.do(ctx, "POST", "/v1/auth/2fa/verify", "", map[string]string{"mfa_token": mfaToken, "code": code}, &s)
	return &s, err
}

// MFAEmail emails a 6-digit sign-in code (valid 10 minutes, at most 3 per sign-in).
func (c *Client) MFAEmail(ctx context.Context, mfaToken string) (sentTo string, sendsLeft int, err error) {
	var r struct {
		SentTo    string `json:"sent_to"`
		SendsLeft int    `json:"sends_left"`
	}
	err = c.do(ctx, "POST", "/v1/auth/2fa/email", "", map[string]string{"mfa_token": mfaToken}, &r)
	return r.SentTo, r.SendsLeft, err
}

// EmailCodesSend emails a code to switch emailed sign-in codes on or off (purpose "enable" | "disable").
func (c *Client) EmailCodesSend(ctx context.Context, token, purpose string) (string, error) {
	var r struct {
		SentTo string `json:"sent_to"`
	}
	err := c.do(ctx, "POST", "/v1/auth/2fa/email/send", token, map[string]string{"purpose": purpose}, &r)
	return r.SentTo, err
}

// EmailCodesSet switches emailed sign-in codes on or off with the code from the email
// (turning them off also takes an authenticator app code).
func (c *Client) EmailCodesSet(ctx context.Context, token string, on bool, code string) (*User, error) {
	path := "/v1/auth/2fa/email/disable"
	if on {
		path = "/v1/auth/2fa/email/enable"
	}
	var r struct {
		User User `json:"user"`
	}
	if err := c.do(ctx, "POST", path, token, map[string]string{"code": code}, &r); err != nil {
		return nil, err
	}
	return &r.User, nil
}

func (c *Client) Me(ctx context.Context, token string) (*User, error) {
	var r struct {
		User User `json:"user"`
	}
	if err := c.do(ctx, "GET", "/v1/me", token, nil, &r); err != nil {
		return nil, err
	}
	return &r.User, nil
}

// rawBody is a request body sent as-is (an image), not as JSON.
type rawBody struct {
	ctype string
	data  []byte
}

// MaxAvatar is the largest profile picture blaze-auth accepts (0.9.3).
const MaxAvatar = 256 << 10

// AvatarUpload sets the account's profile picture (image/webp, png or jpeg, at most 256 KB).
func (c *Client) AvatarUpload(ctx context.Context, token, ctype string, data []byte) (*User, error) {
	switch ctype {
	case "image/webp", "image/png", "image/jpeg":
	default:
		return nil, &APIError{Status: 415, Code: "unsupported_type"}
	}
	if len(data) == 0 || len(data) > MaxAvatar {
		return nil, &APIError{Status: 413, Code: "too_large"}
	}
	var r struct {
		User User `json:"user"`
	}
	if err := c.do(ctx, "POST", "/v1/me/avatar", token, rawBody{ctype, data}, &r); err != nil {
		return nil, err
	}
	return &r.User, nil
}

// AvatarDelete goes back to the letter avatar.
func (c *Client) AvatarDelete(ctx context.Context, token string) (*User, error) {
	var r struct {
		User User `json:"user"`
	}
	if err := c.do(ctx, "POST", "/v1/me/avatar/delete", token, map[string]string{}, &r); err != nil {
		return nil, err
	}
	return &r.User, nil
}

func (c *Client) Logout(ctx context.Context, token string) error {
	return c.do(ctx, "POST", "/v1/logout", token, map[string]string{}, nil)
}

func (c *Client) DeleteAccount(ctx context.Context, token string) error {
	return c.do(ctx, "POST", "/v1/account/delete", token, map[string]string{"confirm": "delete"}, nil)
}

// Refresh swaps a provider refresh token for new tokens (the Worker adds the secret).
func (c *Client) Refresh(ctx context.Context, provider, refreshToken string) (*Tokens, error) {
	var r Redeemed
	if err := c.do(ctx, "POST", "/oauth/"+url.PathEscape(provider)+"/refresh", "", map[string]string{"refresh_token": refreshToken}, &r); err != nil {
		return nil, err
	}
	if r.Tokens == nil || r.Tokens.AccessToken == "" {
		return nil, &APIError{Status: 502, Code: "no_token"}
	}
	return r.Tokens, nil
}

// ---------------- browser sign-in through the loopback ----------------

func randB64(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// PKCE makes a verifier and its S256 challenge.
func PKCE() (verifier, challenge string) {
	verifier = randB64(32)
	h := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(h[:])
}

// Connect runs the one-click flow for a connector and returns its tokens.
func (c *Client) Connect(ctx context.Context, provider string, scopes []string, open func(string)) (*Redeemed, error) {
	return c.authorize(ctx, "/oauth/"+url.PathEscape(provider)+"/start", scopes, open)
}

// SignInWith runs "Sign in with Google/GitHub" and returns a Blaze session.
func (c *Client) SignInWith(ctx context.Context, provider string, open func(string)) (*Session, error) {
	r, err := c.authorize(ctx, "/v1/auth/"+url.PathEscape(provider)+"/start", nil, open)
	if err != nil {
		return nil, err
	}
	if r.Kind != "session" || r.Token == "" || r.User == nil {
		return nil, &APIError{Status: 502, Code: "no_session"}
	}
	return &Session{Token: r.Token, User: *r.User}, nil
}

type cbResult struct {
	code, errCode string
}

// authorize: listen on 127.0.0.1:<random>, open the Worker's start URL, wait for the
// redirect back with our state, then redeem the one-time code with the verifier.
func (c *Client) authorize(ctx context.Context, startPath string, scopes []string, open func(string)) (*Redeemed, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("Blaze couldn't open a local port for the sign-in: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	state := randB64(24)
	verifier, challenge := PKCE()
	redirect := fmt.Sprintf("http://127.0.0.1:%d/cb", port)

	got := make(chan cbResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/cb", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state {
			writePage(w, false, "Sign-in didn't finish", "That link doesn't match the sign-in Blaze started. Start again from Blaze.")
			return
		}
		res := cbResult{code: q.Get("code"), errCode: q.Get("error")}
		if res.errCode == "" && res.code == "" {
			res.errCode = "missing_code"
		}
		if res.errCode != "" {
			msg := "The sign-in came back with an error (" + res.errCode + "). Go back to Blaze and try again."
			if res.errCode == "access_denied" {
				msg = "You declined access. Nothing changed; you can close this tab."
			}
			writePage(w, false, "Sign-in didn't finish", msg)
		} else {
			writePage(w, true, "You're signed in", "You can close this tab and go back to Blaze.")
		}
		select {
		case got <- res:
		default:
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		// Let the browser finish loading the "you're signed in" page first.
		go func() {
			time.Sleep(1500 * time.Millisecond)
			sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = srv.Shutdown(sctx)
		}()
	}()

	v := url.Values{}
	v.Set("state", state)
	v.Set("code_challenge", challenge)
	v.Set("code_challenge_method", "S256")
	v.Set("redirect", redirect)
	if len(scopes) > 0 {
		v.Set("scope", strings.Join(scopes, " "))
	}
	open(c.Base + startPath + "?" + v.Encode())

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	var res cbResult
	select {
	case res = <-got:
	case <-ctx.Done():
		return nil, ErrCancelled
	case <-time.After(timeout):
		return nil, ErrCancelled
	}
	if res.errCode != "" {
		if res.errCode == "not_configured" {
			return nil, ErrNotConfigured
		}
		return nil, &APIError{Status: 400, Code: res.errCode}
	}
	var out Redeemed
	if err := c.do(ctx, "POST", "/oauth/redeem", "", map[string]string{"code": res.code, "code_verifier": verifier, "client": "Blaze Desktop"}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func writePage(w http.ResponseWriter, ok bool, title, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	mark := "#1F4BD8"
	if !ok {
		mark = "#111110"
	}
	esc := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Blaze</title><body style="font:15px/1.5 system-ui,sans-serif;background:#FBFBFA;color:#111110;display:grid;place-items:center;height:100vh;margin:0"><div style="max-width:440px;padding:32px;border:1px solid rgba(17,17,16,.18);background:#fff;box-shadow:inset 3px 0 0 %s"><div style="font:500 11px monospace;letter-spacing:.12em;text-transform:uppercase;color:%s">Blaze</div><h2 style="margin:8px 0 6px;font-weight:600">%s</h2><p style="margin:0;color:rgba(17,17,16,.6)">%s</p></div></body>`,
		mark, mark, esc(title), esc(msg))
}
