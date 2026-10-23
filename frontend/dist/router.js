// BlazeRouter in the UI: the top-bar button (icon + status dot), the in-app dashboard
// view (show("router", page)), the Settings card that replaced "Models", and the
// presets at the top of the model popover and /model. The old Models and Providers &
// keys editors stay reachable behind "Classic editor".
const Router = (() => {
  const ICON = '<svg class="i" viewBox="0 0 16 16"><circle cx="3" cy="8" r="1.5"/><circle cx="13" cy="3.5" r="1.5"/><circle cx="13" cy="8" r="1.5"/><circle cx="13" cy="12.5" r="1.5"/><path d="M4.5 8h7M4.5 8 11.5 3.5M4.5 8l7 4.5"/></svg>';
  let RV = null;         // last RouterView from Go
  let classic = false;   // Settings: show the old editors under the card
  const lvl = v => (v && v.level) || "red";
  const note = v => !v ? "Starting…" : !v.enabled ? "Off" : (v.note || (v.running ? "Running" : "Stopped"));

  async function info() { try { RV = await API().RouterInfo(); } catch (e) {} paint(); return RV; }

  function paint() {
    const b = $("#routerBtn"); if (!b) return;
    b.style.display = RV && RV.enabled ? "" : "none"; // v0.19.1: Blaze Cloud answers; the router is opt-in
    b.innerHTML = `${ICON}<span class="bt">Router</span><span class="brdot ${lvl(RV)}"></span>`;
    b.title = "BlazeRouter · " + note(RV) + (RV && RV.url ? " · " + RV.url : "");
    b.classList.toggle("on", typeof view !== "undefined" && view === "router");
    const dot = $(".brview .brbar .brdot"); if (dot) dot.className = "brdot " + lvl(RV);
    const n = $(".brview .brbar .note"); if (n) n.textContent = note(RV);
  }

  function button() {
    let b = $("#routerBtn");
    if (!b) {
      b = h("button", "routerbtn"); b.id = "routerBtn";
      const t = $("#themeBtn"); if (t) t.before(b); else $("#top").append(b);
    }
    b.onclick = () => show("router");
    paint();
  }

  // The view: header (status, on/off, open in browser) and the dashboard.
  async function routerView(el, page) {
    page = page || "";
    const v = await info();
    const wrap = h("div", "brview");
    const bar = h("div", "brbar", `<span class="brdot ${lvl(v)}"></span><span class="label">BlazeRouter</span><span class="note"></span>`);
    $(".note", bar).textContent = note(v);
    const sw = h("button", "brswitch" + (v && v.enabled ? " on" : "")); sw.title = v && v.enabled ? "Turn BlazeRouter off" : "Turn BlazeRouter on";
    sw.onclick = async () => { const err = await API().SetRouterOn(!(RV && RV.enabled)); if (err) toastErr(err); show("router", page); };
    const ob = h("button", "btn ghost xs", `${icon("open", "i sm")}Open in browser`);
    ob.onclick = async () => { const err = await API().OpenRouterDashboard(); if (err) toastErr(err); };
    bar.append(sw, ob);
    wrap.append(bar); el.append(wrap);
    if (v && !v.enabled) { wrap.append(offPanel()); return; }
    const url = await API().RouterDashboardURL(page);
    if (!url) { wrap.append(offPanel("BlazeRouter couldn't start. " + note(RV))); return; }
    const f = h("iframe", "brframe"); f.src = url + (url.includes("#") ? "&" : "#") + "theme=" + (document.documentElement.dataset.theme || "light"); f.title = "BlazeRouter dashboard";
    f.setAttribute("referrerpolicy", "no-referrer");
    wrap.append(f);
    info();
  }

  function offPanel(msg) {
    const o = h("div", "broff", `<div><div class="label acc">BlazeRouter</div><div class="big">${esc(msg || "BlazeRouter is off.")}</div><p>It routes every chat across your free providers, with fallback when one is resting. Your keys stay in Blaze's shared store, so Blaze CLI keeps working.</p></div>`);
    const b = h("button", "btn accent", "Turn on");
    b.onclick = async () => { const err = await API().SetRouterOn(true); if (err) toastErr(err); show("router"); };
    $("div", o).append(b);
    return o;
  }

  function toastErr(m) { if (typeof toast === "function") toast(m, "err"); else console.warn(m); }

  // Settings › BlazeRouter (replaces "Models"; "Providers & keys" moved into it).
  async function settingsCard(col, redraw) {
    const v = await info();
    col.append(h("p", "lead", "BlazeRouter runs on this computer and routes every chat across your free providers, falling back when one is rate-limited. Add keys, build combos and watch usage on its dashboard."));
    const c = h("div", "brcard", `<div class="top"><span class="brdot ${lvl(v)}"></span><span class="t">BlazeRouter</span><span class="grow"></span></div><div class="s"></div><div class="kv"></div><div class="acts"></div>`);
    $(".s", c).textContent = note(v);
    const sw = h("button", "brswitch" + (v && v.enabled ? " on" : "")); sw.title = "On / off";
    sw.onclick = async () => { const err = await API().SetRouterOn(!(RV && RV.enabled)); if (err) toastErr(err); redraw(); };
    $(".top", c).append(sw);
    const kv = $(".kv", c);
    const row = (k, val) => kv.append(h("span", "label", esc(k)), h("span", "mono", esc(val)));
    row("Address", v && v.url ? v.url.replace(/\/$/, "") + "/v1" : "not running");
    row("Model in use", (S && S.model) || "—");
    row("Blaze uses it", v && v.in_use ? "yes" : "no — pick a BlazeRouter preset in the model menu");
    if (v && v.key) row("Local key", v.key.slice(0, 6) + "…" + v.key.slice(-4));
    const acts = $(".acts", c);
    const a = (cls, label, fn) => { const b = h("button", "btn " + cls, label); b.onclick = fn; acts.append(b); };
    a("accent", "Providers & keys", () => show("router", "providers"));
    a("", "Combos", () => show("router", "combos"));
    a("", "Usage", () => show("router", "usage"));
    a("ghost", `${icon("open", "i sm")}Open in browser`, async () => { const err = await API().OpenRouterDashboard(); if (err) toastErr(err); });
    col.append(c);
    const cl = h("div", "brclassic", `<a class="link">${classic ? "Hide the classic editor" : "Classic editor"}</a><span class="sub"> · the old Models and Providers & keys pages</span>`);
    $("a", cl).onclick = () => { classic = !classic; redraw(); };
    col.append(cl);
    if (classic) {
      col.append(h("h2", "brh", "Models"));
      await setModels(col, redraw);
      col.append(h("h2", "brh", "Providers & keys"));
      await setProviders(col, redraw);
    }
  }

  // Model popover: BlazeRouter presets first (the curated ones), then "All presets".
  async function picks() { try { return await API().RouterPicker() || []; } catch (e) { return []; } }
  function pickerTop(body, close, picks) {
    if (!RV || !RV.enabled) return; // off: Blaze Cloud and Custom only
    picks = picks || [];
    const top = picks.filter(p => p.group === "blazerouter" || p.group === "combo");
    const more = picks.filter(p => p.group === "preset");
    if (!top.length) return;
    body.append(h("div", "grp brgrp", "BlazeRouter"));
    const opt = p => {
      const b = h("button", "opt brpick" + (p.current ? " sel" : ""), `<span class="on-mark">${p.current ? icon("check", "i sm") : ""}</span><span class="col2"><span class="ot">${esc(p.name.replace(/^BlazeRouter · /, ""))}</span><span class="od">${esc(p.desc)}</span></span>`);
      b.onclick = async () => { const l = await API().SelectModel(p.provider, p.model); setModel(l); close(); info(); };
      return b;
    };
    top.forEach(p => body.append(opt(p)));
    if (more.length) {
      const list = h("div", "models"); list.style.display = "none";
      const m = h("button", "opt", `<span class="on-mark"></span><span class="col2"><span class="ot">All BlazeRouter presets</span><span class="od">${more.length} more: auto/&lt;category&gt;:&lt;tier&gt; and friends</span></span><span class="chev">${icon("chev", "i sm")}</span>`);
      m.onclick = () => { list.style.display = list.style.display ? "" : "none"; };
      more.forEach(p => list.append(opt(p)));
      body.append(m, list);
    }
    body.append(h("div", "grp brgrp", "Direct"));
  }

  // /model: BlazeRouter's submenu.
  async function menuItems() {
    const picks = await API().RouterPicker() || [];
    return picks.filter(p => p.group !== "provider").map(p => ({ name: p.model, desc: (p.current ? "in use · " : "") + p.desc, run: async () => { setModel(await API().SelectModel(p.provider, p.model)); info(); } }));
  }

  window.VIEWS = window.VIEWS || {};
  window.VIEWS.router = routerView;
  window.SETTINGS_FNS = window.SETTINGS_FNS || {};
  window.SETTINGS_FNS.models = settingsCard;

  // The embedded dashboard follows Blaze's theme, and asks Blaze to open links / save its theme.
  new MutationObserver(() => {
    const f = $(".brview iframe.brframe");
    if (f && f.contentWindow) f.contentWindow.postMessage({ blazeTheme: document.documentElement.dataset.theme }, "*");
  }).observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
  window.addEventListener("message", e => {
    const f = $(".brview iframe.brframe"), d = e.data;
    if (!f || e.source !== f.contentWindow || !d || !d.blazeRouter) return;
    if (d.blazeRouter === "open" && /^https?:\/\//.test(d.url || "")) API().OpenURL(d.url);
    if (d.blazeRouter === "theme" && (d.theme === "light" || d.theme === "dark") && typeof Theme !== "undefined") Theme.set(d.theme);
  });

  const init = () => {
    if (!window.go || !window.runtime) { setTimeout(init, 100); return; }
    button(); info();
    on("router", v => { RV = v; paint(); });
  };
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", init); else init();

  // addModels: where "Add a model" goes. BlazeRouter's Providers page when it is on, else Settings › Models.
  function addModels() { if (RV && RV.enabled) show("router", "providers"); else show("settings", "models"); }

  return { info, paint, addModels, picks, pickerTop, menuItems, get view() { return RV; } };
})();
window.Router = Router;
