// BlazeRouter dashboard. Plain JS, no build step: served by the local server from
// files embedded in Blaze. Every call carries the session token Blaze opened the page
// with (or the API key the user pasted).
"use strict";
const $ = (s, el = document) => el.querySelector(s);
const $$ = (s, el = document) => [...el.querySelectorAll(s)];
const esc = s => String(s == null ? "" : s).replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
const h = (tag, cls, html) => { const e = document.createElement(tag); if (cls) e.className = cls; if (html != null) e.innerHTML = html; return e; };
const pad = n => String(n).padStart(2, "0");
const fmtN = n => (n = n || 0) >= 1e9 ? (n / 1e9).toFixed(1) + "B" : n >= 1e6 ? (n / 1e6).toFixed(1) + "M" : n >= 1e3 ? (n / 1e3).toFixed(1) + "K" : String(n);
const ago = t => { const s = Math.max(0, (Date.now() - Date.parse(t)) / 1000); return s < 60 ? Math.round(s) + "s ago" : s < 3600 ? Math.round(s / 60) + "m ago" : s < 86400 ? Math.round(s / 3600) + "h ago" : Math.round(s / 86400) + "d ago"; };

const PAGES = [["home", "Home"], ["providers", "Providers"], ["combos", "Combos"], ["usage", "Usage & logs"], ["quota", "Quota"], ["settings", "Settings"]];
const embedded = window.parent !== window;
let token = "";
let page = "home";
let S = null; // /api/state
const params = new URLSearchParams(location.hash.slice(1));
(() => {
  token = params.get("t") || "";
  try { if (token) sessionStorage.setItem("br-token", token); else token = sessionStorage.getItem("br-token") || ""; } catch (e) {}
  if (params.get("p") && PAGES.some(p => p[0] === params.get("p"))) page = params.get("p");
  if (location.hash) history.replaceState(null, "", location.pathname + location.search); // the token leaves the address bar
})();

async function api(path, body) {
  const r = await fetch("/api/" + path, { method: body ? "POST" : "GET", headers: { "X-Blaze-Token": token, "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined });
  let j = {};
  try { j = await r.json(); } catch (e) {}
  if (r.status === 401) { gate(); throw new Error("signed out"); }
  if (!r.ok) throw new Error(j.error || "HTTP " + r.status);
  return j;
}

function toast(msg) {
  const t = $("#toast"); t.textContent = msg; t.classList.add("on");
  clearTimeout(toast.t); toast.t = setTimeout(() => t.classList.remove("on"), 2600);
}
async function copy(text, what) {
  try { await navigator.clipboard.writeText(text); } catch (e) {
    const ta = h("textarea"); ta.value = text; document.body.append(ta); ta.select(); document.execCommand("copy"); ta.remove();
  }
  toast((what || "Copied") + " to the clipboard");
}
// Links: inside Blaze the page asks Blaze to open them in the browser.
function openExt(url) {
  if (embedded) window.parent.postMessage({ blazeRouter: "open", url }, "*");
  else window.open(url, "_blank", "noopener");
}
document.addEventListener("click", e => {
  const a = e.target.closest("a[data-ext]");
  if (a) { e.preventDefault(); openExt(a.href); }
});

// ---------- theme: follows Blaze's sun/moon toggle when embedded ----------
const SUN = '<svg viewBox="0 0 16 16"><circle cx="8" cy="8" r="3"/><path d="M8 1.5v1.8M8 12.7v1.8M1.5 8h1.8M12.7 8h1.8M3.4 3.4l1.3 1.3M11.3 11.3l1.3 1.3M3.4 12.6l1.3-1.3M11.3 4.7l1.3-1.3"/></svg>';
const MOON = '<svg viewBox="0 0 16 16"><path d="M13.5 9.8A5.8 5.8 0 0 1 6.2 2.5a5.8 5.8 0 1 0 7.3 7.3z"/></svg>';
function setTheme(t, save) {
  const r = document.documentElement;
  r.classList.add("fade"); r.dataset.theme = t; setTimeout(() => r.classList.remove("fade"), 380);
  $("#theme").innerHTML = t === "dark" ? MOON : SUN;
  if (save) { try { localStorage.setItem("blaze-theme", t); } catch (e) {} if (embedded) window.parent.postMessage({ blazeRouter: "theme", theme: t }, "*"); }
  if (page === "home" && S) drawTopo();
}
window.addEventListener("message", e => { if (e.data && e.data.blazeTheme) setTheme(e.data.blazeTheme, false); });
$("#theme").innerHTML = document.documentElement.dataset.theme === "dark" ? MOON : SUN;
$("#theme").onclick = () => setTheme(document.documentElement.dataset.theme === "dark" ? "light" : "dark", true);
$("#refresh").onclick = () => load(true);

// ---------- shell ----------
function nav() {
  const n = $("#nav"); n.innerHTML = "";
  PAGES.forEach(([id, name], i) => {
    const b = h("button", id === page ? "on" : "", `<span class="n">${pad(i + 1)}</span><span class="lbl">${esc(name)}</span>`);
    b.title = name;
    b.onclick = () => go(id);
    n.append(b);
  });
}
function go(id) { page = id; nav(); draw(); $("#main").focus({ preventScroll: true }); }

function gate() {
  $("#view").innerHTML = "";
  const g = h("div", "gate", `<div class="label acc">BlazeRouter</div><h1 style="font-size:28px;letter-spacing:-.03em">Open from Blaze</h1>
    <p class="sub">Click the BlazeRouter button in Blaze's top bar, or paste your BlazeRouter API key (it starts with <span class="mono">br-</span>).</p>
    <div class="row"><input class="field mono" type="password" placeholder="br-…" aria-label="API key"><button class="btn primary"><span>Continue</span></button></div>`);
  $("button", g).onclick = () => { token = $("input", g).value.trim(); try { sessionStorage.setItem("br-token", token); } catch (e) {} load(true); };
  $("input", g).onkeydown = e => { if (e.key === "Enter") $("button", g).click(); };
  $("#view").append(g);
  $("#stext").textContent = "Signed out"; $("#sdot").className = "dot";
}

async function load(redraw) {
  try { S = await api("state"); } catch (e) { if (e.message !== "signed out") { $("#stext").textContent = "Can't reach BlazeRouter"; $("#sdot").className = "dot red"; } return; }
  const st = S.status || {};
  $("#sdot").className = "dot " + (st.level || "");
  $("#stext").textContent = st.note || "";
  $("#ver").textContent = S.version || "";
  const ep = (S.url || location.origin) + "/v1";
  $("#endpoint").textContent = ep; $("#endpoint").onclick = () => copy(ep, "Endpoint");
  if (redraw) draw();
}

function draw() {
  const v = $("#view"); v.innerHTML = "";
  $("#crumb").textContent = (PAGES.find(p => p[0] === page) || PAGES[0])[1];
  if (!S) return;
  const el = h("div", "page"); v.append(el);
  ({ home: Home, providers: Providers, combos: Combos, usage: Usage, quota: Quota, settings: Settings })[page](el);
}

function head(el, eyebrow, title, lead, acts) {
  const d = h("div", "phead", `<div><div class="label acc">${esc(eyebrow)}</div><h1>${title}</h1>${lead ? `<p>${lead}</p>` : ""}</div>`);
  if (acts) { const a = h("div", "acts"); acts.forEach(x => a.append(x)); d.append(a); }
  el.append(d);
}
function block(el, title, right) {
  const s = h("section", "blk");
  const b = h("div", "bh", `<h2>${esc(title)}</h2>`);
  if (right) { if (typeof right === "string") b.append(h("span", "label", right)); else { right.style.marginLeft = "auto"; b.append(right); } }
  s.append(b); el.append(s);
  return s;
}
function btn(label, cls, fn) { const b = h("button", "btn " + (cls || ""), `<span>${label}</span>`); if (fn) b.onclick = fn; return b; }
function busy(b, text, fn) {
  return async () => { const was = b.innerHTML; b.disabled = true; b.innerHTML = `<span>${esc(text)}</span>`; try { await fn(); } finally { b.disabled = false; b.innerHTML = was; } };
}
const keyMasked = k => k ? k.slice(0, 6) + "••••••••" + k.slice(-4) : "";
const provName = id => ((S.providers || []).find(p => p.id === id) || {}).name || id;
const ready = () => (S.providers || []).filter(p => p.ready);
const status = s => s >= 400 || !s ? "bad" : "";

// ---------- 01 Home ----------
function snippets() {
  const base = (S.url || location.origin), key = S.key || "br-…";
  return [
    ["OpenAI SDK", "Any OpenAI client: set the base URL and the key.", `from openai import OpenAI\n\nclient = OpenAI(base_url="${base}/v1", api_key="${key}")\nreply = client.chat.completions.create(\n    model="auto",  # or a combo, or provider/model\n    messages=[{"role": "user", "content": "Hello"}],\n)\nprint(reply.choices[0].message.content)`],
    ["curl", "Streams with \"stream\": true.", `curl ${base}/v1/chat/completions \\\n  -H "Authorization: Bearer ${key}" \\\n  -H "Content-Type: application/json" \\\n  -d '{"model": "auto", "messages": [{"role": "user", "content": "Hello"}]}'`],
    ["Cursor", "Settings › Models › API keys: OpenAI key + Override OpenAI Base URL, then add the model names.", `Override OpenAI Base URL:  ${base}/v1\nOpenAI API key:            ${key}\nModel names to add:        auto, auto/fast${(S.combos || []).slice(1).map(c => ", " + c.id).join("")}`],
    ["Claude Code", "Anthropic's Messages API is translated, so Claude Code works as is.", `export ANTHROPIC_BASE_URL=${base}\nexport ANTHROPIC_AUTH_TOKEN=${key}\nexport ANTHROPIC_MODEL=auto\nclaude`],
    ["Cline · Continue · Roo", "Choose the \"OpenAI Compatible\" provider.", `Base URL:  ${base}/v1\nAPI key:   ${key}\nModel ID:  auto`],
    ["Gemini SDK", "Gemini's generateContent API is translated too.", `curl "${base}/v1beta/models/auto:generateContent?key=${key}" \\\n  -H "Content-Type: application/json" \\\n  -d '{"contents": [{"parts": [{"text": "Hello"}]}]}'`],
    ["Blaze CLI", "Point Blaze CLI's custom provider at BlazeRouter.", `blaze config set custom_base ${base}/v1\nblaze key custom ${key}\nblaze --model custom/auto`],
  ];
}

function Home(el) {
  const st = S.status || {}, today = S.today || {};
  const hero = h("div", "hero");
  const left = h("div", "", `<div class="dotfield"></div><div class="label">BlazeRouter · local AI gateway</div><h1>One endpoint. Every free model.</h1>
    <p>Point any app at BlazeRouter and it routes each request across your free tiers: when one is rate limited, out of quota or down, the next one answers.</p>`);
  const cta = h("div", "cta");
  cta.append(btn("Copy endpoint", "primary", () => copy((S.url || location.origin) + "/v1", "Endpoint")), btn("Add a provider", "", () => go("providers")));
  left.append(cta);
  const sys = h("dl", "sys", `<div class="cap label">System</div>
    <dt>Status</dt><dd><span class="dot ${esc(st.level)}"></span><span>${st.running ? "Running" : "Off"}</span></dd>
    <dt>Endpoint</dt><dd><span class="mono">${esc((S.url || location.origin) + "/v1")}</span></dd>
    <dt>API key</dt><dd><span class="mono" id="hk">${esc(keyMasked(S.key))}</span></dd>
    <dt>Providers ready</dt><dd><span class="mono">${st.ready || 0}</span></dd>
    <dt>Requests · 24h</dt><dd><span class="mono">${today.requests || 0}</span></dd>`);
  const kdd = $("#hk", sys).parentElement;
  const show = h("button", "btn sm ghost", "<span>Show</span>"); show.onclick = () => { const on = show.textContent === "Show"; $("#hk", sys).textContent = on ? S.key : keyMasked(S.key); show.innerHTML = `<span>${on ? "Hide" : "Show"}</span>`; };
  kdd.append(show, btn("Copy", "sm", () => copy(S.key, "API key")));
  const right = h("div"); right.append(sys);
  hero.append(left, right); el.append(hero);
  if (!ready().length) {
    const s = block(el, "Start here", "no keys yet");
    s.append(h("div", "list", `<div class="item"><span class="n">!</span><div><div class="t">Add one free key</div><div class="s">Gemini, Groq, Cerebras, OpenRouter, Mistral and NVIDIA all have free tiers. One key is enough for "auto" to work.</div></div><div class="acts"></div></div>`));
    $(".acts", s).append(btn("Providers", "primary sm", () => go("providers")));
  }
  // point your client
  const sn = block(el, "Point your client", "base URL + key");
  const box = h("div", "snips"); const nv = h("nav"); const code = h("div", "code");
  const list = snippets();
  const pick = i => {
    $$("button", nv).forEach((b, j) => b.classList.toggle("on", i === j));
    code.innerHTML = `<div class="how">${esc(list[i][1])}</div><pre>${esc(list[i][2])}</pre>`;
    code.append(Object.assign(btn("Copy", "sm copy", () => copy(list[i][2], list[i][0] + " snippet")), {}));
  };
  list.forEach((s, i) => { const b = h("button", "", `<span class="n">${pad(i + 1)}</span><span>${esc(s[0])}</span>`); b.onclick = () => pick(i); nv.append(b); });
  box.append(nv, code); sn.append(box); pick(0);
  // topology
  const tp = block(el, "Provider topology", "live");
  tp.append(h("div", "topo", `<svg id="topo" viewBox="0 0 1000 300" preserveAspectRatio="xMidYMid meet" role="img" aria-label="Providers connected to BlazeRouter"></svg>`));
  drawTopo();
  // recent
  const rc = block(el, "Recent requests");
  rc.append(logTable(S.recent || [], true));
}

function drawTopo() {
  const svg = $("#topo"); if (!svg) return;
  const ps = ready().slice(0, 14);
  const W = 1000, H = 300, cx = W / 2, cy = H / 2;
  let out = "";
  const busy = {}; (S.recent || []).forEach(e => { if (Date.now() - Date.parse(e.time) < 15 * 60e3) busy[e.provider] = 1; });
  ps.forEach((p, i) => {
    const left = i % 2 === 0, k = Math.floor(i / 2), rows = Math.ceil(ps.length / 2);
    const x = left ? 150 : W - 150, y = rows <= 1 ? cy : 40 + k * ((H - 80) / Math.max(1, rows - 1));
    const lvl = p.health.cooling ? "amber" : p.health.fails > 2 ? "red" : "green";
    out += `<line x1="${cx}" y1="${cy}" x2="${x}" y2="${y}" class="${busy[p.id] ? "live" : ""}"/>`;
    out += `<g><rect class="pn" x="${x - 110}" y="${y - 15}" width="220" height="30"/><rect x="${x - 102}" y="${y - 4}" width="8" height="8" class="dot ${lvl}" style="fill:${getComputedStyle(document.documentElement).getPropertyValue("--ink-weak")}"/>
      <text x="${x - 88}" y="${y + 4}">${esc((p.name.length > 24 ? p.name.slice(0, 23) + "…" : p.name).toUpperCase())}</text></g>`;
  });
  if (!ps.length) out += `<text x="${cx}" y="${cy + 60}" text-anchor="middle">NO PROVIDERS YET · ADD A KEY</text>`;
  out += `<g class="node-hub"><rect x="${cx - 80}" y="${cy - 22}" width="160" height="44"/><text class="hubt" x="${cx}" y="${cy + 4}" text-anchor="middle">BLAZEROUTER</text></g>`;
  svg.innerHTML = out;
  const col = { green: getComputedStyle(document.documentElement).getPropertyValue("--accent"), amber: "#d98a00", red: "#d0362f" };
  $$("rect.dot", svg).forEach(r => { const l = r.classList[1]; r.style.fill = col[l] || ""; });
}

function logTable(rows, short) {
  const t = h("table");
  t.innerHTML = `<colgroup><col style="width:${short ? 90 : 120}px"><col style="width:110px"><col><col style="width:64px"><col style="width:84px"><col style="width:110px">${short ? "" : '<col style="width:80px">'}</colgroup>
    <thead><tr><th>When</th><th>Client</th><th>Asked → answered by</th><th class="num">Status</th><th class="num">Time</th><th class="num">Tokens in/out</th>${short ? "" : '<th class="num">Fallbacks</th>'}</tr></thead>`;
  const tb = h("tbody");
  rows.forEach(e => {
    const tr = h("tr", "", `<td title="${esc(e.time)}">${esc(ago(e.time))}</td><td>${esc(e.client || "—")}</td>
      <td title="${esc(e.error || "")}"><span class="mono">${esc(e.asked || "auto")}</span> → ${e.provider ? `${esc(provName(e.provider))} <span class="mono sub">${esc(e.model)}</span>` : `<span class="sub">${esc(e.error || "no answer")}</span>`}</td>
      <td class="num"><span class="st ${status(e.status)}">${e.status || "—"}</span></td><td class="num">${e.ms ? (e.ms / 1000).toFixed(2) + "s" : "—"}</td>
      <td class="num">${fmtN(e.in)} / ${fmtN(e.out)}</td>${short ? "" : `<td class="num">${e.fallbacks || 0}</td>`}`);
    tb.append(tr);
  });
  if (!rows.length) tb.append(h("tr", "", `<td colspan="${short ? 6 : 7}" class="sub" style="text-align:center;padding:22px">No requests yet. Send one with the snippet above.</td>`));
  t.append(tb);
  return t;
}

// ---------- 02 Providers ----------
let pfilter = "all", pquery = "";
function freeLine(p) {
  const f = p.free_info; if (!f) return p.free ? "Free tier" : "Paid";
  const t = { "recurring-daily": "Daily free quota", "recurring-monthly": "Monthly free quota", "recurring-uncapped": "Free, rate limited", "recurring-credit": "Recurring credit", "one-time-initial": "Signup credit", local: "On this computer" }[f.type] || f.type;
  const n = f.monthly ? ` · ~${fmtN(f.monthly)} tok/mo` : f.credit ? ` · ${fmtN(f.credit)} tok once` : "";
  return t + n + (f.tos && f.tos !== "ok" ? ` · ToS ${f.tos}` : "");
}
function Providers(el) {
  head(el, "02 · Providers", "Your models, one place", "Add a key once: BlazeRouter, Blaze and Blaze CLI all use it (kept in your system keychain). A second key for the same provider joins its key ring and takes over when the first runs out.");
  const tools = h("div", "tools");
  const q = h("input", "field"); q.placeholder = `Search ${(S.providers || []).length} providers`; q.value = pquery; q.setAttribute("aria-label", "Search providers");
  const chips = h("div", "chips");
  [["all", "All"], ["ready", "Set up"], ["free", "Free tier"]].forEach(([id, n]) => { const b = h("button", pfilter === id ? "on" : "", n); b.onclick = () => { pfilter = id; draw(); }; chips.append(b); });
  tools.append(q, chips, h("span", "grow"), h("span", "meta", `${ready().length} ready`));
  el.append(tools);
  const lst = h("div", "list"); lst.style.borderTop = "1px solid var(--ink-hair)";
  const fill = () => {
    lst.innerHTML = "";
    const ql = pquery.toLowerCase();
    const items = (S.providers || []).filter(p => (pfilter === "all" || (pfilter === "ready" ? p.ready : p.free)) && (!ql || (p.name + " " + p.id).toLowerCase().includes(ql)));
    items.forEach((p, i) => lst.append(providerItem(p, i)));
    if (!items.length) lst.append(h("div", "empty", "<b>Nothing matches</b>Try another word."));
  };
  q.oninput = () => { pquery = q.value; fill(); };
  fill(); el.append(lst);
}
function providerItem(p, i) {
  const it = h("div", "item");
  const hl = p.health || {};
  const state = p.ready ? (hl.cooling ? `<span class="tag">Resting ${hl.cooling}s</span>` : `<span class="tag acc">Ready</span>`) : p.needs_base ? `<span class="tag">Needs address</span>` : "";
  it.innerHTML = `<span class="n">${pad(i + 1)}</span><div><div class="t">${esc(p.name)} ${state} ${p.keys.length > 1 ? `<span class="tag">${p.keys.length} keys</span>` : ""}</div>
    <div class="s"><span class="meta">${esc(freeLine(p))}</span>${p.ready ? ` · <span class="mono">${esc(p.model || "choose a model")}</span>${p.requests ? ` · ${p.requests} req/24h` : ""}` : ""}</div></div>`;
  const acts = h("div", "acts");
  const tg = btn(p.ready ? "Manage" : "Add key", p.ready ? "sm" : "sm primary");
  acts.append(tg); it.append(acts);
  const panel = h("div", "panel"); panel.hidden = true; it.append(panel);
  tg.onclick = () => { panel.hidden = !panel.hidden; if (!panel.hidden && !panel.dataset.built) { panel.dataset.built = 1; providerPanel(panel, p); } };
  if (location.hash === "#" + p.id) setTimeout(() => tg.click());
  return it;
}
function providerPanel(x, p) {
  const msg = h("div", "note");
  const done = async (ok, text) => { msg.className = "note " + (ok ? "ok" : "bad"); msg.textContent = text; if (ok) { await load(false); setTimeout(draw, 900); } };
  if (p.custom || p.base_hint) {
    const r = h("div", "row", `<span class="label" style="width:90px">Address</span><input class="field mono" placeholder="${esc(p.custom ? p.base_hint : "Your account id (or the full URL)")}" value="${esc(p.custom ? (p.base || "") : (p.needs_base ? "" : p.base))}">${p.custom ? `<input class="field" style="max-width:180px;flex:none" placeholder="Name (optional)">` : ""}`);
    const sv = btn("Save address", "sm");
    sv.onclick = async () => { try { await api("providers/base", { provider: p.id, base: $("input", r).value.trim(), name: p.custom ? $$("input", r)[1].value.trim() : "" }); done(true, "Address saved."); } catch (e) { done(false, e.message); } };
    r.append(sv); x.append(r);
    if (p.base_hint && !p.custom) x.append(h("div", "note", `Template: <span class="mono">${esc(p.base_hint)}</span>`));
  }
  if (p.keys.length) {
    const kl = h("div");
    p.keys.forEach((k, i) => {
      const d = h("div", "k", `<span class="mono">${esc(k.masked)}</span>${k.main ? '<span class="tag">Main</span>' : `<span class="tag">Key ${i + 1}</span>`}${k.env ? `<span class="tag">from ${esc(p.env)}</span>` : ""}<span class="grow"></span>`);
      if (!k.env) { const rm = btn("Remove", "sm ghost"); rm.onclick = async () => { await api("providers/remove-key", { provider: p.id, index: i }); done(true, "Key removed."); }; d.append(rm); }
      kl.append(d);
    });
    x.append(kl);
  }
  if (!p.local) {
    const kr = h("div", "row", `<span class="label" style="width:90px">${p.keys.length ? "Another key" : "Key"}</span><input class="field mono" type="password" autocomplete="off" placeholder="${esc(p.key_hint && p.key_hint !== "…" ? p.key_hint : "Paste your key")}" aria-label="API key for ${esc(p.name)}">`);
    const sv = btn("Test & save", "sm primary");
    sv.onclick = busy(sv, "Testing…", async () => { try { const r = await api("providers/key", { provider: p.id, key: $("input", kr).value.trim() }); done(true, `Saved. Using ${r.model || "its default model"}.${r.note ? " " + r.note : ""}`); } catch (e) { done(false, e.message); } });
    $("input", kr).onkeydown = e => { if (e.key === "Enter") sv.click(); };
    kr.append(sv);
    if (p.key_page) { const g = h("a", "btn sm", "<span>Get a key ↗</span>"); g.href = p.key_page; g.dataset.ext = 1; g.target = "_blank"; kr.append(g); }
    x.append(kr);
    if (p.id === "openrouter") {
      const oc = btn("Sign in with OpenRouter (one click)", "sm");
      oc.onclick = busy(oc, "Waiting for you in the browser…", async () => { try { const r = await api("providers/connect", { provider: "openrouter" }); done(true, "Connected. Using " + r.model + "."); } catch (e) { done(false, e.message); } });
      x.append(oc);
    }
  } else if (!p.ready) {
    const b = btn("Check and add", "sm primary");
    b.onclick = busy(b, "Checking…", async () => { try { const r = await api("providers/key", { provider: p.id, key: "" }); done(true, "Added. Using " + r.model + "."); } catch (e) { done(false, e.message); } });
    x.append(b);
  }
  x.append(msg);
  if (!p.ready) return;
  // model
  const mr = h("div", "row", `<span class="label" style="width:90px">Model</span>`);
  const sel = h("select", "field mono"); sel.innerHTML = `<option>${esc(p.model || "")}</option>`; sel.setAttribute("aria-label", "Model for " + p.name);
  const rf = btn("Refresh list", "sm ghost");
  const loadModels = async refresh => {
    try {
      const l = await api(`models?provider=${encodeURIComponent(p.id)}${refresh ? "&refresh=1" : ""}`);
      const ms = (l.models || []).filter(m => m.tools !== "no");
      sel.innerHTML = ms.map(m => `<option value="${esc(m.id)}" ${m.id === p.model ? "selected" : ""}>${esc(m.id)}${m.free ? "  · free" : ""}${m.tools === "yes" ? "  · tools" : ""}</option>`).join("") || `<option>${esc(p.model)}</option>`;
      if (l.error) done(false, l.error);
    } catch (e) {}
  };
  sel.onchange = async () => { await api("providers/model", { provider: p.id, model: sel.value }); p.model = sel.value; done(true, `${p.name} now uses ${sel.value}.`); };
  rf.onclick = busy(rf, "Loading…", () => loadModels(true));
  mr.append(sel, rf); x.append(mr); loadModels(false);
  const ar = h("div", "row");
  const tb = btn("Test", "sm"); tb.onclick = busy(tb, "Testing…", async () => { const r = await api("providers/test", { provider: p.id }); done(r.ok, r.detail); });
  const qb = btn("Plan & quota", "sm"); qb.onclick = () => { qprov = p.id; go("quota"); };
  const rm = btn(`Remove ${p.name}`, "sm ghost");
  rm.onclick = async () => { if (!rm.dataset.sure) { rm.dataset.sure = 1; rm.innerHTML = "<span>Remove its keys too? Click again</span>"; return; } await api("providers/remove", { provider: p.id }); done(true, p.name + " removed."); };
  ar.append(tb, qb, h("span", "grow"), rm); x.append(ar);
  if (p.health && p.health.last_error) x.append(h("div", "note", "Last error: " + esc(p.health.last_error)));
}

// ---------- 03 Combos ----------
let editing = null;
function Combos(el) {
  const nb = btn("New combo", "primary", () => { editing = { name: "", strategy: "priority", targets: ready().slice(0, 3).map(p => ({ provider: p.id, model: p.model })) }; draw(); });
  head(el, "03 · Combos", "Fallback chains", "A combo is a model name your apps can ask for. It tries its models in the order its strategy picks; one that is rate limited, out of quota or failing is skipped until it recovers.", [nb]);
  const cards = h("div", "cards");
  (S.combos || []).forEach((c, i) => cards.append(comboCard(c, i)));
  el.append(cards);
  if (editing) el.append(comboEditor());
}
function comboCard(c, i) {
  const strat = (S.strategies || []).find(s => s.ID === c.strategy) || {};
  const using = S.blaze_model === "blazerouter/" + c.id;
  const d = h("div", "card", `<div class="ix">${pad(i + 1)}</div><h3>${esc(c.name)} ${c.builtin ? '<span class="tag acc">Built in</span>' : ""} ${using ? '<span class="tag ink">Blaze uses this</span>' : ""}</h3>
    <div class="meta">model "${esc(c.id)}" · ${esc(strat.Name || c.strategy)} · ${c.usable}/${(c.rows || []).length} usable</div>
    <div class="note">${esc(c.note || strat.Desc || "")}</div>`);
  const ch = h("div", "chain");
  const order = (c.order || []).map(t => t.provider + "|" + t.model);
  (c.rows || []).forEach((r, j) => {
    const lvl = !r.ready ? "" : r.health.cooling ? "amber" : r.health.fails > 2 ? "red" : "green";
    const pos = order.indexOf(r.provider + "|" + r.model);
    ch.append(h("div", "", `<span class="mono weak">${pad(j + 1)}</span><span class="mono">${esc(r.name)} · ${esc(r.model)}</span><span class="row" style="gap:6px">${pos === 0 ? '<span class="tag acc">next</span>' : ""}${!r.ready ? '<span class="tag">no key</span>' : r.health.cooling ? `<span class="tag">resting ${r.health.cooling}s</span>` : ""}<span class="dot ${lvl}"></span></span>`));
  });
  if (!(c.rows || []).length) ch.append(h("div", "note", c.builtin ? "Add a provider key and it joins Auto by itself." : "No models in this combo yet."));
  d.append(ch);
  const f = h("div", "foot");
  if (!using) f.append(btn("Use in Blaze", "sm", async () => { await api("blaze/use", { model: c.id }); toast(`Blaze now answers through ${c.name}`); load(true); }));
  f.append(btn("Copy name", "sm ghost", () => copy(c.id, "Model name")));
  if (!c.builtin) {
    f.append(btn("Edit", "sm", () => { editing = JSON.parse(JSON.stringify(c)); draw(); }));
    const del = btn("Delete", "sm ghost"); del.onclick = async () => { if (!del.dataset.sure) { del.dataset.sure = 1; del.innerHTML = "<span>Sure? Click again</span>"; return; } await api("combos/delete", { id: c.id }); load(true); };
    f.append(del);
  }
  d.append(f);
  return d;
}
function comboEditor() {
  const e = editing;
  const ed = h("div", "editor", `<div class="label acc">${e.id ? "Edit combo" : "New combo"}</div>`);
  const nm = h("div", "row", `<span class="label" style="width:90px">Name</span><input class="field" placeholder="Coding" value="${esc(e.name)}" aria-label="Combo name">`);
  const st = h("div", "row", `<span class="label" style="width:90px">Strategy</span>`);
  const ss = h("select", "field"); ss.innerHTML = (S.strategies || []).map(s => `<option value="${s.ID}" ${s.ID === e.strategy ? "selected" : ""}>${esc(s.Name)} — ${esc(s.Desc)}</option>`).join("");
  st.append(ss);
  const rows = h("div");
  const provs = (S.providers || []).filter(p => p.ready || e.targets.some(t => t.provider === p.id));
  const drawRows = () => {
    rows.innerHTML = "";
    e.targets.forEach((t, i) => {
      const r = h("div", "trow", `<span class="mono weak">${pad(i + 1)}</span>`);
      const ps = h("select", "field"); ps.innerHTML = provs.map(p => `<option value="${p.id}" ${p.id === t.provider ? "selected" : ""}>${esc(p.name)}</option>`).join("");
      const ms = h("input", "field mono"); ms.value = t.model || ""; ms.placeholder = "model id (blank = provider's model)";
      ps.onchange = () => { t.provider = ps.value; t.model = (provs.find(p => p.id === ps.value) || {}).model || ""; ms.value = t.model; };
      ms.oninput = () => t.model = ms.value;
      const a = h("span", "row");
      const up = btn("↑", "sm ghost", () => { if (i) { [e.targets[i - 1], e.targets[i]] = [e.targets[i], e.targets[i - 1]]; drawRows(); } });
      const rm = btn("Remove", "sm ghost", () => { e.targets.splice(i, 1); drawRows(); });
      a.append(up, rm); r.append(ps, ms, a); rows.append(r);
    });
    if (!provs.length) rows.append(h("div", "note", "Add a provider key first; combos are made of models you can use."));
  };
  drawRows();
  const add = btn("Add a model", "sm", () => { const p = provs[0]; if (p) { e.targets.push({ provider: p.id, model: p.model }); drawRows(); } });
  const msg = h("div", "note");
  const save = btn("Save combo", "primary", async () => {
    e.name = $("input", nm).value.trim(); e.strategy = ss.value;
    try { await api("combos", { combo: e }); editing = null; toast("Combo saved"); load(true); } catch (er) { msg.className = "note bad"; msg.textContent = er.message; }
  });
  const cancel = btn("Cancel", "ghost", () => { editing = null; draw(); });
  ed.append(nm, st, rows, h("div", "row"), msg); $$(".row", ed).pop().append(add, h("span", "grow"), cancel, save);
  setTimeout(() => ed.scrollIntoView({ block: "nearest" }));
  return ed;
}

// ---------- 04 Usage & logs ----------
async function Usage(el) {
  const clr = btn("Clear log", "ghost");
  head(el, "04 · Usage & logs", "What went where", "Every request through BlazeRouter: who asked, which model answered, how long it took and how many tokens it used. Prompts and replies are never stored.", [clr]);
  clr.onclick = async () => { if (!clr.dataset.sure) { clr.dataset.sure = 1; clr.innerHTML = "<span>Clear every entry? Click again</span>"; return; } await api("logs/clear", {}); load(true); };
  let u, logs;
  try { [u, logs] = await Promise.all([api("usage"), api("logs?n=200")]); } catch (e) { return; }
  const t = u.today || {}, w = u.week || {};
  const m = h("div", "grid metrics");
  const cell = (k, v, c) => h("div", "metric", `<div class="label acc">${k}</div><div class="v">${v}</div><div class="c">${c}</div>`);
  m.append(cell("Requests · 24h", t.requests || 0, `${w.requests || 0} this week`), cell("Tokens in", fmtN(t.in), "last 24 hours"), cell("Tokens out", fmtN(t.out), "last 24 hours"),
    cell("Errors", t.errors || 0, t.requests ? Math.round(100 * (t.errors || 0) / t.requests) + "% of requests" : "none yet"), cell("Saved", fmtN(Math.round((t.saved || 0) / 4)), "tokens by compression"));
  el.append(m);
  const hb = block(el, "Requests per hour", "last 24 hours");
  const hr = u.hourly || []; const mx = Math.max(1, ...hr);
  hb.append(h("div", "chart", hr.map((n, i) => `<div class="${n ? "" : "z"}" style="height:${Math.max(2, Math.round(n / mx * 100))}%" title="${n} requests, ${23 - i}h ago"></div>`).join("")));
  hb.append(h("div", "chartx", `<span class="label">24h ago</span><span class="label">now</span>`));
  const bp = block(el, "By provider", "24 hours");
  const tbl = h("table"); tbl.innerHTML = `<colgroup><col><col style="width:28%"><col style="width:90px"><col style="width:80px"><col style="width:120px"><col style="width:90px"></colgroup><thead><tr><th>Provider</th><th>Share</th><th class="num">Requests</th><th class="num">Errors</th><th class="num">Tokens in/out</th><th class="num">Avg</th></tr></thead>`;
  const tb = h("tbody");
  (u.by_provider || []).forEach(s => tb.append(h("tr", "", `<td>${esc(provName(s.provider))}</td><td><div class="bar"><i style="width:${Math.round(s.share * 100)}%"></i></div></td><td class="num">${s.requests}</td><td class="num">${s.errors}</td><td class="num">${fmtN(s.in)} / ${fmtN(s.out)}</td><td class="num">${(s.avg_ms / 1000).toFixed(2)}s</td>`)));
  if (!(u.by_provider || []).length) tb.append(h("tr", "", `<td colspan="6" class="sub" style="text-align:center;padding:22px">No traffic in the last 24 hours.</td>`));
  tbl.append(tb); bp.append(tbl);
  const bm = block(el, "By model", "7 days");
  const t2 = h("table"); t2.innerHTML = `<colgroup><col><col style="width:90px"><col style="width:80px"><col style="width:140px"></colgroup><thead><tr><th>Model</th><th class="num">Requests</th><th class="num">Errors</th><th class="num">Tokens in/out</th></tr></thead>`;
  const b2 = h("tbody");
  (u.by_model_week || []).slice(0, 20).forEach(s => b2.append(h("tr", "", `<td>${esc(provName(s.provider))} <span class="mono sub">${esc(s.model)}</span></td><td class="num">${s.requests}</td><td class="num">${s.errors}</td><td class="num">${fmtN(s.in)} / ${fmtN(s.out)}</td>`)));
  if (!(u.by_model_week || []).length) b2.append(h("tr", "", `<td colspan="4" class="sub" style="text-align:center;padding:22px">No traffic this week.</td>`));
  t2.append(b2); bm.append(t2);
  const lg = block(el, "Request log", `${(logs || []).length} newest`);
  lg.append(logTable(logs || [], false));
}

// ---------- 05 Quota ----------
let qprov = "";
function Quota(el) {
  head(el, "05 · Quota", "Free allowances", "What each provider gives for free (from OmniRoute's researched catalog), what you used today, and anything resting after a limit. \"Check\" asks the provider about your key's plan.");
  const lst = h("div", "list"); lst.style.borderTop = "1px solid var(--ink-hair)";
  const rs = ready();
  rs.forEach((p, i) => {
    const f = p.free_info || {};
    const it = h("div", "item", `<span class="n">${pad(i + 1)}</span><div><div class="t">${esc(p.name)} ${p.health.cooling ? `<span class="tag">resting ${p.health.cooling}s${p.health.resting ? " · " + esc(p.health.resting) : ""}</span>` : '<span class="tag acc">ok</span>'}</div>
      <div class="s"><span class="meta">${esc(freeLine(p))}</span>${f.note ? " · " + esc(f.note) : ""} · ${p.requests} requests today${p.errors ? ` · ${p.errors} errors` : ""}</div></div>`);
    const acts = h("div", "acts"); const out = h("div", "panel"); out.hidden = true;
    const ck = btn("Check", "sm");
    ck.onclick = busy(ck, "Checking…", async () => {
      out.hidden = false;
      try {
        const q = await api("quota?provider=" + encodeURIComponent(p.id));
        out.innerHTML = (q.rows || []).map(r => `<div class="k"><span class="sub" style="width:180px">${esc(r.label)}</span><span class="mono">${esc(r.value)}</span></div>`).join("") + (q.note ? `<div class="note">${esc(q.note)}</div>` : "");
        if (q.page) { const a = h("a", "btn sm", `<span>Open ${esc(p.name)}'s usage page ↗</span>`); a.href = q.page; a.dataset.ext = 1; out.append(a); }
      } catch (e) { out.innerHTML = `<div class="note bad">${esc(e.message)}</div>`; }
    });
    acts.append(ck); it.append(acts, out); lst.append(it);
    if (qprov === p.id) { setTimeout(() => { ck.click(); it.scrollIntoView({ block: "center" }); }); qprov = ""; }
  });
  if (!rs.length) lst.append(h("div", "empty", "<b>No providers yet</b>Add a key on the Providers page."));
  el.append(lst);
}

// ---------- 06 Settings ----------
function Settings(el) {
  const s = S.settings || {}, st = S.status || {};
  head(el, "06 · Settings", "BlazeRouter settings", "The server runs on this computer only (127.0.0.1). Keys stay in Blaze's shared key store; these settings live in " + esc(S.home || "~/.blaze") + "/desktop/blazerouter.json.");
  const sv = block(el, "Server");
  const kv = h("div", "kv");
  const port = h("div", "", `<div><div class="t">Port</div><div class="d">Default 20128. If it's busy BlazeRouter takes the next free one (now ${st.port || "—"}).</div></div><input class="field mono" style="width:120px" value="${s.port}" aria-label="Port">`);
  const ps = btn("Save", "sm"); ps.onclick = async () => { const v = parseInt($("input", port).value, 10); try { await api("settings", { port: v }); toast("Restarting on port " + v + "…"); setTimeout(() => { location.href = location.href.replace(/:\d+\//, ":" + v + "/"); }, 1200); } catch (e) { toast(e.message); } };
  port.append(ps);
  const auto = h("div", "", `<div><div class="t">Start with Blaze</div><div class="d">Turn the server on whenever Blaze opens.</div></div><span></span>`);
  const sw = h("button", "switch" + (s.autostart ? " on" : "")); sw.setAttribute("aria-label", "Start with Blaze"); sw.onclick = async () => { await api("settings", { autostart: !s.autostart }); load(true); };
  auto.append(sw);
  const run = h("div", "", `<div><div class="t">Server</div><div class="d">${esc(st.note || "")}. Turning it off closes this page; turn it back on from Blaze's top bar.</div></div><span></span>`);
  run.append(btn("Turn off", "sm ghost", async () => { await api("settings", { enabled: false }).catch(() => {}); $("#view").innerHTML = '<div class="gate"><h1 style="font-size:24px">BlazeRouter is off</h1><p class="sub">Turn it back on from the BlazeRouter button in Blaze\'s top bar.</p></div>'; }));
  kv.append(port, auto, run); sv.append(kv);
  const cp = block(el, "Prompt compression", "saves tokens");
  const opts = h("div", "opts");
  [["off", "Off", "Requests go out exactly as sent."], ["light", "Light", "Trailing spaces and runs of blank lines. Never changes meaning. Default."],
   ["standard", "Standard", "Also tool output: colour codes, repeated lines folded, long logs cut to head and tail."], ["aggressive", "Aggressive", "Also cuts older turns to their first part. For long agent sessions on tight quotas."]].forEach(([id, n, d]) => {
    const b = h("button", s.compression === id ? "on" : "", `<span class="t"><span class="r"></span>${n}</span><span class="d">${d}</span>`);
    b.onclick = async () => { await api("settings", { compression: id }); load(true); };
    opts.append(b);
  });
  cp.append(opts);
  const kb = block(el, "API key");
  const k2 = h("div", "kv");
  const kr = h("div", "", `<div><div class="t">Local key</div><div class="d">Apps send it as <span class="mono">Authorization: Bearer …</span>. Rotate it if it leaked; apps using the old one stop working.</div></div><span class="mono">${esc(keyMasked(S.key))}</span>`);
  const ka = h("span", "row"); ka.append(btn("Copy", "sm", () => copy(S.key, "API key")), btn("Rotate", "sm ghost", async () => { await api("key/rotate", {}); toast("New key made"); load(true); }));
  kr.append(ka); k2.append(kr);
  const bu = h("div", "", `<div><div class="t">Blaze's own chats</div><div class="d">${S.blaze_model && S.blaze_model.startsWith("blazerouter/") ? "Go through BlazeRouter (" + esc(S.blaze_model.slice(12)) + ")." : "Use " + esc(S.blaze_model) + " directly. Choose BlazeRouter in Blaze's model picker, or \"Use in Blaze\" on a combo."}</div></div><span></span><span></span>`);
  k2.append(bu); kb.append(k2);
  const ab = block(el, "About");
  ab.append(h("div", "list", `<div class="item"><span class="n">i</span><div><div class="t">BlazeRouter ${esc(S.version || "")}</div><div class="s">Built into Blaze Desktop. A native Go port of the core of <a href="https://github.com/diegosouzapw/OmniRoute" data-ext>OmniRoute</a> by diegosouzapw, used under the MIT licence (see THIRD_PARTY_NOTICES.md). Free-tier data comes from OmniRoute's catalog.</div></div><div class="acts"></div></div>`));
}

// ---------- boot ----------
nav();
if (!token) gate(); else load(true);
setInterval(() => { if (token && document.visibilityState === "visible" && !editing && !document.activeElement.matches("input,select,textarea")) load(page === "home"); }, 15000);
