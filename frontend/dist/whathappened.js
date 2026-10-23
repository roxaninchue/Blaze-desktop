// "What happened" under each reply (v0.19.7): the turn id that matches the gateway's logs, time to
// first word, each step's model and service, every Blaze Cloud attempt (retries, hedges, errors),
// and the tools with their timings. Closed by default; for working out why a reply was odd.
(() => {
  const ms = v => v == null ? "" : v < 1000 ? `${Math.round(v)} ms` : `${(v / 1000).toFixed(2)} s`;
  const sec = v => v == null ? "" : v < 1 ? `${Math.round(v * 1000)} ms` : `${v.toFixed(2)} s`;
  const e = s => (typeof esc === "function" ? esc(String(s == null ? "" : s)) : String(s == null ? "" : s));

  // 0.19.16: never which provider or model answered, unless the developer flag is on.
  function clean(t) {
    const P = window.PRIVACY; if (!P || P.dev()) return t;
    const s = x => P.scrub(x || "");
    return Object.assign({}, t, { error: s(t.error),
      steps: (t.steps || []).map(x => Object.assign({}, x, { route: P.route(x.route || "Blaze"), provider: "", model: "", error: s(x.error), note: P.note(x.note) })),
      attempts: (t.attempts || []).map(x => Object.assign({}, x, { provider: "Blaze Cloud", model: "", error: s(x.error), server_timing: s(x.server_timing), lane: s(x.lane) })) });
  }
  function body(t) {
    t = clean(t);
    const rows = [];
    rows.push(`<div class="wh-kv"><span>Request ID</span><span class="mono">${e(t.turn)}</span><button class="btn ghost xs wh-copy" title="Copy the request ID">Copy</button></div>`);
    rows.push(`<div class="wh-kv"><span>First word</span><span>${t.ttft_ms ? ms(t.ttft_ms) : "none streamed"}</span></div>`);
    rows.push(`<div class="wh-kv"><span>Total</span><span>${sec(t.seconds)}</span></div>`);
    const ids = (t.attempts || []).map(a => a.request_id).filter(Boolean);
    if (ids.length) rows.push(`<div class="wh-kv"><span>Gateway IDs</span><span class="mono">${ids.map(e).join(", ")}</span></div>`);
    const st = stats();
    if (st) rows.push(`<div class="wh-kv"><span>Recent replies</span><span>first word p50 ${ms(st.p50)}, p95 ${ms(st.p95)} over ${st.n}</span></div>`);
    if (t.error) rows.push(`<div class="wh-kv bad"><span>Error</span><span>${e(t.error)}</span></div>`);
    const steps = (t.steps || []).map(s => `<tr><td>${s.n}</td><td>${e(s.route || s.provider || "")}${s.model && s.model !== s.route ? ` <span class="weak">${e(s.model)}</span>` : ""}</td><td>${s.ttft_ms ? ms(s.ttft_ms) : ""}</td><td>${sec(s.seconds)}</td><td>${s.tokens_in || s.tokens_out ? `${s.tokens_in || 0} → ${s.tokens_out || 0}` : ""}</td><td>${e(s.finish || "")}${s.error ? ` <span class="bad">${e(s.error)}</span>` : ""}${s.note ? ` <span class="weak">${e(s.note)}</span>` : ""}</td></tr>`).join("");
    if (steps) rows.push(`<div class="wh-h">Steps</div><table class="wh-t"><tr><th>#</th><th>Model</th><th>First word</th><th>Took</th><th>Tokens</th><th>Ended</th></tr>${steps}</table>`);
    const att = (t.attempts || []).map((a, i) => `<tr><td>${i + 1}</td><td>${e(a.provider || "")}${a.model ? ` · ${e(a.model)}` : ""}</td><td>${a.status || ""}</td><td>${a.ttfb_ms ? ms(a.ttfb_ms) : ""}</td><td>${sec(a.seconds)}</td><td>${[a.client_ttft_ms ? "first word " + ms(a.client_ttft_ms) : "", a.server_ttft_ms ? "gateway TTFT " + ms(a.server_ttft_ms) : "", a.lane ? a.lane + " lane" : "", a.fast_lane ? "fast lane" : "", a.fallback ? "after a fallback" : "", a.tools_dropped ? "tools dropped" : "", a.hedged ? "hedged" : "", a.replay ? "replayed" : "", a.finish || ""].filter(Boolean).join(", ")}${a.server_timing ? ` <span class="weak mono">${e(a.server_timing)}</span>` : ""}${a.error ? ` <span class="bad">${e(a.error)}</span>` : ""}</td></tr>`).join("");
    if (att) rows.push(`<div class="wh-h">Blaze Cloud requests</div><table class="wh-t"><tr><th>#</th><th>Provider · model</th><th>Status</th><th>Gateway first byte</th><th>Took</th><th>Notes</th></tr>${att}</table>`);
    const tools = (t.tools || []).map(x => `<tr><td>${x.step}</td><td class="mono">${e(x.name)}</td><td>${sec(x.seconds)}</td><td>${x.blocked ? "blocked" : x.error ? `<span class="bad">${e(x.error)}</span>` : "ok"}</td></tr>`).join("");
    rows.push(tools ? `<div class="wh-h">Tools</div><table class="wh-t"><tr><th>Step</th><th>Tool</th><th>Took</th><th>Result</th></tr>${tools}</table>` : `<div class="wh-h">Tools</div><div class="weak small">None</div>`);
    return rows.join("");
  }

  function attach(el, t) {
    if (!el || !t || el.querySelector(".wh")) return;
    const box = h("div", "wh");
    const btn = h("button", "btn ghost xs wh-btn", "What happened");
    const pan = h("div", "wh-pan"); pan.hidden = true;
    btn.onclick = () => {
      if (pan.hidden && !pan.innerHTML) {
        pan.innerHTML = body(t);
        const c = pan.querySelector(".wh-copy");
        if (c) c.onclick = () => { try { navigator.clipboard.writeText(t.turn); c.textContent = "Copied"; } catch (x) {} };
      }
      pan.hidden = !pan.hidden; btn.classList.toggle("on", !pan.hidden);
    };
    box.append(btn, pan);
    const meta = el.querySelector(".meta"); (meta || el).after ? (meta ? meta.after(box) : el.append(box)) : el.append(box);
  }

  // Client time to first word over this session's replies (p50 / p95), for the TTFT targets.
  const TT = [];
  function stats() {
    if (!TT.length) return null;
    const v = [...TT].sort((x, y) => x - y), q = p => v[Math.min(v.length - 1, Math.floor(p * (v.length - 1) + 0.5))];
    return { p50: q(0.5), p95: q(0.95), n: v.length };
  }
  on("trace", t => { if (t && t.ttft_ms > 0) { TT.push(t.ttft_ms); if (TT.length > 200) TT.shift(); } });
  on("trace", t => { const el = window.__lastReply; window.__lastReply = null; attach(el, t); });
  window.WhatHappened = { attach, body, stats };
})();
