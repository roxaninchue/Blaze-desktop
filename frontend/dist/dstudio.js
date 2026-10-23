// v0.20 Design studio, phase 1: the studio layout over the Design tab.
//   mode rail (Page · 3D · Motion · Audio, History, New) | canvas with floating glass
//   toolbars, zoom and the critic's score | the versions drawer | a side panel with
//   Chat · Inspect · Critic · Brand.
// Page mode: every preview is drawn at its real device width and scaled to fit, so two
// versions compared side by side always share one zoom. Select (V) gives eight handles in
// the preview (picker.js); the Inspect tab sets exact position, size, colours (colour
// wheel + hex, colorwheel.js), type size, padding and radius, all written back to the
// source through ApplyEdit. Everything app.js, studio.js and artboard.js already do keeps
// working: this file re-arranges their elements and adds to them, it replaces nothing.
(() => {
  Object.assign(IC, {
    m_page: '<rect x="2.5" y="1.5" width="11" height="13" rx="1.5"/><path d="M5 5h6M5 8h6M5 11h3.5"/>',
    m_3d: '<path d="m8 1.8 5.5 3.1v6.2L8 14.2l-5.5-3.1V4.9z"/><path d="M2.5 4.9 8 8l5.5-3.1M8 8v6.2"/>',
    m_motion: '<path d="M1.5 8c1.6-4 3.4-4 5 0s3.4 4 5 0"/><circle cx="13.2" cy="8" r="1.4"/>',
    m_audio: '<path d="M2 6.5v3M4.75 4.5v7M7.5 2.5v11M10.25 5v6M13 7v2"/>',
    m_history: '<path d="M2.5 8a5.5 5.5 0 1 0 1.6-3.9"/><path d="M2.5 2.5v2.6h2.6M8 5v3.2l2 1.3"/>',
    m_inspect: '<rect x="2" y="2" width="12" height="12" rx="1.5"/><path d="M5 5.5h6M5 8h3.5M5 10.5h5"/>',
    m_move: '<path d="M8 1.5v13M1.5 8h13M8 1.5 6 3.5M8 1.5l2 2M8 14.5l-2-2M8 14.5l2-2M1.5 8l2-2M1.5 8l2 2M14.5 8l-2-2M14.5 8l-2 2"/>',
    m_reset: '<path d="M3 3.5v3h3"/><path d="M3.4 6.5A5 5 0 1 1 3 9"/>',
    m_text: '<path d="M3 3.5h10M8 3.5v9.5M6 13h4"/>',
    m_compare: '<rect x="1.5" y="3" width="5.5" height="10" rx="1"/><rect x="9" y="3" width="5.5" height="10" rx="1"/>',
    m_ask: '<path d="M2.5 3.5h11v7.5H7l-3 2.5V11H2.5z"/>',
  });
  const call = (fn, ...a) => (API()[fn] ? API()[fn](...a) : Promise.resolve(null));
  const note = (t, kind) => (typeof toast === "function" ? toast(t, kind) : toastLine(t));
  const DS = {
    mode: "page",
    tab: localStorage.getItem("blaze.dtab") || "chat",
    zoom: 1,
    cmpView: localStorage.getItem("blaze.cmpview") || "side", // side | slider
    box: null, // the selected element's live box from the preview
  };
  window.DSTUDIO = DS;
  const MODES = [
    { id: "page", label: "Page", key: "1", title: "Pages, posters, screens" },
    { id: "3d", label: "3D", key: "2", title: "3D scenes", soon: true },
    { id: "motion", label: "Motion", key: "3", title: "Animation", soon: true },
    { id: "audio", label: "Audio", key: "4", title: "Sound", soon: true },
  ];
  const TABS = [["chat", "Chat"], ["inspect", "Inspect"], ["critic", "Critic"], ["brand", "Brand"]];
  const DEVW = { desktop: 1280, tablet: 820, phone: 390 };
  const frame = () => $("#dstage .pframe");
  const post = m => { const f = frame(); try { f && f.contentWindow.postMessage(m, "*"); } catch (e) {} };

  // ---------- the shell ----------
  const _designView = designView;
  designView = function (el) {
    _designView(el);
    const wrap = $(".design", el); if (!wrap) return;
    wrap.classList.add("studio");
    const dmain = $(".dmain", wrap), bar = $(".dbar", dmain), stage = $("#dstage", dmain), strip = $("#dstrip", dmain), side = $(".dside", wrap);

    // Mode rail.
    const rail = h("nav", "drail");
    rail.setAttribute("aria-label", "Design modes");
    MODES.forEach(m => {
      const b = h("button", "rm" + (m.soon ? " soon" : ""), `${icon("m_" + m.id, "i")}<span>${m.label}</span>${m.soon ? '<em>Soon</em>' : ""}`);
      b.dataset.m = m.id; b.title = m.soon ? `${m.title}: coming in the next update` : `${m.title} (${m.key})`;
      if (m.soon) b.setAttribute("aria-disabled", "true");
      b.onclick = () => setMode(m.id);
      rail.append(b);
    });
    rail.append(h("span", "rsep"));
    const hist = h("button", "rm", `${icon("m_history", "i")}<span>History</span>`); hist.id = "drhist"; hist.title = "Versions";
    hist.onclick = () => { const t = $("#dvtoggle"); if (t) t.click(); syncRail(); setTimeout(layoutFrames, 40); };
    rail.append(hist, h("span", "rgrow"));
    const nb = $("#dnew", bar);
    if (nb) { nb.className = "rm"; nb.innerHTML = `${icon("plus", "i")}<span>New</span>`; rail.append(nb); }
    wrap.prepend(rail);

    // The canvas: the stage plus floating toolbars over it.
    const center = h("div", "dcenter");
    stage.before(center); center.append(stage);
    bar.classList.add("ftool", "ft-tl");
    center.append(bar);
    const tr = h("div", "ftool ft-tr");
    ["#dphone", "#dship", "#dopen", "#dexport"].forEach(s => { const b = $(s, bar); if (b) tr.append(b); });
    const ex = $("#dexport", tr); if (ex) { ex.classList.remove("ghost"); ex.classList.add("accent"); }
    center.append(tr);
    // Compare lives in the toolbar now (the strip's checkbox stays the source of truth).
    const cmp = h("button", "btn ghost sm", `${icon("m_compare", "i sm")}<span class="btxt">Compare</span>`); cmp.id = "dcmpbtn"; cmp.title = "Compare two versions at the same zoom";
    cmp.onclick = () => { const c = $("#dcmp"); if (!c) return; c.checked = !c.checked; c.onchange(); if (c.checked && !stripOpen) $("#drhist").click(); syncBar(); };
    const dirs = $("#ddirs", bar); if (dirs) dirs.after(cmp); else bar.append(cmp);
    const score = $("#dscore", bar); if (score) { const bl = h("div", "ftool ft-bl"); bl.append(score); center.append(bl); }
    const zoom = h("div", "ftool ft-br dzoom", `<button class="zb zo" title="Zoom out (Ctrl −)">−</button><button class="zl" title="Fit (Ctrl 0)">100%</button><button class="zb zi" title="Zoom in (Ctrl +)">+</button>`);
    zoom.id = "dzoom";
    $(".zo", zoom).onclick = () => setZoom(DS.zoom / 1.2);
    $(".zi", zoom).onclick = () => setZoom(DS.zoom * 1.2);
    $(".zl", zoom).onclick = () => setZoom(1);
    center.append(zoom);
    center.addEventListener("wheel", e => { if (!(e.ctrlKey || e.metaKey) || !$(".frames.zoomed", center)) return; e.preventDefault(); setZoom(DS.zoom * (1 - Math.max(-0.5, Math.min(0.5, e.deltaY * 0.01)))); }, { passive: false });
    panCanvas(center);
    strip.classList.add("ddrawer");
    // A compare-view switch in the drawer head: side by side (default) or the slider.
    const head = $(".dstriphead", strip);
    if (head) {
      const cv = h("div", "seg cvseg", `<button data-v="side" title="Side by side, same zoom">Side by side</button><button data-v="slider" title="One frame, drag to reveal">Slider</button>`);
      cv.querySelectorAll("button").forEach(b => b.onclick = () => { DS.cmpView = b.dataset.v; localStorage.setItem("blaze.cmpview", DS.cmpView); syncBar(); drawStage(); });
      const t = $("#dcmp", head); if (t) t.closest("label").after(cv); else head.append(cv);
    }

    // Side panel: Chat · Inspect · Critic · Brand.
    const kids = [...side.childNodes];
    const tabs = h("div", "dtabs", `<div class="seg" role="tablist">${TABS.map(([k, l]) => `<button role="tab" data-p="${k}">${l}</button>`).join("")}</div>`);
    const panes = h("div", "dpanes");
    TABS.forEach(([k]) => { const p = h("div", "dpane p-" + k); p.dataset.p = k; panes.append(p); });
    kids.forEach(n => $(".p-chat", panes).append(n));
    side.append(tabs, panes);
    tabs.querySelectorAll("button").forEach(b => b.onclick = () => setTab(b.dataset.p));

    setTab(DS.tab, true);
    syncRail(); syncBar();
    call("Design").then(d => { if (d && d.mode && d.mode !== DS.mode) setMode(d.mode, true); });
  };

  // Drag empty canvas (not the frames) to pan when zoomed in.
  function panCanvas(center) {
    let s = null;
    center.addEventListener("pointerdown", e => {
      const fr = e.target.closest(".frames.zoomed"); if (!fr || e.target.closest(".fbox") || e.button !== 0) return;
      s = { x: e.clientX, y: e.clientY, l: fr.scrollLeft, t: fr.scrollTop, fr }; fr.classList.add("grabbing");
      try { fr.setPointerCapture(e.pointerId); } catch (x) {}
    });
    center.addEventListener("pointermove", e => { if (!s) return; s.fr.scrollLeft = s.l - (e.clientX - s.x); s.fr.scrollTop = s.t - (e.clientY - s.y); });
    const end = () => { if (s) s.fr.classList.remove("grabbing"); s = null; };
    center.addEventListener("pointerup", end); center.addEventListener("pointercancel", end);
  }

  function setMode(m, quiet) {
    const def = MODES.find(x => x.id === m) || MODES[0];
    if (def.soon) { if (!quiet) note(`${def.label} mode is coming in the next update. Page mode is ready now.`); return; }
    DS.mode = def.id;
    syncRail();
    if (!quiet) call("SetStudioMode", def.id);
  }
  function syncRail() {
    $$(".drail [data-m]").forEach(b => b.classList.toggle("on", b.dataset.m === DS.mode));
    const hb = $("#drhist"); if (hb) hb.classList.toggle("on", !!stripOpen);
  }
  function syncBar() {
    const c = $("#dcmp"), on = !!(c && c.checked);
    const b = $("#dcmpbtn"); if (b) b.classList.toggle("on", on);
    $$(".cvseg button").forEach(x => x.classList.toggle("on", x.dataset.v === DS.cmpView));
    const seg = $(".cvseg"); if (seg) seg.style.display = on ? "" : "none";
  }
  function setTab(k, quiet) {
    if (!TABS.some(t => t[0] === k)) k = "chat";
    DS.tab = k; localStorage.setItem("blaze.dtab", k);
    $$(".dtabs [data-p]").forEach(b => { b.classList.toggle("on", b.dataset.p === k); b.setAttribute("aria-selected", b.dataset.p === k ? "true" : "false"); });
    $$(".dpanes .dpane").forEach(p => p.classList.toggle("on", p.dataset.p === k));
    if (k === "inspect") drawInspect();
    if (k === "critic") drawCritic();
    if (k === "brand") drawBrand();
    if (k === "chat" && !quiet) { const i = $("#input"); if (i) i.focus(); }
  }
  DS.setTab = setTab;

  // ---------- page: real device widths, one shared zoom ----------
  function setZoom(z) { DS.zoom = Math.max(0.25, Math.min(4, z)); layoutFrames(); }
  function layoutFrames() {
    const st = $("#dstage"), zl = $("#dzoom"); if (!st) return;
    const frames = $(".frames", st);
    const usable = frames && !st.classList.contains("artboard") && !$(".fbox.slider", frames) && $$(".fbox", frames).length;
    if (zl) zl.style.display = usable ? "" : "none";
    if (!usable) return;
    frames.classList.add("zoomed");
    let sizer = $(".zsizer", frames), inner = $(".zinner", frames);
    if (!inner) {
      sizer = h("div", "zsizer"); inner = h("div", "zinner");
      $$(":scope > .fbox", frames).forEach(b => inner.append(b));
      sizer.append(inner); frames.append(sizer);
    }
    const boxes = $$(".fbox", inner), n = boxes.length, gap = 40, w = DEVW[device] || DEVW.desktop;
    const tw = n * w + (n - 1) * gap;
    // Clear of the floating toolbar at the top (it wraps on narrow windows), and of the zoom
    // and the score at the bottom.
    const tl = $(".dcenter .ft-tl"), top = Math.max(64, (tl ? tl.offsetTop + tl.offsetHeight : 52) + 14), bottom = 56;
    sizer.style.marginTop = top + "px"; sizer.style.marginBottom = bottom + "px";
    const availW = Math.max(120, frames.clientWidth - 56), availH = Math.max(120, frames.clientHeight - top - bottom);
    const fit = Math.min(1, availW / tw);
    const s = fit * DS.zoom;
    const th = Math.max(320, Math.round(availH / fit)); // the frame fills the canvas at fit
    boxes.forEach(b => { b.style.width = w + "px"; b.style.height = th + "px"; b.style.flex = "none"; b.style.maxWidth = "none"; });
    inner.style.width = tw + "px"; inner.style.gap = gap + "px"; inner.style.transform = `scale(${s})`; inner.style.setProperty("--inv", (1 / s).toFixed(3));
    sizer.style.width = Math.ceil(tw * s) + "px"; sizer.style.height = Math.ceil(th * s) + "px";
    frames.classList.toggle("grab", DS.zoom > 1.001);
    DS.scale = s;
    if (zl) $(".zl", zl).textContent = Math.round(s * 100) + "%";
  }
  DS.layout = layoutFrames;
  const _drawStage = drawStage;
  drawStage = function () {
    _drawStage();
    layoutFrames();
    syncBar();
    // Select mode in the studio means handles: tell the frame once it has loaded.
    const f = frame(); if (f && selectOn) f.addEventListener("load", () => setTimeout(() => post({ blaze: "mode", on: selectOn && compare.length !== 2 }), 30), { once: true });
  };
  let ro = null;
  setInterval(() => {
    const st = $("#dstage .frames.zoomed");
    if (!st || (ro && ro.el === st) || typeof ResizeObserver === "undefined") return;
    if (ro) ro.disconnect();
    ro = new ResizeObserver(() => { clearTimeout(ro.t); ro.t = setTimeout(layoutFrames, 50); }); ro.el = st; ro.observe(st);
  }, 800);

  // ---------- selection → Inspect ----------
  const _drawSelBar = drawSelBar;
  drawSelBar = function () {
    _drawSelBar();
    if (!selection) DS.box = null;
    else { const one = selection.items[selection.items.length - 1]; DS.box = one && one.box ? { ...one.box } : DS.box; }
    if (DS.tab === "inspect") drawInspect();
  };
  addEventListener("message", e => {
    const m = e.data || {};
    if (m.blaze === "selected" && view === "design") {
      // First pick while chatting with an empty composer: show the element's controls.
      const i = $("#input");
      if (m.items && m.items.length && DS.tab === "chat" && (!i || !i.value.trim())) setTab("inspect");
    }
    if (m.blaze === "box" && m.box && view === "design") { DS.box = m.box; syncBoxFields(); }
  });

  function selected() { return selection && selection.items && selection.items.length ? selection.items[selection.items.length - 1] : null; }
  function label(it) {
    if (!it) return "";
    if (it.part) return "3D part · " + (it.part.label || it.part.id);
    const c = it.target && it.target.classes && it.target.classes[0];
    return "<" + it.tag + ">" + (c ? "." + c : "");
  }
  function writeStyle(it, props) {
    if (!it || !it.target) return;
    window.postMessage({ blaze: "edit", edit: { kind: "style", target: it.target, props }, html: it.html, selector: it.selector }, "*");
  }
  function numRow(lab, key, unit) {
    const r = h("label", "inum", `<span>${lab}</span><input type="number" step="1" data-k="${key}" aria-label="${lab}"><em>${unit || ""}</em>`);
    const inp = $("input", r);
    inp.onkeydown = e => { e.stopPropagation(); if (e.key === "Enter") { e.preventDefault(); inp.blur(); } if (e.key === "ArrowUp" || e.key === "ArrowDown") { e.preventDefault(); inp.value = (+inp.value || 0) + (e.key === "ArrowUp" ? 1 : -1) * (e.shiftKey ? 10 : 1); commitBox(key, inp.value); } };
    inp.onchange = () => commitBox(key, inp.value);
    return r;
  }
  function commitBox(k, v) {
    if (v === "" || isNaN(+v)) { syncBoxFields(); return; }
    if (viewing) { note("You're viewing an old version. Go back to live to edit."); syncBoxFields(); return; }
    post({ blaze: "setbox", box: { [k]: Math.round(+v) } });
  }
  function syncBoxFields() {
    const b = DS.box; if (!b) return;
    $$(".p-inspect input[data-k]").forEach(i => { if (document.activeElement !== i && b[i.dataset.k] !== undefined) i.value = b[i.dataset.k]; });
  }
  function sliderRow(lab, prop, min, max, val, it) {
    const r = h("label", "isl", `<span>${lab}</span><input type="range" min="${min}" max="${max}" value="${val}" aria-label="${lab}"><input type="number" class="iv" min="${min}" max="${max}" value="${val}" aria-label="${lab} value"><em>px</em>`);
    const rng = $("input[type=range]", r), num = $(".iv", r);
    const live = v => { rng.value = v; num.value = v; post({ blaze: "style", prop, value: v + "px" }); };
    const commit = v => writeStyle(it, { [prop]: Math.round(+v) + "px" });
    rng.oninput = () => live(rng.value); rng.onchange = () => commit(rng.value);
    num.onkeydown = e => { e.stopPropagation(); if (e.key === "Enter") { e.preventDefault(); num.blur(); } };
    num.onchange = () => { live(num.value); commit(num.value); };
    return r;
  }
  function drawInspect() {
    const p = $(".p-inspect"); if (!p) return;
    p.innerHTML = "";
    const it = selected();
    if (!it) {
      p.append(h("div", "iempty", `<div class="ie-ico">${icon("m_move", "i")}</div><div class="ie-t">Nothing selected</div><p>Press <span class="kbd">V</span> or <b>Select</b>, then click anything in the preview. Drag it to move, pull a handle to resize, or set exact values here. Every change is saved to the source as a version.</p>`));
      const b = h("button", "btn line sm", `${icon("cursor", "i sm")}Start selecting`);
      b.onclick = () => { if (!selectOn) $("#dselect").click(); };
      p.append(b);
      return;
    }
    const st = it.styles || {}, many = selection.items.length > 1;
    const head = h("div", "ihead", `<span class="ilab">${esc(many ? selection.items.length + " elements" : label(it))}</span><span class="itext">${esc((it.text || "").slice(0, 80))}</span>`);
    p.append(head);
    if (it.part) { p.append(h("p", "inote", "3D parts are adjusted from the bar under the preview: colour, scale and turn, with arrow keys to move them. A full 3D mode is coming next.")); return; }
    if (!many) {
      const g = h("div", "igrp", `<div class="igh">Position and size</div>`);
      const grid = h("div", "igrid");
      grid.append(numRow("X", "x", "px"), numRow("Y", "y", "px"), numRow("W", "w", "px"), numRow("H", "h", "px"));
      g.append(grid);
      const acts = h("div", "irow");
      const rs = h("button", "btn ghost xs", `${icon("m_reset", "i sm")}Reset position`);
      rs.onclick = () => { post({ blaze: "setbox", box: { x: 0, y: 0 } }); };
      acts.append(rs, h("span", "ihint", "Arrows nudge · Shift ×10"));
      g.append(acts);
      p.append(g);
      DS.box = DS.box || it.box || null; syncBoxFields();
    }
    const g2 = h("div", "igrp", `<div class="igh">Colour</div>`);
    const bg = st.background && !/rgba\(0, 0, 0, 0\)|transparent/.test(st.background) ? st.background : "";
    [["Text", "color", st.color], ["Fill", "background-color", bg || "rgba(255,255,255,0)"]].forEach(([lab, prop, v]) => {
      const f = BlazeColor.field({
        label: lab, value: BlazeColor.util.css2hex(v || "#000000"), alpha: true,
        onChange: c => post({ blaze: "style", prop, value: c }),
        onCommit: c => { if (viewing) { note("You're viewing an old version. Go back to live to edit."); return; } selection.items.forEach(x => writeStyle(x, { [prop]: c })); },
      });
      g2.append(f.el);
    });
    p.append(g2);
    const g3 = h("div", "igrp", `<div class="igh">Type and shape</div>`);
    g3.append(
      sliderRow("Size", "font-size", 8, 120, parseInt(st.fontSize) || 16, it),
      sliderRow("Padding", "padding", 0, 120, parseInt(st.padding) || 0, it),
      sliderRow("Radius", "border-radius", 0, 64, parseInt(st.borderRadius) || 0, it),
    );
    p.append(g3);
    const g4 = h("div", "igrp iacts");
    const et = h("button", "btn line sm", `${icon("m_text", "i sm")}Edit text`); et.onclick = () => post({ blaze: "retype" });
    if (!it.leaf || !(it.text || "").trim() || many) et.disabled = true;
    const ask = h("button", "btn line sm", `${icon("m_ask", "i sm")}Ask Blaze`);
    ask.onclick = () => { setTab("chat"); const b = $(".selbar .sbacts [data-a=Ask]"); if (b) b.click(); };
    g4.append(et, ask);
    p.append(g4);
  }

  // ---------- Critic and Brand tabs (studio.js draws the content) ----------
  function drawCritic() {
    const p = $(".p-critic"); if (!p) return;
    p.innerHTML = "";
    const S2 = window.STUDIO;
    if (S2 && S2.criticInto && S2.criticInto(p)) return;
    p.append(h("div", "iempty", `<div class="ie-t">No score yet</div><p>After each change the critic checks the page at phone, tablet and desktop widths for contrast, alt text, tap targets, overflow and weight, then scores it here.</p>`));
  }
  async function drawBrand() {
    const p = $(".p-brand"); if (!p) return;
    p.innerHTML = "";
    const S2 = window.STUDIO;
    const host = h("div", "bhost"); p.append(host);
    if (S2 && S2.brandInto) await S2.brandInto(host);
    // Edit the kit's colours with the wheel.
    const b = (await call("BrandKit")) || {};
    const g = h("div", "igrp bcols", `<div class="igh">Brand colours</div>`);
    const save = async patch => { const cur = (await call("BrandKit")) || {}; Object.assign(cur, patch); await call("SetBrand", cur); };
    [["Ink", "ink"], ["Paper", "paper"]].forEach(([lab, k]) => {
      g.append(BlazeColor.field({ label: lab, value: b[k] || (k === "ink" ? "#111112" : "#ffffff"), alpha: false, onCommit: c => save({ [k]: c }) }).el);
    });
    const acc = (b.accents || []).slice(0, 6);
    acc.forEach((c, i) => g.append(BlazeColor.field({ label: "Accent " + (i + 1), value: c, alpha: false, onCommit: async v => { const cur = (await call("BrandKit")) || {}; const a = (cur.accents || []).slice(); a[i] = v; save({ accents: a }); } }).el));
    if (acc.length < 6) {
      const add = h("button", "btn ghost xs", `${icon("plus", "i sm")}Add accent`);
      add.onclick = async () => { const cur = (await call("BrandKit")) || {}; await save({ accents: (cur.accents || []).concat("#4da8ff") }); drawBrand(); };
      g.append(add);
    }
    p.append(g);
  }
  DS.refreshTab = () => { if (DS.tab === "critic") drawCritic(); if (DS.tab === "inspect") drawInspect(); };

  // Re-draw the Critic tab when the score changes.
  const _drawVersions = drawVersions;
  drawVersions = function () { _drawVersions(); if (DS.tab === "critic" && view === "design") drawCritic(); syncRail(); };

  // ---------- keys ----------
  addEventListener("keydown", e => {
    if (view !== "design") return;
    const typing = /^(INPUT|TEXTAREA|SELECT)$/.test((document.activeElement || {}).tagName || "") || (document.activeElement && document.activeElement.isContentEditable);
    if ((e.ctrlKey || e.metaKey) && !e.altKey && (e.key === "=" || e.key === "+" || e.key === "-" || e.key === "0") && $(".frames.zoomed")) {
      e.preventDefault(); setZoom(e.key === "0" ? 1 : e.key === "-" ? DS.zoom / 1.2 : DS.zoom * 1.2); return;
    }
    if (typing || e.ctrlKey || e.metaKey || e.altKey) return;
    if (e.key === "v" || e.key === "V") { const b = $("#dselect"); if (b) { e.preventDefault(); b.click(); } return; }
    const m = MODES.find(x => x.key === e.key); if (m) { e.preventDefault(); setMode(m.id); return; }
    if (selection && selectOn && e.key.startsWith("Arrow")) {
      e.preventDefault();
      const s = e.shiftKey ? 10 : 1;
      post({ blaze: "nudge", dx: e.key === "ArrowLeft" ? -s : e.key === "ArrowRight" ? s : 0, dy: e.key === "ArrowUp" ? -s : e.key === "ArrowDown" ? s : 0 });
    }
  });

  // ---------- every colour input in Design gets the wheel ----------
  let mo = null;
  const upgrade = () => { if (view !== "design" || !window.BlazeColor) return; const d = $(".design"); if (d) BlazeColor.upgradeAll(d); $$(".pop").forEach(p => BlazeColor.upgradeAll(p)); };
  if (typeof MutationObserver !== "undefined") {
    mo = new MutationObserver(() => { clearTimeout(mo.t); mo.t = setTimeout(upgrade, 30); });
    mo.observe(document.body, { childList: true, subtree: true });
  }
})();
