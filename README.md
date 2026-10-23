# Blaze Desktop

Blaze, the Android assistant, as a desktop app for **Windows, macOS and Linux**, with dark frosted glass, rounded everything, and the drop in its gold halo.

Built with [Wails](https://wails.io) on the same Go core as Blaze CLI (PC detection, free model router,
tools, scheduler). Settings, keys, memory and skills live in `~/.blaze` and are shared with the CLI.

## Install (latest release)
- **Windows**: `Blaze-…-windows-amd64-setup.exe` (or the portable exe). SmartScreen: More info › Run anyway.
- **macOS**: `Blaze-…-macos-universal.dmg`, drag to Applications, then right-click › Open the first time.
- **Arch**: `sudo pacman -U blaze-desktop-*.pkg.tar.zst` (needs `webkit2gtk-4.1`).
- **Debian/Ubuntu**: `sudo apt install ./blaze-desktop_*_amd64.deb`.
- **Other Linux**: unpack the tar.gz and run `blaze-desktop` (needs GTK 3 + WebKitGTK 4.1).

## What it does
- **Chat that works on your computer**: streaming answers with plain-English steps, Plan and Build modes, Auto or Autopilot approvals, and a daily spend cap.
- **BlazeRouter**: a local OpenAI-compatible gateway on `127.0.0.1:20128` that starts with Blaze and routes every chat across your free providers, falling back when one is rate-limited or out of quota. Every OmniRoute `auto/*` preset (auto/best-free, auto/best-coding, auto/chaos…), your own combos, keys, usage and quota live on its dashboard (the Router button in the top bar). Point Cursor, Claude Code or any OpenAI client at it too.
- **Any model**: pick a BlazeRouter preset, a provider and model, or the classic Auto chain, with live health, several keys per provider, and usage per model.
- **Design studio**: live preview at phone, tablet and desktop widths, edits that write back to your source, Directions as live branches, brand kit, the critic, visual history, phone preview, and one-click ship to Cloudflare or GitHub Pages.
- **3D mockups**: any 3D request starts as a Three.js mockup in Design. Click a part to recolour, scale, turn or nudge it, with every change written back to the file. When you named a program (Blender, FreeCAD, OpenSCAD and others), Blaze builds it there next, from the same parts.
- **Specialists on request**: Blaze does your tasks itself. Ask it to split a big task and parallel agents each work on their own git worktree, reviewed before anything lands.
- **Lives in the background**: tray icon, quick ask from anywhere (Ctrl+Shift+Space, Option+Space on a Mac), native notifications, and optional proactive suggestions.
- **Sees and hears**: reads the front window, attaches images, files and folders, and turns speech into text with Whisper on your own PC.
- **Memory and skills**: learns facts and workflows as you go, with 40 bundled skills (including a student pack: study planner, tutor with flashcards, lecture notes, essay coach, group projects and Google Classroom sync; and a seller pack: content calendar, product listings, ad lab, competitor briefs, WhatsApp replies and a weekly store digest) you can edit or turn off. Blaze combines them when a request falls between skills (a hand-drawn poster, a pixel-art game with music), and saves a combination that keeps coming up as a skill of its own.
- **Connected**: one-click Google, GitHub, Spotify, Notion and Slack, MCP servers shared with Blaze CLI (`~/.blaze/mcp.json`), and keys that sync through your Blaze account.
- **Blaze Link**: pair Blaze for Android by QR code (Phone view, `/phone`). Ask the phone to work on your PC, or let Blaze here text, WhatsApp or call through the phone, always with your approval there. Direct and encrypted on your Wi-Fi or Tailscale.
- **Updates itself** from the Blaze downloads site (blazeinchue.pages.dev), only installing releases whose SHA256SUMS carry a valid signature.

What changed in each version: [`CHANGELOG.md`](CHANGELOG.md).

## Credits
BlazeRouter is a Go port of [OmniRoute](https://github.com/diegosouzapw/OmniRoute) by diegosouzapw (MIT). See [`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md).

## Signed updates (v0.15.1)
Every release since v0.15.1 carries two extra files:

- `SHA256SUMS`: one `<sha256>  <file>` line (sha256sum format) for every other release asset.
- `SHA256SUMS.sig`: the **ed25519** signature of `SHA256SUMS`, base64 on one line. CI makes it with
  `ci/sign` from the `BLAZE_UPDATE_SIGNING_KEY` Actions secret (base64 of the 32-byte ed25519 seed;
  the 64-byte seed+public form is accepted too). The private key exists nowhere else.

Before the in-app updater (Settings › Updates, `/update`, or "update yourself") replaces anything, it
downloads both files, checks the signature against the public key built into the app
(`updsig.PublicKey` in `internal/updsig`, used by `update_verify.go`), then checks the downloaded file's SHA-256 against its
line. A missing or bad signature, a hash mismatch, or a file that isn't listed means the download is
deleted and nothing is installed ("This update isn't signed by Blaze, so I didn't install it").
There is no grace period: v0.15.1 and later only install signed releases. (Versions up to v0.15.0 have
no verifier and update into v0.15.1 as before.) For local testing only, `BLAZE_ALLOW_UNSIGNED_UPDATE=1`
skips the refusal and logs a loud warning; never set it on a normal install.

Public key (raw, base64), since v0.19.6: `moDRRNAnYCRxRjblqUI1oDlGgfRI13NumGA4sLrd4QI=`. v0.19.6 also trusts the previous key `h+BLX1pI7e4k9Xti6+dRn8tAz+kfZ+GgKqmoiUNkMFo=` (v0.19.2 to v0.19.5) for one release; the release after drops it. Versions up to v0.19.1 trusted `A/yWqdv1xeM1tQhvGj/2uzF2bFpp6OTBrONLxkkctUA=` and need one manual reinstall.

```
-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEAmoDRRNAnYCRxRjblqUI1oDlGgfRI13NumGA4sLrd4QI=
-----END PUBLIC KEY-----
```

**Verify a download by hand.** Put the file next to `SHA256SUMS` and `SHA256SUMS.sig` from the same release, then either:

```sh
go run ./ci/verify ~/Downloads/blaze          # checks the signature and every listed file present
# or with standard tools (OpenSSL 3, save the PEM above as blaze-update.pem):
base64 -d SHA256SUMS.sig > SHA256SUMS.sig.bin
openssl pkeyutl -verify -pubin -inkey blaze-update.pem -rawin -in SHA256SUMS -sigfile SHA256SUMS.sig.bin
sha256sum -c --ignore-missing SHA256SUMS
```

minisign/signify use their own file formats, so they can't check `SHA256SUMS.sig` directly; use one of the above.

**Key rotation.** Installed apps only trust the keys they were built with, so rotate in two releases:
1. Generate a new pair with Go's `crypto/ed25519`. Add the new public key to `updsig.TrustedKeys`
   (next to the old one) and release with the **old** secret still set. Apps now trust both keys.
2. Replace the `BLAZE_UPDATE_SIGNING_KEY` secret with the new base64 seed, set `PublicKey` to the new key,
   drop the old one from `TrustedKeys`, and release. Destroy the old seed.

`ci/sign` refuses a secret whose public half isn't in `TrustedKeys`, so a wrong secret fails the
release instead of shipping files no app will accept. If the private key leaks, do the same two steps at
once and delete any release you didn't make; apps still on older versions trust the old key until they
update. If the secret is missing, CI still publishes, warns, and skips the signature, and v0.15.1+ apps
then refuse that release.

## Build
Go 1.26 + `go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0`, then `wails build`
(Linux: `-tags webkit2_41`). CI builds every platform on push and publishes a release;
UI screenshots go to the `shots` branch.

**Why `frontend/dist` is committed.** The frontend is hand-written vanilla JS, CSS and HTML that
lives directly in `frontend/dist`. There is no `package.json`, bundler or build step
(`frontend:install` and `frontend:build` are empty in `wails.json`), so `dist` *is* the source:
edit files there. Only Wails' generated bindings (`frontend/wailsjs/`) are ignored.

## Security
How sign-in and one-click connectors work, what the `blaze-auth` Worker receives, stores and logs,
where your tokens end up, and how to revoke access: [`docs/SECURITY-auth.md`](docs/SECURITY-auth.md).
