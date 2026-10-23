package main

// Blaze Link (v0.14+): the PC side of phone pairing with Blaze for Android
// (com.blaze.assistant.link.DesktopLink). Ported from PR #3 (branch feature/phone-pairing)
// onto the current app; the wire protocol is unchanged and documented in
// docs/plans/plan-blaze-link.md.
//
// The PC runs a small HTTPS server (port 7377) with its own self-signed certificate. The QR code
// shown in the Phone view carries this PC's addresses, the certificate's SHA-256 fingerprint (the
// phone pins it, so nobody else on the Wi-Fi can pretend to be the PC) and a one-time code that
// expires after 5 minutes. The phone trades the code for a long-lived token. After that:
//
//	phone → PC: POST /v1/task runs a Blaze turn on this PC. The phone long-polls /v1/task/{id}
//	            for progress, approval requests (answered with POST /v1/confirm) and the answer.
//	PC → phone: the phone long-polls GET /v1/inbox. The phone_ask tool drops a request there and
//	            waits for POST /v1/inbox/{id}/reply.
//
// It works wherever the two can reach each other: the same Wi-Fi, or anywhere over Tailscale
// (the QR includes the PC's Tailscale address too). No server in the middle, nothing to pay for.

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"image/png"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"rsc.io/qr"

	"blaze-desktop/internal/config"
	"blaze-desktop/internal/llm"
	"blaze-desktop/internal/tools"
)

const (
	pairPort     = 7377
	pairCodeLife = 5 * time.Minute
	pollWindow   = 70 * time.Second // a phone that polled this recently counts as connected
)

type pairedDevice struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	TokenHash string    `json:"token_hash"`
	PairedAt  time.Time `json:"paired_at"`
}

// phoneTask is one request the phone sent to run on this PC.
type phoneTask struct {
	id, device string
	mu         sync.Mutex
	status     string // running, done, error
	steps      []string
	text       string
	confirm    *taskConfirm
	changed    chan struct{} // closed and replaced on every change, so long-polls wake up
	cancel     context.CancelFunc
	created    time.Time
}

type taskConfirm struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	answer chan bool
}

// phoneReq is a request from this PC's Blaze to the phone (the phone_ask tool).
type phoneReq struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	reply chan string
	taken bool
}

type pairHub struct {
	app *App

	mu       sync.Mutex
	devices  []pairedDevice
	code     string
	codeExp  time.Time
	badTries int
	cert     tls.Certificate
	fp       string
	srv      *http.Server
	errText  string
	tasks    map[string]*phoneTask
	hist     map[string][]llm.Message // each phone's own conversation with this PC
	inbox    []*phoneReq
	inboxSig chan struct{}
	lastPoll map[string]time.Time
}

func pairDir() string {
	d := filepath.Join(config.Dir(), "pair")
	_ = os.MkdirAll(d, 0o700)
	return d
}

func newPairHub(a *App) *pairHub {
	p := &pairHub{app: a, tasks: map[string]*phoneTask{}, hist: map[string][]llm.Message{}, inboxSig: make(chan struct{}), lastPoll: map[string]time.Time{}}
	b, _ := os.ReadFile(filepath.Join(pairDir(), "devices.json"))
	_ = json.Unmarshal(b, &p.devices)
	if len(p.devices) > 0 {
		p.ensureServer()
	}
	return p
}

func (p *pairHub) saveDevices() {
	b, _ := json.MarshalIndent(p.devices, "", "  ")
	_ = os.WriteFile(filepath.Join(pairDir(), "devices.json"), b, 0o600)
}

// ---------- certificate ----------

func (p *pairHub) loadCert() error {
	cf, kf := filepath.Join(pairDir(), "cert.pem"), filepath.Join(pairDir(), "key.pem")
	if c, err := tls.LoadX509KeyPair(cf, kf); err == nil {
		p.cert = c
	} else {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return err
		}
		serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
		host, _ := os.Hostname()
		tpl := &x509.Certificate{
			SerialNumber: serial,
			Subject:      pkix.Name{CommonName: "Blaze Desktop " + host},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().AddDate(20, 0, 0),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			DNSNames:     []string{"blaze.local"},
		}
		der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
		if err != nil {
			return err
		}
		kb, _ := x509.MarshalECPrivateKey(key)
		certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})
		_ = os.WriteFile(cf, certPEM, 0o600)
		_ = os.WriteFile(kf, keyPEM, 0o600)
		if p.cert, err = tls.X509KeyPair(certPEM, keyPEM); err != nil {
			return err
		}
	}
	sum := sha256.Sum256(p.cert.Certificate[0])
	p.fp = hex.EncodeToString(sum[:])
	return nil
}

// ---------- server ----------

// ensureServer starts the HTTPS server once. Call with p.mu NOT held.
func (p *pairHub) ensureServer() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.srv != nil {
		return
	}
	if err := p.loadCert(); err != nil {
		p.errText = "Couldn't make the pairing certificate: " + err.Error()
		return
	}
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(pairPort))
	if err != nil {
		p.errText = fmt.Sprintf("Port %d is busy (%v). Close whatever uses it, or another Blaze window, then try again.", pairPort, err)
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/pair", p.hPair)
	mux.HandleFunc("GET /v1/ping", p.auth(p.hPing))
	mux.HandleFunc("POST /v1/task", p.auth(p.hTask))
	mux.HandleFunc("GET /v1/task/{id}", p.auth(p.hTaskGet))
	mux.HandleFunc("POST /v1/task/{id}/stop", p.auth(p.hTaskStop))
	mux.HandleFunc("POST /v1/confirm", p.auth(p.hConfirm))
	mux.HandleFunc("GET /v1/inbox", p.auth(p.hInbox))
	mux.HandleFunc("POST /v1/inbox/{id}/reply", p.auth(p.hReply))
	mux.HandleFunc("POST /v1/unpair", p.auth(p.hUnpair))
	p.srv = &http.Server{
		Handler:           mux,
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{p.cert}, MinVersion: tls.VersionTLS12},
		ReadHeaderTimeout: 10 * time.Second,
	}
	p.errText = ""
	srv := p.srv
	go func() {
		if err := srv.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			p.mu.Lock()
			p.errText = "Pairing server stopped: " + err.Error()
			p.srv = nil
			p.mu.Unlock()
		}
	}()
}

func (p *pairHub) close() {
	p.mu.Lock()
	srv := p.srv
	p.srv = nil
	for _, t := range p.tasks {
		if t.cancel != nil {
			t.cancel()
		}
	}
	p.mu.Unlock()
	if srv != nil {
		_ = srv.Close()
	}
}

func (p *pairHub) auth(next func(w http.ResponseWriter, r *http.Request, dev pairedDevice)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		sum := sha256.Sum256([]byte(tok))
		h := hex.EncodeToString(sum[:])
		p.mu.Lock()
		var dev *pairedDevice
		for i := range p.devices {
			if subtle.ConstantTimeCompare([]byte(p.devices[i].TokenHash), []byte(h)) == 1 {
				d := p.devices[i]
				dev = &d
			}
		}
		p.mu.Unlock()
		if tok == "" || dev == nil {
			pairJSON(w, 401, map[string]string{"error": "This phone isn't paired with this PC any more. Pair again."})
			return
		}
		next(w, r, *dev)
	}
}

func pairJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func pairRead(w http.ResponseWriter, r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v)
}

func pairPCName() string {
	h, _ := os.Hostname()
	if h == "" {
		h = "This PC"
	}
	return h
}

func (p *pairHub) hPair(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if err := pairRead(w, r, &in); err != nil {
		pairJSON(w, 400, map[string]string{"error": "bad request"})
		return
	}
	p.mu.Lock()
	ok := p.code != "" && time.Now().Before(p.codeExp) && subtle.ConstantTimeCompare([]byte(p.code), []byte(strings.TrimSpace(in.Code))) == 1
	if !ok {
		p.badTries++
		if p.badTries >= 5 { // someone is guessing: kill the code, the user can make a new one
			p.code = ""
		}
		p.mu.Unlock()
		pairJSON(w, 403, map[string]string{"error": "That pairing code is wrong or expired. Show a new QR code on the PC and scan again."})
		return
	}
	tok := randHex(32)
	sum := sha256.Sum256([]byte(tok))
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "Phone"
	}
	if len(name) > 60 {
		name = name[:60]
	}
	dev := pairedDevice{ID: randHex(6), Name: name, TokenHash: hex.EncodeToString(sum[:]), PairedAt: time.Now()}
	p.devices = append(p.devices, dev)
	p.code, p.badTries = "", 0
	p.saveDevices()
	p.mu.Unlock()
	p.app.emit("paired", name)
	pairJSON(w, 200, map[string]string{"token": tok, "device_id": dev.ID, "pc_name": pairPCName()})
}

func (p *pairHub) hPing(w http.ResponseWriter, r *http.Request, dev pairedDevice) {
	pairJSON(w, 200, map[string]string{"pc_name": pairPCName(), "version": version, "os": p.app.info.OSName})
}

func (p *pairHub) hUnpair(w http.ResponseWriter, r *http.Request, dev pairedDevice) {
	p.removeDevice(dev.ID)
	pairJSON(w, 200, map[string]bool{"ok": true})
}

func (p *pairHub) removeDevice(id string) {
	p.mu.Lock()
	out := p.devices[:0]
	for _, d := range p.devices {
		if d.ID != id {
			out = append(out, d)
		}
	}
	p.devices = out
	delete(p.hist, id)
	delete(p.lastPoll, id)
	p.saveDevices()
	p.mu.Unlock()
}

// ---------- phone → PC: tasks ----------

func (t *phoneTask) touch() {
	close(t.changed)
	t.changed = make(chan struct{})
}

func (t *phoneTask) step(s string) {
	t.mu.Lock()
	t.steps = append(t.steps, s)
	t.touch()
	t.mu.Unlock()
}

func (p *pairHub) hTask(w http.ResponseWriter, r *http.Request, dev pairedDevice) {
	var in struct {
		Text  string `json:"text"`
		Fresh bool   `json:"fresh"` // start a new conversation
	}
	if err := pairRead(w, r, &in); err != nil || strings.TrimSpace(in.Text) == "" {
		pairJSON(w, 400, map[string]string{"error": "Send the request in text."})
		return
	}
	t := &phoneTask{id: randHex(8), device: dev.ID, status: "running", changed: make(chan struct{}), created: time.Now()}
	p.mu.Lock()
	for id, old := range p.tasks { // forget finished tasks after an hour
		if old.status != "running" && time.Since(old.created) > time.Hour {
			delete(p.tasks, id)
		}
	}
	p.tasks[t.id] = t
	if in.Fresh {
		delete(p.hist, dev.ID)
	}
	p.mu.Unlock()
	go p.runTask(t, dev, in.Text)
	pairJSON(w, 200, map[string]string{"id": t.id})
}

type taskView struct {
	Status  string       `json:"status"`
	Steps   []string     `json:"steps"`
	Text    string       `json:"text"`
	Confirm *taskConfirm `json:"confirm"`
}

// hTaskGet long-polls: it answers at once if anything is new since ?seen=N steps, else waits up to ?wait seconds.
func (p *pairHub) hTaskGet(w http.ResponseWriter, r *http.Request, dev pairedDevice) {
	p.mu.Lock()
	t := p.tasks[r.PathValue("id")]
	p.mu.Unlock()
	if t == nil || t.device != dev.ID {
		pairJSON(w, 404, map[string]string{"error": "No such task."})
		return
	}
	seen, _ := strconv.Atoi(r.URL.Query().Get("seen"))
	confirmSeen := r.URL.Query().Get("confirm")
	wait, _ := strconv.Atoi(r.URL.Query().Get("wait"))
	if wait <= 0 || wait > 30 {
		wait = 25
	}
	deadline := time.After(time.Duration(wait) * time.Second)
	for {
		t.mu.Lock()
		fresh := len(t.steps) > seen || t.status != "running" || (t.confirm != nil && t.confirm.ID != confirmSeen)
		ch := t.changed
		v := taskView{Status: t.status, Steps: append([]string(nil), t.steps...), Text: t.text, Confirm: t.confirm}
		t.mu.Unlock()
		if fresh {
			pairJSON(w, 200, v)
			return
		}
		select {
		case <-ch:
		case <-deadline:
			pairJSON(w, 200, v)
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (p *pairHub) hTaskStop(w http.ResponseWriter, r *http.Request, dev pairedDevice) {
	p.mu.Lock()
	t := p.tasks[r.PathValue("id")]
	p.mu.Unlock()
	if t != nil && t.device == dev.ID && t.cancel != nil {
		t.cancel()
	}
	pairJSON(w, 200, map[string]bool{"ok": true})
}

func (p *pairHub) hConfirm(w http.ResponseWriter, r *http.Request, dev pairedDevice) {
	var in struct {
		Task string `json:"task"`
		ID   string `json:"id"`
		Yes  bool   `json:"yes"`
	}
	if err := pairRead(w, r, &in); err != nil {
		pairJSON(w, 400, map[string]string{"error": "bad request"})
		return
	}
	p.mu.Lock()
	t := p.tasks[in.Task]
	p.mu.Unlock()
	if t == nil || t.device != dev.ID {
		pairJSON(w, 404, map[string]string{"error": "No such task."})
		return
	}
	t.mu.Lock()
	c := t.confirm
	t.mu.Unlock()
	if c != nil && c.ID == in.ID {
		select {
		case c.answer <- in.Yes:
		default:
		}
	}
	pairJSON(w, 200, map[string]bool{"ok": true})
}

// runTask is a Blaze turn on this PC for the phone: the same tools, connectors and memory
// as the app's chat, its own conversation per phone, and approvals asked on the phone
// instead of the PC screen.
func (p *pairHub) runTask(t *phoneTask, dev pairedDevice, text string) {
	a := p.app
	base := a.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, 30*time.Minute)
	defer cancel()
	t.mu.Lock()
	t.cancel = cancel
	t.mu.Unlock()
	finish := func(status, out string) {
		t.mu.Lock()
		t.status, t.text, t.confirm = status, out, nil
		t.touch()
		t.mu.Unlock()
		a.emit("status", "")
	}
	defer func() {
		if r := recover(); r != nil {
			finish("error", fmt.Sprint("Blaze on the PC hit an internal error: ", r))
		}
	}()
	a.emit("status", dev.Name+" asked: "+firstLine(text))

	env := p.taskEnv(t, ctx)
	conv := "phone:" + dev.ID
	env.Conv = func() string { return conv }

	p.mu.Lock()
	hist := append(append([]llm.Message(nil), p.hist[dev.ID]...), llm.Message{Role: "user", Content: text})
	p.mu.Unlock()
	sys := a.system(text) + tools.ExtraLine(conv) + "\nTHIS REQUEST CAME FROM THE USER'S PHONE (" + dev.Name + "), paired with this PC over Blaze Link. They may not be at the computer: do the work here, then answer in a few short sentences that read well on a phone. Approvals are asked on the phone.\n"

	steps := 40
	if a.cfg != nil && a.cfg.MaxSteps > 0 {
		steps = a.cfg.MaxSteps
	}
	save := func() {
		p.mu.Lock()
		p.hist[dev.ID] = trim(hist)
		p.mu.Unlock()
	}
	for step := 0; step < steps; step++ {
		var all []llm.Tool
		for _, tl := range tools.Select(conv) {
			if tl.Function.Name != "phone_ask" { // it's already on the phone
				all = append(all, tl)
			}
		}
		if a.conn != nil {
			all = append(all, a.conn.Tools()...)
		}
		msgs := append([]llm.Message{{Role: "system", Content: sys}}, trim(hist)...)
		r, err := llm.CompleteWith(ctx, a.cfg, msgs, all, llm.Opts{OnStatus: func(s string) { t.step(s) }})
		if err != nil {
			save()
			if ctx.Err() != nil {
				finish("error", "Stopped.")
			} else {
				finish("error", err.Error())
			}
			return
		}
		n := llm.NormalizeCalls(r.ToolCalls, append(tools.Names(), p.connKnown()...))
		hist = append(hist, llm.Message{Role: "assistant", Content: r.Text, ToolCalls: n.Calls})
		if len(n.Calls) == 0 {
			save()
			finish("done", strings.TrimSpace(r.Text))
			return
		}
		for _, tc := range n.Calls {
			if ctx.Err() != nil {
				save()
				finish("error", "Stopped.")
				return
			}
			t.step(stepLabel(tc.Function.Name, tc.Function.Arguments))
			var res string
			if a.conn != nil && a.conn.Handles(tc.Function.Name) {
				res = a.conn.Run(ctx, tc.Function.Name, tc.Function.Arguments)
			} else {
				res = tools.RunCtx(ctx, env, tc.Function.Name, tc.Function.Arguments)
			}
			hist = append(hist, llm.Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: res})
		}
	}
	save()
	finish("error", fmt.Sprintf("Stopped after %d steps without finishing. Say \"continue\" to keep going.", steps))
}

func (p *pairHub) connKnown() []string {
	if p.app.conn == nil {
		return nil
	}
	return p.app.conn.Known()
}

// taskEnv is the tool environment for one phone task: the app's shell profile and jobs,
// progress lines to the phone and approvals asked on the phone.
func (p *pairHub) taskEnv(t *phoneTask, ctx context.Context) *tools.Env {
	a := p.app
	root, sh := a.prefs.Folder, a.info.Shell
	if root == "" {
		root = a.info.Home
	}
	var jobs *tools.Jobs
	if a.env != nil {
		jobs = a.env.Jobs
	}
	if jobs == nil {
		jobs = &tools.Jobs{}
	}
	env := &tools.Env{Info: &a.info, Root: root, Shell: tools.NewShell(sh, a.info.ShellPath, root), Jobs: jobs, Ctx: ctx}
	env.Status = func(s string) { t.step(s) }
	env.Confirm = func(title, detail string) bool {
		c := &taskConfirm{ID: randHex(6), Title: title, Detail: detail, answer: make(chan bool, 1)}
		t.mu.Lock()
		t.confirm = c
		t.touch()
		t.mu.Unlock()
		defer func() { t.mu.Lock(); t.confirm = nil; t.touch(); t.mu.Unlock() }()
		select {
		case v := <-c.answer:
			return v
		case <-time.After(5 * time.Minute):
			return false
		case <-ctx.Done():
			return false
		}
	}
	return env
}

// stepLabel is the one-line progress the phone shows for a tool call (the same plain
// words as the tray status line).
func stepLabel(name, raw string) string { return friendlyTool(name, raw) }

// ---------- PC → phone: phone_ask ----------

func (p *pairHub) hInbox(w http.ResponseWriter, r *http.Request, dev pairedDevice) {
	wait, _ := strconv.Atoi(r.URL.Query().Get("wait"))
	if wait <= 0 || wait > 30 {
		wait = 25
	}
	deadline := time.After(time.Duration(wait) * time.Second)
	for {
		p.mu.Lock()
		p.lastPoll[dev.ID] = time.Now()
		var out []*phoneReq
		for _, q := range p.inbox {
			if !q.taken {
				q.taken = true
				out = append(out, q)
			}
		}
		sig := p.inboxSig
		p.mu.Unlock()
		if len(out) > 0 {
			pairJSON(w, 200, map[string]any{"requests": out})
			return
		}
		select {
		case <-sig:
		case <-deadline:
			pairJSON(w, 200, map[string]any{"requests": []any{}})
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (p *pairHub) hReply(w http.ResponseWriter, r *http.Request, dev pairedDevice) {
	var in struct {
		Text string `json:"text"`
	}
	if err := pairRead(w, r, &in); err != nil {
		pairJSON(w, 400, map[string]string{"error": "bad request"})
		return
	}
	id := r.PathValue("id")
	p.mu.Lock()
	for _, q := range p.inbox {
		if q.ID == id {
			select {
			case q.reply <- in.Text:
			default:
			}
		}
	}
	p.mu.Unlock()
	pairJSON(w, 200, map[string]bool{"ok": true})
}

// connectedPhone is the name of a paired phone that polled recently, or "".
func (p *pairHub) connectedPhone() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, d := range p.devices {
		if time.Since(p.lastPoll[d.ID]) < pollWindow {
			return d.Name
		}
	}
	return ""
}

// askPhone is the phone_ask tool: hand the request to the phone and wait for its answer.
func (p *pairHub) askPhone(task string) string {
	p.mu.Lock()
	n := len(p.devices)
	p.mu.Unlock()
	if n == 0 {
		return "No phone is paired with this PC. The user can pair one in Blaze Desktop › Phone."
	}
	if p.connectedPhone() == "" {
		return "The paired phone isn't reachable right now. Blaze must be running on it, on the same Wi-Fi as this PC (or both on Tailscale). Tell the user that, and don't retry in a loop."
	}
	q := &phoneReq{ID: randHex(6), Text: task, reply: make(chan string, 1)}
	p.mu.Lock()
	p.inbox = append(p.inbox, q)
	close(p.inboxSig)
	p.inboxSig = make(chan struct{})
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		out := p.inbox[:0]
		for _, x := range p.inbox {
			if x != q {
				out = append(out, x)
			}
		}
		p.inbox = out
		p.mu.Unlock()
	}()
	select {
	case ans := <-q.reply:
		if strings.TrimSpace(ans) == "" {
			return "The phone finished but said nothing."
		}
		return "The phone answered: " + ans
	case <-time.After(10 * time.Minute):
		return "The phone didn't answer within 10 minutes (it may be waiting for the user to approve something there)."
	}
}

// ---------- bound to the UI ----------

type DeviceView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	PairedAt  string `json:"paired_at"`
	Connected bool   `json:"connected"`
}

type PairStatus struct {
	Devices   []DeviceView `json:"devices"`
	Addresses []string     `json:"addresses"`
	Port      int          `json:"port"`
	Error     string       `json:"error"`
}

type PairInfo struct {
	QR        string   `json:"qr"` // data: URL of a PNG
	Link      string   `json:"link"`
	Addresses []string `json:"addresses"`
	Expires   int64    `json:"expires"` // unix seconds
	Error     string   `json:"error"`
}

func (a *App) PairStatus() PairStatus {
	p := a.pair
	p.mu.Lock()
	st := PairStatus{Addresses: pairAddrs(), Port: pairPort, Error: p.errText, Devices: []DeviceView{}}
	for _, d := range p.devices {
		st.Devices = append(st.Devices, DeviceView{ID: d.ID, Name: d.Name, PairedAt: d.PairedAt.Format("2 Jan 2006"), Connected: time.Since(p.lastPoll[d.ID]) < pollWindow})
	}
	p.mu.Unlock()
	return st
}

// StartPairing makes a fresh one-time code and returns the QR code for the phone to scan.
func (a *App) StartPairing() PairInfo {
	p := a.pair
	p.ensureServer()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.srv == nil {
		return PairInfo{Error: p.errText}
	}
	addrs := pairAddrs()
	if len(addrs) == 0 {
		return PairInfo{Error: "This PC isn't on a network. Connect to Wi-Fi (or Tailscale) and try again."}
	}
	p.code, p.codeExp, p.badTries = randHex(12), time.Now().Add(pairCodeLife), 0
	q := url.Values{}
	q.Set("v", "1")
	q.Set("h", strings.Join(addrs, ","))
	q.Set("p", strconv.Itoa(pairPort))
	q.Set("fp", p.fp)
	q.Set("c", p.code)
	q.Set("n", pairPCName())
	link := "blaze://pair?" + q.Encode()
	img, err := pairQR(link)
	if err != nil {
		return PairInfo{Error: "Couldn't draw the QR code: " + err.Error()}
	}
	return PairInfo{QR: img, Link: link, Addresses: addrs, Expires: p.codeExp.Unix()}
}

// pairQR draws link as a PNG data: URL (rsc.io/qr, already used by the phone preview).
func pairQR(link string) (string, error) {
	c, err := qr.Encode(link, qr.M)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := png.Encode(&b, qrImage(c, 8)); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes()), nil
}

func (a *App) StopPairing() {
	a.pair.mu.Lock()
	a.pair.code = ""
	a.pair.mu.Unlock()
}

func (a *App) Unpair(id string) { a.pair.removeDevice(id) }

// localAddrs lists this PC's IPv4 addresses a phone could reach: home/office networks first, then Tailscale.
func pairAddrs() []string {
	var lan, ts []string
	ifs, _ := net.Interfaces()
	for _, in := range ifs {
		if in.Flags&net.FlagUp == 0 || in.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := in.Addrs()
		for _, ad := range addrs {
			ipn, ok := ad.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP.To4()
			if ip == nil || ip.IsLinkLocalUnicast() {
				continue
			}
			if ip[0] == 100 && ip[1]&0xC0 == 64 { // 100.64.0.0/10: Tailscale / CGNAT
				ts = append(ts, ip.String())
			} else if ip.IsPrivate() {
				lan = append(lan, ip.String())
			}
		}
	}
	return append(lan, ts...)
}

// deviceNames lists the paired phones for the system prompt.
func (p *pairHub) deviceNames() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var n []string
	for _, d := range p.devices {
		n = append(n, d.Name)
	}
	return strings.Join(n, ", ")
}
