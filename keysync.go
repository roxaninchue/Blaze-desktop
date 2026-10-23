package main

// Model keys follow the Blaze account. On this computer Blaze Desktop and Blaze CLI
// already share one key store (internal/keystore). While signed in, the store is also
// merged with the account's encrypted copy so Blaze on the phone and other PCs get the
// same keys: at start (at most hourly), right after signing in, and whenever a key is
// added or removed here. Nothing runs when signed out.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"blaze-desktop/internal/config"
	"blaze-desktop/internal/keystore"
)

var keySyncMu sync.Mutex

// KeySyncView is what Settings can show about key sync.
type KeySyncView struct {
	SignedIn  bool   `json:"signedIn"`
	LastSync  int64  `json:"lastSync"` // unix seconds, 0 = never
	Available bool   `json:"available"`
	Message   string `json:"message,omitempty"`
}

// syncKeys merges the shared key store with the account. quiet skips the success toast.
func (a *App) syncKeys(quiet bool) (*keystore.SyncResult, error) {
	if a.cloud == nil {
		return nil, keystore.ErrSignedOut
	}
	tok, _ := a.cloud.sec.Get(sessionKey)
	if tok == "" {
		return nil, keystore.ErrSignedOut
	}
	keySyncMu.Lock()
	defer keySyncMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r, err := config.Keys().Sync(ctx, a.cloud.client.HC, a.cloud.client.Base, tok)
	if errors.Is(err, keystore.ErrSignedOut) {
		go a.checkSession() // a refused session signs out here too
	}
	if err == nil && len(r.Pulled) > 0 {
		a.emitModel()
		if a.ctx != nil && !quiet {
			wr.EventsEmit(a.ctx, "toast", map[string]string{"text": "Got your " + providerList(r.Pulled) + " key from your Blaze account", "kind": "ok"})
		}
	}
	return r, err
}

func providerList(ids []string) string {
	n := make([]string, 0, len(ids))
	for _, id := range ids {
		n = append(n, config.Name(id))
	}
	return strings.Join(n, ", ")
}

// startKeySync hooks key changes to the account and syncs at start when stale.
func (a *App) startKeySync() {
	config.KeysChanged = func(string) { go func() { _, _ = a.syncKeys(true) }() }
	if time.Since(config.Keys().LastSync()) > time.Hour {
		_, _ = a.syncKeys(false)
	}
}

// KeySyncNow syncs keys with the Blaze account now (Settings button).
func (a *App) KeySyncNow() KeySyncView {
	r, err := a.syncKeys(true)
	v := a.KeySyncState()
	switch {
	case errors.Is(err, keystore.ErrSignedOut):
		v.Message = "Sign in to your Blaze account to use your keys on your phone too."
	case errors.Is(err, keystore.ErrUnsupported):
		v.Available = false
		v.Message = "Keys are shared with Blaze CLI on this computer. Syncing them to your phone isn't switched on in Blaze's service yet."
	case err != nil:
		v.Message = "Couldn't sync keys: " + err.Error()
	case len(r.Pulled) > 0:
		v.Message = "Got " + providerList(r.Pulled) + " from your Blaze account."
	case r.Pushed:
		v.Message = "Your keys are on your Blaze account now."
	default:
		v.Message = "Keys are up to date."
	}
	return v
}

// KeySyncState is the current key-sync state.
func (a *App) KeySyncState() KeySyncView {
	v := KeySyncView{Available: true}
	if a.cloud != nil {
		if tok, _ := a.cloud.sec.Get(sessionKey); tok != "" {
			v.SignedIn = true
		}
	}
	if t := config.Keys().LastSync(); !t.IsZero() {
		v.LastSync = t.Unix()
	}
	return v
}
