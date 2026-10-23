// Blaze Desktop UI. Talks to Go through window.go.main.App and Wails events.
const API = () => window.go.main.App;
const on = (ev, fn) => window.runtime.EventsOn(ev, fn);
const $ = (s, el = document) => el.querySelector(s);
const $$ = (s, el = document) => [...el.querySelectorAll(s)];
const h = (tag, cls, html) => { const e = document.createElement(tag); if (cls) e.className = cls; if (html != null) e.innerHTML = html; return e; };
const esc = s => MD.esc(String(s ?? ""));
const pad = n => String(n).padStart(2, "0");
const base = p => (p || "").split(/[\\/]/).filter(Boolean).pop() || p;
const sleep = ms => new Promise(r => setTimeout(r, ms));

// Line icons (16px grid, square caps), used across the app.
const IC = {
  plus: '<path d="M8 3v10M3 8h10"/>', x: '<path d="m4 4 8 8M12 4l-8 8"/>', check: '<path d="m3 8.5 3 3 7-7"/>',
  chev: '<path d="m6 4 4 4-4 4"/>', down: '<path d="m4.5 6.5 3.5 3.5 3.5-3.5"/>', up: '<path d="m4.5 9.5 3.5-3.5 3.5 3.5"/>',
  desktop: '<rect x="1.5" y="2.5" width="13" height="8.5"/><path d="M5.5 13.5h5M8 11v2.5"/>', tablet: '<rect x="3" y="1.5" width="10" height="13"/><path d="M7 12.5h2"/>',
  phone: '<rect x="4.5" y="1.5" width="7" height="13"/><path d="M7.25 12.5h1.5"/>', sun: '<circle cx="8" cy="8" r="2.75"/><path d="M8 1.5v1.5M8 13v1.5M1.5 8H3M13 8h1.5M3.4 3.4l1 1M11.6 11.6l1 1M3.4 12.6l1-1M11.6 4.4l1-1"/>',
  moon: '<path d="M13 9.5A5.5 5.5 0 0 1 6.5 3a5.5 5.5 0 1 0 6.5 6.5z"/>', cursor: '<path d="m3 2.5 9.5 4-4 1.5-1.5 4z"/>',
  open: '<path d="M9 2.5h4.5V7M13.5 2.5 7.5 8.5M11.5 9.5v4h-9v-9h4"/>', file: '<path d="M3.5 1.5h6l3 3v10h-9z"/><path d="M9.5 1.5v3h3"/>',
  globe: '<circle cx="8" cy="8" r="6"/><path d="M2 8h12M8 2c2 2 2 10 0 12M8 2c-2 2-2 10 0 12"/>', clock: '<circle cx="8" cy="8" r="6"/><path d="M8 4.5V8l2.5 1.5"/>',
  sparks: '<path d="M8 1.5v4M8 10.5v4M1.5 8h4M10.5 8h4"/>', retry: '<path d="M13 3.5v3h-3"/><path d="M12.6 6.5A5 5 0 1 0 13 9"/>',
  copy: '<rect x="5" y="5" width="8.5" height="8.5"/><path d="M3 10.5H2.5v-8h8V3"/>', send: '<path d="M8 13V3M3.5 7.5 8 3l4.5 4.5"/>', stop: '<rect x="4" y="4" width="8" height="8" rx="2"/>',
  layers: '<path d="m8 2 6 3.5-6 3.5-6-3.5z"/><path d="m2 9 6 3.5L14 9"/>', key: '<circle cx="5" cy="10.5" r="2.5"/><path d="m7 8.5 6-6M11 4.5l1.5 1.5"/>',
  chart: '<path d="M2.5 13.5h11M4.5 11V8M8 11V4.5M11.5 11V6.5"/>', sliders: '<path d="M2.5 4.5h6M11.5 4.5h2M2.5 11.5h2M7.5 11.5h6"/><rect x="8.5" y="3" width="3" height="3"/><rect x="4.5" y="10" width="3" height="3"/>',
  palette: '<rect x="2.5" y="2.5" width="11" height="11"/><path d="M2.5 8h11M8 2.5v11"/>', brain: '<path d="M4 3.5h8v9H4z"/><path d="M6.5 6h3M6.5 8.5h3M6.5 11h1.5"/>',
  book: '<path d="M3 2.5h8.5v11H3zM5.5 2.5v11"/>', cpu: '<rect x="4" y="4" width="8" height="8"/><path d="M6 1.5v2.5M10 1.5v2.5M6 12v2.5M10 12v2.5M1.5 6H4M1.5 10H4M12 6h2.5M12 10h2.5"/>',
  dl: '<path d="M8 2v8.5M4.5 7 8 10.5 11.5 7M2.5 13.5h11"/>',
  grow: '<path d="M9.5 2.5h4v4M13.5 2.5 9.5 6.5M6.5 13.5h-4v-4M2.5 13.5l4-4"/>', shrink: '<path d="M13.5 6.5h-4v-4M9.5 6.5l4-4M2.5 9.5h4v4M6.5 9.5l-4 4"/>', trash: '<path d="M2.5 4.5h11M6 4.5V2.5h4v2M4 4.5l.7 9h6.6l.7-9"/>', ext: '<path d="M9 2.5h4.5V7M13.5 2.5 7.5 8.5"/>',
};
const icon = (n, cls = "i") => `<svg class="${cls}" viewBox="0 0 16 16">${IC[n] || ""}</svg>`;

let S = null;          // State from Go
let view = "work";     // current main view
let tab = "work";      // last chat tab: work | design
let busy = false;
let current = null;    // the Blaze turn being streamed
let convId = "";
let attachments = [];  // @ files for the next message
let pane = null;       // the chat pane (log + composer), moved between Work and Design

// ---------- theme ----------
function applyTheme() {
  const t = S?.prefs?.theme || "system";
  const dark = t === "dark" || (t === "system" && matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.dataset.theme = dark ? "dark" : "light";
}
matchMedia("(prefers-color-scheme: dark)").addEventListener("change", applyTheme);

// ---------- boot ----------
async function boot() {
  // Go's startup runs alongside page load, so keep asking until the backend answers.
  for (let i = 0; i < 40 && !S; i++) {
    try { S = await API().State(); } catch (e) { await sleep(250); }
  }
  if (!S) { $("#view").innerHTML = '<div class="page"><div class="col"><div class="label acc">Blaze</div><h1>Blaze couldn\'t start.</h1><p class="lead">The app\'s backend didn\'t answer. Quit Blaze and open it again.</p></div></div>'; return; }
  applyTheme();
  $("#ver").textContent = S.version || "";
  pane = buildPane();
  setupFrame();
  setFolder(S.prefs.folder);
  setModel(S.model, S.selection);
  await refreshSide();
  hero();
  if (window.OneChat) await OneChat.boot(); // v0.19: one infinite chat + Home tab (onechat.js)
  else { show("work"); if (oneMind() && API().OpenMain) { const c = await API().OpenMain(); convId = "main"; if (c && c.messages && c.messages.length) loadConv(c); } }
  if (window.Onboard && Onboard.shouldOpen(S)) show("setup"); // first run, or still no model
  setInterval(refreshJobCount, 4000);
  if (window.refreshConnCount) refreshConnCount(S.connected);
  if (!S.prefs.no_update_check) setTimeout(() => checkUpdate(true), 3000);
  // v0.19.3: Blaze can stay open for days, so look again once a day (quietly when offline).
  setInterval(() => { if (!S.prefs.no_update_check) checkUpdate(true); }, 864e5);
}

async function refreshState() { S = await API().State(); setModel(S.model, S.selection); return S; }

// ---------- updates ----------
let UPD = null;          // last CheckUpdate result
let updBusy = false;
const updText = t => $$(".updstatus").forEach(e => e.textContent = t);
// Update errors from update.go may carry technical details after "\n\nDetails: ".
const updSplit = err => { const s = String(err || ""), i = s.indexOf("\n\nDetails: "); return i < 0 ? [s, ""] : [s.slice(0, i), s.slice(i + 11)]; };
const updDetails = d => d ? `<details class="upddetails"><summary class="small sub">Details</summary><div class="small sub mono" style="margin-top:4px;word-break:break-all">${esc(d)}</div></details>` : "";
const updError = err => { const [m, d] = updSplit(err); $$(".updstatus").forEach(e => e.innerHTML = esc(m) + updDetails(d)); };
on("update", p => updText(p.text || ""));

async function checkUpdate(auto) {
  let u = null;
  try { u = await API().CheckUpdate(); } catch (e) { u = { error: String(e) }; }
  if (!u || typeof u !== "object") return null;
  UPD = u;
  $("#updCount").textContent = u.available && !u.skipped ? "NEW" : "";
  updPill(u);
  if (auto && u.available && !u.skipped && S.setup_done) updateCard();
  return u;
}

// "Update available" pill in the top bar (v0.19.3): stays visible on every view until the
// update is installed or skipped. A check that failed (offline) changes nothing.
function updPill(u) {
  if (!u || u.error) return;
  let b = $("#updPill");
  if (!(u.available && !u.skipped)) { if (b) b.remove(); return; }
  if (!b) {
    b = h("button", "chip updpill", `<span class="dot"></span><span>Update available</span>`); b.id = "updPill";
    b.onclick = () => { if (typeof show === "function") show("work"); setTimeout(() => { updateCard(); const c = $("#log .col .updcard", pane); if (c) c.scrollIntoView({ block: "nearest" }); }, 60); };
    const at = $("#routerBtn"); if (at) at.before(b); else $("#top").append(b);
  }
  b.title = `Blaze ${u.latest} is out. You have ${u.current}.`;
}

function updateCard() {
  const u = UPD; if (!u || $("#log .col .updcard", pane)) return;
  const card = h("div", "card confirm updcard", `<div class="title"><span class="label acc">Update ready</span></div>
    <div>Blaze <span class="mono ink">${esc(u.latest)}</span> is out. You have <span class="mono">${esc(u.current)}</span>.</div>
    <div class="sub small" style="margin-top:4px">${esc(u.method || "")}</div><div class="label weak updstatus" style="margin-top:8px"></div>
    <div class="actions"><button class="btn accent sm yes">${u.can_auto ? "Update now" : "Open downloads page"}</button><button class="btn line sm no">Later</button><button class="btn ghost sm skip">Skip this version</button></div>`);
  card.querySelector(".yes").onclick = () => { if (!u.can_auto) return API().OpenURL(u.page); card.remove(); SelfUpdate.run(); };
  card.querySelector(".no").onclick = () => card.remove();
  card.querySelector(".skip").onclick = async () => { await API().SkipUpdate(u.latest); UPD.skipped = true; $("#updCount").textContent = ""; updPill(UPD); card.remove(); };
  $("#log .col", pane).append(card); scrollDown(true);
}

async function installUpdate(box) {
  if (updBusy || busy) { updText(busy ? "Finish or stop the current task first." : "Already updating…"); return; }
  updBusy = true;
  box.querySelectorAll("button").forEach(b => b.disabled = true);
  updText("Starting the download…");
  let err = "";
  try { err = await API().InstallUpdate(); } catch (e) { err = String(e); }
  if (err) { updError(err); box.querySelectorAll("button").forEach(b => b.disabled = false); updBusy = false; }
}

// ---------- top bar ----------
function setFolder(path) {
  const home = S.info.home;
  let p = path || "";
  if (home && p.startsWith(home)) p = "~" + p.slice(home.length);
  $("#folderName").textContent = p || "~";
}
// The chip shows the model that answered last, or the default before any answer.
function setModel(label, sel) {
  if (sel) S.selection = sel;
  if (label && window.CloudModels && CloudModels.on()) label = "Blaze Cloud"; // never a provider or model (v0.19.1)
  if (label && window.PRIVACY) label = PRIVACY.route(label); // 0.19.16: nor with your own keys, unless the developer flag is on
  const t = $("#routeText"); if (!t) return;
  t.textContent = label || (S.selection && S.selection.auto ? "Auto · add a model" : "No model");
  $("#routeDot").className = "dot" + (label ? "" : " off");
  $("#route").title = (S.selection && S.selection.auto ? "Auto (free routing)" : "Default: " + (window.PRIVACY ? PRIVACY.route(S.selection?.label || "") : (S.selection?.label || ""))) + " · click to switch";
}

$("#folder").onclick = async () => { const d = await API().PickFolder(); S.prefs.folder = d; setFolder(d); };
$("#route").onclick = e => { e.stopPropagation(); modelPopover($("#route")); };
[["#newChat", () => newChat()], ["#newProject", () => newProject()]].forEach(([s, f]) => { const b = $(s); if (b) b.onclick = f; }); // v0.19: no sidebar
$$(".nav").forEach(b => b.onclick = () => show(b.dataset.view));
$$("#tabs button").forEach(b => b.onclick = () => show(b.dataset.tab));
addEventListener("keydown", e => {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "n") { e.preventDefault(); newChat(); }
  if ((e.ctrlKey || e.metaKey) && e.key === ",") { e.preventDefault(); show("settings"); }
  if (e.key === "Escape") {
    // In the orb, Esc hides Blaze (after closing any open menu first).
    if (document.body.classList.contains("orb") && !popEl && !menuOpen()) { e.preventDefault(); API().HideWindow(); }
    closePop();
  }
});

// Leaving a chat mid-answer: the answer keeps running in Go (sessions.go) and saves into
// its own chat; the sidebar shows it working, and opening it again picks it back up.
function leaveTurn() {
  if (!busy && !current) return;
  thinking(false); current = null; setBusy(false);
}
let LIVE = new Set(); // chats whose answer is running
function markLive() { $$("#chats .chat[data-id]").forEach(b => b.classList.toggle("live", LIVE.has(b.dataset.id))); }
on("live", ids => { LIVE = new Set(ids || []); markLive(); });

// One mind (v0.16): Chat mode has one continuous conversation. "New chat" there starts a
// fresh context instead; projects keep their own chats.
const oneMind = () => !(S && S.project);
async function clearContext() {
  if (!API().ClearContext) return;
  await API().ClearContext();
  if (view !== "work" && view !== "design") show("work");
  const col = $("#log .col", pane);
  if (col && col.children.length && !$(".hero", col)) {
    col.append(h("div", "ctxsep", `<span class="label">Fresh context</span><span class="d">Blaze looks older things up from memory</span>`));
    scrollDown(true);
  }
  $("#input").focus();
}
async function newChat() {
  if (oneMind()) return clearContext();
  leaveTurn();
  await API().NewConversation(); convId = ""; attachments = []; ATT = []; drawAttach();
  hero(); show(tab === "design" ? "design" : "work"); refreshSide();
}

// Links open in the system browser; code copy; step toggles.
document.addEventListener("click", e => {
  const a = e.target.closest("a[data-href]");
  if (a) { e.preventDefault(); API().OpenURL(a.dataset.href); }
  const c = e.target.closest(".copy");
  if (c) { navigator.clipboard.writeText(c.closest(".code").querySelector("pre").textContent); c.textContent = "Copied"; setTimeout(() => c.textContent = "Copy", 1200); }
  const st = e.target.closest(".step .head");
  if (st) st.setAttribute("aria-expanded", st.parentElement.classList.toggle("open"));
  if (!e.target.closest("#menu") && !e.target.closest("#plus")) closeMenu();
  if (!e.target.closest(".pop") && !e.target.closest("#route") && !e.target.closest("[data-pop]")) closePop();
});

document.addEventListener("keydown", e => {
  const st = (e.key === "Enter" || e.key === " ") && e.target.closest && e.target.closest(".step .head");
  if (st) { e.preventDefault(); st.click(); }
});

// ---------- popovers ----------
let popEl = null;
function closePop() { if (popEl) { popEl.remove(); popEl = null; } }
function openPop(anchor, build, opts = {}) {
  closePop();
  const p = h("div", "pop"); popEl = p;
  build(p);
  document.body.append(p);
  const r = anchor.getBoundingClientRect(), w = Math.max(opts.width || 300, p.offsetWidth || 0);
  p.style.width = (opts.width || 300) + "px";
  let left = opts.align === "left" ? r.left : r.right - w;
  left = Math.max(8, Math.min(left, innerWidth - w - 8));
  p.style.left = left + "px"; p.style.top = (r.bottom + 6) + "px";
  return p;
}

const capPills = m => {
  const out = [];
  if (m.tools === "yes") out.push('<span class="pill acc" title="Can call tools">Tools</span>');
  else if (m.tools === "no") out.push('<span class="pill" title="Can\'t call tools: chat only">No tools</span>');
  if (m.vision) out.push('<span class="pill" title="Understands images">Vision</span>');
  if (m.context) out.push(`<span class="pill" title="Context window">${m.context >= 1e6 ? (m.context / 1e6).toFixed(m.context % 1e6 ? 1 : 0) + "M" : Math.round(m.context / 1024) + "K"}</span>`);
  if (m.free) out.push('<span class="pill" title="On a free tier">Free</span>');
  return `<span class="caps">${out.join("")}</span>`;
};

// "Add a model": BlazeRouter when it is on, else Settings › Models (v0.19.1).
function addModels() { if (window.Router && Router.addModels) Router.addModels(); else show("settings", "models"); }

// The model switcher: Auto, or any ready provider and any of its models.
async function modelPopover(anchor) {
  if (popEl && popEl.dataset.kind === "model") { closePop(); return; }
  const st = await refreshState();
  const rpicks = window.Router ? await Router.picks() : [];
  openPop(anchor, p => {
    p.dataset.kind = "model";
    p.innerHTML = `<div class="phd"><span class="label">Model</span><span class="grow"></span><button class="btn ghost xs mg">Settings</button></div><div class="pbody"></div>`;
    $(".mg", p).onclick = () => { closePop(); addModels(); };
    const body = $(".pbody", p);
    const sel = st.selection || { auto: true };
    if (window.Router) Router.pickerTop(body, closePop, rpicks); // BlazeRouter presets first (router.js)
    const auto = h("button", "opt" + (sel.auto ? " sel" : ""), `<span class="on-mark">${sel.auto ? icon("check", "i sm") : ""}</span><span class="col2"><span class="ot">Auto (free routing)</span><span class="od">Fastest healthy model in your chain; skips ones that are resting</span></span>`);
    auto.onclick = async () => { const l = await API().SelectModel("auto", ""); setModel(l); closePop(); };
    body.append(auto);
    const ready = (st.providers || []).filter(x => x.ready && x.id !== "pollinations");
    if (!ready.length) body.append(h("div", "pnote", "No provider is set up yet. Add a free key to start, or switch to Blaze Cloud."));
    ready.forEach(pv => {
      const isCur = !sel.auto && sel.provider === pv.id;
      const row = h("button", "opt" + (isCur ? " sel" : ""), `<span class="on-mark">${isCur ? icon("check", "i sm") : ""}</span><span class="col2"><span class="ot">${esc(pv.short)}</span><span class="om">${esc(isCur ? sel.model : pv.model || "choose a model")}</span></span><span class="chev">${icon("chev", "i sm")}</span>`);
      const list = h("div", "models"); list.style.display = "none";
      row.onclick = async () => {
        if (list.style.display === "") { list.style.display = "none"; return; }
        $$(".models", body).forEach(x => x.style.display = "none");
        list.style.display = ""; list.innerHTML = `<div class="pnote">Loading ${esc(pv.short)}'s models…</div>`;
        const ml = await API().ModelOptions(pv.id, false);
        drawModelRows(list, pv, ml, isCur ? sel.model : pv.model, async m => { const l = await API().SelectModel(pv.id, m.id); setModel(l); closePop(); });
      };
      body.append(row, list);
    });
    const add = h("button", "opt", `<span class="on-mark">${icon("plus", "i sm")}</span><span class="col2"><span class="ot">Add a provider…</span><span class="od">Use your own key from a provider you choose</span></span>`);
    add.onclick = () => { closePop(); addModels(); };
    body.append(add);
  }, { width: 380 });
}

function drawModelRows(list, pv, ml, current, pick) {
  list.innerHTML = "";
  const ms = (ml && ml.models) || [];
  if (ms.length > 8) {
    const q = h("input", "field psearch"); q.placeholder = `Search ${ms.length} models`;
    q.oninput = () => $$(".mrow", list).forEach(r => r.style.display = r.dataset.id.toLowerCase().includes(q.value.toLowerCase()) ? "" : "none");
    list.append(q); setTimeout(() => q.focus(), 0);
  }
  ms.forEach(m => {
    const no = m.tools === "no";
    const r = h("button", "mrow" + (m.id === current ? " cur" : "") + (no ? " no" : ""), `<span class="mid" title="${esc(m.name || m.id)}${m.note ? " · " + esc(m.note) : ""}">${esc(m.id)}</span>${capPills(m)}`);
    r.dataset.id = m.id;
    if (no) r.title = "This model can't call tools, so Blaze can't act with it.";
    r.onclick = e => { e.stopPropagation(); pick(m); };
    list.append(r);
  });
  if (!ms.length) list.append(h("div", "pnote", "No models listed."));
  if (ml && ml.error) list.append(h("div", "pnote", esc(ml.error)));
}

// ---------- sidebar ----------
async function refreshSide() {
  S.project = (await API().State()).project;
  if (!$("#chats")) { if (window.OneChat) OneChat.sync(); return; } // v0.19: no sidebar to draw
  const pid = S.project ? S.project.id : "";
  const projects = await API().Projects() || [];
  const pn = $("#projects"); pn.innerHTML = "";
  const item = (id, name, sub) => {
    const b = h("button", "chat proj" + (id === pid ? " on" : ""), `<span class="sq"></span><span class="t">${esc(name)}</span>`);
    b.title = sub || "";
    b.onclick = async () => {
      if (id !== pid) {
        leaveTurn(); const p = await API().OpenProject(id); convId = ""; if (p.folder) { S.prefs.folder = p.folder; setFolder(p.folder); } hero(); await refreshSide(); }
      if (id) show("project", id); else show("work");
    };
    pn.append(b);
  };
  item("", "General", "Chats without a project");
  projects.forEach(p => item(p.id, p.name, p.folder));

  $("#chatsLabel").textContent = "Chats";
  $("#chatsCtx").textContent = "· " + (S.project ? S.project.name : "General");
  const nb = $("#newChat"); if (nb) nb.style.display = pid ? "" : "none"; // one mind: no new chats in Chat mode
  if (!pid) { // one mind: the one conversation, plus the read-only archive of old chats
    $("#chatsLabel").textContent = "Conversation";
    $("#chatsCtx").textContent = "· one mind";
    const nav = $("#chats"); nav.innerHTML = "";
    const m = h("button", "chat main" + ((view === "work" || view === "design") && (convId === "main" || !convId) ? " on" : ""), `<span class="t">Blaze</span><span class="when">${esc(S.version || "")}</span>`);
    m.title = "Your one continuous conversation. /clear starts a fresh context.";
    m.onclick = () => openChat("main");
    nav.append(m);
    const arch = API().ArchivedConversations ? await API().ArchivedConversations() || [] : [];
    if (arch.length) {
      const a = h("button", "chat arch", `<span class="t">Archive</span><span class="when">${arch.length}</span>`);
      a.title = "Chats from before one mind (read-only)"; a.onclick = () => show("archive");
      nav.append(a);
    }
    return;
  }
  const list = await API().Conversations(pid) || [];
  if (API().Live) LIVE = new Set(await API().Live() || []);
  const nav = $("#chats"); nav.innerHTML = "";
  if (!list.length) nav.append(h("div", "none", "No chats yet"));
  list.forEach(c => {
    const b = h("button", "chat" + (c.id === convId && (view === "work" || view === "design") ? " on" : "") + (LIVE.has(c.id) ? " live" : ""), `<span class="t">${esc(c.title)}</span><span class="when">${esc(c.updated || "")}</span><span class="x" title="Delete">${icon("x", "i sm")}</span>`);
    b.dataset.id = c.id;
    b.onclick = async e => {
      if (e.target.closest("input")) return; // renaming
      if (e.target.closest(".x")) { await API().DeleteConversation(c.id); if (c.id === convId) { convId = ""; hero(); } refreshSide(); return; }
      if (c.id === convId && b.classList.contains("on")) return;
      openChat(c.id);
    };
    if (window.Polish) Polish.decorate(b, c); // AI titles: animate, double-click to rename (polish.js)
    nav.append(b);
  });
}

async function openChat(id) {
  leaveTurn();
  const conv = await API().OpenConversation(id);
  convId = id;
  if (conv.folder) { S.prefs.folder = conv.folder; setFolder(conv.folder); }
  loadConv(conv);
  if (conv.running) { // still answering: pick the live answer back up
    const col = $("#log .col", pane);
    if (col) { const hr = $(".hero", col); if (hr) hr.remove(); current = blazeMsg(); col.append(current.el); setBusy(true); thinking(true, "Still working…"); }
  }
  show(tab === "design" ? "design" : "work");
  refreshSide();
}

async function refreshJobCount() {
  const t = await API().Jobs();
  const n = (String(t || "").match(/\[running/g) || []).length;
  $("#jobCount").textContent = n ? pad(n) : "";
}

// ---------- views ----------
function show(v, arg) {
  if (v === "chat") v = "work";
  view = v;
  if (v === "work" || v === "design") { tab = v; API().SetTab(v); }
  const navV = v === "connector" || v === "mcp" || v === "connectors" ? "accounts" : v;
  $$(".nav").forEach(b => b.classList.toggle("on", b.dataset.view === navV));
  $$("#tabs button").forEach(b => b.classList.toggle("on", b.dataset.tab === v));
  $("#tabs").classList.toggle("dim", v !== "work" && v !== "design");
  if (v === "design") $("#designDot").className = "";
  const el = $("#view"); pane.remove(); el.innerHTML = ""; closeMenu(); closePop();
  // Views from other files (accounts.js, onboard.js…) register in window.VIEWS.
  ((window.VIEWS && window.VIEWS[v]) || ({ work: workView, design: designView, settings: settingsView, doctor: doctorView, jobs: jobsView, schedule: scheduleView, project: projectView, newproject: newProjectView, memory: (e, a) => memoryView(e, a) })[v])(el, arg);
  $$("#chats .chat").forEach(b => b.classList.remove("on"));
  if (window.Router) Router.paint();
  if (window.Shell) Shell.sync();
}

function workView(el) {
  pane.classList.remove("narrow");
  el.append(pane);
  scrollDown(true);
  $("#input").focus();
}

// Page scaffold: eyebrow, title, lead, optional actions on the right.
function page(el, eyebrow, title, lead) {
  const pg = h("div", "page"); const col = h("div", "col"); pg.append(col); el.append(pg);
  const head = h("div", "phead", `<div class="ph"><div class="label acc">${esc(eyebrow)}</div><h1>${title}</h1>${lead ? `<p class="lead">${lead}</p>` : ""}</div>`);
  const acts = h("div", "acts"); acts.style.cssText = "display:flex;gap:8px"; head.append(acts);
  col.append(head);
  return { col, acts };
}
function group(title, desc, right) {
  const g = h("div", "group");
  const gh = h("div", "gh", `<span class="label">${esc(title)}</span>`);
  if (right) gh.append(right);
  g.append(gh);
  if (desc) g.append(h("div", "gd", desc));
  return g;
}

// ---------- chat pane ----------
function buildPane() {
  const p = h("div", "chatpane");
  p.innerHTML = `<div id="log"><div class="col"></div></div>
  <div id="composer"><div class="cwrap">
    <div id="menu" class="menu" style="display:none"></div>
    <div class="inbox">
      <div id="attach" class="attach"></div>
      <div class="irow">
        ${composerLead()}
        <div class="ifield"><span class="phint" aria-hidden="true"></span><textarea id="input" rows="1" placeholder="Ask Blaze to do something…" aria-label="Message Blaze"></textarea></div>
        ${composerTail()}
        <button id="send" class="sendbtn" title="Send (Enter)" aria-label="Send">${icon("send", "i sm")}<span class="sr">Send</span></button>
      </div>
    </div>
    <div id="visionwarn" class="visionwarn" style="display:none"></div>
    <div class="hintline"><span class="label weak" id="hint"></span></div>
  </div></div>`;
  const input = $("#input", p);
  input.addEventListener("input", () => { grow(); onType(); sendLabel(); });
  input.addEventListener("keydown", onKey);
  // Mid-answer the button stops the answer, unless there's a message to send: that one queues.
  $("#send", p).onclick = () => busy && !input.value.trim() && !ATT.length ? API().Stop() : submit();
  $("#plus", p).onclick = () => menuOpen() ? closeMenu() : openMenu("plus", plusItems(), "Attach");
  initComposer(p);
  if (window.Bubbles) Bubbles.attach(p);
  return p;
}
function grow() {
  const i = $("#input"); if (!i) return; i.style.height = "auto"; i.style.height = Math.min(i.scrollHeight, 240) + "px";
  // A long or multi-line message keeps the rounded ends (mobile v0.24.1): the pill becomes a large-radius box.
  const ib = i.closest(".inbox"); if (ib) ib.classList.toggle("tall", i.value.includes("\n") || i.scrollHeight > 52);
}

function hero() {
  const col = $("#log .col", pane); col.innerHTML = "";
  if (document.body.classList.contains("orb")) {
    const box = h("div", "hero orbhero");
    const cv = h("canvas", "orbbig"); cv.width = 360; cv.height = 360;
    box.append(cv, h("div", "big", "Ask anything"), h("p", "lead", "Blaze can act on this computer: files, commands, code, the web."));
    col.append(box); particleOrb(cv, 180);
    return;
  }
  // v0.19.14 less clutter: the greeting, one line, three tries (the same starters as Home). The OS
  // label, the shortcut hint and the "Try" label are gone; the project name stays inside a project.
  const hr = new Date().getHours();
  const hello = hr >= 5 && hr < 12 ? "Good morning" : hr >= 12 && hr < 17 ? "Good afternoon" : "Good evening";
  const who = (typeof ACCT !== "undefined" && ACCT && ACCT.signed_in && ACCT.user && (ACCT.user.name || "").trim().split(/\s+/)[0]) || "";
  const box = h("div", "hero");
  box.innerHTML = `<img class="hmark" src="logo.svg" alt="">${S.project ? `<div class="label acc">${esc(S.project.name)}</div>` : ""}<div class="big">${esc(hello)}${who ? `, ${esc(who)}` : ""}.</div>
    <p class="lead">What should we get done? Type <span class="kbd">/</span> for commands or <span class="kbd">@</span> to attach a file.</p>`;
  const sug = h("div", "suggest three");
  const tries = window.Home && Home.starters ? Home.starters() : [["Explore", "What's in this folder? Give me a short tour."], ["Fix", "Run the tests here and fix whatever fails."], ["System", "How much disk space is left, and what's using the most?"]];
  tries.forEach(([k, t]) => {
    const b = h("button", "", `<span class="label">${k}</span><span>${esc(t)}</span>`);
    b.onclick = () => { $("#input").value = t; submit(); };
    sug.append(b);
  });
  box.append(sug); col.append(box);
  const noModel = () => { if (S.setup_done && !(S.providers || []).some(p => p.ready && p.id !== "pollinations")) {
    const c = h("div", "callout acc", `${icon("key")}<div><b>No model yet.</b> Blaze needs one AI model that can use tools. A free key takes a minute. <a data-go="providers" style="color:var(--accent);cursor:pointer">Add one</a></div>`);
    c.style.marginTop = "18px"; $("a", c).onclick = () => addModels();
    box.append(c);
  } };
  if (window.CloudModels) CloudModels.heroCallout(box, noModel); else noModel(); // v0.19: Blaze Cloud needs only a sign-in
}

// Messages loaded from a saved chat have no time; live ones are stamped (bubbles.js groups by it).
let LOADING = false;
const stampNow = () => LOADING ? 0 : Date.now();
function loadConv(conv) {
  LOADING = true;
  try { loadConvInner(conv); } finally { LOADING = false; }
}
function loadConvInner(conv) {
  const col = $("#log .col", pane); col.innerHTML = "";
  if (!conv || !conv.messages || !conv.messages.length) { hero(); return; }
  let turn = null;
  const turns = [];
  const results = {};
  const anchors = {}; // agent handle -> the turn that dispatched it (its card goes right after)
  conv.messages.forEach(m => { if (m.role === "tool") results[m.tool_call_id] = m.content; });
  conv.messages.forEach(m => {
    if (m.role === "user" && isAgentResult(m.content)) { col.append(agentNote(m.content)); turn = null; }
    else if (m.role === "user") { col.append(userMsg(m.content, m.attach)); turn = null; }
    else if (m.role === "assistant") {
      if (!turn) { turn = blazeMsg(); turns.push(turn); col.append(turn.el); }
      (m.tool_calls || []).forEach(tc => {
        if (Agents.isMainTool(tc.function.name)) {
          const hm = /agent "([^"]+)"/.exec(results[tc.id] || "");
          if (tc.function.name === "dispatch_agent" && hm) anchors[hm[1]] = turn.el;
          return;
        }
        const s = addStep(turn, tc.id, tc.function.name, tc.function.arguments); finishStep(turn, s, results[tc.id] || "");
      });
      // "(cut off here)" marks, for the model, where a reply broke off; the user sees the text joined.
      if (m.content && m.content !== "…") turn.text += (turn.text ? "\n\n" : "") + m.content.replace(/\n\n\(cut off here\)$/, "");
    }
  });
  turns.forEach(t => renderAnswer(t, true));
  // Background agents: each card right after the turn that started it.
  const seen = new Set();
  Object.keys(anchors).forEach(k => { const v = (conv.agents || []).find(a => a.handle === k); if (v) { Agents.load([v], col, { [k]: anchors[k] }); seen.add(k); } });
  Agents.load((conv.agents || []).filter(a => !seen.has(a.handle)), col);
  // Saved chats: steps collapsed behind "Worked · N steps" (no timing is saved).
  turns.forEach(t => finishWork(t, null, STEPS.workedLine(t.order.length, null)));
  scrollDown(true);
}

// Where a background agent's result came back into the chat (a platform message, not the user).
const isAgentResult = c => String(c || "").startsWith("[Background agent result");
function agentNote(content) {
  const m = /^\[Background agent result · ([^·\]]+) · (\w+)/.exec(content) || [];
  const word = { done: "finished", failed: "couldn't finish" }[(m[2] || "").trim()] || "reported back";
  const who = typeof Specs !== "undefined" && Specs.has((m[1] || "").trim()) ? "Specialist" : "Agent";
  return h("div", "agnote", `<span class="label">${who} · <b>${esc((m[1] || "").trim())}</b> ${esc(word)}</span>`);
}

// User bubbles hide attached file bodies and long design-edit context.
function userMsg(content, att) {
  const files = [];
  let text = content.replace(/\n*<attached path="([^"]*)"([^>]*)>[\s\S]*?<\/attached>/g, (_, p, rest) => { if (!/kind=/.test(rest)) files.push(p); return ""; });
  if (text.startsWith("[Autopilot] ")) return apNote();
  const m = h("div", "msg user"); m.dataset.at = stampNow();
  if (text.startsWith(CONTINUE_TAG)) { m.classList.add("cont"); m.append(h("div", "bubble", `${icon("retry", "i sm")} Continue`)); return m; }
  if (text.startsWith("[Design edit]")) {
    const act = (text.match(/^\[Design edit\] (.*)/) || [])[1] || "Edit";
    const req = (text.match(/\nRequest: ([\s\S]*)$/) || [])[1] || "";
    const tags = [...text.matchAll(/\nElement: (.*)/g)].map(x => x[1]);
    m.append(h("div", "bubble", `<span class="label acc">${esc(act)}</span> ${esc(req)}`));
    if (tags.length) m.append(h("div", "chips", tags.map(t => `<span class="fchip">${esc(t.length > 40 ? "…" + t.slice(-40) : t)}</span>`).join("")));
  } else {
    m.append(h("div", "bubble", esc(text)));
    if (files.length) m.append(h("div", "chips", files.map(f => `<span class="fchip">@ ${esc(f)}</span>`).join("")));
    const ar = attachRow(att); if (ar) m.append(ar);
  }
  return m;
}

function blazeMsg() {
  // A new answer starts while the previous one is still live (autopilot's autoreply, retry):
  // close the previous one's working line so it never shimmers forever.
  if (current && current.workEl && !current.finished) finishWork(current);
  const el = h("div", "msg blaze"); el.dataset.at = stampNow();
  el.append(h("div", "who", `<span class="mk"></span><span class="label">Blaze</span>`));
  // One quiet status line per turn (Settings › Behaviour › Show every step turns the raw list back on).
  const workEl = h("button", "work", `<span class="sq"></span><span class="wt"></span><svg class="i sm chev" viewBox="0 0 16 16">${IC.down}</svg>`);
  workEl.style.display = "none";
  const stepsEl = h("div", "steps"); stepsEl.style.display = "none";
  const mdEl = h("div", "md");
  el.append(workEl, stepsEl, mdEl);
  const turn = { el, workEl, stepsEl, mdEl, text: "", steps: {}, order: [], started: Date.now(), open: false };
  el.__turn = turn;
  workEl.onclick = () => {
    turn.open = !turn.open;
    workEl.classList.toggle("open", turn.open);
    stepsEl.style.display = turn.open && turn.order.length ? "" : "none";
    workEl.setAttribute("aria-expanded", turn.open);
  };
  return turn;
}

const showSteps = () => !!(S && S.prefs && S.prefs.show_steps);

// ---------- v0.4.1 answer cards ----------
// Long detail folds into a card above the short answer (Settings › Behaviour › Fold long answers).
const foldCards = () => !(S && S.prefs && S.prefs.fold_cards === false);
const CARD_MAX = () => document.body.classList.contains("orb") ? 220 : 260;
const cardRO = typeof ResizeObserver === "function" ? new ResizeObserver(es => es.forEach(e => fitCard(e.target.closest(".acard")))) : null;

function renderAnswer(turn, final) {
  // Follow-ups ("<<follow-ups: a | b | c>>") become pills under the newest answer (v0.17.1).
  const fu = MD.splitFollowups(turn.text, final);
  if (window.Bubbles) Bubbles.followups(turn, final ? fu.followups : []);
  const a = MD.answer(fu.text, { fold: foldCards(), final });
  $$(".acard .abody", turn.mdEl).forEach(b => cardRO && cardRO.unobserve(b));
  turn.cards = a.cards;
  if (!a.cards.length) { turn.mdEl.classList.remove("hascards"); turn.mdEl.innerHTML = MD.render(a.rest); return; }
  const open = turn.cardOpen || (turn.cardOpen = {});
  turn.mdEl.classList.add("hascards");
  turn.mdEl.innerHTML = a.cards.map((c, i) => `<div class="acard${open[i] ? " open" : ""}${c.live ? " live" : ""}" data-i="${i}">
    <div class="ahead"><button class="atitle" aria-expanded="${!!open[i]}"><span class="amk"></span><span class="att">${MD.esc(c.title)}</span></button>
      <button class="aico acopy" title="Copy" aria-label="Copy this card">${icon("copy", "i sm")}</button>
      <button class="aico atog" title="${open[i] ? "Collapse" : "Expand"}" aria-label="${open[i] ? "Collapse" : "Expand"} ${MD.esc(c.title)}" aria-expanded="${!!open[i]}">${icon(open[i] ? "shrink" : "grow", "i sm")}</button></div>
    <div class="awrap"><div class="abody md">${MD.render(c.body)}</div><div class="afade"></div></div>
    <button class="amore" aria-label="${open[i] ? "Collapse" : "Show all of"} ${MD.esc(c.title)}" aria-expanded="${!!open[i]}">${icon("down", "i sm")}</button></div>`).join("")
    + (a.rest ? `<div class="short md">${MD.render(a.rest)}</div>` : "");
  $$(".acard", turn.mdEl).forEach(el => {
    const i = +el.dataset.i;
    el.style.setProperty("--amax", CARD_MAX() + "px");
    fitCard(el);
    if (cardRO) cardRO.observe($(".abody", el));
    const tog = () => toggleCard(el, turn, i);
    $(".atitle", el).onclick = tog; $(".atog", el).onclick = tog; $(".amore", el).onclick = tog;
    $(".acopy", el).onclick = () => {
      navigator.clipboard.writeText((turn.cards[i] || {}).body || "");
      const b = $(".acopy", el); b.innerHTML = icon("check", "i sm"); setTimeout(() => b.innerHTML = icon("copy", "i sm"), 1200);
    };
  });
}

// A card that fits under the max height shows in full: no fade, no expand buttons.
function fitCard(el) {
  if (!el) return;
  const h = $(".abody", el).scrollHeight;
  if (!h) return;
  const fits = h <= CARD_MAX() + 24;
  el.classList.toggle("fits", fits); el.classList.toggle("tall", !fits);
}

function toggleCard(el, turn, i) {
  if (!el.classList.contains("tall")) return;
  const wrap = $(".awrap", el), body = $(".abody", el);
  const open = !el.classList.contains("open");
  (turn.cardOpen || (turn.cardOpen = {}))[i] = open;
  const from = open ? CARD_MAX() : body.scrollHeight, to = open ? body.scrollHeight : CARD_MAX();
  wrap.style.maxHeight = from + "px";
  el.classList.toggle("open", open);
  void wrap.offsetHeight;
  wrap.style.maxHeight = to + "px";
  const end = e => { if (e && e.target !== wrap) return; wrap.style.maxHeight = ""; wrap.removeEventListener("transitionend", end); };
  wrap.addEventListener("transitionend", end); setTimeout(end, 450);
  const label = (open ? "Collapse " : "Expand ") + $(".att", el).textContent;
  $$(".atitle, .atog, .amore", el).forEach(b => b.setAttribute("aria-expanded", open));
  const t = $(".atog", el); t.title = open ? "Collapse" : "Expand"; t.setAttribute("aria-label", label); t.innerHTML = icon(open ? "shrink" : "grow", "i sm");
  $(".amore", el).setAttribute("aria-label", open ? label : "Show all of " + $(".att", el).textContent);
  if (!open) { const r = el.getBoundingClientRect(), log = $("#log", pane); if (log && r.top < log.getBoundingClientRect().top) el.scrollIntoView({ block: "start", behavior: "smooth" }); }
}
const clip = (t, n = 60) => { t = String(t || "").replace(/\s+/g, " ").trim(); return t.length > n ? t.slice(0, n - 1) + "…" : t; };

// The live status line: a plain-English phrase with a shimmer, crossfading when it changes.
function setStatus(turn, text) {
  if (!turn || !text) return;
  const w = turn.workEl, wt = $(".wt", w);
  w.style.display = ""; w.classList.add("live"); w.classList.remove("done");
  liveMeta(turn);
  const t = clip(text);
  if (turn.statusText === t) return;
  turn.statusText = t;
  const old = $$(".phrase", wt);
  const span = h("span", "phrase shim in", esc(t));
  wt.append(span);
  requestAnimationFrame(() => span.classList.remove("in"));
  old.forEach(o => { o.classList.add("out"); setTimeout(() => o.remove(), 260); });
  scrollDown();
}

// The live card's counter: "Working · 4 steps · 1m 12s", ticking once a second.
function liveMeta(turn) {
  const w = turn.workEl;
  let m = $(".wmeta", w);
  if (!m) { m = h("span", "wmeta label"); $(".wt", w).before(m); }
  const draw = () => {
    if (turn.finished) { clearInterval(turn.tick); turn.tick = 0; m.remove(); return; }
    const n = turn.order.length, secs = (Date.now() - turn.started) / 1000;
    const tk = turn.tokens, k = v => v >= 1000 ? (v / 1000).toFixed(1) + "k" : String(v);
    m.textContent = ["Working", n ? `${n} step${n === 1 ? "" : "s"}` : "", secs >= 1 ? (secs >= 60 ? `${Math.floor(secs / 60)}m ${Math.round(secs % 60)}s` : `${Math.round(secs)}s`) : "", tk ? `in ${k(tk.in)} · out ${k(tk.out)}` : "", mbText(turn)].filter(Boolean).join(" · ");
  };
  draw();
  if (!turn.tick) turn.tick = setInterval(draw, 1000);
}

// Low-data mode (v0.15): data used by this task's model calls, "0.4 MB".
const lowData = () => !!(S && S.prefs && S.prefs.low_data);
function mbText(turn) {
  if (!lowData() || !turn || !turn.bytes) return "";
  const mb = turn.bytes / 1048576;
  return (mb < 0.1 ? mb.toFixed(2) : mb.toFixed(1)) + " MB";
}

// When the turn ends the working line collapses to "Worked · 6 steps · 14s" (or nothing if
// no tools ran). Safe to call twice: the first call wins.
function finishWork(turn, seconds, label) {
  if (!turn || turn.finished) return;
  turn.finished = true;
  const w = turn.workEl;
  if (turn.tick) { clearInterval(turn.tick); turn.tick = 0; }
  const wm = $(".wmeta", w); if (wm) wm.remove();
  turn.statusText = "";
  if (!turn.order.length) { w.style.display = "none"; w.classList.remove("live"); $(".wt", w).innerHTML = ""; return; }
  const secs = seconds != null ? seconds : (Date.now() - turn.started) / 1000;
  const n = turn.order.length;
  const txt = (label || STEPS.workedLine(n, secs)) + (mbText(turn) ? " · " + mbText(turn) : "");
  w.classList.remove("live"); w.classList.add("done");
  $(".wt", w).innerHTML = `<span class="phrase">${esc(txt)}</span>`;
  w.title = `Click to ${turn.open ? "hide" : "see"} each step and its exact command`;
  w.setAttribute("aria-expanded", turn.open);
  w.style.display = showSteps() ? "none" : "";
}

// Plain words for a tool call: steps.js (no commands, paths or JSON while working).
const friendly = (name, args) => STEPS.describe(name, args);

function scrollDown(force) {
  const log = $("#log", pane); if (!log) return;
  if (force || log.scrollHeight - log.scrollTop - log.clientHeight < 160) log.scrollTop = log.scrollHeight;
}

function setBusy(b) {
  busy = b;
  document.body.classList.toggle("busy", !!b);
  if (window.Shell) Shell.sync();
  sendLabel();
  hintText();
}
function sendLabel() {
  const send = $("#send", pane), i = $("#input", pane); if (!send) return;
  const stop = busy && !(i && i.value.trim()) && !ATT.length;
  // The pill composer (v0.17.1): the mic shows while it's empty, a round send arrow once there's
  // text, a stop square while Blaze works.
  const ib = send.closest(".inbox");
  if (ib) { ib.classList.toggle("has-text", !!(i && i.value.trim()) || ATT.length > 0 || attachments.length > 0); ib.classList.toggle("busy", !!busy); ib.classList.toggle("hasatt", ATT.length > 0 || attachments.length > 0); }
  if (send.dataset.stop === String(stop)) return;
  send.dataset.stop = String(stop);
  send.title = stop ? "Stop (Esc)" : "Send (Enter)"; send.setAttribute("aria-label", stop ? "Stop" : "Send");
  send.innerHTML = stop ? `${icon("stop", "i sm")}<span class="sr">Stop</span>` : `${icon("send", "i sm")}<span class="sr">Send</span>`;
  send.classList.toggle("accent", !stop); send.classList.toggle("line", stop);
}

let thinkingEl = null;
function thinking(onOff, text) {
  if (!showSteps()) {
    // Hidden steps: everything feeds the one status line; it stays until the turn ends.
    if (onOff && current) setStatus(current, text || (current.order.length ? current.statusText || "Working on it" : "Thinking"));
    // The answer is streaming and no tools ran yet: the text is the progress, drop the line.
    if (!onOff && current && !current.order.length) { current.workEl.style.display = "none"; current.statusText = ""; $(".wt", current.workEl).innerHTML = ""; }
    return;
  }
  if (thinkingEl) { thinkingEl.remove(); thinkingEl = null; }
  if (onOff && current) {
    thinkingEl = h("div", "thinking", `<span class="sq has-orb"><blaze-emblem class="morb" size="22" live aria-hidden="true"></blaze-emblem></span><span>${esc(text || "Thinking")}</span>`);
    current.el.append(thinkingEl); scrollDown();
  }
}

function submit() {
  const input = $("#input", pane);
  let text = input.value.trim();
  if (!text && !ATT.length) return;
  if (!text) text = ATT.some(a => a.kind === "image") ? "Look at this." : "Take a look at this.";
  if (/^\/\S+$/.test(text)) { const c = matchCommands(text.slice(1))[0]; if (c) { input.value = ""; runCommand(c); return; } }
  input.value = ""; grow();
  sendText(text, attachments.slice(), takeAttach());
  attachments = []; drawAttach();
}

async function runCommand(c) {
  if (c.sub) { openMenu("sub", await c.sub(), c.name); return; }
  await c.run();
}

async function sendText(text, files, att) {
  const col = $("#log .col", pane);
  const heroEl = $(".hero", col); if (heroEl) heroEl.remove();
  // A new message moves on: an older reply's Continue would resume the wrong thing.
  $$(".cutrow", col).forEach(r => r.remove()); $$(".contbtn", col).forEach(b => b.remove());
  const bubble = userMsg(text + (files || []).map(f => `\n<attached path="${f}"></attached>`).join(""), att);
  col.append(bubble);
  if (busy) {
    // Blaze is mid-answer: no queue. Go stops that answer (keeping its text) and answers
    // this at once; the "turn" event opens the new answer.
    scrollDown(true);
    await API().SendWith(text, files || [], att || []);
    return;
  }
  current = blazeMsg(); current.pendingUser = true; col.append(current.el);
  setBusy(true); thinking(true);
  scrollDown(true);
  const r = await API().SendWith(text, files || [], att || []);
  if (r === "queued" && current && current.pendingUser) { current.el.remove(); current = null; bubble.classList.add("queued"); }
}

// A main turn starts in Go: a user message (maybe queued) or a background agent's result.
on("turn", t => {
  if (t && t.conv) convId = t.conv;
  const col = $("#log .col", pane); if (!col) return;
  if (t.kind === "agent") {
    if (current && current.pendingUser) { current.el.remove(); current = null; const q = $$(".msg.user", col).pop(); if (q) q.classList.add("queued"); }
    if (current) return;
    col.append(agentNote(`[Background agent result · ${t.handle} · ${(Agents.recs[t.handle] || { v: {} }).v.status || "done"}`));
  } else if (current && current.pendingUser) { current.pendingUser = false; return; }
  else if (current) return;
  else { const q = $(".msg.user.queued", col); if (q) q.classList.remove("queued"); }
  current = blazeMsg(); col.append(current.el);
  setBusy(true); thinking(true); scrollDown();
});

// One row per tool step: plain words on the row; the exact command or tool call and its
// output only inside the expanded row (mono).
const MAX_STEPS_SHOWN = 6;
function addStep(turn, id, name, args) {
  if (showSteps() || turn.open) turn.stepsEl.style.display = "";
  const s = h("div", "step run", `<div class="head" role="button" tabindex="0" aria-expanded="false"><span class="st"></span><span class="ph">${esc(friendly(name, args))}</span><span class="sum"></span><span class="dur"></span><svg class="i sm chev" viewBox="0 0 16 16">${IC.down}</svg></div>
    <div class="body"><div class="blk"><span class="label">${esc(STEPS.detailLabel(name))}</span><pre class="cmd"></pre></div><div class="blk out"><span class="label">Output</span><pre class="res"></pre></div></div>`);
  $(".cmd", s).textContent = STEPS.detail(name, args);
  s.dataset.tool = name;
  turn.stepsEl.append(s); turn.steps[id] = s; turn.order.push(s);
  foldSteps(turn);
  return s;
}

// Older steps fold behind one "N earlier steps" line.
function foldSteps(turn) {
  const vis = turn.order.filter(s => !s.dataset.dup);
  let more = $(".more", turn.stepsEl);
  if (vis.length <= MAX_STEPS_SHOWN || turn.expanded) { if (more) more.remove(); vis.forEach(s => s.classList.remove("hide")); return; }
  const hidden = vis.slice(0, vis.length - MAX_STEPS_SHOWN);
  vis.forEach(s => s.classList.toggle("hide", hidden.includes(s)));
  if (!more) { more = h("button", "more"); more.onclick = () => { turn.expanded = true; foldSteps(turn); }; turn.stepsEl.prepend(more); }
  more.textContent = `${hidden.length} earlier step${hidden.length > 1 ? "s" : ""} · show`;
}

const isBad = r => /^Error|^exit [1-9]|did NOT approve|^Refused|^Blaze couldn't|^Tool error/.test(r || "");
function summary(result) {
  const lines = String(result || "").split("\n").map(x => x.trim()).filter(Boolean);
  let first = lines[0] || "";
  first = first.replace(/^Error in [\w_]+:\s*/, "").replace(/^Error:\s*/, "");
  return first.length > 90 ? first.slice(0, 90) + "…" : first;
}

function finishStep(turn, s, result) {
  if (!s) return;
  const bad = isBad(result);
  const declined = /did NOT approve|declined/i.test(result || "");
  s.classList.remove("run"); s.classList.add(bad ? "bad" : "ok");
  const sum = summary(result);
  s.dataset.err = bad ? sum : "";
  // A failed step says so in plain words; what went wrong is inside the expanded row.
  s.querySelector(".sum").textContent = bad ? (declined ? "Declined" : "Failed") : "";
  // Only steps that took a noticeable time show it.
  if (s._t0) { const d = (Date.now() - s._t0) / 1000; s.querySelector(".dur").textContent = d < 1 ? "" : d < 60 ? d.toFixed(d < 10 ? 1 : 0) + "s" : Math.floor(d / 60) + "m " + Math.round(d % 60) + "s"; }
  s.querySelector(".res").textContent = String(result || "").trim() || "(no output)";
  // The same error again collapses into the previous row with a ×N count.
  if (bad && turn) {
    const i = turn.order.indexOf(s), prev = turn.order.slice(0, i).reverse().find(x => !x.dataset.dup);
    if (prev && prev.classList.contains("bad") && prev.dataset.tool === s.dataset.tool && prev.dataset.err === sum) {
      s.dataset.dup = "1"; s.classList.add("hide");
      const n = (+prev.dataset.n || 1) + 1; prev.dataset.n = n;
      let rep = $(".rep", prev); if (!rep) { rep = h("span", "rep"); prev.querySelector(".sum").after(rep); }
      rep.textContent = "×" + n;
      foldSteps(turn);
    }
  }
}

function routeMeta(turn, d) {
  if (!turn || $(".meta", turn.el)) return;
  const m = h("div", "meta");
  const secs = d && d.seconds ? ` · ${d.seconds.toFixed(1)}s` : "";
  // Blaze Cloud: just "Blaze Cloud", no provider, model or "X didn't answer, so Y did" (v0.19.1, as on mobile).
  if (d && window.CloudModels && CloudModels.on()) d = Object.assign({}, d, { route: "Blaze Cloud", note: "" });
  if (d && window.PRIVACY) d = Object.assign({}, d, { route: PRIVACY.route(d.route), note: PRIVACY.note(d.note) });
  m.innerHTML = `<span class="route"><span class="dot${d && d.keyless ? " off" : ""}"></span>${esc((d && d.route) || "")}${secs}</span>${d && d.note ? `<span class="note">${esc(d.note)}</span>` : ""}<span class="grow"></span><span class="acts"><button class="btn ghost xs cp" title="Copy answer">${icon("copy", "i sm")}</button><button class="btn ghost xs rt" title="Ask again">${icon("retry", "i sm")}</button></span>`;
  const text = turn.text;
  $(".cp", m).onclick = () => navigator.clipboard.writeText(text);
  $(".rt", m).onclick = () => retry();
  turn.el.append(m);
}

// ---------- Continue: pick up a reply that was cut off ----------
// Shown when a reply hit the model's output limit, the stream broke, the user pressed
// Stop, an error ended it part-way, or it ran out of steps. The model sees what it
// already wrote (Go keeps the partial text) and carries on from there.
const CONTINUE_TAG = "[Continue]";
const CONTINUE_TEXT = CONTINUE_TAG + " Pick up exactly where you stopped.";
function continueReply() {
  $$(".contbtn").forEach(b => b.closest(".cutrow") ? b.closest(".cutrow").remove() : b.remove());
  sendText(CONTINUE_TEXT, [], []);
}
function continueBtn(cls = "accent") {
  const b = h("button", `btn sm ${cls} contbtn`, `${icon("retry", "i sm")}Continue`);
  b.title = "Carry on from where the reply stopped";
  b.onclick = e => { e.stopPropagation(); continueReply(); };
  return b;
}
function cutRow(target, why) {
  if (!target || $(".cutrow", target)) return;
  const row = h("div", "cutrow", `<span class="label">${esc(why)}</span><span class="grow"></span>`);
  row.append(continueBtn());
  target.append(row);
}

// The error card: one plain sentence, what each model said (short), and actions.
function errorCard(target, e) {
  if (typeof e === "string") e = { message: e, detail: "" };
  if (window.PRIVACY) { // 0.19.16: what failed is said plainly, never which provider or model
    const P = PRIVACY;
    e = Object.assign({}, e, { message: P.scrub(e.message), detail: P.scrub(e.detail), failures: (e.failures || []).map(f => ({ name: P.route(f.name), message: P.scrub(f.message) })) });
  }
  const fails = e.failures || [];
  const card = h("div", "card err");
  const setup = e.kind === "setup";
  card.innerHTML = `<div class="title"><span class="label ink">${setup ? "Set up a model" : "Couldn't finish"}</span></div>
    <div class="msgtext">${esc(e.message || "Something went wrong.")}</div>
    ${fails.length > 1 ? `<div class="fails">${fails.map(f => `<div><span class="n">${esc(f.name)}</span><span>${esc(f.message)}</span></div>`).join("")}</div>` : ""}
    <div class="actions"></div><pre></pre>`;
  const acts = $(".actions", card);
  const btn = (cls, html, fn) => { const b = h("button", "btn sm " + cls, html); b.onclick = fn; acts.append(b); return b; };
  if (setup) btn("accent", `${icon("key", "i sm")}Add a model`, () => addModels());
  else if (e.kind !== "steps") btn("accent", `${icon("retry", "i sm")}Retry`, () => retry());
  if (e.kind !== "steps") { const sw = btn("line", `${icon("layers", "i sm")}Switch model`, ev => { ev.stopPropagation(); modelPopover(sw); }); sw.dataset.pop = "1"; }
  if (e.kind === "steps" || e.partial) acts.prepend(continueBtn(e.kind === "steps" ? "accent" : "line"));
  if (e.detail && e.detail !== e.message) btn("ghost", "Details", () => card.classList.toggle("open"));
  $("pre", card).textContent = e.detail || "";
  target.append(card);
}

// ---------- events from Go ----------
on("thinking", () => { if (current) thinking(true); });
// Backend progress lines can carry raw commands ("Running: cat …"): only plain words reach the line.
on("status", t => { const p = STEPS.plainStatus(window.PRIVACY ? PRIVACY.scrub(t) : t); if (current && p) thinking(true, p); });
on("delta", d => {
  if (!current) return;
  thinking(false);
  current.text += d;
  renderAnswer(current, false);
  scrollDown();
});
on("tool", t => {
  if (!current) return;
  if (Agents.isMainTool(t.name)) { thinking(true, t.name === "dispatch_agent" ? "Handing this to a background agent" : t.name === "dispatch_specialist" ? "Briefing a specialist" : t.name === "apply_specialist" ? "Applying a specialist's changes" : /specialist/.test(t.name) ? "Checking on the specialists" : "Checking on your agents"); return; }
  thinking(false);
  if (current.text) current.text += "\n\n";
  addStep(current, t.id, t.name, t.args)._t0 = Date.now();
  thinking(true, friendly(t.name, t.args));
  scrollDown();
});
on("tool_done", t => {
  if (!current) return;
  const s = current.steps[t.id];
  finishStep(current, s, t.result);
  if (view === "design" && s && /^file_(write|edit)$/.test(s.dataset.tool)) reloadPreview();
});
on("repair", r => {
  if (!current) return;
  if (showSteps() || current.open) current.stepsEl.style.display = "";
  const n = (r.dropped || []).length;
  let fx = $(".fixnote", current.stepsEl);
  if (!fx) { fx = h("div", "fixnote"); current.stepsEl.append(fx); }
  fx.dataset.n = (+fx.dataset.n || 0) + n;
  fx.textContent = `Skipped ${fx.dataset.n} malformed tool call${fx.dataset.n > 1 ? "s" : ""} from the model and asked it again.`;
  fx.title = (r.dropped || []).join("\n");
});
on("route", r => { if (r && r.route) setModel(r.route); });
// Token meter (long-chat engine): the live card shows in/out; near the model's limit,
// offer a fresh chat that starts from a summary (once per chat).
let freshOffered = null;
on("tokens", t => {
  if (!t) return;
  if (current) { current.tokens = t; current.bytes = (current.bytes || 0) + (t.bytes || 0); if (current.workEl && !current.finished && current.workEl.classList.contains("live")) liveMeta(current); }
  if (t.high && freshOffered !== convId && current) {
    freshOffered = convId;
    const c = h("div", "card freshoffer", `<div class="title"><span class="label acc">Long chat</span></div><div class="detail">This chat is near the model's limit (${Math.round(t.in / 1000)}k of ${Math.round(t.limit / 1000)}k tokens). Start a fresh chat that picks up from a summary?</div><div class="actions"><button class="btn accent sm go">Start a fresh chat with a summary</button><button class="btn ghost sm no">Not now</button></div>`);
    $(".go", c).onclick = async () => { $(".go", c).disabled = true; $(".go", c).textContent = "Summarising…"; const id = await API().FreshChat(); if (id) { await refreshSide(); openChat(id); } else c.remove(); };
    $(".no", c).onclick = () => c.remove();
    current.el.append(c); scrollDown();
  }
});
on("model", m => { if (m) setModel(m.label, m.selection); });
on("confirm", c => {
  if (c.agent && Agents.confirm(c)) return; // a background agent's approval shows on its card
  thinking(false);
  const btns = c.always
    ? `<button class="btn accent sm yes">Allow once</button><button class="btn line sm al">Always allow</button><button class="btn ghost sm no">Don't allow</button>`
    : `<button class="btn accent sm yes">Yes, do it</button><button class="btn line sm no">No</button>`;
  const card = h("div", "card confirm", `<div class="title"><span class="label acc">${esc(c.title)}</span></div><div class="detail">${esc(c.detail)}</div>
    <div class="actions">${btns}</div>`);
  const done = choice => { API().AnswerChoice(c.id, choice); card.classList.add("done"); card.querySelector(".actions").innerHTML = `<span class="label">${choice === "always" ? "Always allowed" : choice === "once" ? "Approved" : "Declined"}</span>`; thinking(true); };
  card.querySelector(".yes").onclick = () => done("once");
  card.querySelector(".no").onclick = () => done("no");
  if (c.always) card.querySelector(".al").onclick = () => done("always");
  (current ? current.el : $("#log .col", pane)).append(card);
  scrollDown(true);
});
on("done", d => {
  thinking(false);
  // v0.19.14: an ack ("Got it, … I'll confirm when it's set.") is a plain Blaze bubble: no meta row.
  if (current && d && d.ack) { renderAnswer(current, true); finishWork(current); current.el.classList.add("ack"); }
  else if (current) { renderAnswer(current, true); finishWork(current, d && d.seconds); routeMeta(current, d); if (d && d.cut) cutRow(current.el, "The reply was cut off"); }
  if (d && d.route) setModel(d.route);
  window.__lastReply = current && current.el; current = null; setBusy(false); scrollDown(); refreshSide();
});
// v0.19.14: a background run's final (Done ✓ … / Couldn't finish …) lands as its own plain Blaze
// message in its chat, even while another answer is on its way.
function bgFinalBubble(text, kind) {
  const el = h("div", "msg blaze bgfinal" + (kind ? " " + kind : "")); el.dataset.at = stampNow();
  el.append(h("div", "who", `<span class="mk"></span><span class="label">Blaze</span>`));
  const workEl = h("button", "work"); workEl.style.display = "none";
  const stepsEl = h("div", "steps"); stepsEl.style.display = "none";
  const mdEl = h("div", "md");
  el.append(workEl, stepsEl, mdEl);
  const t = { el, workEl, stepsEl, mdEl, text: String(text || ""), steps: {}, order: [], started: Date.now(), open: false, finished: true };
  el.__turn = t;
  renderAnswer(t, true);
  return el;
}
on("blaze_msg", m => {
  if (!m || !m.text) return;
  if (m.conv && convId && m.conv !== convId) { refreshSide(); return; } // saved in its own chat
  const col = $("#log .col", pane); if (!col) return;
  const el = bgFinalBubble(m.text, m.kind);
  if (current && current.el && current.el.parentNode === col) col.insertBefore(el, current.el); else col.append(el);
  scrollDown(); refreshSide();
});
// Focus decides whether a final also sends a desktop notification (only when Blaze isn't focused).
(() => {
  const tell = () => { try { API().SetFocused(document.hasFocus() && !document.hidden); } catch (e) {} };
  window.addEventListener("focus", tell); window.addEventListener("blur", tell);
  document.addEventListener("visibilitychange", tell);
  try { const tz = Intl.DateTimeFormat().resolvedOptions().timeZone; if (tz) API().SetTimeZone(tz); } catch (e) {}
  setTimeout(tell, 500);
})();
on("error", e => {
  thinking(false);
  if (current) { if (current.text) renderAnswer(current, true); finishWork(current); }
  errorCard(current ? current.el : $("#log .col", pane), e);
  window.__lastReply = current && current.el; current = null; setBusy(false); scrollDown(true); refreshSide();
});
on("stopped", d => {
  thinking(false);
  if (current) { if (current.text) renderAnswer(current, true); finishWork(current); }
  if (current) {
    const m = h("div", "meta", `<span class="route">Stopped</span><span class="grow"></span>`);
    if ((d && d.resumable) || current.text) m.append(continueBtn("ghost xs"));
    current.el.append(m);
  }
  window.__lastReply = current && current.el; current = null; setBusy(false); refreshSide();
});
on("design", d => {
  if (view === "design") { drawDesign(); return; }
  const mode = S.prefs.design_mode || "ask";
  // A 3D mockup is made to be picked and edited, so it opens Design unless the user chose Never.
  if (mode === "always" || (d && d.three && mode !== "never")) { show("design"); return; }
  $("#designDot").className = "dotnew";
  if (mode === "never") return;
  if (!d || !d.target) return;
  const what = d.target.kind === "url" ? d.target.url : d.target.path;
  const card = h("div", "card confirm", `<div class="title"><span class="label acc">Design ready</span></div><div>Preview <span class="mono">${esc(what || "the design")}</span> live, with versions and point-to-edit.</div>
    <div class="actions"><button class="btn accent sm yes">Open Design</button><button class="btn line sm no">Not now</button><button class="btn ghost sm al">Always open</button></div>`);
  card.querySelector(".yes").onclick = () => { card.remove(); show("design"); };
  card.querySelector(".no").onclick = () => card.remove();
  card.querySelector(".al").onclick = async () => { await API().SetDesignMode("always"); S.prefs.design_mode = "always"; card.remove(); show("design"); };
  $("#log .col", pane).append(card); scrollDown(true);
});

// ---------- orb (quick ask) and window frame ----------
// The orb is a compact mode of the one window: no sidebar or top bar, just the chat
// with a small bar on top. Go resizes and pins the window and emits "orb".
function setOrb(onOff) {
  document.body.classList.toggle("orb", !!onOff);
  if (S && S.desktop) S.desktop.orb = !!onOff;
  if (onOff) {
    if (!$("#orbbar")) {
      const bar = h("div", "", `<canvas width="44" height="44"></canvas><span class="label ink">Blaze</span><span class="grow"></span>
        <button class="themebtn" title="Switch theme"></button>
        <button class="btn ghost xs" data-o="new" title="New chat">${icon("plus", "i sm")}<span>New</span></button>
        <button class="btn ghost xs" data-o="expand" title="Open the full window">${icon("open", "i sm")}<span>Expand</span></button>
        <button class="btn ghost xs" data-o="hide" title="Hide (Esc)">${icon("x", "i sm")}<span>Hide</span></button>`);
      bar.id = "orbbar"; bar.style.setProperty("--wails-draggable", "drag");
      $$("button", bar).forEach(b => b.style.setProperty("--wails-draggable", "no-drag"));
      $("[data-o=new]", bar).onclick = () => newChat();
      $("[data-o=expand]", bar).onclick = () => API().ExpandOrb();
      $("[data-o=hide]", bar).onclick = () => API().HideWindow();
      $("#main").prepend(bar);
      particleOrb($("canvas", bar), 22);
      Agents.orbBadge();
    }
    if (view !== "work") show("work");
    pane.classList.add("narrow");
  } else {
    const bar = $("#orbbar"); if (bar) bar.remove();
    pane.classList.remove("narrow");
  }
  if ($(".hero", pane)) hero();
  const i = $("#input"); if (i) setTimeout(() => i.focus(), 50);
}
on("orb", v => setOrb(v));

// A sphere of square particle dots, slowly turning. Stops when its canvas leaves the page.
function particleOrb(cv, size) {
  const ctx = cv.getContext("2d"), dpr = cv.width / size, N = size > 60 ? 260 : 60;
  const pts = Array.from({ length: N }, (_, i) => { const y = 1 - (i + .5) * 2 / N, r = Math.sqrt(1 - y * y), t = i * 2.39996; return [Math.cos(t) * r, y, Math.sin(t) * r]; });
  const t0 = performance.now();
  const frame = now => {
    if (!cv.isConnected && now - t0 > 1000) return;
    const css = getComputedStyle(document.documentElement);
    const acc = css.getPropertyValue("--accent").trim() || "#E6E6EA", ink = css.getPropertyValue("--ink").trim() || "#111110";
    const still = window.BlazeMotion && !window.BlazeMotion.enabled, el = still ? 0 : now - t0;   // reduced motion: one still frame
    const a = el / 4000, R = size * .38 * dpr, c = size / 2 * dpr, breathe = 1 + Math.sin(el / 900) * .03;
    ctx.clearRect(0, 0, cv.width, cv.height);
    pts.forEach(([x, y, z], i) => {
      const xr = x * Math.cos(a) - z * Math.sin(a), zr = x * Math.sin(a) + z * Math.cos(a);
      const yr = y * Math.cos(.4) - zr * Math.sin(.4), zz = y * Math.sin(.4) + zr * Math.cos(.4);
      const d = (zz + 1) / 2, s = Math.max(1, (size > 60 ? 2.6 : 1.6) * dpr * (.45 + d * .75));
      ctx.globalAlpha = .18 + d * .82;
      ctx.fillStyle = i % 7 === 0 ? ink : acc;
      ctx.fillRect(c + xr * R * breathe - s / 2, c + yr * R * breathe - s / 2, s, s);
    });
    ctx.globalAlpha = 1;
    if (still) setTimeout(() => requestAnimationFrame(frame), 500); else requestAnimationFrame(frame);
  };
  requestAnimationFrame(frame);
}

// Windows runs frameless: our own minimise / maximise / close in the top bar.
function setupFrame() {
  const os = (S.desktop && S.desktop.os) || S.info.os;
  document.body.classList.add("os-" + os);
  if (!(S.desktop && S.desktop.frameless)) return;
  const top = $("#top");
  const ctl = h("div", "winctl", `<button data-w="min" title="Minimise"><svg class="i sm" viewBox="0 0 16 16"><path d="M3.5 8.5h9"/></svg></button><button data-w="max" title="Maximise"><svg class="i sm" viewBox="0 0 16 16"><rect x="3.5" y="3.5" width="9" height="9"/></svg></button><button data-w="close" class="close" title="Close">${icon("x", "i sm")}</button>`);
  ctl.style.setProperty("--wails-draggable", "no-drag");
  $("[data-w=min]", ctl).onclick = () => API().Minimise();
  $("[data-w=max]", ctl).onclick = () => API().ToggleMaximise();
  $("[data-w=close]", ctl).onclick = () => API().CloseWindow();
  top.append(ctl);
  top.addEventListener("dblclick", e => { if (!e.target.closest("button")) API().ToggleMaximise(); });
}

// ---------- command menu ----------
// /model is two levels: Auto or a provider, then that provider's models.
async function modelMenuItems() {
  const items = (await API().Models() || []).map(m => m.provider === "blaze-cloud"
    ? { name: m.name, desc: (m.current ? "in use · " : "") + m.model, run: async () => setModel(await API().SelectModel("blaze-cloud", "")) }
    : m.provider === "blazerouter" && window.Router
    ? { name: "BlazeRouter", desc: (m.current ? "in use · " : "") + m.model, sub: () => Router.menuItems() }
    : m.provider === "auto"
    ? { name: "Auto (free routing)", desc: (m.current ? "in use · " : "") + "fastest healthy model in your chain", run: async () => setModel(await API().SelectModel("auto", "")) }
    : { name: m.name, desc: (m.current ? "in use · " : "") + m.model, sub: async () => {
        const ml = await API().ModelOptions(m.provider, false);
        return (ml.models || []).filter(x => x.tools !== "no").map(x => ({ name: x.id, desc: [x.tools === "yes" ? "tools" : x.tools === "no" ? "no tools" : "", x.vision ? "vision" : "", x.id === m.model ? "current" : ""].filter(Boolean).join(" · "), run: async () => setModel(await API().SelectModel(m.provider, x.id)) }));
      } });
  return items.concat([{ name: "Manage models…", desc: "Settings › Models", run: () => addModels() }]);
}

const COMMANDS = [
  { name: "new", desc: "New chat in a project (in Chat: a fresh context)", run: () => newChat() },
  { name: "model", desc: "Switch provider and model, or Auto", sub: modelMenuItems },
  { name: "router", desc: "BlazeRouter: providers, keys, combos, usage", run: () => show("router") },
  { name: "project", desc: "Switch project or make a new one", sub: async () => [{ name: "General", desc: "No project", run: () => switchProject("") }].concat((await API().Projects() || []).map(p => ({ name: p.name, desc: p.folder, run: () => switchProject(p.id) }))).concat([{ name: "New project…", desc: "Pick a folder", run: () => newProject() }]) },
  { name: "folder", desc: "Change the working folder", run: () => $("#folder").click() },
  { name: "design", desc: "Open the Design tab", run: () => show("design") },
  { name: "work", desc: "Back to the Work tab", run: () => show("work") },
  { name: "attach", desc: "Attach a project file (or type @)", run: () => { const i = $("#input"); i.value += (i.value && !i.value.endsWith(" ") ? " " : "") + "@"; i.focus(); onType(); } },
  { name: "retry", desc: "Ask the last message again", run: () => retry() },
  { name: "stop", desc: "Stop the current task", run: () => API().Stop() },
  { name: "clear", desc: "Fresh context window (the conversation and memory stay)", run: () => clearContext() },
  { name: "skills", desc: "Use one of your skills", sub: skillMenuItems },
  { name: "plan", desc: "Plan mode: read and scout only, then a plan (Ctrl+Tab)", run: () => setWorkMode("plan") },
  { name: "build", desc: "Build mode: every tool (Ctrl+Tab)", run: () => setWorkMode("build") },
  { name: "autopilot", desc: "Work until done; decide instead of asking (Shift+Tab)", run: () => setApproval("autopilot") },
  { name: "auto", desc: "Ask before risky actions (Shift+Tab)", run: () => setApproval("auto") },
  { name: "image", desc: "Attach images", run: () => pick("image") },
  { name: "voice", desc: "Speak instead of typing (local Whisper)", run: () => micClick() },
  { name: "theme", desc: "System, light or dark", sub: async () => ["system", "light", "dark"].map(t => ({ name: t, desc: t === S.prefs.theme ? "current" : "", run: async () => { await API().SetTheme(t); S.prefs.theme = t; applyTheme(); } })) },
  { name: "jobs", desc: "Background jobs and their logs", run: () => show("jobs") },
  { name: "schedule", desc: "Scheduled jobs", run: () => show("schedule") },
  { name: "doctor", desc: "Check models, shell and tools", run: () => show("doctor") },
  { name: "usage", desc: "Tokens and cost per model", run: () => show("settings", "usage") },
  { name: "memory", desc: "Your memory graph: what Blaze remembers", run: () => show("memory") },
  { name: "settings", desc: "Models, behaviour, appearance", run: () => show("settings") },
  { name: "update", desc: "Check for a new version of Blaze", run: () => show("settings", "updates") },
];

// /skills: the skills Blaze can use, with their tags (Settings › Skills) for the filter.
async function skillMenuItems() {
  const s = (await API().State()).skills || [];
  if (!s.length) return [{ name: "No skills yet", desc: "Ask Blaze to save one after a task", run: () => {} }];
  let tags = {};
  try { (await API().SkillList() || []).forEach(v => { tags[v.name] = v.tags || []; }); } catch (e) { tags = {}; }
  return s.map(x => { const n = x.split(":")[0]; return { name: n, desc: x.split(":").slice(1).join(":").trim(), tags: tags[n] || [], run: () => { const i = $("#input"); i.value = `Use the ${n} skill: `; i.focus(); } }; });
}

// Every menu narrows as you type with the same ranker (filter.js): the "/" menu, the pickers
// it opens (skills, model, project, theme…) and @files. A picker filters on the text typed
// after it opened (menu.base); "/skills sli" opens the skills picker already narrowed.
let menu = { items: [], idx: 0, kind: "" };
const menuOpen = () => $("#menu") && $("#menu").style.display !== "none";
const matchCommands = q => PickFilter.rank(COMMANDS, q);
const NOUN = { skills: "skill", model: "model", project: "project", theme: "theme", commands: "command", attach: "item" };
function closeMenu() { const m = $("#menu"); if (m) m.style.display = "none"; menu.kind = ""; menu.all = null; menu.cmd = ""; const pl = $("#plus"); if (pl) pl.classList.remove("open"); }

async function openMenu(kind, items, title, opts = {}) {
  if (kind === "root") items = COMMANDS.map(c => ({ ...c, slash: true }));
  if (kind === "model") { items = await modelMenuItems(); title = "Model"; }
  const pickers = kind === "sub" || kind === "model" || kind === "root";
  const input = $("#input");
  menu = { items: items || [], all: pickers ? (items || []) : null, idx: 0, kind, title, q: "",
    base: opts.base != null ? opts.base : (input ? input.value : ""), cmd: opts.cmd || "" };
  if (pickers && opts.q) filterMenu(opts.q); else drawMenu();
  const pl = $("#plus"); if (pl) pl.classList.toggle("open", kind === "plus");
}

// Narrow an open picker to q; the first match is selected.
function filterMenu(q) {
  if (!menu.all) return;
  menu.q = q || "";
  menu.items = menu.q.trim() ? PickFilter.rank(menu.all, menu.q) : menu.all.map(it => ({ ...it, _pos: null, _dpos: null }));
  menu.idx = 0;
  drawMenu();
}

function drawMenu() {
  const m = $("#menu"); if (!m) return;
  if (!menu.items.length && !(menu.all && menu.q.trim())) { closeMenu(); return; }
  const filtering = menu.all && menu.q.trim();
  const head = menu.title ? `<div class="mhead label"><span>${esc(menu.title)}</span>${filtering ? `<span class="mcount">${menu.items.length} of ${menu.all.length}</span>` : ""}</div>` : "";
  const noun = NOUN[String(menu.title || menu.cmd).toLowerCase()] || "item";
  m.innerHTML = head + (menu.items.length
    ? menu.items.slice(0, 60).map((it, i) => `<button class="mi ${i === menu.idx ? "on" : ""}" data-i="${i}"><span class="mn">${it.icon ? cmIcon(it.icon) : ""}${it.slash ? "/" : ""}${PickFilter.hl(it.name, it._pos)}${it.sub ? " ›" : ""}</span><span class="md2">${PickFilter.hl(it.desc || "", it._dpos)}</span></button>`).join("") +
      `<div class="mfoot label weak">↑↓ move · Enter pick · Esc close</div>`
    : `<div class="mempty">No ${esc(noun)} matches '${esc(menu.q.trim())}'</div><div class="mfoot label weak">Backspace to widen · Esc close</div>`);
  m.style.display = "";
  m.querySelectorAll(".mi").forEach(b => { b.onmouseenter = () => { menu.idx = +b.dataset.i; m.querySelectorAll(".mi").forEach(x => x.classList.toggle("on", x === b)); }; b.onclick = () => pickMenu(+b.dataset.i); });
  const sel = m.querySelector(".mi.on"); if (sel && sel.scrollIntoView) sel.scrollIntoView({ block: "nearest" });
}

async function pickMenu(i) {
  const it = menu.items[i]; if (!it) return;
  const input = $("#input");
  if (menu.kind === "file") {
    input.value = input.value.replace(/(^|\s)@[^\s]*$/, "$1");
    if (!attachments.includes(it.name)) attachments.push(it.name);
    drawAttach(); closeMenu(); input.focus(); return;
  }
  if (menu.kind === "cmd" || menu.kind === "root") input.value = "";
  else if (menu.all) input.value = menu.cmd ? "" : menu.base; // drop the filter text
  if (menu.kind === "plus" && !it.sub) { closeMenu(); await it.run(); return; }
  if (it.sub) { openMenu("sub", await it.sub(), it.name, { base: input.value }); return; }
  closeMenu(); grow();
  await it.run();
  if ((view === "work" || view === "design") && $("#input")) $("#input").focus();
}

let filesCache = null, filesAt = 0;
async function onType() {
  const input = $("#input"), v = input.value.slice(0, input.selectionStart);
  if (/^\/\S*$/.test(v) && input.value === v) { openMenu("cmd", matchCommands(v.slice(1)).map(c => ({ ...c, slash: true }))); return; }
  // "/skills sli", "/model gpt", "/theme d": open that command's picker, narrowed.
  const pre = input.value === v && v.match(/^\/(\S+) (.*)$/s);
  const pc = pre && COMMANDS.find(c => c.name === pre[1].toLowerCase() && c.sub);
  if (pc) {
    if (menu.all && menu.cmd === pc.name) { filterMenu(pre[2]); return; }
    const items = await pc.sub();
    const now = input.value.match(/^\/(\S+) (.*)$/s);
    if (!now || now[1].toLowerCase() !== pc.name) { onType(); return; }
    openMenu("sub", items, pc.name, { base: `/${now[1]} `, cmd: pc.name, q: now[2] });
    return;
  }
  // An open picker (skills, model…) narrows to what was typed after it opened.
  if (menu.all && menu.kind && input.value.startsWith(menu.base) && !menu.cmd) { filterMenu(input.value.slice(menu.base.length)); return; }
  const at = v.match(/(?:^|\s)@([^\s]*)$/);
  if (at) {
    if (!filesCache || Date.now() - filesAt > 15000) { filesCache = await API().Files() || []; filesAt = Date.now(); }
    const hits = PickFilter.rank(filesCache.map(f => ({ name: f, base: base(f) })), at[1], { desc: () => "" })
      .map(f => ({ ...f, prio: PickFilter.matchName(f.base, at[1].toLowerCase()) })).sort((a, b) => ((a.prio ? a.prio.tier : 9) - (b.prio ? b.prio.tier : 9))).slice(0, 30);
    openMenu("file", hits.map(f => ({ name: f.name, desc: "", _pos: f._pos })), "Attach a file");
    if (!hits.length) { $("#menu").innerHTML = `<div class="mhead label">Attach a file</div><div class="mempty">No file matches '${esc(at[1])}' in this folder</div>`; $("#menu").style.display = ""; }
    return;
  }
  if (menu.kind === "cmd" || menu.kind === "file" || menu.all) closeMenu();
}

function onKey(e) {
  if (menuOpen() && menu.items.length) {
    const n = Math.min(menu.items.length, 60);
    if (e.key === "ArrowDown") { e.preventDefault(); menu.idx = (menu.idx + 1) % n; drawMenu(); return; }
    if (e.key === "ArrowUp") { e.preventDefault(); menu.idx = (menu.idx - 1 + n) % n; drawMenu(); return; }
    if (e.key === "Enter" || (e.key === "Tab" && !e.shiftKey && !e.ctrlKey)) { e.preventDefault(); pickMenu(menu.idx); return; }
  }
  // A picker filtered down to nothing: Enter/Tab don't send the filter text as a message
  // (except a typed "/model is slow…" line, which closes the picker and sends as usual).
  if (menuOpen() && menu.all && !menu.items.length && (e.key === "Enter" || e.key === "Tab")) {
    if (menu.cmd && e.key === "Enter" && !e.shiftKey) closeMenu(); else { e.preventDefault(); return; }
  }
  if (e.key === "Escape") { closeMenu(); return; }
  if (e.key === "Backspace" && !e.target.value && popAttach()) return;
  if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); submit(); }
}

// drawAttach lives in composer.js (the @ files and the + / drop / paste attachments).

async function retry() {
  if (busy) return;
  const col = $("#log .col", pane);
  const msgs = col.querySelectorAll(".msg");
  const last = msgs[msgs.length - 1];
  if (last && last.classList.contains("blaze")) last.remove();
  col.querySelectorAll(":scope > .card.err").forEach(c => c.remove());
  current = blazeMsg(); col.append(current.el);
  setBusy(true); thinking(true);
  const r = await API().Retry();
  if (r !== "ok") { current.el.remove(); current = null; setBusy(r === "busy"); }
}

async function switchProject(id) {
  if (busy) return;
  const p = await API().OpenProject(id); convId = "";
  if (p.folder) { S.prefs.folder = p.folder; setFolder(p.folder); }
  await refreshSide(); hero();
  if (id) show("project", id); else show("work");
}

// ---------- projects ----------
async function newProject() {
  const folder = await API().ChooseFolder();
  if (folder) show("newproject", folder);
}

function newProjectView(el, folder) {
  const { col } = page(el, "New project", "Set up the project", "A project keeps its own chats and instructions, and Blaze always works in its folder.");
  const g = h("div", "box pad formgrid");
  g.innerHTML = `<div><div class="label" style="margin-bottom:6px">Folder</div><div class="mono" style="color:var(--ink)">${esc(folder)}</div></div>
    <div><div class="label" style="margin-bottom:6px">Name</div><input class="field nm" value="${esc(base(folder))}"></div>
    <div><div class="label" style="margin-bottom:6px">Instructions <span class="weak">optional</span></div><textarea class="field ins" placeholder="Anything Blaze should always follow here, e.g. 'TypeScript, pnpm, Tailwind. Run pnpm test before saying done.'"></textarea></div>`;
  const row = h("div", "keyrow"); row.style.marginTop = "16px";
  const ok = h("button", "btn accent", "Create project"), cancel = h("button", "btn line", "Cancel");
  ok.onclick = async () => {
    try {
      const p = await API().CreateProject(folder, $(".nm", g).value);
      await API().UpdateProject(p.id, $(".nm", g).value, $(".ins", g).value);
      S.prefs.folder = p.folder; setFolder(p.folder); convId = ""; hero(); await refreshSide(); show("project", p.id);
    } catch (e) { ok.textContent = String(e); }
  };
  cancel.onclick = () => show(tab);
  row.append(ok, cancel); col.append(g, row);
}

async function projectView(el, id) {
  const d = await API().ProjectDetails(id);
  const p = d.project;
  const { col, acts } = page(el, "Project", esc(p.name), `<span class="mono small">${esc(p.folder)}</span>`);
  const nc = h("button", "btn accent", `${icon("plus", "i sm")}New chat`); nc.onclick = () => newChat();
  const ds = h("button", "btn line", "Open Design"); ds.onclick = () => show("design");
  acts.append(ds, nc);

  const g1 = group("Chats");
  const rows = h("div", "rows");
  (d.chats || []).forEach((c, i) => { const r = h("div", "r click", `<span class="n">${pad(i + 1)}</span><div class="main"><div class="t">${esc(c.title)}</div></div><span class="label weak">${esc(c.updated)}</span>`); r.onclick = () => openChat(c.id); rows.append(r); });
  if (!(d.chats || []).length) rows.append(h("div", "empty", "<b>No chats yet</b>Start one with New chat."));
  g1.append(rows);

  const g2 = group("Instructions", "Blaze always follows these in this project.");
  const box = h("div", "box pad formgrid");
  const nm = h("input", "field"); nm.value = p.name;
  const ins = h("textarea", "field"); ins.value = p.instructions || ""; ins.placeholder = "e.g. Python 3.12 with uv. Keep functions small. Never push to main.";
  const sv = h("button", "btn line sm", "Save"); sv.style.justifySelf = "start";
  sv.onclick = async () => { await API().UpdateProject(p.id, nm.value, ins.value); sv.textContent = "Saved"; setTimeout(() => sv.textContent = "Save", 1200); refreshSide(); };
  box.append(nm, ins, sv); g2.append(box);

  // The pinned brief Blaze sends with every turn and keeps up to date (update_brief).
  const gb = group("Brief", "Pinned for Blaze every turn: purpose, rules, key files, in progress. Blaze updates it too.");
  const bbox = h("div", "box pad formgrid");
  const bt = h("textarea", "field brief"); bt.placeholder = "Purpose: …\nRules: …\nKey files: …\nIn progress: …"; bt.maxLength = 2400;
  const bcount = h("span", "label weak");
  const bsv = h("button", "btn line sm", "Save brief"); bsv.style.justifySelf = "start";
  const bdraw = () => bcount.textContent = `${bt.value.length} / 2400`;
  bt.oninput = bdraw;
  if (API().ProjectBrief) API().ProjectBrief(p.id).then(b => { bt.value = b || ""; bdraw(); });
  bsv.onclick = async () => { const err = await API().SetProjectBrief(p.id, bt.value); bsv.textContent = err ? "Couldn't save" : "Saved"; setTimeout(() => bsv.textContent = "Save brief", 1200); };
  const brow = h("div", "brow"); brow.append(bsv, bcount);
  bbox.append(bt, brow); gb.append(bbox);

  const g3 = group("Git");
  g3.append(d.branch ? h("div", "", `<div style="margin-bottom:8px">Branch <span class="mono acc2">${esc(d.branch)}</span></div><div class="logbox">${esc(d.status || "Clean")}</div>`) : h("div", "rows", `<div class="empty">Not a git repository.</div>`));

  const g4 = group("Recently changed files", "Click a file to attach it to a new message.");
  const fr = h("div", "rows");
  (d.recent || []).forEach((f, i) => {
    const r = h("div", "r click", `<span class="n">${pad(i + 1)}</span><div class="main"><div class="t mono small">${esc(f.path)}</div></div><span class="label weak">${esc(f.when)}</span>`);
    r.onclick = () => { attachments = [f.path]; drawAttach(); show("work"); };
    fr.append(r);
  });
  if (!(d.recent || []).length) fr.append(h("div", "empty", "No files yet."));
  g4.append(fr);

  const g5 = h("div", "group");
  const del = h("button", "btn ghost sm danger", `${icon("trash", "i sm")}Remove project`);
  del.onclick = async () => { if (del.dataset.sure) { await API().DeleteProject(p.id); await refreshSide(); hero(); show("work"); } else { del.dataset.sure = 1; del.textContent = "Remove it and its chats? Files stay on disk. Click again"; } };
  g5.append(del);
  col.append(g1, g2, gb, g3, g4, g5);
}

// ---------- design ----------
let D = null;            // DesignView from Go
let viewing = 0;         // version being viewed (0 = live)
let compare = [];        // versions picked to compare
let device = "desktop", selectOn = false, selection = null, frameTheme = "light";
let stripOpen = localStorage.getItem("blaze.strip") !== "0";

function designView(el) {
  const wrap = h("div", "design");
  const w = +localStorage.getItem("blaze.dside") || 400;
  wrap.style.setProperty("--dside", Math.max(300, Math.min(w, 760)) + "px");
  wrap.innerHTML = `<div class="dmain">
      <div class="dbar">
        <button id="dtarget" class="picker" title="What to preview"><span class="pk">Preview</span>${icon("file", "i sm")}<span class="pv" id="dtargetName">Choose…</span>${icon("down", "i sm caret")}</button>
        <span class="vsep"></span>
        <div class="seg icons" id="ddev"><button data-d="desktop" title="Desktop width">${icon("desktop")}</button><button data-d="tablet" title="Tablet width">${icon("tablet")}</button><button data-d="phone" title="Phone width">${icon("phone")}</button></div>
        <div class="seg icons" id="dtheme"><button data-t="light" title="Light">${icon("sun")}</button><button data-t="dark" title="Dark">${icon("moon")}</button></div>
        <span class="grow"></span>
        <button id="dselect" class="btn line sm" title="Point at an element to edit it">${icon("cursor", "i sm")}<span class="btxt">Select</span></button>
        <button id="ddirs" class="btn ghost sm" title="Ask for 3 different directions">${icon("sparks", "i sm")}<span class="btxt">Directions</span></button>
        <button id="dvtoggle" class="btn ghost sm" title="Show or hide versions">${icon("clock", "i sm")}<span class="btxt">Versions</span><span class="vcount" id="dvcount"></span></button>
        <button id="dopen" class="btn ghost sm icon" title="Open in browser">${icon("open", "i sm")}</button>
      </div>
      <div class="dstage" id="dstage"></div>
      <div class="dstrip${stripOpen ? "" : " closed"}" id="dstrip"><div class="dstriphead"><span class="label">Versions</span><span class="vcount" id="dvn"></span><span class="grow"></span><label class="toggle"><input type="checkbox" id="dcmp"><span class="label">Compare</span></label><button class="btn ghost xs" id="dstripx" title="Hide versions">${icon("down", "i sm")}</button></div><div id="dversions" class="dversions"></div></div>
    </div>
    <div class="dsplit" title="Drag to resize"></div>
    <div class="dside"></div>`;
  el.append(wrap);
  pane.classList.add("narrow");
  $(".dside", wrap).append(pane);
  $("#ddev").querySelectorAll("button").forEach(b => b.onclick = () => { device = b.dataset.d; drawStage(); });
  $("#dtheme").querySelectorAll("button").forEach(b => b.onclick = () => { frameTheme = b.dataset.t; drawStage(); });
  $("#dselect").onclick = () => { selectOn = !selectOn; drawStage(); };
  $("#ddirs").onclick = () => directions();
  $("#dopen").onclick = () => API().OpenPreview(viewing);
  const strip = () => { $("#dstrip").classList.toggle("closed", !stripOpen); localStorage.setItem("blaze.strip", stripOpen ? "1" : "0"); $("#dvtoggle").classList.toggle("on", stripOpen); };
  $("#dvtoggle").onclick = () => { stripOpen = !stripOpen; strip(); };
  $("#dstripx").onclick = () => { stripOpen = false; strip(); };
  strip();
  $("#dcmp").onchange = () => { compare = []; drawVersions(); drawStage(); };
  $("#dtarget").dataset.pop = "1";
  $("#dtarget").onclick = e => { e.stopPropagation(); targetPopover(); };
  // Resizable split between preview and chat.
  const split = $(".dsplit", wrap);
  split.onmousedown = e => {
    e.preventDefault(); split.classList.add("drag");
    const right = wrap.getBoundingClientRect().right;
    $$("iframe", wrap).forEach(f => f.style.pointerEvents = "none");
    const move = ev => { const nw = Math.max(300, Math.min(right - ev.clientX, wrap.clientWidth - 360)); wrap.style.setProperty("--dside", nw + "px"); };
    const up = () => { split.classList.remove("drag"); removeEventListener("mousemove", move); removeEventListener("mouseup", up); $$("iframe", wrap).forEach(f => f.style.pointerEvents = ""); localStorage.setItem("blaze.dside", parseInt(getComputedStyle(wrap).getPropertyValue("--dside"))); };
    addEventListener("mousemove", move); addEventListener("mouseup", up);
  };
  drawDesign();
}

function targetLabel() {
  const t = (D && D.target) || {};
  if (t.kind === "url") return t.url;
  if (t.kind === "file") return t.path;
  return (D && D.files && D.files.length) ? "Choose a page…" : "Nothing to preview yet";
}

// The Preview picker shows the pages this task works on first. Tabs: Current (files the
// chat changed, plus what's being previewed), Other (the rest of the folder), All.
let pvTab = "current";
function previewGroups() {
  const files = (D && D.files) || [], t = (D && D.target) || {};
  const tracked = new Set((D && D.tracked) || []);
  if (t.kind === "file" && t.path) tracked.add(t.path);
  let current = files.filter(f => tracked.has(f));
  // Nothing tracked yet: the previewed page's own folder counts as current.
  if (!current.length && t.kind === "file" && t.path) {
    const dir = t.path.includes("/") ? t.path.slice(0, t.path.lastIndexOf("/") + 1) : "";
    current = files.filter(f => dir ? f.startsWith(dir) && !f.slice(dir.length).includes("/") : !f.includes("/"));
  }
  const cur = new Set(current);
  return { all: files, current, other: files.filter(f => !cur.has(f)) };
}

function targetPopover() {
  openPop($("#dtarget"), p => {
    p.innerHTML = `<div class="phd"><span class="label">Preview</span></div><div class="ptabs"></div><div class="pbody"></div>`;
    const body = $(".pbody", p), tabs = $(".ptabs", p), t = (D && D.target) || {};
    const g = previewGroups();
    if (!g.current.length && pvTab === "current") pvTab = g.all.length ? "all" : "current";
    const seg = h("div", "seg ptabseg");
    [["all", "All"], ["current", "Current"], ["other", "Other"]].forEach(([k, l]) => {
      const b = h("button", pvTab === k ? "on" : "", `${l} <span class="pcount">${g[k].length}</span>`);
      b.onclick = e => { e.stopPropagation(); pvTab = k; draw(); };
      seg.append(b);
    });
    tabs.append(seg);
    const q = h("input", "field psearch");
    q.oninput = () => $$(".opt.f", body).forEach(r => r.style.display = r.dataset.f.toLowerCase().includes(q.value.toLowerCase()) ? "" : "none");
    function draw() {
      $$("button", seg).forEach((b, i) => b.classList.toggle("on", ["all", "current", "other"][i] === pvTab));
      body.innerHTML = "";
      const files = g[pvTab];
      if (files.length > 10) { q.placeholder = `Search ${files.length} files`; body.append(q); }
      files.forEach(f => {
        const cur = t.kind === "file" && t.path === f;
        const o = h("button", "opt f" + (cur ? " sel" : ""), `<span class="on-mark">${cur ? icon("check", "i sm") : icon("file", "i sm")}</span><span class="col2"><span class="om" style="color:var(--ink)">${esc(f)}</span></span>`);
        o.dataset.f = f;
        o.onclick = async () => { closePop(); D = await API().SetDesignTarget("file", f); viewing = 0; drawDesign(); };
        body.append(o);
      });
      if (q.value) q.oninput();
      if (!files.length) body.append(h("div", "pnote", !g.all.length ? "No HTML or SVG files in this folder yet." : pvTab === "current" ? "This task hasn't touched a page yet. Check Other or All." : "Nothing else here: every page belongs to this task."));
    }
    draw();
    const form = h("div", "urlform", `<input class="field mono" placeholder="localhost:5173" value="${esc(t.kind === "url" ? t.url : "")}"><button class="btn accent sm">Preview</button>`);
    const go = async () => { const v = $("input", form).value.trim(); if (!v) return; closePop(); D = await API().SetDesignTarget("url", v); viewing = 0; drawDesign(); };
    $("button", form).onclick = go; $("input", form).onkeydown = e => { if (e.key === "Enter") go(); };
    const ft = h("div", "pft"); ft.style.display = "block"; ft.append(h("div", "label weak", "Local server URL"), form); form.style.padding = "6px 0 0";
    p.append(ft);
  }, { width: 340, align: "left" });
}

async function drawDesign() {
  if (view !== "design") return;
  D = await API().Design();
  if (!$("#dtarget")) return;
  const t = D.target || {};
  $("#dtargetName").textContent = targetLabel();
  $("#dtarget").querySelector("svg").outerHTML = icon(t.kind === "url" ? "globe" : "file", "i sm");
  const n = (D.versions || []).length;
  $("#dvcount").textContent = n ? pad(n) : "";
  $("#dvn").textContent = n ? `${n} saved` : "";
  drawVersions(); drawStage();
}

// kept for /design url flows: focus the URL field in the target popover
function urlPrompt() { targetPopover(); setTimeout(() => { const i = popEl && $(".urlform input", popEl); if (i) i.focus(); }, 0); }

function frameURL(n) {
  const t = D.target || {};
  let u = D.base + "/" + (t.kind === "file" ? t.path.split("/").map(encodeURIComponent).join("/") : "");
  const q = [];
  if (n) q.push("__v=" + n);
  if (t.kind === "file" && /\.svg$/i.test(t.path)) q.push("wrap=1");
  q.push("t=" + Date.now());
  return u + "?" + q.join("&");
}

function drawStage() {
  const st = $("#dstage"); if (!st) return;
  $$("#ddev button").forEach(b => b.classList.toggle("on", b.dataset.d === device));
  $$("#dtheme button").forEach(b => b.classList.toggle("on", b.dataset.t === frameTheme));
  const sb = $("#dselect");
  sb.classList.toggle("accent", selectOn); sb.classList.toggle("line", !selectOn);
  sb.innerHTML = `${icon("cursor", "i sm")}<span class="btxt">${selectOn ? "Selecting · Esc" : "Select"}</span>`;
  st.innerHTML = "";
  if (!D || !D.target || !D.target.kind) {
    st.innerHTML = `<div class="dempty"><div class="label acc">Design</div><div class="big2">Nothing to preview yet.</div><p class="lead">Ask for something visual in the chat and it appears here live, version by version. Or choose a page or a local dev server with the Preview picker above.</p><div class="keyrow"></div></div>`;
    const b = h("button", "btn accent sm", `${icon("sparks", "i sm")}Suggest 3 directions`); b.onclick = () => directions();
    const u = h("button", "btn line sm", `${icon("globe", "i sm")}Local server…`); u.onclick = e => { e.stopPropagation(); urlPrompt(); };
    $(".keyrow", st).append(b, u);
    return;
  }
  const isUrl = D.target.kind === "url";
  if (viewing && !isUrl) {
    const v = (D.versions || []).find(x => x.n === viewing);
    const bar = h("div", "viewing", `<span class="label acc">Version ${pad(viewing)}</span><span class="sub">${esc(v ? v.summary : "")}</span><span class="grow"></span>`);
    const rs = h("button", "btn accent xs", "Restore this"); rs.onclick = async () => { const r = await API().RestoreVersion(viewing); if (r !== "ok") rs.textContent = r; else { viewing = 0; drawDesign(); } };
    const lv = h("button", "btn line xs", "Back to live"); lv.onclick = () => { viewing = 0; drawStage(); drawVersions(); };
    bar.append(rs, lv); st.append(bar);
  }
  const frames = h("div", "frames");
  const list = compare.length === 2 ? compare : [viewing];
  list.forEach(n => {
    const box = h("div", "fbox " + device);
    if (compare.length === 2) box.prepend(h("div", "label fcap", n ? "Version " + pad(n) : "Live"));
    const f = h("iframe", "pframe"); f.src = frameURL(n);
    f.onload = () => { try { f.contentWindow.postMessage({ blaze: "mode", on: selectOn && compare.length !== 2 }, "*"); f.contentWindow.postMessage({ blaze: "theme", value: frameTheme }, "*"); } catch (e) {} };
    box.append(f); frames.append(box);
  });
  st.append(frames);
  if (selection && selectOn) drawSelBar();
}

function reloadPreview() {
  clearTimeout(reloadPreview.t);
  reloadPreview.t = setTimeout(() => { if (view === "design" && !viewing) { selection = null; drawStage(); } }, 400);
}

function drawVersions() {
  const box = $("#dversions"); if (!box || !D) return;
  box.innerHTML = "";
  const vs = (D.versions || []).slice().reverse();
  if (!vs.length) { box.append(h("div", "vempty", "Each change Blaze makes is saved here as a version you can view, compare and restore.")); return; }
  const cmp = $("#dcmp").checked;
  vs.forEach((v, i) => {
    const t = h("button", "vtile" + ((viewing === v.n && !cmp) || compare.includes(v.n) ? " on" : ""));
    const thumb = h("div", "vthumb");
    if (i < 6 && D.target.kind === "file") { const f = h("iframe"); f.src = frameURL(v.n); f.tabIndex = -1; f.loading = "lazy"; thumb.append(f); }
    else thumb.append(h("div", "vn", pad(v.n)));
    t.append(thumb, h("div", "vcap", `<b>${pad(v.n)}</b><span>${esc(v.summary || "Change")}</span>`));
    t.title = (v.summary || "") + (v.when ? " · " + v.when : "");
    t.onclick = () => {
      if (cmp) { compare = compare.includes(v.n) ? compare.filter(x => x !== v.n) : compare.concat(v.n).slice(-2); }
      else { viewing = D.target.kind === "url" ? 0 : v.n; if (D.target.kind === "url") { API().RestoreVersion(v.n).then(drawDesign); return; } }
      drawVersions(); drawStage();
    };
    box.append(t);
  });
}

function directions() {
  const t = D && D.target && D.target.kind ? (D.target.path || D.target.url) : "";
  show("design");
  $("#input").value = t
    ? `Suggest 3 clearly different design directions for ${t}, as separate files design/option-a.html, option-b.html and option-c.html. One line on each.`
    : "Suggest 3 clearly different design directions for a page for this project, as self-contained files design/option-a.html, option-b.html and option-c.html. One line on each.";
  $("#input").focus(); grow();
}

// Messages from the preview (selection).
addEventListener("message", e => {
  const m = e.data || {};
  if (m.blaze === "selected") { selection = m.items.length ? m : null; drawSelBar(); }
  if (m.blaze === "down") { closePop(); closeMenu(); }
});
// Clicking into the preview (or any iframe) moves focus out of the app's document:
// treat it as a click outside and collapse open popovers. Covers pages the picker
// script couldn't be injected into (e.g. a dev server that blocks it).
addEventListener("blur", () => setTimeout(() => {
  const a = document.activeElement;
  if (a && a.tagName === "IFRAME") { closePop(); closeMenu(); }
}, 0));
addEventListener("keydown", e => { if (e.key === "Escape" && view === "design" && selectOn) { selectOn = false; selection = null; drawStage(); } });

function drawSelBar() {
  const st = $("#dstage"); if (!st) return;
  const old = $(".selbar", st); if (old) old.remove();
  if (!selection) return;
  const items = selection.items, one = items[items.length - 1];
  const bar = h("div", "selbar");
  const what = items.length > 1 ? pad(items.length) + (items.every(it => it.part) ? " parts" : " elements") : one.part ? "3D part" : "&lt;" + esc(one.tag) + "&gt;";
  bar.innerHTML = `<div class="sbhead"><span class="label acc">${what}</span><span class="sbtext">${esc((one.text || "").slice(0, 60))}</span><button class="x" title="Clear">${icon("x", "i sm")}</button></div>
    <div class="sbacts"><button data-a="Change">Change</button><button data-a="Remove">Remove</button><button data-a="Restyle">Restyle</button><button data-a="Replace text">Replace text</button><button data-a="Ask">Ask…</button></div>
    <div class="sbform" style="display:none"><input class="field" placeholder=""><button class="btn accent sm go">Do it</button></div>
    <div class="sbtweak" style="display:none">
      <label><span class="label">Text</span><input type="color" data-p="color"></label>
      <label><span class="label">Fill</span><input type="color" data-p="background-color"></label>
      <label><span class="label">Size</span><input type="range" min="8" max="96" data-p="font-size" data-u="px"></label>
      <label><span class="label">Padding</span><input type="range" min="0" max="80" data-p="padding" data-u="px"></label>
      <label><span class="label">Radius</span><input type="range" min="0" max="48" data-p="border-radius" data-u="px"></label>
      <button class="btn accent sm apply">Apply</button>
    </div>`;
  const frame = $(".pframe", st);
  const post = msg => { try { frame && frame.contentWindow.postMessage(msg, "*"); } catch (e) {} };
  $(".x", bar).onclick = () => { selection = null; post({ blaze: "clear" }); bar.remove(); };
  const form = $(".sbform", bar), input = $("input", form), tweak = $(".sbtweak", bar);
  let action = "";
  const tweaks = {};
  bar.querySelectorAll(".sbacts button").forEach(b => b.onclick = () => {
    action = b.dataset.a;
    bar.querySelectorAll(".sbacts button").forEach(x => x.classList.toggle("on", x === b));
    if (action === "Remove") { designEdit("Remove", "Remove this element from the design completely."); return; }
    tweak.style.display = action === "Restyle" ? "" : "none";
    form.style.display = "";
    input.placeholder = { Change: "What should change? e.g. make it a 3-column grid", Restyle: "Describe the style, or use the controls", "Replace text": "New text", Ask: "Ask anything about this part" }[action];
    if (action === "Replace text") input.value = one.text;
    input.focus();
  });
  tweak.querySelectorAll("input").forEach(inp => {
    const s = one.styles || {};
    if (inp.dataset.p === "color") inp.value = rgbHex(s.color);
    if (inp.dataset.p === "background-color") inp.value = rgbHex(s.background);
    if (inp.dataset.p === "font-size") inp.value = parseInt(s.fontSize) || 16;
    if (inp.dataset.p === "padding") inp.value = parseInt(s.padding) || 0;
    if (inp.dataset.p === "border-radius") inp.value = parseInt(s.borderRadius) || 0;
    inp.oninput = () => { const v = inp.value + (inp.dataset.u || ""); tweaks[inp.dataset.p] = v; post({ blaze: "style", prop: inp.dataset.p, value: v }); };
  });
  $(".apply", tweak).onclick = () => designEdit("Restyle", (input.value ? input.value + "\n" : "") + "Set these CSS values in the source: " + Object.entries(tweaks).map(([k, v]) => `${k}: ${v}`).join("; "));
  const go = () => { if (!input.value.trim() && action !== "Restyle") return; designEdit(action, action === "Replace text" ? `Replace its text with: ${input.value}` : input.value); };
  $(".go", form).onclick = go;
  input.onkeydown = e => { if (e.key === "Enter") { e.preventDefault(); go(); } };
  st.append(bar);
}

function designEdit(action, request) {
  if (!selection || busy) return;
  const t = D.target.kind === "url" ? `dev server ${D.target.url} (find the source file)` : D.target.path;
  let text = `[Design edit] ${action}\nFile: ${t}`;
  selection.items.forEach(it => {
    // A 3D part is an entry in the page's blaze-parts JSON, not a tag in the markup.
    text += it.part ? `\nPart: ${it.part.id} (${it.part.label || it.part.id})\n\`\`\`json\n${it.html}\n\`\`\`` : `\nElement: ${it.selector}\n\`\`\`html\n${it.html}\n\`\`\``;
  });
  text += `\nRequest: ${request}`;
  selection = null; selectOn = false;
  drawStage();
  sendText(text, []);
}

function rgbHex(c) {
  const m = (c || "").match(/\d+/g); if (!m || m.length < 3) return "#000000";
  return "#" + m.slice(0, 3).map(x => (+x).toString(16).padStart(2, "0")).join("");
}

// ---------- setup ----------
// The first-run flow is onboard.js (window.VIEWS.setup).

function pcTable(info) {
  const kv = h("div", "kv");
  const add = (k, v) => kv.append(h("span", "label", k), h("span", "v", esc(v)));
  add("System", `${info.os_name} · ${info.arch}`);
  add("Processor", `${info.cpu || "Unknown"} · ${info.cores} threads`);
  add("Memory", `${Math.round(info.ram_gb)} GB RAM · ${Math.round(info.disk_free_gb)} GB free disk`);
  add("Graphics", (info.gpu || []).join(", ") || "None detected");
  add("Shell", `${info.shell} · ${info.shell_path}`);
  add("Packages", (info.pkg_managers || []).join(", ") || "None found");
  add("Tools", Object.keys(info.tools || {}).sort().join("  ") || "None");
  return kv;
}

// Provider list with keys: add/test/remove keys, get-a-key page, per-provider model, quota.
// opts.setup shows only the free providers first, with the rest behind "More providers".
function providerRows(redraw, opts = {}) {
  const wrap = h("div");
  const rows = h("div", "rows prov");
  const free = ["gemini", "groq", "cerebras", "openrouter", "nvidia", "mistral"];
  let list = (S.providers || []).filter(p => p.id !== "pollinations");
  let rest = [];
  if (opts.setup) { rest = list.filter(p => !free.includes(p.id) && !p.ready); list = list.filter(p => free.includes(p.id) || p.ready); }
  const draw = (target, items) => items.forEach(p => target.append(...providerRow(p, redraw)));
  draw(rows, list);
  wrap.append(rows);
  if (rest.length) {
    const more = h("button", "btn ghost sm", `${icon("down", "i sm")}More providers (${rest.length})`); more.style.marginTop = "8px";
    more.onclick = () => { more.remove(); const r2 = h("div", "rows prov"); r2.style.marginTop = "8px"; draw(r2, rest); wrap.append(r2); };
    wrap.append(more);
  }
  return wrap;
}

function providerRow(p, redraw) {
  const r = h("div", "r");
  const status = p.ready ? `<span class="pill acc">${p.keys > 1 ? p.keys + " keys" : p.keyless ? "Local" : "Ready"}</span>` : "";
  r.innerHTML = `<span class="dot ${p.ready ? "" : "off"}"></span><div class="main"><div class="t">${esc(p.short)} ${p.free && !p.keyless ? '<span class="pill">Free tier</span>' : ""} ${status}</div><div class="s">${p.ready ? `<span class="mono">${esc(p.model || "no model chosen")}</span>` : p.keyless ? "Runs on this computer, no key" : p.id === "custom" ? "Any OpenAI-compatible service" : "Key starts " + esc(p.key_hint)}</div></div>`;
  const acts = h("div", "acts");
  const tgl = h("button", p.ready ? "btn ghost sm" : "btn line sm", p.ready ? `Manage${icon("down", "i sm")}` : `${icon("plus", "i sm")}Add`);
  acts.append(tgl); r.append(acts);
  const x = h("div", "xpand"); x.style.display = "none";
  tgl.onclick = () => { x.style.display = x.style.display ? "" : "none"; if (!x.dataset.built) { x.dataset.built = 1; buildProviderPanel(x, p, redraw); } };
  return [r, x];
}

async function buildProviderPanel(x, p, redraw) {
  x.innerHTML = "";
  const msg = h("div", "s small"); msg.style.marginTop = "8px";
  const done = async res => {
    if (res.ok) { msg.innerHTML = `<span class="ok">Saved (${esc(res.where)}).</span> ${esc(res.note || "")} Using <span class="mono">${esc(res.model)}</span>.`; await refreshState(); setTimeout(redraw, 900); }
    else msg.innerHTML = `<span class="bad">${esc(res.error)}</span>`;
  };
  if (p.id === "custom") {
    const f = h("div", "keyrow", `<input class="field mono cb" placeholder="https://my-service.example.com/v1" value="${esc(p.base || "")}"><input class="field cn" placeholder="Name (optional)" style="max-width:180px">`);
    f.style.marginBottom = "8px";
    $(".cb", f).onchange = () => API().SetCustomProvider($(".cb", f).value, $(".cn", f).value);
    $(".cn", f).onchange = () => API().SetCustomProvider($(".cb", f).value, $(".cn", f).value);
    x.append(f);
  }
  if (p.ready && !p.keyless) {
    x.append(h("div", "label sub2", "Keys"));
    const kl = h("div", "keylist");
    (await API().Keys(p.id) || []).forEach((k, i) => {
      const d = h("div", "", `<span>${esc(k.masked)}</span>${k.main ? '<span class="pill">Main</span>' : `<span class="pill">Key ${i + 1}</span>`}<span class="weak small">${esc(k.source)}</span><span class="grow"></span>`);
      const rm = h("button", "btn ghost xs", "Remove"); rm.onclick = async () => { await API().RemoveKey(p.id, i); await refreshState(); redraw(); };
      d.append(rm); kl.append(d);
    });
    x.append(kl);
  }
  if (!p.keyless) {
    x.append(h("div", "label sub2", p.ready ? "Add another key" : "Key"));
    const kr = h("div", "keyrow", `<input class="field mono" type="password" placeholder="${esc(p.key_hint && p.key_hint !== "…" ? p.key_hint : "Paste your key")}">`);
    if (p.key_page) { const g = h("button", "btn line sm", `Get a key${icon("ext", "i sm")}`); g.onclick = () => API().OpenURL(p.key_page); kr.append(g); }
    const sv = h("button", "btn accent sm", "Test & save");
    sv.onclick = async () => { sv.disabled = true; sv.textContent = "Testing…"; msg.textContent = ""; const res = await API().AddProvider(p.id, $("input", kr).value.trim()); sv.disabled = false; sv.textContent = "Test & save"; done(res); };
    $("input", kr).onkeydown = e => { if (e.key === "Enter") sv.click(); };
    kr.append(sv); x.append(kr);
    if (p.id === "openrouter") {
      const oc = h("button", "btn line sm", "Sign in with OpenRouter instead"); oc.style.marginTop = "8px";
      oc.onclick = async () => { oc.disabled = true; oc.textContent = "Waiting for you in the browser…"; done(await API().ConnectOpenRouter()); oc.disabled = false; oc.textContent = "Sign in with OpenRouter instead"; };
      x.append(oc);
    }
  } else if (!p.ready) {
    const b = h("button", "btn accent sm", "Check and add"); b.onclick = async () => { b.disabled = true; b.textContent = "Checking…"; done(await API().AddProvider(p.id, "")); b.disabled = false; b.textContent = "Check and add"; };
    x.append(b);
  }
  x.append(msg);
  if (p.ready) {
    x.append(h("div", "label sub2", "Model for " + p.short));
    const mb = h("div", "modelbox");
    const bar = h("div", "mbar", `<span class="mono small grow cur">${esc(p.model || "none")}</span>`);
    const rf = h("button", "btn ghost xs", `${icon("retry", "i sm")}Refresh list`);
    const dt = h("button", "btn ghost xs", "Check my plan");
    dt.title = "Sends each model a one-word test to see which ones your key's plan allows (uses a little quota)";
    bar.append(rf, dt);
    const ml = h("div", "mlist"); const mf = h("div", "mfoot2");
    mb.append(bar, ml, mf); x.append(mb);
    const load = async (how) => {
      ml.innerHTML = `<div class="pnote">${how === "detect" ? "Checking which models your plan allows…" : "Loading models…"}</div>`;
      const l = how === "detect" ? await API().DetectModels(p.id) : await API().ModelOptions(p.id, how === "refresh");
      drawModelRows(ml, p, l, p.model, async m => {
        if (m.tools === "no") { mf.innerHTML = `<span class="bad">${esc(m.id)} can't call tools, so Blaze can't act with it. Pick one marked Tools.</span>`; return; }
        await API().SetProviderModel(p.id, m.id); p.model = m.id; $(".cur", bar).textContent = m.id; $$(".mrow", ml).forEach(r => r.classList.toggle("cur", r.dataset.id === m.id)); await refreshState();
      });
      mf.innerHTML = `${l.plan ? `<span class="pill acc">${esc(l.plan)}</span>` : ""}<span>${(l.models || []).length} models${l.fetched ? " · live list " + esc(l.fetched) : " · built-in list"}</span>`;
    };
    rf.onclick = () => load("refresh"); dt.onclick = () => load("detect");
    load("");
    const qa = h("div", "keyrow"); qa.style.marginTop = "12px";
    const qb = h("button", "btn line sm", `${icon("chart", "i sm")}Plan & quota`);
    const tb = h("button", "btn line sm", "Test");
    const mk = h("button", "btn line sm", "Make default");
    const rm = h("button", "btn ghost sm danger", `${icon("trash", "i sm")}Remove ${esc(p.short)}`);
    const qout = h("div"); qout.style.marginTop = "8px";
    qb.onclick = async () => { qb.disabled = true; qout.innerHTML = `<div class="sub small">Checking…</div>`; const q = await API().Quota(p.id); qb.disabled = false;
      qout.innerHTML = `<div class="qrows">${(q.rows || []).map(r => `<span>${esc(r.label)}</span><span class="mono">${esc(r.value)}</span>`).join("")}</div>${q.note ? `<div class="sub small" style="margin-top:6px">${esc(q.note)}</div>` : ""}`;
      if (q.page) { const o = h("button", "btn ghost xs", `Open ${esc(p.short)}'s usage page${icon("ext", "i sm")}`); o.style.marginTop = "6px"; o.onclick = () => API().OpenURL(q.page); qout.append(o); } };
    tb.onclick = async () => { tb.disabled = true; tb.textContent = "Testing…"; const c = await API().TestModel(p.id, ""); tb.disabled = false; tb.textContent = "Test"; qout.innerHTML = `<div class="small ${c.ok ? "ok" : "bad"}">${esc(c.detail)}</div>`; };
    mk.onclick = async () => { const l = await API().SelectModel(p.id, ""); setModel(l); await refreshState(); qout.innerHTML = `<div class="small ok">${esc(p.short)} is now the default.</div>`; };
    rm.onclick = async () => { if (!rm.dataset.sure) { rm.dataset.sure = 1; rm.textContent = "Remove its keys too? Click again"; return; } await API().RemoveProvider(p.id); await refreshState(); redraw(); };
    qa.append(tb, qb, mk, h("span", "grow"), rm); x.append(qa, qout);
  }
}

// ---------- settings ----------
const SECTIONS = [
  ["models", "Models", "layers"], ["usage", "Usage", "chart"],
  ["behaviour", "Behaviour", "sliders"], ["appearance", "Appearance", "palette"], ["memory", "Memory", "brain"],
  ["skills", "Skills", "book"], ["computer", "This computer", "cpu"], ["updates", "Updates", "dl"],
];
let settingsSection = "models";

function settingsView(el, focus) {
  if (focus === "providers") { addModels(); return; } // moved into BlazeRouter
  if (focus) settingsSection = focus;
  if (!SECTIONS.some(x => x[0] === settingsSection)) settingsSection = "models";
  const wrap = h("div", "settings");
  const nav = h("nav", "snav", `<span class="label">Settings</span>`);
  const pageEl = h("div", "spage"); const col = h("div", "col"); pageEl.append(col);
  SECTIONS.forEach(([id, name, ic]) => {
    const b = h("button", id === settingsSection ? "on" : "", `${icon(ic)}<span>${esc(name)}</span>`);
    b.dataset.s = id;
    if (id === "updates") b.append(h("span", "count", UPD && UPD.available && !UPD.skipped ? "NEW" : ""));
    b.onclick = () => { settingsSection = id; $$("button", nav).forEach(x => x.classList.toggle("on", x === b)); drawSection(); };
    nav.append(b);
  });
  nav.append(h("div", "sver", `Blaze ${esc(S.version)} · shared with Blaze CLI in ~/.blaze`));
  wrap.append(nav, pageEl); el.append(wrap);
  const drawSection = async () => {
    await refreshState();
    col.innerHTML = ""; pageEl.scrollTop = 0;
    const [, name] = SECTIONS.find(s => s[0] === settingsSection) || SECTIONS[0];
    const head = h("div", "phead", `<div class="ph"><div class="label acc">Settings</div><h1>${esc(name)}</h1></div>`);
    col.append(head);
    await ((window.SETTINGS_FNS && window.SETTINGS_FNS[settingsSection]) || ({ models: setModels, providers: setProviders, usage: setUsage, behaviour: setBehaviour, appearance: setAppearance, memory: setMemory, skills: setSkills, computer: setComputer, updates: setUpdates })[settingsSection])(col, drawSection);
  };
  drawSection();
}

async function setModels(col, redraw) {
  const sel = S.selection || { auto: true };
  col.append(h("p", "lead", "Choose what answers your messages. Only models that can call tools are used for work and Design; the keyless model is never used for them."));
  col.lastChild.style.marginBottom = "20px";
  // 01 default
  const g1 = group("Default model");
  const ch = h("div", "choice");
  const a = h("button", sel.auto ? "on" : "", `<span class="radio"></span><span><span class="ct">Auto (free routing)</span><span class="cd" style="display:block">Uses your chain below: the fastest healthy model goes first, and it moves on when one is rate limited, out of quota or failing.</span></span>`);
  const f = h("button", sel.auto ? "" : "on", `<span class="radio"></span><span><span class="ct">One model</span><span class="cd" style="display:block">${sel.auto ? "Always start with a model you pick." : `Using <span class="mono">${esc(sel.label)}</span>.`}</span></span>`);
  ch.append(a, f); g1.append(ch);
  const ready = (S.providers || []).filter(p => p.ready && p.id !== "pollinations");
  const pk = h("div", "picker2");
  const ps = h("select", "field"); ps.innerHTML = ready.map(p => `<option value="${p.id}" ${!sel.auto && sel.provider === p.id ? "selected" : ""}>${esc(p.short)}</option>`).join("") || `<option value="">No provider yet</option>`;
  const msel = h("div", "field mono", ""); msel.style.cssText = "display:flex;align-items:center;cursor:pointer;overflow:hidden;white-space:nowrap"; msel.tabIndex = 0;
  const box = h("div", "modelbox"); box.style.display = "none";
  const fill = async () => {
    const pid = ps.value; if (!pid) { msel.textContent = "Add a provider first"; return; }
    const pv = ready.find(p => p.id === pid);
    const curModel = !sel.auto && sel.provider === pid ? sel.model : pv.model;
    msel.textContent = curModel || "Choose a model";
    box.innerHTML = `<div class="mlist"><div class="pnote">${BrandProgress.skeleton(3)}</div></div>`;
    const l = await API().ModelOptions(pid, false);
    drawModelRows($(".mlist", box), pv, l, curModel, async m => {
      if (m.tools === "no") { msel.textContent = `${m.id} can't call tools; pick one marked Tools`; return; }
      setModel(await API().SelectModel(pid, m.id)); redraw();
    });
  };
  msel.onclick = () => { box.style.display = box.style.display ? "" : "none"; };
  ps.onchange = () => { fill(); box.style.display = ""; };
  const use = h("button", "btn accent", "Use this model");
  use.onclick = async () => { if (!ps.value) { addModels(); return; } const m = msel.textContent.trim(); setModel(await API().SelectModel(ps.value, /\s/.test(m) ? "" : m)); redraw(); };
  pk.append(ps, msel, use);
  const fixedArea = h("div"); fixedArea.append(pk, box);
  fixedArea.style.display = sel.auto ? "none" : "";
  a.onclick = async () => { setModel(await API().SelectModel("auto", "")); redraw(); };
  f.onclick = () => { a.classList.remove("on"); f.classList.add("on"); fixedArea.style.display = ""; fill(); };
  if (!sel.auto) fill();
  g1.append(fixedArea);
  const rt = S.routing || {};
  const fb = h("label", "toggle", `<input type="checkbox" ${rt.fallback ? "checked" : ""}><span><span class="tt">When the chosen model fails, continue with the chain</span><span class="td">Rate limits, outages and timeouts hand the turn to the next healthy model instead of stopping.</span></span>`);
  fb.style.marginTop = "12px"; fb.style.display = sel.auto ? "none" : "flex";
  $("input", fb).onchange = e => { rt.fallback = e.target.checked; API().SetRouting(rt); };
  g1.append(fb);

  // 02 chain
  const fs = h("button", "btn ghost xs", "Use my free stack"); fs.title = "Every free-tier provider with a key, in the recommended order";
  const g2 = group("Auto chain", "Tried top to bottom. Health updates live: resting models wait out their limit, then come back.", fs);
  const rows = h("div", "rows");
  const status = await API().RouterStatus() || [];
  const stat = {}; status.forEach(s => stat[s.provider + "|" + s.model] = s);
  const chain = (S.chain || []).slice();
  const save = async c => { await API().SetChain(c); redraw(); };
  fs.onclick = async () => { await API().UseFreeStack(); redraw(); };
  chain.forEach((e, i) => {
    const pv = (S.providers || []).find(p => p.id === e.provider) || { short: e.provider };
    const st = stat[e.provider + "|" + e.model];
    const health = !pv.ready ? "No key" : st ? (st.cooling ? `Resting ${st.cooling}s${st.resting ? " · " + st.resting : ""}` : st.ok ? `${st.avg_ms} ms avg · ${st.ok} ok` : st.fails ? "Failing" : "Ready") : "Not tried yet";
    const noTools = st && !st.can_tools;
    const r = h("div", "r chainrow", `<span class="n">${pad(i + 1)}</span><div class="main"><div class="t">${esc(pv.short)} <span class="dot ${!pv.ready || noTools ? "off" : st && st.cooling ? "warn" : ""}"></span>${noTools ? '<span class="pill">No tools · skipped</span>' : ""}</div><div class="s"><span class="mono">${esc(e.model)}</span> · ${esc(health)}${st && st.last_error ? ` · <span title="${esc(st.last_error)}">last error</span>` : ""}</div></div>`);
    const ac = h("div", "acts");
    const up = h("button", "btn ghost xs icon", icon("up", "i sm")); up.disabled = i === 0; up.title = "Move up";
    const dn = h("button", "btn ghost xs icon", icon("down", "i sm")); dn.disabled = i === chain.length - 1; dn.title = "Move down";
    const rm = h("button", "btn ghost xs icon", icon("x", "i sm")); rm.title = "Remove from chain";
    up.onclick = () => { const c = chain.slice(); [c[i - 1], c[i]] = [c[i], c[i - 1]]; save(c); };
    dn.onclick = () => { const c = chain.slice(); [c[i + 1], c[i]] = [c[i], c[i + 1]]; save(c); };
    rm.onclick = () => save(chain.filter((_, j) => j !== i));
    ac.append(up, dn, rm); r.append(ac); rows.append(r);
  });
  if (!chain.length) rows.append(h("div", "empty", "<b>The chain is empty</b>Add a provider's key and it joins here, or use your free stack."));
  g2.append(rows);
  // add to chain
  const addRow = h("div", "keyrow"); addRow.style.marginTop = "8px";
  const ap = h("select", "field"); ap.style.maxWidth = "200px"; ap.innerHTML = ready.map(p => `<option value="${p.id}">${esc(p.short)}</option>`).join("");
  const am = h("input", "field mono"); am.placeholder = "model id (blank = provider's model)";
  const ab = h("button", "btn line sm", `${icon("plus", "i sm")}Add to chain`);
  ab.onclick = () => { if (!ap.value) return; save(chain.concat([{ provider: ap.value, model: am.value.trim() }])); };
  if (ready.length) { addRow.append(ap, am, ab); g2.append(addRow); }

  // 03 routing
  const g3 = group("Routing");
  const bx = h("div", "box pad formgrid");
  const tg = (on, title, desc, fn) => { const l = h("label", "toggle", `<input type="checkbox" ${on ? "checked" : ""}><span><span class="tt">${title}</span><span class="td">${desc}</span></span>`); $("input", l).onchange = e => fn(e.target.checked); return l; };
  bx.append(
    tg(rt.fastest_first, "Fastest healthy model first", "Among equally healthy models, Auto tries the one that has been answering fastest.", v => { rt.fastest_first = v; API().SetRouting(rt); }),
    tg(rt.keyless, "Keyless fallback for plain chat", "When no model is set up at all, Pollinations may answer questions in Work, without tools. It is never used for actions or Design.", v => { rt.keyless = v; API().SetRouting(rt); }),
  );
  const cap = h("div", "keyrow", `<span class="tt" style="color:var(--ink);flex:1">Daily spend cap (USD) <span class="td sub small" style="display:block">Past it, a paid default model hands over to the free Auto chain until tomorrow. Today so far: $${(rt.today_cost || 0).toFixed(4)}.</span></span><input class="field mono" style="max-width:110px" placeholder="none" value="${rt.daily_cap || ""}">`);
  $("input", cap).onchange = e => { rt.daily_cap = parseFloat(e.target.value) || 0; API().SetRouting(rt); };
  bx.append(cap); g3.append(bx);
  col.append(g1, g2, g3);
}

async function setProviders(col, redraw) {
  col.append(h("p", "lead", "Add keys for the AI services you use. A second key for the same service joins its key ring: when one runs out of quota, Blaze carries on with the next. Keys are stored in your system keychain."));
  col.lastChild.style.marginBottom = "20px";
  const g = group("Providers");
  g.append(providerRows(redraw));
  col.append(g);
}

async function setUsage(col, redraw) {
  const u = await API().Usage() || {};
  const fmtN = n => n >= 1e6 ? (n / 1e6).toFixed(1) + "M" : n >= 1e3 ? (n / 1e3).toFixed(1) + "K" : String(n || 0);
  const money = t => t && t.priced ? "$" + (t.cost || 0).toFixed(t.cost < 1 ? 4 : 2) : (t && t.cost ? "≈$" + t.cost.toFixed(4) : "$0");
  col.append(h("p", "lead", "Tokens per day and model, with a cost estimate where the price is known. Free tiers count as $0. Counted on this computer."));
  col.lastChild.style.marginBottom = "20px";
  const t = u.today || {}, m = u.month || {};
  const stats = h("div", "stats", `<div><div class="label">Today</div><div class="v">${fmtN((t.in || 0) + (t.out || 0))}</div><div class="sub small">tokens · ${t.calls || 0} calls</div></div>
    <div><div class="label">Today's cost</div><div class="v">${money(t)}</div><div class="sub small">estimated</div></div>
    <div><div class="label">This month</div><div class="v">${fmtN((m.in || 0) + (m.out || 0))}</div><div class="sub small">tokens · ${m.calls || 0} calls</div></div>
    <div><div class="label">Month's cost</div><div class="v">${money(m)}</div><div class="sub small">estimated</div></div>`);
  const days = u.days || [];
  const max = Math.max(1, ...days.map(d => d.tokens));
  const chart = h("div", "chart", days.map(d => `<div class="${d.tokens ? "" : "z"}" style="height:${Math.max(2, Math.round(d.tokens / max * 100))}%" title="${esc(d.day)}: ${fmtN(d.tokens)} tokens · ${d.calls} calls"></div>`).join(""));
  const cx = h("div", "chartx", `<span class="label weak">${esc((days[0] || {}).day || "")}</span><span class="label weak">Last 14 days</span><span class="label weak">Today</span>`);
  const g1 = group("Overview"); g1.append(stats, chart, cx);
  const rs = h("button", "btn ghost xs", "Reset");
  rs.onclick = async () => { if (!rs.dataset.sure) { rs.dataset.sure = 1; rs.textContent = "Clear all usage? Click again"; return; } await API().ResetUsage(); redraw(); };
  const g2 = group("By model · this month", "", rs);
  const rows = h("div", "rows");
  (u.models || []).forEach((x, i) => rows.append(h("div", "r", `<span class="n">${pad(i + 1)}</span><div class="main"><div class="t">${esc((S.providers.find(p => p.id === x.provider) || {}).short || x.provider)} <span class="mono small sub">${esc(x.model)}</span></div><div class="s">${fmtN(x.in)} in · ${fmtN(x.out)} out · ${x.calls} calls</div></div><span class="mono small">${money(x)}</span>`)));
  if (!(u.models || []).length) rows.append(h("div", "empty", "<b>No usage yet</b>It shows up here after your first message."));
  g2.append(rows);
  col.append(g1, g2);
}

async function setBehaviour(col, redraw) {
  const g1 = group("Modes", "Both show in the composer and are remembered.");
  const bx = h("div", "box pad formgrid");
  bx.append(modeSetting("Work mode", "Ctrl+Tab", [["plan", "Plan", "Read and scout only, then a plan. Enforced: tools that change things aren't offered."], ["build", "Build", "Every tool: write code, run commands, install."]], MODES.work_mode, async k => { await setWorkMode(k); redraw(); }));
  bx.append(modeSetting("Approval", "Shift+Tab", [["auto", "Auto", "Risky commands, deletes and installs ask you first."], ["autopilot", "Autopilot", "Works until done. Questions get “you’re away, decide by best practice”. Disk-wiping commands stay blocked."]], MODES.approval, async k => { await setApproval(k); redraw(); }));
  g1.append(bx);
  const gA = group("Chat");
  const bxA = h("div", "box pad formgrid"); gA.append(bxA);
  const g2 = group("Design tab", "When Blaze makes something visual, open it in Design:");
  const seg2 = h("div", "seg");
  [["ask", "Ask first"], ["always", "Always"], ["never", "Never"]].forEach(([k, t]) => { const b = h("button", (S.prefs.design_mode || "ask") === k ? "on" : "", t); b.onclick = async () => { await API().SetDesignMode(k); S.prefs.design_mode = k; redraw(); }; seg2.append(b); });
  g2.append(seg2);
  const g3 = group("Style", "How Blaze should talk to you (optional).");
  const pers = h("textarea", "field"); pers.placeholder = "e.g. Short answers. Explain like I'm new to coding."; pers.value = S.persona || "";
  const ps = h("button", "btn line sm", "Save style"); ps.style.marginTop = "8px";
  ps.onclick = async () => { await API().SetPersona(pers.value); ps.textContent = "Saved"; setTimeout(() => ps.textContent = "Save style", 1200); };
  g3.append(pers, ps);
  const t2 = h("label", "toggle", `<input type="checkbox" ${S.prefs.show_steps ? "checked" : ""}><span><span class="tt">Show every step (power users)</span><span class="td">Off: one plain-English status line per answer; click “Worked · N steps” to see each step and its exact command.</span></span>`);
  $("input", t2).onchange = e => { API().SetShowSteps(e.target.checked); S.prefs.show_steps = e.target.checked; };
  bxA.append(t2);
  const t3 = h("label", "toggle", `<input type="checkbox" ${foldCards() ? "checked" : ""}><span><span class="tt">Fold long answers into cards</span><span class="td">The short answer stays in view; tables, long lists and reports go in a card you can expand.</span></span>`);
  $("input", t3).onchange = e => { API().SetFoldCards(e.target.checked); S.prefs.fold_cards = e.target.checked; };
  bxA.append(t3);
  const t4 = h("label", "toggle", `<input type="checkbox" ${S.prefs.auto_titles !== false ? "checked" : ""}><span><span class="tt">Name chats by their topic</span><span class="td">A tiny call on your cheapest model titles each chat by what it's mostly about, and updates it as the chat moves on. Double-click a chat to rename it yourself.</span></span>`);
  $("input", t4).onchange = e => { API().SetAutoTitles(e.target.checked); S.prefs.auto_titles = e.target.checked; };
  bxA.append(t4);
  col.append(g1, gA, voiceGroup(redraw), backgroundGroup(redraw), agentsGroup(redraw), g2, Polish.personalityGroup(redraw), g3);
}

function modeSetting(title, key, opts, cur, pick) {
  const r = h("div", "modeset", `<div class="mst"><span class="tt">${esc(title)}</span><span class="kbd">${esc(key)}</span></div>`);
  const seg = h("div", "mseg big");
  opts.forEach(([k, t]) => { const b = h("button", k === cur ? "on" : "", esc(t)); b.onclick = () => pick(k); seg.append(b); });
  const d = h("div", "td", esc((opts.find(o => o[0] === cur) || opts[0])[2]));
  r.append(seg, d);
  if (title === "Approval") seg.classList.toggle("pilot", cur === "autopilot");
  return r;
}

// Settings › Behaviour › Voice input (local Whisper).
function voiceGroup(redraw) {
  const g = group("Voice input", "Speech to text with Whisper, on this PC. Free and offline after a one-time download.");
  const bx = h("div", "box pad formgrid"); g.append(bx);
  (async () => {
    let st = null; try { st = await API().STTStatus(); } catch (e) {}
    if (!st || typeof st !== "object") { bx.append(h("div", "td", "Not available in this build.")); return; }
    const state = st.ready ? `Ready · ${esc(st.model)} model` : st.engine_how === "manual" && !st.engine ? esc(st.note) : "Not set up yet. Click the mic in the composer to set it up.";
    bx.append(h("div", "modeset", `<div class="mst"><span class="tt">Status</span></div><div class="td">${state}</div>`));
    const seg = h("div", "mseg big");
    (st.models || []).forEach(m => { const b = h("button", m.name === st.model ? "on" : "", `${esc(m.name)} · ${m.mb} MB`); b.title = m.note; b.onclick = async () => { await API().SetSTT(m.name, ""); redraw(); }; seg.append(b); });
    const r = h("div", "modeset", `<div class="mst"><span class="tt">Model</span></div>`); r.append(seg, h("div", "td", "Bigger is more accurate and slower. A new model downloads the next time you use the mic."));
    bx.append(r);
    const lang = h("input", "field mono"); lang.value = st.lang || "auto"; lang.placeholder = "auto";
    lang.onchange = () => API().SetSTT("", lang.value || "auto");
    const lr = h("div", "modeset", `<div class="mst"><span class="tt">Language</span></div>`); lr.append(lang, h("div", "td", "auto, or a language code like en, fr, sw."));
    bx.append(lr);
  })();
  return g;
}

// Settings › Behaviour › Background agents: how many run at once.
function agentsGroup(redraw) {
  const g = group("Background agents & specialists", "Blaze does your tasks itself. It starts a background agent or splits work across specialists only when you ask (and for Design's Directions). More at once finishes sooner but works your computer harder.");
  const row = h("div", "hkrow", `<span class="tt">Run at most</span>`);
  const seg = h("div", "seg");
  const cur = S.prefs.max_agents || (S.info && S.info.cores && S.info.cores <= 2 ? 1 : 3);
  [1, 2, 3, 4, 6].forEach(n => { const b = h("button", cur === n ? "on" : "", `${n} at once`); b.onclick = async () => { S.prefs.max_agents = await API().SetMaxAgents(n); redraw(); }; seg.append(b); });
  row.append(seg);
  const bx = h("div", "box pad formgrid"); bx.append(row, h("div", "hknote", "Extra agents and specialists wait in a queue and start when one finishes. Specialists that change code each work in their own git worktree (or a copy of the folder) until Blaze reviews and applies their changes."));
  g.append(bx);
  return g;
}

// Settings › Behaviour › Background & shortcut.
function backgroundGroup(redraw) {
  const d = S.desktop || {};
  const mac = d.os === "darwin";
  const where = mac ? "menu bar" : "system tray";
  const g = group("Background & shortcut", `Blaze can stay in the ${where} and open a small quick-ask orb from anywhere.`);
  const bx = h("div", "box pad formgrid");
  const tb = h("label", "toggle", `<input type="checkbox" ${d.background ? "checked" : ""}><span><span class="tt">Keep running in the background</span><span class="td">Closing the window hides Blaze in the ${where}. Quit from the ${where} menu.${d.tray ? "" : " The " + where + " isn't available here, so closing still quits."}</span></span>`);
  $("input", tb).onchange = async e => { await API().SetBackground(e.target.checked); d.background = e.target.checked; };
  const ta = h("label", "toggle", `<input type="checkbox" ${d.autostart ? "checked" : ""}><span><span class="tt">Start Blaze when you sign in</span><span class="td">It starts hidden, ready for the shortcut.</span></span>`);
  $("input", ta).onchange = async e => { const err = await API().SetAutostart(e.target.checked); if (err) { e.target.checked = !e.target.checked; note.textContent = err; } else redraw(); };
  const row = h("div", "hkrow", `<span class="tt">Quick-ask shortcut</span>`);
  const seg = h("div", "seg");
  const opts = (d.presets || []).concat([{ id: "off", label: "Off" }]);
  opts.forEach(p => { const b = h("button", d.hotkey === p.id ? "on" : "", esc(p.label)); b.onclick = async () => { S.desktop = { ...d, ...(await API().SetHotkey(p.id)) }; redraw(); }; seg.append(b); });
  row.append(seg);
  const note = h("div", "hknote");
  const st = h("div", "hkstatus");
  if (d.wayland) st.innerHTML = `<span class="dot ink"></span><span>Wayland doesn't let apps grab a global shortcut. Bind one in your desktop's keyboard settings to run <span class="mono">blaze-desktop --orb</span>.</span>`;
  else if (d.hotkey === "off") st.innerHTML = `<span class="dot off"></span><span>Shortcut off. The ${where} icon still opens quick ask.</span>`;
  else st.innerHTML = `<span class="dot ${d.hotkey_ok ? "" : "ink"}"></span><span>${esc(d.hotkey_note || "")}</span>`;
  if (d.needs_access) {
    const acts = h("div", "keyrow"); acts.style.marginTop = "8px";
    const ga = h("button", "btn accent sm", "Grant access"); ga.onclick = () => API().GrantAccess();
    const tr = h("button", "btn line sm", "Try again"); tr.onclick = async () => { S.desktop = { ...d, ...(await API().RetryHotkey()) }; redraw(); };
    acts.append(ga, tr); st.append(acts);
  }
  bx.append(tb, ta, row, st, note);
  g.append(bx);
  return g;
}

async function setAppearance(col, redraw) {
  const g = group("Theme");
  const seg = h("div", "seg");
  [["system", "System"], ["light", "Light"], ["dark", "Dark"]].forEach(([t, n]) => { const b = h("button", S.prefs.theme === t ? "on" : "", n); b.onclick = async () => { await API().SetTheme(t); S.prefs.theme = t; applyTheme(); redraw(); }; seg.append(b); });
  g.append(seg);
  const motion = h("label", "toggle", `<input id="reduceMotionToggle" type="checkbox" ${localStorage.getItem('blaze.reduceMotion') === '1' ? 'checked' : ''}><span><span class="tt">Reduce motion</span><span class="td">Turns off the glass shimmer and animated effects.</span></span>`);
  $("input", motion).onchange = e => {
    localStorage.setItem('blaze.reduceMotion', e.target.checked ? '1' : '0');
    if (window.BlazeMotion && typeof window.BlazeMotion.sync === 'function') window.BlazeMotion.sync();
    else if (window.BlazeMotion) window.BlazeMotion.enabled = !e.target.checked;
    document.documentElement.classList.toggle('reduce-motion', e.target.checked);
  };
  g.append(motion);
  col.append(g);
}

async function setMemory(col) {
  const g = group("What Blaze remembers", "Lasting facts Blaze saves as you work. Edit freely, one fact per line. Saved in Blaze's local database (~/.blaze/blaze.db); each chat only gets the facts that matter to it.");
  g.id = "memory";
  const mem = h("textarea", "field"); mem.style.minHeight = "260px"; mem.value = S.memory || ""; mem.placeholder = "Nothing yet. Blaze saves lasting facts here as you work, or type your own.";
  const ms = h("button", "btn line sm", "Save"); ms.style.marginTop = "8px"; ms.onclick = async () => { await API().SetMemory(mem.value); ms.textContent = "Saved"; setTimeout(() => ms.textContent = "Save", 1200); };
  const mg = h("button", "btn ghost sm", "Open the memory graph"); mg.style.cssText = "margin:8px 0 0 6px"; mg.onclick = () => show("memory");
  g.append(mem, ms, mg); col.append(g);
}

async function setSkills(col) {
  const g = group("Skills", "Reusable instructions Blaze loads when a task matches. Ask Blaze to save one after a task; same SKILL.md format as the phone app.");
  const sk = h("div", "rows");
  (S.skills || []).forEach((s, i) => { const [n, ...d] = s.split(":"); sk.append(h("div", "r", `<span class="n">${pad(i + 1)}</span><div class="main"><div class="t mono small">${esc(n)}</div><div class="s">${esc(d.join(":").trim())}</div></div><span></span>`)); });
  if (!(S.skills || []).length) sk.append(h("div", "empty", "<b>No skills yet</b>After a task, say “save this as a skill”."));
  g.append(sk); col.append(g);
}

async function setComputer(col, redraw) {
  const rf = h("button", "btn ghost xs", `${icon("retry", "i sm")}Detect again`);
  rf.onclick = async () => { rf.textContent = "Detecting…"; await API().RefreshPC(); redraw(); };
  const g = group("Detected", "Blaze reads this itself so it never has to ask what you're running.", rf);
  g.append(pcTable(S.info)); col.append(g);
}

async function setUpdates(col) {
  const g = group("Updates"); g.id = "updates";
  const bx = h("div", "box pad");
  const ur = h("div", ""); const us = h("div", "label weak updstatus"); us.style.marginTop = "8px";
  const ub = h("div", "keyrow"); ub.style.marginTop = "12px";
  const chk = h("button", "btn line sm", "Check for updates"); const ins = h("button", "btn accent sm", "Update now"); ins.style.display = "none";
  const drawU = u => {
    if (!u) { ur.textContent = `You have ${S.version}.`; return; }
    if (u.error) ur.textContent = `You have ${u.current || S.version}. ${u.error}`;
    else if (u.available) ur.textContent = `Blaze ${u.latest} is out. You have ${u.current}. ${u.method || ""}`;
    else ur.textContent = `You're on the latest version, ${u.current}.`;
    ins.style.display = u.available ? "" : "none";
    ins.textContent = u.can_auto ? "Update now" : "Open downloads page";
  };
  chk.onclick = async () => { chk.disabled = true; chk.textContent = "Checking…"; drawU(await checkUpdate(false)); chk.disabled = false; chk.textContent = "Check again"; };
  ins.onclick = () => UPD && (UPD.can_auto ? installUpdate(bx) : API().OpenURL(UPD.page));
  const t3 = h("label", "toggle", `<input type="checkbox" ${S.prefs.no_update_check ? "" : "checked"}><span class="tt">Check for updates when Blaze opens</span>`);
  $("input", t3).onchange = e => { API().SetUpdateCheck(e.target.checked); S.prefs.no_update_check = !e.target.checked; };
  t3.style.marginTop = "14px"; t3.style.display = "flex";
  ub.append(chk, ins); bx.append(ur, us, ub, t3); drawU(UPD); g.append(bx); col.append(g);
}

// ---------- doctor ----------
function doctorView(el) {
  const { col, acts } = page(el, "Doctor", "Check everything", "Pings each model in your route and checks the shell, tools, disk and scheduler.");
  const rows = h("div", "rows"); const run = h("button", "btn accent", "Run checks");
  acts.append(run); col.append(rows);
  run.onclick = async () => {
    run.disabled = true; run.textContent = "Checking…"; rows.innerHTML = `<div class="empty">Running checks…</div>`;
    const res = await API().Doctor() || [];
    rows.innerHTML = "";
    const ok = res.filter(r => r.ok).length;
    run.disabled = false; run.textContent = `${ok} of ${res.length} passed · run again`;
    res.forEach((r, i) => {
      const row = h("div", "r", `<span class="dot ${r.ok ? "" : "ink"}"></span><div class="main"><div class="t">${esc(r.name)}</div><div class="s">${esc(r.detail)}</div></div>`);
      // A check that can be fixed in one click (a macOS privacy pane) gets a button; the rest a status label.
      if (!r.ok && r.fix) { const b = h("button", "btn line sm fixbtn", `Fix${icon("ext", "i sm")}`); b.onclick = () => API().OpenURL(r.fix); row.append(b); }
      else row.append(h("span", `pill ${r.ok ? "acc" : "ink"}`, r.ok ? "OK" : "Fix"));
      rows.append(row);
    });
  };
  run.click();
}

// ---------- jobs ----------
function jobsView(el) {
  const { col, acts } = page(el, "Jobs", "Background jobs", "Servers, watchers and long builds Blaze started. They stop when you quit Blaze.");
  const rf = h("button", "btn line sm", `${icon("retry", "i sm")}Refresh`); acts.append(rf);
  const rows = h("div", "rows"); const box = h("div", "logbox"); box.style.marginTop = "14px"; box.style.display = "none";
  col.append(rows, box);
  const draw = async () => {
    const t = String(await API().Jobs() || "");
    rows.innerHTML = "";
    const jobs = [...t.matchAll(/^Job (\d+) \[([^\]]+)\] (.*)$/gm)];
    jobs.forEach(m => {
      const id = +m[1], running = /^running/.test(m[2]);
      const r = h("div", "r", `<span class="dot ${running ? "" : "off"}"></span><div class="main"><div class="t mono small">${esc(m[3])}</div><div class="s">Job ${id} · ${esc(m[2])}</div></div>`);
      const a = h("div", "acts");
      const lg = h("button", "btn ghost xs", "Log"); lg.onclick = async () => { box.style.display = ""; box.textContent = await API().JobLog(id); };
      a.append(lg);
      if (running) { const st = h("button", "btn line xs", "Stop"); st.onclick = async () => { box.style.display = ""; box.textContent = await API().JobStop(id); refreshJobCount(); draw(); }; a.append(st); }
      r.append(a); rows.append(r);
    });
    if (!jobs.length) { rows.append(h("div", "empty", "<b>No background jobs</b>When Blaze starts a server or watcher, it shows up here.")); if (t.trim() && !/no jobs/i.test(t)) { box.style.display = ""; box.textContent = t; } }
  };
  rf.onclick = draw; draw();
}

// ---------- schedule ----------
function scheduleView(el) {
  const { col } = page(el, "Schedule", "Scheduled jobs", `They run even when Blaze is closed, through ${S.info.os === "windows" ? "Task Scheduler" : "cron"}. To add one, ask in chat, e.g. “every day at 7 back up my notes folder”.`);
  const body = h("div"); col.append(body);
  const draw = async () => {
    body.innerHTML = "";
    const list = await API().Schedules() || [];
    const rows = h("div", "rows"); const box = h("div", "logbox"); box.style.display = "none"; box.style.marginTop = "14px";
    list.forEach((j, i) => {
      const r = h("div", "r", `<span class="n">${pad(i + 1)}</span><div class="main"><div class="t">${esc(j.name)} <span class="pill">${esc(j.when)}</span></div><div class="s mono">${esc(j.command || "blaze: " + j.prompt)}</div></div>`);
      const b = h("div", "acts");
      const lg = h("button", "btn ghost xs", "Log"); lg.onclick = async () => { box.style.display = ""; box.textContent = await API().ScheduleLog(j.name); };
      const rm = h("button", "btn ghost xs", "Remove"); rm.onclick = async () => { await API().RemoveSchedule(j.name); draw(); };
      b.append(lg, rm); r.append(b); rows.append(r);
    });
    if (!list.length) rows.append(h("div", "empty", "<b>Nothing scheduled yet</b>Ask in chat to schedule a job."));
    body.append(rows, box);
  };
  draw();
}

window.addEventListener("load", () => {
  if (window.go && window.runtime) boot();
  else document.body.innerHTML = '<p style="padding:40px;font-family:monospace">Blaze UI needs the desktop app.</p>';
});
