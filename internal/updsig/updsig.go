// Package updsig signs and verifies Blaze release files.
//
// Every release carries two extra assets:
//
//	SHA256SUMS      one "<sha256 hex>  <file name>" line per release asset (sha256sum format)
//	SHA256SUMS.sig  the ed25519 signature of SHA256SUMS, base64 on one line
//
// CI signs SHA256SUMS with the private key in the BLAZE_UPDATE_SIGNING_KEY secret
// (ci/sign). The app verifies the signature with TrustedKeys, then checks the
// downloaded file's SHA-256 against its line, before it installs anything.
package updsig

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// PublicKey is Blaze's release-signing public key (raw 32-byte ed25519, base64).
// The matching private key is a masked, protected CI/CD variable on the GitLab project
// (BLAZE_UPDATE_SIGNING_KEY_HEX), read by the release build and handed to ci/sign; it is never
// committed or printed. v0.19.6 rotated to this key.
const PublicKey = "moDRRNAnYCRxRjblqUI1oDlGgfRI13NumGA4sLrd4QI="

// PreviousKey signed v0.19.2 to v0.19.5. v0.19.6 still trusts it (and is signed with it) so those
// installs can update into v0.19.6; the release after that drops it.
const PreviousKey = "h+BLX1pI7e4k9Xti6+dRn8tAz+kfZ+GgKqmoiUNkMFo="

// TrustedKeys are the keys a release may be signed with. Normally just PublicKey;
// during a key rotation one release trusts the old and the new key together (see
// "Signed updates" in the README).
var TrustedKeys = []string{PublicKey, PreviousKey}

// Keys is a set of trusted public keys; a signature by any one of them is accepted.
type Keys []ed25519.PublicKey

// SumsName and SigName are the release asset names.
const (
	SumsName = "SHA256SUMS"
	SigName  = "SHA256SUMS.sig"
)

// MaxSumsSize caps how much of SHA256SUMS / .sig is read.
const MaxSumsSize = 1 << 20

// Error is a verification failure. Every one means: don't install.
type Error struct{ Reason string }

func (e *Error) Error() string { return "update not verified: " + e.Reason }

func fail(format string, a ...any) error { return &Error{Reason: fmt.Sprintf(format, a...)} }

// IsVerifyError reports whether err is a verification failure (as opposed to nil).
func IsVerifyError(err error) bool { var e *Error; return errors.As(err, &e) }

// ParsePublicKey decodes a base64 raw ed25519 public key.
func ParsePublicKey(b64 string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("bad ed25519 public key")
	}
	return ed25519.PublicKey(b), nil
}

// DefaultKeys is TrustedKeys, decoded.
func DefaultKeys() Keys {
	var ks Keys
	for _, s := range TrustedKeys {
		k, err := ParsePublicKey(s)
		if err != nil {
			panic(err)
		}
		ks = append(ks, k)
	}
	return ks
}

// Has reports whether pub is one of ks.
func (ks Keys) Has(pub ed25519.PublicKey) bool {
	for _, k := range ks {
		if k.Equal(pub) {
			return true
		}
	}
	return false
}

// ParsePrivateKey accepts base64 of the 32-byte seed or of the 64-byte seed+public key.
func ParsePrivateKey(b64 string) (ed25519.PrivateKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, errors.New("signing key is not valid base64")
	}
	switch len(b) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(b), nil
	case ed25519.PrivateKeySize:
		k := ed25519.NewKeyFromSeed(b[:32])
		if !bytes.Equal(k[32:], b[32:]) {
			return nil, errors.New("signing key: public half doesn't match the seed")
		}
		return k, nil
	}
	return nil, fmt.Errorf("signing key must be 32 or 64 bytes, got %d", len(b))
}

// Sign returns the SHA256SUMS.sig contents for sums.
func Sign(priv ed25519.PrivateKey, sums []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sums)) + "\n")
}

// decodeSig accepts the base64 text form (what CI writes) or a raw 64-byte signature.
func decodeSig(sig []byte) ([]byte, error) {
	if len(sig) == ed25519.SignatureSize {
		return sig, nil
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(b) != ed25519.SignatureSize {
		return nil, fail("%s is not a valid ed25519 signature", SigName)
	}
	return b, nil
}

// VerifySums checks that sig is a signature over sums by one of keys.
func VerifySums(keys Keys, sums, sig []byte) error {
	if len(sig) == 0 {
		return fail("%s is missing or empty", SigName)
	}
	s, err := decodeSig(sig)
	if err != nil {
		return err
	}
	for _, k := range keys {
		if len(k) == ed25519.PublicKeySize && ed25519.Verify(k, sums, s) {
			return nil
		}
	}
	return fail("the signature on %s doesn't match Blaze's key", SumsName)
}

// ParseSums reads sha256sum output: "<hex>  name" or "<hex> *name" (binary mode),
// LF or CRLF line ends, blank lines and # comments ignored. Names are bare file names.
func ParseSums(sums []byte) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(sums))
	sc.Buffer(make([]byte, 64*1024), MaxSumsSize)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimRight(sc.Text(), "\r \t")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		i := strings.IndexAny(line, " \t")
		if i != 64 {
			return nil, fail("%s line %d isn't \"<sha256>  <file>\"", SumsName, n)
		}
		h := strings.ToLower(line[:64])
		if _, err := hex.DecodeString(h); err != nil {
			return nil, fail("%s line %d has a bad hash", SumsName, n)
		}
		name := strings.TrimLeft(line[64:], " \t")
		name = strings.TrimPrefix(name, "*")
		if name == "" {
			return nil, fail("%s line %d has no file name", SumsName, n)
		}
		if prev, ok := out[name]; ok && prev != h {
			return nil, fail("%s lists %s twice with different hashes", SumsName, name)
		}
		out[name] = h
	}
	if err := sc.Err(); err != nil {
		return nil, fail("couldn't read %s: %v", SumsName, err)
	}
	if len(out) == 0 {
		return nil, fail("%s is empty", SumsName)
	}
	return out, nil
}

// FileSHA256 hashes the file at path.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyFile checks the signature on sums, then that the file at path (released as
// assetName) has the hash sums lists for it.
func VerifyFile(keys Keys, sums, sig []byte, assetName, path string) error {
	if len(sums) == 0 {
		return fail("%s is missing or empty", SumsName)
	}
	if err := VerifySums(keys, sums, sig); err != nil {
		return err
	}
	list, err := ParseSums(sums)
	if err != nil {
		return err
	}
	want, ok := list[assetName]
	if !ok {
		return fail("%s isn't listed in %s", assetName, SumsName)
	}
	got, err := FileSHA256(path)
	if err != nil {
		return fail("couldn't read the download: %v", err)
	}
	if got != want {
		return fail("%s's SHA-256 is %s but %s says %s", assetName, got, SumsName, want)
	}
	return nil
}

// Fetch downloads a small file (SHA256SUMS or its .sig), at most MaxSumsSize bytes.
func Fetch(c *http.Client, url string, hdr http.Header) ([]byte, error) {
	if url == "" {
		return nil, fail("no download address")
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fail("bad download address: %v", err)
	}
	for k, v := range hdr {
		req.Header[k] = v
	}
	res, err := c.Do(req)
	if err != nil {
		return nil, fail("couldn't download %s: %v", url, err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fail("couldn't download %s (HTTP %d)", url, res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, MaxSumsSize+1))
	if err != nil {
		return nil, fail("couldn't download %s: %v", url, err)
	}
	if len(b) > MaxSumsSize {
		return nil, fail("%s is too large", url)
	}
	return b, nil
}

// VerifyDownload fetches SHA256SUMS and SHA256SUMS.sig from sumsURL and sigURL and
// runs VerifyFile. An empty URL means the release didn't publish that file.
func VerifyDownload(c *http.Client, keys Keys, sumsURL, sigURL string, hdr http.Header, assetName, path string) error {
	if sumsURL == "" {
		return fail("the release has no %s", SumsName)
	}
	if sigURL == "" {
		return fail("the release has no %s", SigName)
	}
	sums, err := Fetch(c, sumsURL, hdr)
	if err != nil {
		return err
	}
	sig, err := Fetch(c, sigURL, hdr)
	if err != nil {
		return err
	}
	return VerifyFile(keys, sums, sig, assetName, path)
}
