package router

// The HTTP side: OpenAI-compatible /v1 (chat, models, embeddings), Anthropic's
// /v1/messages, Gemini's /v1beta/models/{model}:generateContent, the dashboard and
// its JSON API.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"blaze-desktop/internal/config"
	"blaze-desktop/internal/llm"
)

const maxBody = 64 << 20

// Handler is the whole server (exported for tests).
func (r *Router) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", r.handleChat)
	mux.HandleFunc("/chat/completions", r.handleChat)
	mux.HandleFunc("/v1/models", r.handleModels)
	mux.HandleFunc("/models", r.handleModels)
	mux.HandleFunc("/v1/embeddings", r.handleEmbeddings)
	mux.HandleFunc("/v1/messages", r.handleAnthropic)
	mux.HandleFunc("/v1/messages/count_tokens", r.handleCountTokens)
	mux.HandleFunc("/v1beta/models/", r.handleGemini)
	mux.HandleFunc("/health", func(w http.ResponseWriter, req *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "name": "BlazeRouter", "version": r.opt.Version})
	})
	mux.HandleFunc("/api/", r.handleAPI)
	mux.HandleFunc("/dashboard", func(w http.ResponseWriter, req *http.Request) { http.Redirect(w, req, "/dashboard/", http.StatusFound) })
	mux.Handle("/dashboard/", http.StripPrefix("/dashboard/", dashboardFiles()))
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/" {
			http.Redirect(w, req, "/dashboard/", http.StatusFound)
			return
		}
		writeJSON(w, 404, oaError(404, "Not found: "+req.URL.Path))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !localHost(req.Host) {
			http.Error(w, "BlazeRouter only answers requests addressed to this computer", http.StatusForbidden)
			return
		}
		api := strings.HasPrefix(req.URL.Path, "/v1") || req.URL.Path == "/chat/completions" || req.URL.Path == "/models"
		if api {
			// Any local app (a browser-based client too) may call the API with the key.
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, x-api-key, x-goog-api-key, anthropic-version, anthropic-beta")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			if req.Method == http.MethodOptions {
				w.WriteHeader(204)
				return
			}
			if !r.authorized(req) {
				msg := "BlazeRouter needs its API key: Authorization: Bearer <key>. Copy it from the dashboard (Blaze's top bar › BlazeRouter)."
				switch {
				case strings.HasPrefix(req.URL.Path, "/v1/messages"):
					writeJSON(w, 401, anthropicError(401, msg))
				case strings.HasPrefix(req.URL.Path, "/v1beta"):
					writeJSON(w, 401, geminiError(401, msg))
				default:
					writeJSON(w, 401, oaError(401, msg))
				}
				return
			}
		}
		mux.ServeHTTP(w, req)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func oaError(code int, msg string) map[string]any {
	t := "api_error"
	switch code {
	case 400:
		t = "invalid_request_error"
	case 401:
		t = "authentication_error"
	case 404:
		t = "not_found_error"
	case 429:
		t = "rate_limit_exceeded"
	}
	return map[string]any{"error": map[string]any{"message": msg, "type": t, "code": code}}
}

func readBody(req *http.Request) (map[string]any, error) {
	if req.Method != http.MethodPost {
		return nil, errors.New("use POST")
	}
	var body map[string]any
	dec := json.NewDecoder(io.LimitReader(req.Body, maxBody))
	if err := dec.Decode(&body); err != nil {
		return nil, fmt.Errorf("the request body isn't valid JSON: %v", err)
	}
	return body, nil
}

func flush(w http.ResponseWriter) { _ = http.NewResponseController(w).Flush() }

func routeHeaders(w http.ResponseWriter, up *upstream) {
	w.Header().Set("X-BlazeRouter-Provider", up.t.Provider)
	w.Header().Set("X-BlazeRouter-Model", up.t.Model)
	if up.plan.Combo != "" {
		w.Header().Set("X-BlazeRouter-Combo", up.plan.Combo)
	}
}

// logFail records a request no target could take.
func (r *Router) logFail(asked, combo, client, format string, code int, err error, stream bool) {
	e := Entry{Time: time.Now(), Asked: asked, Combo: combo, Status: code, Error: trunc(err.Error(), 200), Client: client, Format: format, Stream: stream}
	var re *RouteError
	if errors.As(err, &re) {
		e.Route = re.Attempts
	}
	r.log.add(e)
}

func errCode(err error) int {
	var re *RouteError
	if errors.As(err, &re) {
		return re.Code
	}
	if errors.Is(err, ErrNoModel) {
		return 404
	}
	return 502
}

// route plans, compresses and dispatches an OpenAI-format request.
func (r *Router) route(ctx context.Context, body map[string]any, stream bool) (*upstream, Plan, error) {
	asked := str(body["model"])
	pl, err := r.plan(asked, len(list(body["tools"])) > 0)
	if err != nil {
		return nil, pl, fmt.Errorf("model %q isn't one BlazeRouter knows: use auto, a combo, or provider/model (GET /v1/models lists them): %w", asked, err)
	}
	saved := Compress(body, r.Settings().Compression)
	up, err := r.dispatch(ctx, body, pl, stream)
	if up != nil {
		up.saved = saved
	}
	return up, pl, err
}

// ---------------- OpenAI chat ----------------

func (r *Router) handleChat(w http.ResponseWriter, req *http.Request) {
	body, err := readBody(req)
	if err != nil {
		writeJSON(w, 400, oaError(400, err.Error()))
		return
	}
	client := clientName(req.UserAgent())
	stream, _ := body["stream"].(bool)
	asked := str(body["model"])
	up, pl, err := r.route(req.Context(), body, stream)
	if err != nil {
		code := errCode(err)
		r.logFail(asked, pl.Combo, client, "openai", code, err, stream)
		writeJSON(w, code, oaError(code, err.Error()))
		return
	}
	defer up.resp.Body.Close()
	routeHeaders(w, up)
	tl := &tally{}
	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(200)
		if !isSSE(up.resp) {
			// The provider answered in one piece: send it as one chunk.
			res, perr := parseUpstream(up, nil)
			if res != nil {
				tl.usage = res.Usage
				tl.outChars = len(res.Text)
				writeChunks(w, res, up.t.Model)
			}
			r.finish(up, tl, 200, perr, client, "openai", true)
			return
		}
		sc := bufio.NewScanner(io.MultiReader(bytes.NewReader(up.first), up.resp.Body))
		sc.Buffer(make([]byte, 64<<10), 16<<20)
		for sc.Scan() {
			line := sc.Bytes()
			if t := bytes.TrimSpace(line); bytes.HasPrefix(t, []byte("data:")) {
				if d := bytes.TrimSpace(t[5:]); !bytes.Equal(d, []byte("[DONE]")) {
					tl.add(d)
				}
			}
			_, _ = w.Write(append(line, '\n'))
			if len(bytes.TrimSpace(line)) == 0 {
				flush(w)
			}
		}
		flush(w)
		r.finish(up, tl, 200, sc.Err(), client, "openai", true)
		return
	}
	if isSSE(up.resp) {
		res, perr := parseUpstream(up, nil)
		if perr != nil && res == nil {
			r.finish(up, tl, 502, perr, client, "openai", false)
			writeJSON(w, 502, oaError(502, perr.Error()))
			return
		}
		tl.usage, tl.outChars = res.Usage, len(res.Text)
		writeJSON(w, 200, completion(res, up.t.Model))
		r.finish(up, tl, 200, nil, client, "openai", false)
		return
	}
	raw, rerr := io.ReadAll(io.LimitReader(up.resp.Body, maxBody))
	tl.add(raw)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(raw)
	r.finish(up, tl, 200, rerr, client, "openai", false)
}

// parseUpstream reads a whole answer (stream or JSON) into a Result.
func parseUpstream(up *upstream, onText func(string)) (*llm.Result, error) {
	rd := io.MultiReader(bytes.NewReader(up.first), up.resp.Body)
	if isSSE(up.resp) {
		return llm.ParseStream(rd, onText, nil)
	}
	raw, err := io.ReadAll(io.LimitReader(rd, maxBody))
	if err != nil {
		return nil, err
	}
	return llm.ParseStream(strings.NewReader("data: "+string(bytes.ReplaceAll(raw, []byte("\n"), nil))+"\n"), onText, nil)
}

func finishReason(res *llm.Result) string {
	if len(res.ToolCalls) > 0 {
		return "tool_calls"
	}
	if res.Finish != "" {
		return res.Finish
	}
	return "stop"
}

func completion(res *llm.Result, model string) map[string]any {
	msg := map[string]any{"role": "assistant", "content": res.Text}
	if len(res.ToolCalls) > 0 {
		msg["tool_calls"] = res.ToolCalls
	}
	out := map[string]any{"id": "chatcmpl-br" + fmt.Sprint(time.Now().UnixNano()), "object": "chat.completion", "created": time.Now().Unix(), "model": model,
		"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finishReason(res)}}}
	if res.Usage != nil {
		out["usage"] = map[string]any{"prompt_tokens": res.Usage.Prompt, "completion_tokens": res.Usage.Completion, "total_tokens": res.Usage.Prompt + res.Usage.Completion}
	}
	return out
}

func writeChunks(w http.ResponseWriter, res *llm.Result, model string) {
	id := "chatcmpl-br" + fmt.Sprint(time.Now().UnixNano())
	send := func(delta map[string]any, finish any) {
		b, _ := json.Marshal(map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\n", b)
	}
	d := map[string]any{"role": "assistant", "content": res.Text}
	if len(res.ToolCalls) > 0 {
		var calls []any
		for i, c := range res.ToolCalls {
			calls = append(calls, map[string]any{"index": i, "id": c.ID, "type": "function", "function": map[string]any{"name": c.Function.Name, "arguments": c.Function.Arguments}})
		}
		d["tool_calls"] = calls
	}
	send(d, nil)
	send(map[string]any{}, finishReason(res))
	fmt.Fprint(w, "data: [DONE]\n\n")
	flush(w)
}

// ---------------- models ----------------

type modelEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
	Name    string `json:"name,omitempty"`
	Kind    string `json:"blazerouter_kind,omitempty"` // combo, model
	Free    bool   `json:"free,omitempty"`
	Tools   string `json:"tools,omitempty"`
}

// ModelList is every model a client can ask for: the auto combos, the user's combos,
// then provider/model for each provider that has a key.
func (r *Router) ModelList() []modelEntry {
	now := time.Now().Unix()
	var out []modelEntry
	for _, p := range AutoPresets {
		name := "Auto"
		if p.ID != "auto" {
			name = "Auto " + p.Name
		}
		out = append(out, modelEntry{ID: p.ID, Object: "model", Created: now, OwnedBy: "blazerouter", Name: name + ": " + p.Desc, Kind: "combo", Free: true})
	}
	for _, c := range r.Settings().Combos {
		out = append(out, modelEntry{ID: c.ID, Object: "model", Created: now, OwnedBy: "blazerouter", Name: c.Name, Kind: "combo"})
	}
	cfg := r.cfg()
	for _, p := range routable() {
		if !llm.Usable(cfg, p.ID) {
			continue
		}
		seen := map[string]bool{}
		add := func(m string, mi llm.ModelInfo) {
			if m == "" || seen[m] {
				return
			}
			seen[m] = true
			out = append(out, modelEntry{ID: p.ID + "/" + m, Object: "model", Created: now, OwnedBy: p.ID, Name: firstOf(mi.Name, m), Kind: "model", Free: mi.Free || p.Free, Tools: string(mi.Tools)})
		}
		add(cfg.Model(p.ID), llm.Info(p.ID, cfg.Model(p.ID)))
		for _, mi := range llm.Catalog[p.ID] {
			add(mi.ID, mi)
		}
	}
	return out
}

func firstOf(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

func (r *Router) handleModels(w http.ResponseWriter, req *http.Request) {
	writeJSON(w, 200, map[string]any{"object": "list", "data": r.ModelList()})
}

// ---------------- embeddings ----------------

// embedDefaults are the embedding models of providers whose OpenAI layer serves /embeddings.
var embedDefaults = []struct{ provider, model string }{
	{"gemini", "gemini-embedding-001"}, {"mistral", "mistral-embed"}, {"github-models", "openai/text-embedding-3-small"},
	{"openai", "text-embedding-3-small"}, {"nvidia", "nvidia/nv-embedqa-e5-v5"}, {"ollama", "nomic-embed-text"},
}

func (r *Router) handleEmbeddings(w http.ResponseWriter, req *http.Request) {
	body, err := readBody(req)
	if err != nil {
		writeJSON(w, 400, oaError(400, err.Error()))
		return
	}
	cfg := r.cfg()
	asked := strings.TrimPrefix(str(body["model"]), "blazerouter/")
	var ts []Target
	if ok, _ := isAuto(asked); ok {
		for _, d := range embedDefaults {
			if llm.Usable(cfg, d.provider) && (d.provider != "ollama" || inChain(cfg, "ollama")) {
				ts = append(ts, Target{Provider: d.provider, Model: d.model, Name: config.Name(d.provider)})
			}
		}
	} else if i := strings.Index(asked, "/"); i > 0 && llm.Usable(cfg, asked[:i]) {
		ts = append(ts, Target{Provider: asked[:i], Model: asked[i+1:], Name: config.Name(asked[:i])})
	} else {
		for _, d := range embedDefaults {
			if d.model == asked && llm.Usable(cfg, d.provider) {
				ts = append(ts, Target{Provider: d.provider, Model: d.model})
			}
		}
	}
	client := clientName(req.UserAgent())
	if len(ts) == 0 {
		err := errors.New("no embedding model available: add a Gemini, Mistral, GitHub Models or OpenAI key, or ask for provider/model")
		r.logFail(asked, "", client, "embeddings", 404, err, false)
		writeJSON(w, 404, oaError(404, err.Error()))
		return
	}
	var attempts []Attempt
	for _, t := range ts {
		p, _ := config.ProviderByID(t.Provider)
		key := ""
		if ks := cfg.AllKeys(t.Provider); len(ks) > 0 {
			key = ks[0]
		}
		b := map[string]any{}
		for k, v := range body {
			b[k] = v
		}
		b["model"] = t.Model
		if t.Provider == "nvidia" && b["input_type"] == nil {
			b["input_type"] = "query"
		}
		raw, _ := json.Marshal(b)
		t0 := time.Now()
		hreq, _ := http.NewRequestWithContext(req.Context(), "POST", strings.TrimRight(p.BaseURL, "/")+"/embeddings", bytes.NewReader(raw))
		hreq.Header.Set("Content-Type", "application/json")
		for k, v := range llm.AuthHeaders(p.ID, key) {
			hreq.Header.Set(k, v)
		}
		resp, err := upstreamClient.Do(hreq)
		if err != nil {
			attempts = append(attempts, Attempt{t.Provider, t.Model, 0, trunc(err.Error(), 200)})
			continue
		}
		out, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			attempts = append(attempts, Attempt{t.Provider, t.Model, resp.StatusCode, errText(out)})
			continue
		}
		var u struct {
			Usage struct {
				Prompt int `json:"prompt_tokens"`
			} `json:"usage"`
		}
		_ = json.Unmarshal(out, &u)
		in := u.Usage.Prompt
		if in == 0 {
			in = llm.Estimate(len(raw))
		}
		r.log.add(Entry{Time: time.Now(), Asked: asked, Provider: t.Provider, Model: t.Model, Status: 200, Ms: time.Since(t0).Milliseconds(), In: in, Client: client, Format: "embeddings", Fallbacks: len(attempts), Route: attempts})
		w.Header().Set("X-BlazeRouter-Provider", t.Provider)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write(out)
		return
	}
	err = &RouteError{Code: 502, Msg: "BlazeRouter: no embedding model could answer", Attempts: attempts}
	r.logFail(asked, "", client, "embeddings", 502, err, false)
	writeJSON(w, 502, oaError(502, err.Error()))
}

func inChain(cfg *config.Config, pid string) bool {
	for _, e := range cfg.Chain {
		if e.Provider == pid {
			return true
		}
	}
	return false
}

// ---------------- Anthropic ----------------

func (r *Router) handleAnthropic(w http.ResponseWriter, req *http.Request) {
	in, err := readBody(req)
	if err != nil {
		writeJSON(w, 400, anthropicError(400, err.Error()))
		return
	}
	stream, _ := in["stream"].(bool)
	model := str(in["model"])
	body := fromAnthropic(in)
	// Claude Code asks for claude-* models by name: those route through auto unless
	// the user has a provider that serves that exact id.
	if strings.HasPrefix(model, "claude-") {
		if _, err := r.plan(model, false); err != nil {
			body["model"] = "auto"
		}
	}
	client := clientName(req.UserAgent())
	up, pl, err := r.route(req.Context(), body, stream)
	if err != nil {
		code := errCode(err)
		r.logFail(model, pl.Combo, client, "anthropic", code, err, stream)
		writeJSON(w, code, anthropicError(code, err.Error()))
		return
	}
	defer up.resp.Body.Close()
	routeHeaders(w, up)
	tl := &tally{}
	if !stream {
		res, perr := parseUpstream(up, nil)
		if res == nil {
			r.finish(up, tl, 502, perr, client, "anthropic", false)
			writeJSON(w, 502, anthropicError(502, perr.Error()))
			return
		}
		tl.usage, tl.outChars = res.Usage, len(res.Text)
		writeJSON(w, 200, toAnthropic(res, model, up.chars))
		r.finish(up, tl, 200, nil, client, "anthropic", false)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	as := &anthropicStream{model: model, emit: func(ev string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev, b)
		flush(w)
	}}
	as.start()
	res, perr := parseUpstream(up, func(s string) { as.text(s) })
	if res == nil {
		res = &llm.Result{}
	}
	as.finish(res, up.chars)
	tl.usage, tl.outChars = res.Usage, len(res.Text)
	r.finish(up, tl, 200, perr, client, "anthropic", true)
}

func (r *Router) handleCountTokens(w http.ResponseWriter, req *http.Request) {
	in, err := readBody(req)
	if err != nil {
		writeJSON(w, 400, anthropicError(400, err.Error()))
		return
	}
	b, _ := json.Marshal(fromAnthropic(in))
	writeJSON(w, 200, map[string]any{"input_tokens": llm.Estimate(len(b))})
}

// ---------------- Gemini ----------------

func (r *Router) handleGemini(w http.ResponseWriter, req *http.Request) {
	rest := strings.TrimPrefix(req.URL.Path, "/v1beta/models/")
	model, action, _ := strings.Cut(rest, ":")
	if req.Method == http.MethodGet && action == "" {
		writeJSON(w, 200, map[string]any{"name": "models/" + model, "displayName": model, "supportedGenerationMethods": []string{"generateContent", "streamGenerateContent"}})
		return
	}
	if action != "generateContent" && action != "streamGenerateContent" {
		writeJSON(w, 404, geminiError(404, "BlazeRouter supports generateContent and streamGenerateContent"))
		return
	}
	in, err := readBody(req)
	if err != nil {
		writeJSON(w, 400, geminiError(400, err.Error()))
		return
	}
	stream := action == "streamGenerateContent"
	body := fromGemini(in, model)
	client := clientName(req.UserAgent())
	up, pl, err := r.route(req.Context(), body, stream)
	if err != nil {
		code := errCode(err)
		r.logFail(model, pl.Combo, client, "gemini", code, err, stream)
		writeJSON(w, code, geminiError(code, err.Error()))
		return
	}
	defer up.resp.Body.Close()
	routeHeaders(w, up)
	tl := &tally{}
	if !stream {
		res, perr := parseUpstream(up, nil)
		if res == nil {
			r.finish(up, tl, 502, perr, client, "gemini", false)
			writeJSON(w, 502, geminiError(502, perr.Error()))
			return
		}
		tl.usage, tl.outChars = res.Usage, len(res.Text)
		writeJSON(w, 200, toGemini(res, model, up.chars, true))
		r.finish(up, tl, 200, nil, client, "gemini", false)
		return
	}
	sse := req.URL.Query().Get("alt") == "sse"
	if sse {
		w.Header().Set("Content-Type", "text/event-stream")
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(200)
	n := 0
	emit := func(v map[string]any) {
		b, _ := json.Marshal(v)
		if sse {
			fmt.Fprintf(w, "data: %s\n\n", b)
		} else {
			if n == 0 {
				fmt.Fprint(w, "[")
			} else {
				fmt.Fprint(w, ",\n")
			}
			_, _ = w.Write(b)
		}
		n++
		flush(w)
	}
	res, perr := parseUpstream(up, func(s string) { emit(toGemini(&llm.Result{Text: s}, model, 0, false)) })
	if res == nil {
		res = &llm.Result{}
	}
	final := toGemini(&llm.Result{ToolCalls: res.ToolCalls, Usage: res.Usage, Finish: res.Finish}, model, up.chars, true)
	emit(final)
	if !sse {
		fmt.Fprint(w, "]")
	}
	tl.usage, tl.outChars = res.Usage, len(res.Text)
	r.finish(up, tl, 200, perr, client, "gemini", true)
}
