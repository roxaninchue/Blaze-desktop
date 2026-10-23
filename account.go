package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"blaze-desktop/internal/cloud"
	"blaze-desktop/internal/config"
	"blaze-desktop/internal/connect"
)

// Blaze accounts and one-click connectors, through blaze-auth (cloud/ in this repo,
// a free Cloudflare Worker). The session token lives in the OS keychain; the signed-in
// user's name and email are cached in ~/.blaze/desktop/account.json for the UI.

const sessionKey = "blaze-account:session"

type cloudState struct {
	mu      sync.Mutex
	client  *cloud.Client
	sec     connect.Secrets
	prov    *cloud.Providers
	checked time.Time
	user    *cloud.User
	pending string // "google" | "github" while a browser sign-in runs
	cancel  context.CancelFunc
	online  bool
}

func accountFile() string { return filepath.Join(config.Dir(), "desktop", "account.json") }

func (a *App) startCloud() {
	dir := filepath.Join(config.Dir(), "desktop")
	a.cloud = &cloudState{client: cloud.New(cloud.BaseURL(dir)), sec: connect.Keychain()}
	if b, err := os.ReadFile(accountFile()); err == nil {
		var u cloud.User
		if json.Unmarshal(b, &u) == nil && u.ID != "" {
			a.cloud.user = &u
		}
	}
	a.conn.SetBroker(&broker{cs: a.cloud})
	go func() {
		a.cloud.refreshProviders(true)
		a.conn.SetBroker(&broker{cs: a.cloud}) // redraw with what the service offers
		a.checkSession()
		a.startKeySync()
		a.startCloudSync() // Settings › Account › Sync across devices (cloudsync_app.go)
	}()
}

// refreshProviders asks the service which sign-ins are switched on (cached 10 minutes).
func (cs *cloudState) refreshProviders(force bool) {
	cs.mu.Lock()
	if !force && time.Since(cs.checked) < 10*time.Minute {
		cs.mu.Unlock()
		return
	}
	cs.checked = time.Now()
	c := cs.client
	cs.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := c.Providers(ctx)
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if err != nil {
		cs.online = false
		return
	}
	cs.prov, cs.online = p, true
}

func (a *App) emitAccount() {
	if a.ctx != nil {
		wr.EventsEmit(a.ctx, "account", a.AccountState())
	}
}

// checkSession confirms a saved session is still good (signed out elsewhere, deleted…).
func (a *App) checkSession() {
	tok, _ := a.cloud.sec.Get(sessionKey)
	if tok == "" {
		if a.cloud.user != nil {
			a.setSession(nil)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	u, err := a.cloud.client.Me(ctx, tok)
	var ae *cloud.APIError
	switch {
	case err == nil:
		a.cloud.mu.Lock()
		a.cloud.user = u
		a.cloud.mu.Unlock()
		a.saveUser(u)
		a.emitAccount()
	case errors.As(err, &ae) && ae.Status == 401:
		a.setSession(nil)
	}
}

func (a *App) saveUser(u *cloud.User) {
	if u == nil {
		_ = os.Remove(accountFile())
		return
	}
	_ = os.MkdirAll(filepath.Dir(accountFile()), 0o700)
	b, _ := json.MarshalIndent(u, "", "  ")
	_ = os.WriteFile(accountFile(), b, 0o600)
}

// setSession keeps a new session (or clears it with nil) and tells the UI.
func (a *App) setSession(s *cloud.Session) error {
	cs := a.cloud
	if s == nil {
		_ = cs.sec.Delete(sessionKey)
		cs.mu.Lock()
		cs.user = nil
		cs.mu.Unlock()
		a.saveUser(nil)
		a.emitAccount()
		return nil
	}
	if err := cs.sec.Set(sessionKey, s.Token); err != nil {
		return err
	}
	u := s.User
	cs.mu.Lock()
	cs.user = &u
	cs.mu.Unlock()
	a.saveUser(&u)
	a.emitAccount()
	go func() { _, _ = a.syncKeys(false) }() // bring this account's model keys here, and ours there
	return nil
}

// ---------------- bound to the frontend ----------------

// AccountView is Settings › Account and the sign-in screen.
type AccountView struct {
	SignedIn bool            `json:"signed_in"`
	User     *cloud.User     `json:"user"`
	Login    map[string]bool `json:"login"` // email, google, github: switched on at the service
	Online   bool            `json:"online"`
	Pending  string          `json:"pending"` // provider while its browser sign-in runs
	Service  string          `json:"service"` // the Worker's base URL
}

func (a *App) AccountState() AccountView {
	a.waitReady()
	cs := a.cloud
	if cs == nil {
		return AccountView{}
	}
	go cs.refreshProviders(false)
	cs.mu.Lock()
	defer cs.mu.Unlock()
	v := AccountView{User: cs.user, SignedIn: cs.user != nil, Online: cs.online, Pending: cs.pending, Service: cs.client.Base,
		Login: map[string]bool{"email": cs.online}}
	if cs.prov != nil {
		for k, on := range cs.prov.Login {
			v.Login[k] = on
		}
	}
	return v
}

// AccountResult answers a sign-in or sign-up.
type AccountResult struct {
	Error string       `json:"error,omitempty"`
	State *AccountView `json:"state,omitempty"`
	MFA   *MFAStep     `json:"mfa,omitempty"`  // the sign-in needs a second factor
	Sent  string       `json:"sent,omitempty"` // a code was emailed to this (masked) address
}

// MFAStep is the two-factor step of a sign-in (blaze-auth 0.9.1).
type MFAStep struct {
	Token     string   `json:"token"`
	Methods   []string `json:"methods"` // app, email, recovery
	EmailHint string   `json:"email_hint"`
	SentTo    string   `json:"sent_to,omitempty"` // emailed already (no authenticator app)
	SendError string   `json:"send_error,omitempty"`
}

func hasMethod(ms []string, m string) bool {
	for _, x := range ms {
		if x == m {
			return true
		}
	}
	return false
}

// mfaStep turns an mfa_required refusal into the two-factor step. Without an authenticator
// app the code is emailed straight away.
func (a *App) mfaStep(err error) *MFAStep {
	var ae *cloud.APIError
	if !errors.As(err, &ae) || ae.Code != "mfa_required" || ae.MFAToken == "" {
		return nil
	}
	st := &MFAStep{Token: ae.MFAToken, Methods: ae.Methods, EmailHint: ae.EmailHint}
	if len(st.Methods) == 0 {
		st.Methods = []string{"app"} // blaze-auth 0.9.0 sent no methods: app codes only
	}
	if !hasMethod(st.Methods, "app") && hasMethod(st.Methods, "email") {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if to, _, e := a.cloud.client.MFAEmail(ctx, st.Token); e != nil {
			st.SendError = cloud.Message(e)
		} else {
			st.SentTo = to
		}
	}
	return st
}

// AccountMFAVerify finishes a two-factor sign-in with an app, emailed or recovery code.
func (a *App) AccountMFAVerify(token, code string) AccountResult {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if code == "" {
		return AccountResult{Error: "Enter the code."}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := a.cloud.client.MFAVerify(ctx, token, code)
	if err != nil {
		return AccountResult{Error: cloud.Message(err)}
	}
	if err := a.setSession(s); err != nil {
		return AccountResult{Error: err.Error()}
	}
	st := a.AccountState()
	return AccountResult{State: &st}
}

// AccountMFAEmail emails a sign-in code ("Email me a code instead", "Send a new code").
func (a *App) AccountMFAEmail(token string) AccountResult {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	to, _, err := a.cloud.client.MFAEmail(ctx, token)
	if err != nil {
		if errors.Is(err, cloud.ErrNotConfigured) {
			return AccountResult{Error: "Emailed codes aren't switched on yet. Use your authenticator app."}
		}
		return AccountResult{Error: cloud.Message(err)}
	}
	return AccountResult{Sent: to}
}

// AccountEmailCodesSend emails the code that switches emailed sign-in codes on or off.
func (a *App) AccountEmailCodesSend(on bool) AccountResult {
	tok, _ := a.cloud.sec.Get(sessionKey)
	if tok == "" {
		return AccountResult{Error: "You're signed out. Sign in again."}
	}
	purpose := "disable"
	if on {
		purpose = "enable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	to, err := a.cloud.client.EmailCodesSend(ctx, tok, purpose)
	if err != nil {
		if errors.Is(err, cloud.ErrNotConfigured) {
			return AccountResult{Error: "Emailed codes aren't switched on at Blaze yet."}
		}
		return AccountResult{Error: cloud.Message(err)}
	}
	return AccountResult{Sent: to}
}

// AccountEmailCodesSet switches emailed sign-in codes on or off with the emailed code.
func (a *App) AccountEmailCodesSet(on bool, code string) AccountResult {
	tok, _ := a.cloud.sec.Get(sessionKey)
	if tok == "" {
		return AccountResult{Error: "You're signed out. Sign in again."}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	u, err := a.cloud.client.EmailCodesSet(ctx, tok, on, strings.ReplaceAll(strings.TrimSpace(code), " ", ""))
	if err != nil {
		return AccountResult{Error: cloud.Message(err)}
	}
	a.cloud.mu.Lock()
	a.cloud.user = u
	a.cloud.mu.Unlock()
	a.saveUser(u)
	a.emitAccount()
	st := a.AccountState()
	return AccountResult{State: &st}
}

// AccountAvatar sets the profile picture from a data: URL the page made (already cropped to a
// 256 px square and compressed), or removes it when dataURL is "" (back to the letter avatar).
func (a *App) AccountAvatar(dataURL string) AccountResult {
	tok, _ := a.cloud.sec.Get(sessionKey)
	if tok == "" {
		return AccountResult{Error: "You're signed out. Sign in again."}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var u *cloud.User
	var err error
	if dataURL == "" {
		u, err = a.cloud.client.AvatarDelete(ctx, tok)
	} else {
		ctype, data, ok := parseDataURL(dataURL)
		if !ok {
			return AccountResult{Error: "Use a PNG, JPEG or WebP picture."}
		}
		u, err = a.cloud.client.AvatarUpload(ctx, tok, ctype, data)
	}
	if err != nil {
		return AccountResult{Error: cloud.Message(err)}
	}
	a.cloud.mu.Lock()
	a.cloud.user = u
	a.cloud.mu.Unlock()
	a.saveUser(u)
	a.emitAccount()
	st := a.AccountState()
	return AccountResult{State: &st}
}

// parseDataURL splits "data:image/webp;base64,..." into its type and bytes.
func parseDataURL(s string) (string, []byte, bool) {
	head, b64, ok := strings.Cut(s, ",")
	if !ok || !strings.HasPrefix(head, "data:") || !strings.HasSuffix(head, ";base64") {
		return "", nil, false
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", nil, false
	}
	return strings.TrimSuffix(strings.TrimPrefix(head, "data:"), ";base64"), data, true
}

// AccountEmail signs in (mode "signin") or creates an account (mode "signup").
func (a *App) AccountEmail(mode, name, email, password string) AccountResult {
	email = strings.TrimSpace(email)
	if email == "" || password == "" {
		return AccountResult{Error: "Enter your email and password."}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var s *cloud.Session
	var err error
	if mode == "signup" {
		if len(password) < 8 {
			return AccountResult{Error: "Use a password of at least 8 characters."}
		}
		s, err = a.cloud.client.SignUp(ctx, strings.TrimSpace(name), email, password)
	} else {
		s, err = a.cloud.client.SignIn(ctx, email, password)
	}
	if err != nil {
		if m := a.mfaStep(err); m != nil {
			return AccountResult{MFA: m}
		}
		return AccountResult{Error: cloud.Message(err)}
	}
	if err := a.setSession(s); err != nil {
		return AccountResult{Error: err.Error()}
	}
	st := a.AccountState()
	return AccountResult{State: &st}
}

// AccountSignIn starts "Sign in with Google/GitHub" in the browser. The result
// arrives as an "account" event; "" means the browser is open.
func (a *App) AccountSignIn(provider string) string {
	if provider != "google" && provider != "github" {
		return "Blaze can't sign in with " + provider + "."
	}
	cs := a.cloud
	cs.refreshProviders(false)
	cs.mu.Lock()
	if cs.prov == nil || !cs.prov.Login[provider] {
		cs.mu.Unlock()
		if !cs.online {
			return "Couldn't reach Blaze's sign-in service. Check your internet connection."
		}
		return "Sign in with " + titleCase(provider) + " isn't switched on yet. Use your email instead."
	}
	if cs.cancel != nil {
		cs.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 11*time.Minute)
	cs.cancel, cs.pending = cancel, provider
	cs.mu.Unlock()
	a.emitAccount()
	go func() {
		defer cancel()
		s, err := cs.client.SignInWith(ctx, provider, func(u string) { wr.BrowserOpenURL(a.ctx, u) })
		cs.mu.Lock()
		superseded := cs.pending != provider || ctx.Err() == context.Canceled
		if !superseded {
			cs.pending, cs.cancel = "", nil
		}
		cs.mu.Unlock()
		if superseded {
			return
		}
		if err == nil {
			err = a.setSession(s)
		}
		if m := a.mfaStep(err); m != nil {
			a.emitAccount()
			wr.EventsEmit(a.ctx, "account_mfa", m) // the sign-in page asks for the second factor
			wr.WindowUnminimise(a.ctx)
			wr.WindowShow(a.ctx)
			return
		}
		if err != nil {
			a.emitAccount()
			wr.EventsEmit(a.ctx, "toast", map[string]string{"text": cloud.Message(err), "kind": "error"})
			return
		}
		wr.EventsEmit(a.ctx, "toast", map[string]string{"text": "Signed in as " + s.User.Email, "kind": "ok"})
		wr.WindowUnminimise(a.ctx)
		wr.WindowShow(a.ctx)
	}()
	return ""
}

// AccountCancel stops a browser sign-in in progress.
func (a *App) AccountCancel() {
	cs := a.cloud
	cs.mu.Lock()
	if cs.cancel != nil {
		cs.cancel()
	}
	cs.cancel, cs.pending = nil, ""
	cs.mu.Unlock()
	a.emitAccount()
}

// AccountSignOut ends the session here and at the service.
func (a *App) AccountSignOut() string {
	if tok, _ := a.cloud.sec.Get(sessionKey); tok != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = a.cloud.client.Logout(ctx, tok) // best effort: offline still signs out here
		cancel()
	}
	_ = a.setSession(nil)
	return ""
}

// AccountDelete deletes the Blaze account (connectors and keys on this computer stay).
func (a *App) AccountDelete() string {
	tok, _ := a.cloud.sec.Get(sessionKey)
	if tok == "" {
		_ = a.setSession(nil)
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := a.cloud.client.DeleteAccount(ctx, tok); err != nil {
		var ae *cloud.APIError
		if !(errors.As(err, &ae) && ae.Status == 401) {
			return cloud.Message(err)
		}
	}
	_ = a.setSession(nil)
	return ""
}

// ConnectorConnectManual connects through the user's own developer app or token.
func (a *App) ConnectorConnectManual(id string) connect.ConnectResult {
	return a.conn.ConnectManual(id)
}

func titleCase(s string) string {
	switch s {
	case "github":
		return "GitHub"
	case "google":
		return "Google"
	}
	return s
}

// ---------------- the connectors' broker ----------------

// broker adapts cloud.Client to connect.Broker.
type broker struct{ cs *cloudState }

func (b *broker) Known() bool {
	b.cs.mu.Lock()
	defer b.cs.mu.Unlock()
	return b.cs.online && b.cs.prov != nil
}

func (b *broker) Available(id string) bool {
	go b.cs.refreshProviders(false)
	b.cs.mu.Lock()
	defer b.cs.mu.Unlock()
	return b.cs.online && b.cs.prov != nil && b.cs.prov.Connectors[id]
}

func toToken(t *cloud.Tokens) *connect.Token {
	tok := &connect.Token{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, TokenType: t.TokenType, Scope: t.Scope}
	if t.ExpiresIn > 0 {
		tok.ExpiresAt = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second).UnixMilli()
	}
	return tok
}

func (b *broker) Connect(ctx context.Context, id string, scopes []string, open func(string)) (*connect.Token, []string, string, error) {
	r, err := b.cs.client.Connect(ctx, id, scopes, open)
	if err != nil {
		if errors.Is(err, cloud.ErrNotConfigured) {
			go b.cs.refreshProviders(true)
		}
		return nil, nil, "", err
	}
	if r.Tokens == nil || r.Tokens.AccessToken == "" {
		return nil, nil, "", errors.New("the sign-in service sent no token back")
	}
	return toToken(r.Tokens), r.Scopes, r.Tokens.Account, nil
}

func (b *broker) Refresh(ctx context.Context, id, rt string) (*connect.Token, error) {
	t, err := b.cs.client.Refresh(ctx, id, rt)
	if err != nil {
		var un *cloud.Unreachable
		var ae *cloud.APIError
		switch {
		case errors.As(err, &un), errors.Is(err, cloud.ErrCancelled):
			return nil, connect.ErrBrokerUnreachable
		case errors.Is(err, cloud.ErrBadAnswer),
			errors.As(err, &ae) && (ae.Status >= 500 || ae.Status == 408 || ae.Status == 429):
			// The service is up but hiccuped (5xx, rate limit, a proxy page): keep the
			// connection; only a real refusal (invalid_grant, revoked) means Reconnect.
			return nil, fmt.Errorf("%w: %v", connect.ErrBrokerTemporary, err)
		}
		return nil, err
	}
	return toToken(t), nil
}

func (b *broker) Message(err error) string { return cloud.Message(err) }
