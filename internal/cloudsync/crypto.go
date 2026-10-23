// Package cloudsync is Blaze Desktop's client for Blaze cloud sync (Blaze Gateway /v1/sync,
// gateway 0.4.0+): the end-to-end encrypted sync Blaze Android uses for chats, memory,
// settings and designs. Desktop and Blaze CLI share one ~/.blaze on a PC, so one client here
// covers both. Not wired to the UI yet (see work/v1sync-design.md in the Desktop project).
//
// crypto.go and item.go are aligned with Blaze CLI internal/cloudsync (same API and golden
// vectors). The wire format is the one in Blaze Android connect/SyncCrypto.kt and
// SyncMerge.kt; keep all three in step:
//
//	key   = PBKDF2-HMAC-SHA256(passphrase, salt, 600000, 32 bytes); the salt is random, kept on the gateway
//	blob  = base64(nonce[12] || AES-256-GCM(gzip(plaintext), aad = "blaze-sync-v1|<kind>|<id>"))
//	check = seal of "blaze-sync-check-v1" under kind "_check", id "v1"
//	plaintext (envelope) = {"v":1,"kind":k,"id":i,"t":<ms>,"data":{…}} or {…,"del":true}
//
// The passphrase never leaves this machine and is never stored; only the derived key is.
package cloudsync

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"strconv"
	"strings"
)

const (
	Iterations = 600_000
	KDF        = "pbkdf2-sha256:600000"
	aadPrefix  = "blaze-sync-v1"
	checkKind  = "_check"
	checkID    = "v1"
	checkText  = "blaze-sync-check-v1"
	maxPlain   = 16 << 20 // a gunzipped item is never bigger than this
)

// ErrBadPassphrase: the passphrase doesn't open the account's check blob.
var ErrBadPassphrase = errors.New("that passphrase doesn't match the one used on your other devices")

// NewSalt is 16 random bytes, base64 (standard, padded), as the phone makes it.
func NewSalt() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// IterationsOf reads a gateway "kdf" string ("pbkdf2-sha256:600000"); Iterations when it says nothing usable.
func IterationsOf(kdf string) int {
	p := strings.ToLower(strings.TrimSpace(kdf))
	if !strings.HasPrefix(p, "pbkdf2-sha256") {
		return Iterations
	}
	_, after, ok := strings.Cut(p, ":")
	if !ok {
		return Iterations
	}
	n, err := strconv.Atoi(after)
	if err != nil || n < 10_000 || n > 10_000_000 {
		return Iterations
	}
	return n
}

// DeriveKey is slow on purpose (600k rounds).
func DeriveKey(passphrase, saltB64 string, iterations int) ([]byte, error) {
	salt, err := base64.StdEncoding.DecodeString(strings.TrimSpace(saltB64))
	if err != nil {
		return nil, errors.New("bad salt")
	}
	if iterations <= 0 {
		iterations = Iterations
	}
	return pbkdf2.Key(sha256.New, passphrase, salt, iterations, 32)
}

func aad(kind, id string) []byte { return []byte(aadPrefix + "|" + kind + "|" + id) }

func gcm(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("bad key")
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

// Seal encrypts one item's plaintext, bound to its kind and id.
func Seal(key []byte, kind, id, plain string) (string, error) {
	g, err := gcm(key)
	if err != nil {
		return "", err
	}
	var z bytes.Buffer
	w := gzip.NewWriter(&z)
	w.Write([]byte(plain))
	w.Close()
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := g.Seal(nonce, nonce, z.Bytes(), aad(kind, id))
	return base64.StdEncoding.EncodeToString(out), nil
}

// Open fails when the key is wrong, the blob was changed, or it belongs to another kind / id.
func Open(key []byte, kind, id, blob string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(blob))
	if err != nil {
		return "", errors.New("bad blob")
	}
	if len(raw) <= 28 {
		return "", errors.New("blob too short")
	}
	g, err := gcm(key)
	if err != nil {
		return "", err
	}
	z, err := g.Open(nil, raw[:12], raw[12:], aad(kind, id))
	if err != nil {
		return "", err
	}
	r, err := gzip.NewReader(bytes.NewReader(z))
	if err != nil {
		return "", err
	}
	b, err := io.ReadAll(io.LimitReader(r, maxPlain+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxPlain {
		return "", errors.New("item too large")
	}
	return string(b), nil
}

// MakeCheck is the blob a second device verifies the passphrase against.
func MakeCheck(key []byte) (string, error) { return Seal(key, checkKind, checkID, checkText) }

// Checks is true when key opens the account's check blob.
func Checks(key []byte, check string) bool {
	s, err := Open(key, checkKind, checkID, check)
	return err == nil && s == checkText
}
