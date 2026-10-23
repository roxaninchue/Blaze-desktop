package main

// Signed updates. Before Blaze replaces itself (binary swap, pacman/apt install,
// NSIS run or .dmg copy) it downloads SHA256SUMS and SHA256SUMS.sig from the same
// release, checks the ed25519 signature with the key built in below, then checks the
// downloaded file's SHA-256 against its line. Anything else (no signature, a bad one,
// a hash mismatch, a file that isn't listed) and the download is deleted and nothing
// is installed. There's no grace period: every version from v0.15.1 on only installs
// signed releases. See "Signed updates" in the README.

import (
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"blaze-desktop/internal/updsig"
)

// updatePublicKey is Blaze's release-signing public key (raw ed25519, base64). It is
// defined in internal/updsig (shared with ci/sign and ci/verify); during a key rotation
// updsig.TrustedKeys also lists the next key. The private half is only in the
// BLAZE_UPDATE_SIGNING_KEY Actions secret.
const updatePublicKey = updsig.PublicKey

// unsignedMsg is what the user sees when a download fails verification.
const unsignedMsg = "This update isn't signed by Blaze, so I didn't install it."

// detailsSep splits the plain message from the technical details; the frontend shows
// the part after it behind a "Details" toggle.
const detailsSep = "\n\nDetails: "

// allowUnsigned is the development escape hatch: BLAZE_ALLOW_UNSIGNED_UPDATE=1.
func allowUnsigned() bool { return strings.TrimSpace(os.Getenv("BLAZE_ALLOW_UNSIGNED_UPDATE")) == "1" }

// releaseFileURL is where to fetch a release asset, the same way download() does.
func (a *App) releaseFileURL(as ghAsset) string { return as.Download }

// verifyUpdate checks file (downloaded as asset as) against rel's signed SHA256SUMS.
// On failure it deletes file and returns the message to show.
func (a *App) verifyUpdate(rel *ghRelease, as ghAsset, file string) error {
	var sums, sig ghAsset
	for _, x := range rel.Assets {
		switch x.Name {
		case updsig.SumsName:
			sums = x
		case updsig.SigName:
			sig = x
		}
	}
	hdr := http.Header{}
	hdr.Set("Accept", "application/octet-stream")
	hdr.Set("User-Agent", "Blaze-Desktop/"+version)
	sumsURL, sigURL := "", ""
	if sums.Name != "" {
		sumsURL = a.releaseFileURL(sums)
	}
	if sig.Name != "" {
		sigURL = a.releaseFileURL(sig)
	}
	err := updsig.VerifyDownload(&http.Client{Timeout: 60 * time.Second}, updsig.DefaultKeys(), sumsURL, sigURL, hdr, as.Name, file)
	if err == nil {
		log.Printf("update: %s %s verified against the signed %s", rel.Tag, as.Name, updsig.SumsName)
		return nil
	}
	if allowUnsigned() {
		log.Printf("!!!!!!!! update: INSTALLING AN UNVERIFIED UPDATE because BLAZE_ALLOW_UNSIGNED_UPDATE=1 (%s %s): %v !!!!!!!!", rel.Tag, as.Name, err)
		a.progress("install", 100, "Warning: installing an unsigned update (BLAZE_ALLOW_UNSIGNED_UPDATE=1)")
		return nil
	}
	_ = os.Remove(file)
	log.Printf("update: refused %s %s: %v", rel.Tag, as.Name, err)
	detail := err.Error()
	var ve *updsig.Error
	if errors.As(err, &ve) {
		detail = ve.Reason
	}
	return errors.New(unsignedMsg + detailsSep + rel.Tag + " " + as.Name + ": " + detail)
}
