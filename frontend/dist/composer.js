// Blaze composer (v0.5): + menu attachments (images, files, folders), drag-and-drop and
// paste, the mic (local Whisper), and the two modes: Work (Plan / Build, Ctrl+Tab) and
// Approval (Auto / Autopilot, Shift+Tab). Loaded before app.js; uses its helpers at call time.

let ATT = [];            // attachments from +, drop and paste: {kind, path, name, size, count, thumb, note}
let MODES = { work_mode: "build", approval: "auto" };
let STT = null;          // last STTStatus
let rec = null;          // the recording in progress

const CIC = {
  image: '<rect x="2" y="2.5" width="12" height="11"/><path d="m2 11 3.5-3.5 3 3 2-2 3.5 3.5"/><rect x="9.5" y="4.5" width="2" height="2"/>',
  file: '<path d="M3.5 1.5h6l3 3v10h-9z"/><path d="M9.5 1.5v3h3"/>',
  folder: '<path d="M1.5 3.5h5l1.5 1.5h6.5v8h-13z"/>',
  mic: '<rect x="6" y="1.5" width="4" height="8"/><path d="M3.5 7.5a4.5 4.5 0 0 0 9 0M8 12v2.5M5.5 14.5h5"/>',
  slash: '<path d="m10.5 2.5-5 11"/>',
  at: '<circle cx="8" cy="8" r="2.5"/><path d="M10.5 8v1.25a1.75 1.75 0 0 0 3.5 0V8A6 6 0 1 0 11 13.2"/>',
  plan: '<path d="M3 3.5h10M3 8h10M3 12.5h6"/>',
  pilot: '<path d="m8 1.5 5 12.5-5-3-5 3z"/>',
  model: '<circle cx="3.5" cy="8" r="1.5"/><circle cx="12.5" cy="4" r="1.5"/><circle cx="12.5" cy="12" r="1.5"/><path d="M5 8h3l3-3.5M8 8l3 3.5"/>',
  views: '<rect x="2.5" y="2.5" width="4.5" height="4.5"/><rect x="9" y="2.5" width="4.5" height="4.5"/><rect x="2.5" y="9" width="4.5" height="4.5"/><rect x="9" y="9" width="4.5" height="4.5"/>',
};
const cmIcon = (n, cls = "i sm") => `<svg class="${cls}" viewBox="0 0 16 16">${CIC[n] || ""}</svg>`;
const fmtSize = n => !n ? "" : n >= 1048576 ? (n / 1048576).toFixed(1) + " MB" : n >= 1024 ? Math.round(n / 1024) + " KB" : n + " B";

// ---------- markup ----------
// v0.17.1 pill composer (mobile v0.24.1): a thin + on the left, the field, then the quiet extras
// (recording line, mode chip, the ? shortcuts) and the mic, with the round Send / Stop at the end.
function composerLead() {
  return `<button id="plus" class="plusbtn" title="Attach images, files or a folder · commands" aria-label="Attach and tools">${icon("plus", "i sm")}</button>`;
}
function composerTail() {
  return `<span id="recline" class="recline" style="display:none"><span class="rdot"></span><span class="rtime label ink">0:00</span><button class="btn ghost xs rcancel" title="Discard">${icon("x", "i sm")}</button></span>
    <span id="modeChip" class="modechip label" title="Not the default mode · Ctrl+Tab / Shift+Tab" style="display:none"></span>
    <button id="keysHelp" class="plusbtn keyshelp" title="">?</button>
    <div id="modes" class="modes">
      <div class="mseg" id="mWork" title="Work mode · Ctrl+Tab"><button data-m="plan">Plan</button><button data-m="build">Build</button></div>
      <div class="mseg" id="mAppr" title="Approval mode · Shift+Tab"><button data-a="auto">Auto</button><button data-a="autopilot">Autopilot</button></div>
    </div>
    <button id="mic" class="plusbtn mic" title="Voice input · Ctrl+Shift+M (local Whisper)" aria-label="Talk">${cmIcon("mic")}</button>`;
}
function composerBar() { return composerLead() + composerTail(); }

// ---------- v0.17.1 rotating placeholder (mobile v0.24.1 ComposerHints) ----------
// Every ~3 s the placeholder fades and slides to the next suggestion. Built only from what is on
// this computer: connected accounts, recent projects and the open folder, plus generic fallbacks.
// No network or model calls. It stops while the field is focused or has text, while recording,
// in Plan / Autopilot (their own placeholder), and when the system asks for reduced motion.
const Hints = (() => {
  const MAX = 8, ROTATE_MS = 3000, DEFAULT = "Ask Blaze to do something…";
  const CONN = { google: ["Check my Gmail…", "What's on my calendar today?"], github: ["Check my GitHub notifications…"],
    spotify: ["Play something on Spotify…"], notion: ["Search my Notion…"], slack: ["Catch up on Slack…"] };
  const FALLBACKS = ["Run the tests here and fix what fails", "How much disk space is left?", "Design a landing page for…",
    "Make me flashcards on…", "Summarise a PDF for me…", "Draw a lion wearing sunglasses"];
  const clean = (t, n = 48) => { t = String(t || "").replace(/\s+/g, " ").trim(); return t.length > n ? t.slice(0, n - 1).trimEnd() + "…" : t; };
  const base = p => String(p || "").replace(/[\\/]+$/, "").split(/[\\/]/).pop();

  // sources: { connectors: ["google", …] (connected ids), projects: ["HomeGlow", …] (newest first), folder: "/path" }
  function build(src = {}) {
    const out = [];
    const add = h => { const t = clean(h); if (t && !out.includes(t) && out.length < MAX) out.push(t); };
    [...new Set((src.connectors || []).map(x => String(x).toLowerCase()))].flatMap(id => (CONN[id] || []).slice(0, 1)).slice(0, 3).forEach(add);
    (src.projects || []).map(p => clean(p, 28)).filter(Boolean).slice(0, 2).forEach(p => add(`Pick up ${p}…`));
    const f = base(src.folder);
    if (f && f !== "~" && !/^[A-Za-z]:$/.test(f)) { add(`What's in ${clean(f, 24)}? Give me a tour`); add(`Run the tests in ${clean(f, 24)}…`); }
    FALLBACKS.forEach(x => { if (out.length < 6) add(x); });
    return out.slice(0, MAX);
  }

  let list = [], idx = 0, timer = 0;
  const reduced = () => { try { return matchMedia("(prefers-reduced-motion: reduce)").matches; } catch (e) { return false; } };
  const defaultMode = () => MODES.work_mode !== "plan" && MODES.approval !== "autopilot";

  async function gather() {
    const src = { connectors: [], projects: [], folder: (typeof S !== "undefined" && S && S.prefs && S.prefs.folder) || "" };
    try { const c = await API().Connectors(); src.connectors = ((c && c.connectors) || []).filter(x => x.status === "connected" && x.enabled !== false).map(x => x.id); } catch (e) {}
    try { src.projects = ((await API().Projects()) || []).map(p => p.name); } catch (e) {}
    return src;
  }

  // v0.19: the chat pane can be off-page (the app opens on Home), so look inside the pane, not the document.
  const root = () => (typeof pane !== "undefined" && pane) || document;
  function show(text, animate) {
    const i = $("#input", root()), ph = $(".phint", root()), ib = i && i.closest(".inbox"); if (!i || !ph || !ib) return;
    const on = defaultMode();
    ib.classList.toggle("hinting", on);
    if (!on) return;
    i.placeholder = text;
    if (!animate || ph.textContent === text) { ph.textContent = text; ph.className = "phint"; return; }
    ph.className = "phint out";
    setTimeout(() => { ph.textContent = text; ph.className = "phint in"; requestAnimationFrame(() => requestAnimationFrame(() => { ph.className = "phint"; })); }, 300);
  }

  function tick() {
    const i = $("#input", root());
    if (!i || list.length < 2 || reduced() || !defaultMode() || document.activeElement === i || i.value || (typeof rec !== "undefined" && rec)) return;
    idx = (idx + 1) % list.length; show(list[idx], true);
  }

  async function start() {
    list = build(await gather());
    idx = 0; show(reduced() || !list.length ? DEFAULT : list[0], false);
    if (!timer) timer = setInterval(tick, ROTATE_MS);
  }

  return { build, start, show, tick, get list() { return list; }, MAX, ROTATE_MS, DEFAULT };
})();
window.Hints = Hints;

function initComposer(p) {
  MODES = (S && S.modes) || MODES;
  $$("#mWork button", p).forEach(b => b.onclick = () => setWorkMode(b.dataset.m));
  $$("#mAppr button", p).forEach(b => b.onclick = () => setApproval(b.dataset.a));
  $("#mic", p).onclick = () => micClick();
  $(".rcancel", p).onclick = () => stopRec(true);
  const input = $("#input", p);
  input.addEventListener("paste", onPaste);
  // v0.19.1: warm the connection to Blaze Cloud on the first key press (the app limits it to once a minute).
  input.addEventListener("keydown", prewarmCloud);
  // Files dropped anywhere on the window attach to the message (Wails resolves real paths).
  if (window.runtime && typeof window.runtime.OnFileDrop === "function") {
    try { window.runtime.OnFileDrop((x, y, paths) => attachPaths(paths), false); } catch (e) {}
  }
  let depth = 0;
  const isFiles = e => e.dataTransfer && [...(e.dataTransfer.types || [])].includes("Files");
  addEventListener("dragenter", e => { if (isFiles(e)) { depth++; document.body.classList.add("dropping"); } });
  addEventListener("dragleave", e => { if (isFiles(e) && --depth <= 0) { depth = 0; document.body.classList.remove("dropping"); } });
  addEventListener("drop", () => { depth = 0; document.body.classList.remove("dropping"); });
  addEventListener("dragover", e => { if (isFiles(e)) e.preventDefault(); });
  drawModes(p);
  setTimeout(() => Hints.start(), 0);
}

// v0.19.1: open the connection to Blaze Cloud before Send. Throttled here too, so typing never floods the bridge.
// v0.19.7: every 25 s at most (was a minute), also when the window becomes visible, and a
// keep-warm ping every 45 s while the window has focus and was used in the last 5 minutes.
let lastPrewarm = 0, lastUse = Date.now();
function prewarmCloud() {
  lastUse = Date.now();
  const now = Date.now(); if (now - lastPrewarm < 25000) return; lastPrewarm = now;
  try { const f = window.go && window.go.main && window.go.main.App && window.go.main.App.CloudPrewarm; if (typeof f === "function") f().catch(() => {}); } catch (e) {}
}
addEventListener("focus", prewarmCloud);
document.addEventListener("visibilitychange", () => { if (!document.hidden) prewarmCloud(); });
["keydown", "pointerdown"].forEach(ev => addEventListener(ev, () => { lastUse = Date.now(); }, { passive: true, capture: true }));
setInterval(() => { if (!document.hidden && document.hasFocus() && Date.now() - lastUse < 300000) { const u = lastUse; prewarmCloud(); lastUse = u; } }, 45000);

// ---------- modes ----------
function drawModes(p = pane) {
  if (!p) return;
  $$("#mWork button", p).forEach(b => b.classList.toggle("on", b.dataset.m === MODES.work_mode));
  $$("#mAppr button", p).forEach(b => b.classList.toggle("on", b.dataset.a === MODES.approval));
  $("#mAppr", p).classList.toggle("pilot", MODES.approval === "autopilot");
  const ib = $(".inbox", p);
  if (ib) { ib.classList.toggle("plan", MODES.work_mode === "plan"); ib.classList.toggle("pilot", MODES.approval === "autopilot"); }
  const i = $("#input", p);
  if (i) i.placeholder = MODES.work_mode === "plan" ? "Plan mode: Blaze reads and scouts, then proposes a plan. It changes nothing." : (MODES.approval === "autopilot" ? "Autopilot: Blaze works until it's done and decides on its own." : (Hints.list[0] && $(".phint", p) && $(".phint", p).textContent) || "Ask Blaze to do something…");
  if (typeof Hints !== "undefined") { const ib2 = $(".inbox", p); if (ib2) ib2.classList.toggle("hinting", MODES.work_mode !== "plan" && MODES.approval !== "autopilot" && !!($(".phint", p) || {}).textContent); }
  if (S) S.modes = MODES;
  const chip = $("#modeChip", p);
  if (chip) {
    const off = MODES.work_mode !== "build" || MODES.approval !== "auto";
    chip.style.display = off ? "" : "none";
    chip.textContent = `${MODES.work_mode === "plan" ? "Plan" : "Build"} · ${MODES.approval === "autopilot" ? "Autopilot" : "Auto"}`;
    chip.classList.toggle("pilot", MODES.approval === "autopilot");
  }
  hintText(p);
}
function applyModes(m) { if (m && m.work_mode) { MODES = { work_mode: m.work_mode, approval: m.approval }; drawModes(); } }
async function setWorkMode(m) { MODES.work_mode = m === "plan" ? "plan" : "build"; drawModes(); try { applyModes(await API().SetWorkMode(MODES.work_mode)); } catch (e) {} flashMode(MODES.work_mode); }
async function setApproval(m) { MODES.approval = m === "autopilot" ? "autopilot" : "auto"; drawModes(); try { applyModes(await API().SetApprovalMode(MODES.approval)); } catch (e) {} flashMode(MODES.approval); }
function flashMode(m) {
  const el = $(`#modes [data-m="${m}"], #modes [data-a="${m}"]`, pane); if (!el) return;
  el.classList.remove("flash"); void el.offsetWidth; el.classList.add("flash");
}

// Ctrl+Tab cycles Plan/Build, Shift+Tab toggles Auto/Autopilot (Alt+Tab belongs to the OS).
function modeKeys(e) {
  if (view !== "work" && view !== "design") return false;
  if ((e.ctrlKey || e.metaKey) && e.shiftKey && !e.altKey && (e.key || "").toLowerCase() === "m") { e.preventDefault(); micClick(); return true; }
  if (e.key !== "Tab" || e.altKey || e.metaKey) return false;
  if (e.ctrlKey && !e.shiftKey) { e.preventDefault(); setWorkMode(MODES.work_mode === "plan" ? "build" : "plan"); return true; }
  if (e.shiftKey && !e.ctrlKey) {
    if (document.activeElement && document.activeElement !== document.body && !document.activeElement.closest("#composer")) return false;
    e.preventDefault(); setApproval(MODES.approval === "autopilot" ? "auto" : "autopilot"); return true;
  }
  return false;
}
addEventListener("keydown", e => { if (!e.defaultPrevented) modeKeys(e); }, true);

function hintText(p = pane) {
  const el = p && $("#hint", p); if (!el) return;
  if (busy) { el.textContent = (MODES.approval === "autopilot" ? "Autopilot is working…" : "Working…") + " send a message to interrupt · Stop ends this answer"; return; }
  const kh = p && $("#keysHelp", p);
  if (kh) kh.title = `/ commands · @ files · + attach · Shift+Enter new line · Ctrl+K search · Ctrl+Tab ${MODES.work_mode === "plan" ? "Plan" : "Build"} · Shift+Tab ${MODES.approval === "autopilot" ? "Autopilot" : "Auto"} · Ctrl+Shift+M voice`;
  el.closest(".hintline") && el.closest(".hintline").classList.remove("flash");
  el.innerHTML = `/ commands · @ files · + attach · Shift+Enter new line<span class="hk"><span class="kbd">Ctrl Tab</span> ${MODES.work_mode === "plan" ? "Plan" : "Build"} · <span class="kbd">Shift Tab</span> ${MODES.approval === "autopilot" ? "Autopilot" : "Auto"}</span>`;
}

// ---------- attachments ----------
function addAttach(list) {
  let skipped = 0;
  (list || []).forEach(a => {
    if (!a || !a.path) { if (a && a.note) toastLine(a.note); return; }
    if (ATT.some(x => x.path === a.path)) return;
    if (ATT.length >= 12) { skipped++; return; }
    ATT.push(a);
  });
  if (skipped) toastLine(`Up to 12 attachments per message.`);
  drawAttach();
  const i = $("#input"); if (i) i.focus();
}
async function attachPaths(paths) { if (paths && paths.length) addAttach(await API().AttachPaths(paths)); }
async function pick(kind) { addAttach(await API().AttachPick(kind)); }

async function onPaste(e) {
  const items = [...((e.clipboardData && e.clipboardData.items) || [])].filter(it => it.kind === "file" && /^image\//.test(it.type));
  if (!items.length) return;
  e.preventDefault();
  for (const it of items) {
    const f = it.getAsFile(); if (!f) continue;
    if (f.size > 12 * 1048576) { toastLine("That image is over 12 MB."); continue; }
    const url = await new Promise(r => { const fr = new FileReader(); fr.onload = () => r(fr.result); fr.onerror = () => r(""); fr.readAsDataURL(f); });
    if (url) addAttach([await API().AttachPasted(f.name || "image.png", url)]);
  }
}

function attachChip(a, onRemove) {
  const meta = a.kind === "folder" ? `${a.count || 0} item${a.count === 1 ? "" : "s"}` : fmtSize(a.size);
  const vis = a.thumb ? `<img src="${a.thumb}" alt="">` : `<span class="ak">${cmIcon(a.kind === "image" ? "image" : a.kind === "folder" ? "folder" : "file")}</span>`;
  const c = h("span", "achip k-" + a.kind, `${vis}<span class="an" title="${esc(a.path)}">${esc(a.name || base(a.path))}</span>${meta ? `<span class="am">${esc(meta)}</span>` : ""}`);
  if (onRemove) { const x = h("button", "ax", icon("x", "i sm")); x.title = "Remove"; x.onclick = onRemove; c.append(x); }
  if (window.CSVKit) CSVKit.decorate(c, a); // W2: a CSV chip gets a "Table" button (local clean + chart + report)
  return c;
}

function drawAttach() {
  const a = $("#attach", pane); if (!a) return;
  a.innerHTML = "";
  attachments.forEach((f, i) => { const c = h("span", "fchip", `@ ${esc(f)} <button title="Remove">×</button>`); $("button", c).onclick = () => { attachments.splice(i, 1); drawAttach(); }; a.append(c); });
  ATT.forEach((x, i) => a.append(attachChip(x, () => { ATT.splice(i, 1); drawAttach(); })));
  if (typeof sendLabel === "function") sendLabel();
  const warn = $("#visionwarn", pane);
  const imgs = ATT.some(x => x.kind === "image");
  if (warn) {
    warn.style.display = imgs && S && !S.vision_model ? "" : "none";
    warn.innerHTML = `${cmIcon("image")}<span>None of your models can see images, so Blaze sends them as file paths. Add a model that can see pictures, or use Blaze Cloud.</span><a class="go">Add a model</a>`;
    const g = $(".go", warn); if (g) g.onclick = () => addModels();
  }
}

// Backspace on an empty input removes the last chip.
function popAttach() {
  if (ATT.length) { ATT.pop(); drawAttach(); return true; }
  if (attachments.length) { attachments.pop(); drawAttach(); return true; }
  return false;
}

function takeAttach() { const a = ATT.slice(); ATT = []; return a; }

// The + menu: attachments first, then the command menu.
function plusItems() {
  return [
    { name: "Image…", icon: "image", desc: "png, jpg, gif, webp · or paste one", run: () => pick("image") },
    { name: "File…", icon: "file", desc: "any file; text goes in the message", run: () => pick("file") },
    { name: "Folder…", icon: "folder", desc: "Blaze gets its file tree", run: () => pick("folder") },
    { name: "CSV report…", icon: "file", desc: "clean a CSV · table, chart, MD/PDF · on this computer", run: () => window.CSVKit && CSVKit.pick() },
    { name: "Voice", icon: "mic", desc: "speak instead of typing · Ctrl+Shift+M", run: () => micClick() },
    { name: "@ File", icon: "at", desc: "or type @ in the box", run: () => { const i = $("#input"); i.value += (i.value && !i.value.endsWith(" ") ? " @" : "@"); i.focus(); i.dispatchEvent(new Event("input")); } },
    { name: MODES.work_mode === "plan" ? "Build mode" : "Plan mode", icon: "plan", desc: (MODES.work_mode === "plan" ? "switch back to every tool (now Plan)" : "read and scout only, then a plan") + " · Ctrl+Tab", run: () => setWorkMode(MODES.work_mode === "plan" ? "build" : "plan") },
    { name: MODES.approval === "autopilot" ? "Auto mode" : "Autopilot", icon: "pilot", desc: (MODES.approval === "autopilot" ? "ask before risky actions again (now Autopilot)" : "work until done, decide instead of asking") + " · Shift+Tab", run: () => setApproval(MODES.approval === "autopilot" ? "auto" : "autopilot") },
    { name: "Model", icon: "model", desc: (($("#routeText") || {}).textContent || "") + (window.CloudModels && CloudModels.on() ? "" : " · Custom"), run: () => { const r = $("#route"); modelPopover($("#plus") || r); } },
    { name: "Views", icon: "views", desc: "Memory, Jobs, Schedule, Accounts, Doctor…", sub: async () => window.BlazeViews ? BlazeViews.items() : [] },
    { name: "Commands", icon: "slash", desc: "type / in the box", sub: async () => COMMANDS.map(c => ({ ...c, slash: true })) },
  ];
}

// User bubble attachments (sent and saved chats).
function attachRow(list) {
  if (!list || !list.length) return null;
  const r = h("div", "chips achips");
  list.forEach(a => r.append(attachChip(a)));
  return r;
}

let toastT = 0;
function toastLine(t) {
  const el = pane && $("#hint", pane); if (!el) return;
  el.textContent = t; clearTimeout(toastT); toastT = setTimeout(hintText, 4000);
  if (el.closest(".hintline")) el.closest(".hintline").classList.add("flash");
}

// ---------- voice ----------
async function micClick() {
  if (rec) { stopRec(false); return; }
  try { STT = await API().STTStatus(); } catch (e) { STT = null; }
  if (!STT || typeof STT !== "object") { voiceCard({ error: "Voice input isn't available in this build." }); return; }
  if (!STT.ready) { voiceCard(STT); return; }
  startRec();
}

// The one-time setup card: what it downloads, with consent and progress.
function voiceCard(st) {
  closeVoice();
  const box = h("div", "vcard");
  box.id = "vcard";
  if (st.error) {
    box.innerHTML = `<div class="vh"><span class="label acc">Voice input</span></div><div class="vt">${esc(st.error)}</div><div class="actions"><button class="btn line sm no">OK</button></div>`;
  } else if (st.engine_how === "manual" && !st.engine) {
    box.innerHTML = `<div class="vh"><span class="label acc">Voice input</span></div><div class="vt">${esc(st.note || "Voice input needs whisper.cpp.")}</div><div class="actions"><button class="btn line sm no">OK</button></div>`;
  } else {
    const m = (st.models || []).find(x => x.name === st.model) || { name: st.model, mb: st.model_mb };
    const parts = [];
    if (!st.engine && st.engine_how === "download") parts.push(`whisper.cpp (about ${st.engine_mb || 10} MB)`);
    if (!st.engine && st.engine_how === "brew") parts.push("whisper.cpp with Homebrew");
    if (!st.have_model) parts.push(`the Whisper <b>${esc(m.name)}</b> model (${m.mb} MB)`);
    box.innerHTML = `<div class="vh"><span class="label acc">Set up voice input</span><span class="label weak">Free · runs on this PC</span></div>
      <div class="vt">Blaze turns speech into text with Whisper, on your own computer. Nothing you say leaves it. It needs a one-time download of ${parts.join(" and ")}.</div>
      <div class="vmodels seg">${(st.models || []).map(x => `<button data-m="${x.name}" class="${x.name === st.model ? "on" : ""}" title="${esc(x.note)}">${esc(x.name)} · ${x.mb} MB</button>`).join("")}</div>
      <div class="vprog" style="display:none"><div class="bar bprog ind"><i></i></div><div class="label weak vpt"></div></div>
      <div class="actions"><button class="btn accent sm yes">Download and set up</button><button class="btn line sm no">Not now</button></div>`;
    $$(".vmodels button", box).forEach(b => b.onclick = async () => { const n = await API().SetSTT(b.dataset.m, ""); STT = n; voiceCard(n); });
    $(".yes", box).onclick = async () => {
      $$("button", box).forEach(b => b.disabled = true);
      $(".vprog", box).style.display = ""; $(".vpt", box).textContent = "Starting…";
      const r = await API().STTSetup();
      if (r === "busy") $(".vpt", box).textContent = "Already downloading…";
    };
  }
  const no = $(".no", box); if (no) no.onclick = () => closeVoice();
  $(".cwrap", pane).prepend(box);
}
function closeVoice() { const v = $("#vcard"); if (v) v.remove(); }

on("stt_progress", p => {
  const box = $("#vcard"); if (!box) return;
  const pr = $(".vprog", box); if (pr) pr.style.display = "";
  BrandProgress.set($(".bar", box), p.pct);
  const t = $(".vpt", box); if (t) t.textContent = p.text || "";
});
on("stt_ready", st => { STT = st; closeVoice(); toastLine("Voice input is ready. Click the mic and speak."); startRec(); });
on("stt_error", msg => {
  const box = $("#vcard"); if (!box) { toastLine(msg); return; }
  $(".vpt", box).textContent = msg; $(".bar", box).classList.add("bad");
  $$("button", box).forEach(b => b.disabled = false);
  $(".yes", box).textContent = "Try again";
});
on("rec_limit", () => { if (rec) stopRec(false); });

async function startRec() {
  if (rec) return;
  // 1) The window's own microphone (WebView2 on Windows, WKWebView on macOS).
  const md = navigator.mediaDevices;
  if (md && md.getUserMedia) {
    try {
      const stream = await md.getUserMedia({ audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true } });
      const AC = window.AudioContext || window.webkitAudioContext;
      const ctx = new AC();
      const src = ctx.createMediaStreamSource(stream);
      const node = ctx.createScriptProcessor(4096, 1, 1);
      const chunks = [];
      node.onaudioprocess = ev => chunks.push(new Float32Array(ev.inputBuffer.getChannelData(0)));
      src.connect(node); node.connect(ctx.destination);
      rec = { kind: "web", stream, ctx, node, src, chunks, rate: ctx.sampleRate, t0: Date.now() };
      recUI(true); return;
    } catch (e) {
      if (e && e.name === "NotAllowedError" && !(STT && STT.native)) { voiceCard({ error: "Blaze isn't allowed to use the microphone. Allow it in your system's privacy settings, then try again." }); return; }
    }
  }
  // 2) A recorder program (WebKitGTK on Linux has no WebView microphone).
  if (STT && STT.native) {
    const err = await API().RecStart();
    if (!err) { rec = { kind: "native", t0: Date.now() }; recUI(true); return; }
    voiceCard({ error: err }); return;
  }
  voiceCard({ error: "No microphone is available to Blaze here. Check that one is connected and allowed. On Linux, installing pipewire (pw-record), pulseaudio-utils or alsa-utils also works." });
}

async function stopRec(cancel) {
  const r = rec; if (!r) return;
  rec = null; recUI(false);
  let t = null;
  if (r.kind === "web") {
    try { r.node.disconnect(); r.src.disconnect(); r.stream.getTracks().forEach(x => x.stop()); r.ctx.close(); } catch (e) {}
    if (cancel) return;
    const secs = (Date.now() - r.t0) / 1000;
    if (secs < 0.4) { toastLine("Hold on a little longer: click the mic, speak, then click again."); return; }
    micBusy(true);
    try { t = await API().Transcribe(wavB64(r.chunks, r.rate)); } catch (e) { t = { error: String(e) }; }
  } else {
    micBusy(!cancel);
    try { t = await API().RecStop(!!cancel); } catch (e) { t = { error: String(e) }; }
    if (cancel) { micBusy(false); return; }
  }
  micBusy(false);
  if (!t) return;
  if (t.error) { toastLine(t.error); return; }
  if (!t.text) { toastLine("Didn't catch any words. Try again a little closer to the mic."); return; }
  insertText(t.text);
}

function insertText(s) {
  const i = $("#input"); if (!i) return;
  const a = i.selectionStart ?? i.value.length, b = i.selectionEnd ?? i.value.length;
  const pre = i.value.slice(0, a), post = i.value.slice(b);
  const sep = pre && !/\s$/.test(pre) ? " " : "";
  i.value = pre + sep + s + post;
  const at = (pre + sep + s).length; i.focus(); i.setSelectionRange(at, at);
  grow();
}

let recTimer = 0;
function recUI(onOff) {
  const m = $("#mic", pane), line = $("#recline", pane); if (!m) return;
  m.classList.toggle("rec", onOff);
  m.title = onOff ? "Stop and transcribe" : "Voice input · Ctrl+Shift+M (local Whisper)";
  line.style.display = onOff ? "" : "none";
  clearInterval(recTimer);
  if (onOff) {
    const t0 = Date.now(), tl = $(".rtime", line);
    const tick = () => { const s = Math.floor((Date.now() - t0) / 1000); tl.textContent = `${Math.floor(s / 60)}:${pad(s % 60)}`; if (s >= 180 && rec) stopRec(false); };
    tick(); recTimer = setInterval(tick, 1000);
  }
}
function micBusy(b) { const m = $("#mic", pane); if (m) { m.classList.toggle("busy", b); m.disabled = b; } if (b) toastLine("Transcribing on this PC…"); else hintText(); }

// Mono float chunks at `rate` → 16 kHz 16-bit WAV, base64.
function wavB64(chunks, rate) {
  let n = 0; chunks.forEach(c => n += c.length);
  const all = new Float32Array(n); let o = 0; chunks.forEach(c => { all.set(c, o); o += c.length; });
  const ratio = rate / 16000, len = Math.floor(all.length / ratio);
  const buf = new ArrayBuffer(44 + len * 2), v = new DataView(buf);
  const str = (p, s) => { for (let i = 0; i < s.length; i++) v.setUint8(p + i, s.charCodeAt(i)); };
  str(0, "RIFF"); v.setUint32(4, 36 + len * 2, true); str(8, "WAVE"); str(12, "fmt "); v.setUint32(16, 16, true);
  v.setUint16(20, 1, true); v.setUint16(22, 1, true); v.setUint32(24, 16000, true); v.setUint32(28, 32000, true);
  v.setUint16(32, 2, true); v.setUint16(34, 16, true); str(36, "data"); v.setUint32(40, len * 2, true);
  for (let i = 0; i < len; i++) {
    const a = Math.floor(i * ratio), b = Math.min(all.length, Math.floor((i + 1) * ratio));
    let s = 0; for (let j = a; j < b; j++) s += all[j];
    const x = Math.max(-1, Math.min(1, s / Math.max(1, b - a)));
    v.setInt16(44 + i * 2, x < 0 ? x * 0x8000 : x * 0x7fff, true);
  }
  const bytes = new Uint8Array(buf); let bin = "";
  for (let i = 0; i < bytes.length; i += 0x8000) bin += String.fromCharCode.apply(null, bytes.subarray(i, i + 0x8000));
  return btoa(bin);
}

// ---------- events from Go ----------
on("modes", m => applyModes(m));
on("autoreply", () => {
  // Autopilot answered the agent's question: close this answer, note it, and keep streaming.
  if (typeof current === "undefined" || !current) return;
  thinking(false);
  renderAnswer(current, true);
  const col = $("#log .col", pane);
  col.append(apNote());
  current = blazeMsg(); col.append(current.el);
  thinking(true, "Deciding and continuing");
  scrollDown();
});
function apNote() { return h("div", "apnote", `<span class="label acc">Autopilot</span><span>You're away, so Blaze decides by best practice and keeps going.</span>`); }
on("ap_blocked", b => {
  if (!current) return;
  current.el.append(h("div", "apnote bad", `<span class="label ink">Blocked</span><span>${esc(b.title)} <span class="mono">${esc((b.detail || "").slice(0, 120))}</span>: ${esc(b.why)}.</span>`));
});
