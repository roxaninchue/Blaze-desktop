// v0.8.0 Design studio: the Design tab's new tools, layered over app.js.
//  1 edits that write back · 2 Directions as live branches · 3 taste + brand kit
//  4 copy any window · 5 the critic · 6 talk while you point · 7 states board
//  8 visual version history · 9 preview on your phone · 10 one-click ship
(function () {
  Object.assign(IC, {
    grid: '<rect x="2" y="2" width="5" height="5"/><rect x="9" y="2" width="5" height="5"/><rect x="2" y="9" width="5" height="5"/><rect x="9" y="9" width="5" height="5"/>',
    qr: '<rect x="2" y="2" width="4.5" height="4.5"/><rect x="9.5" y="2" width="4.5" height="4.5"/><rect x="2" y="9.5" width="4.5" height="4.5"/><path d="M9.5 9.5h2v2M14 9.5v4.5h-4.5M12 12v2"/>',
    rocket: '<path d="M9.5 2.5c2.5 0 4 1.5 4 4L8 12 4 8z"/><path d="M5 9.5 2.5 12M6.5 11 4 13.5M4 8l-1.5-.5L5 5h2.5M8 12l.5 1.5L11 11V8.5"/>',
    drop: '<path d="M8 2s4.5 4.6 4.5 7.5a4.5 4.5 0 0 1-9 0C3.5 6.6 8 2 8 2z"/>',
    window: '<rect x="1.5" y="2.5" width="13" height="11"/><path d="M1.5 5.5h13M4 4h.5M6 4h.5"/>',
    mic: '<rect x="6" y="1.5" width="4" height="8" rx="2"/><path d="M3.5 7.5a4.5 4.5 0 0 0 9 0M8 12v2.5"/>',
    branch: '<circle cx="4" cy="3.5" r="1.5"/><circle cx="4" cy="12.5" r="1.5"/><circle cx="12" cy="6" r="1.5"/><path d="M4 5v6M12 7.5c0 2.5-8 1.5-8 3.5"/>',
    undo: '<path d="M5.5 3 2.5 6l3 3"/><path d="M2.5 6h7a4 4 0 0 1 0 8H7"/>',
    pin: '<path d="M8 14.5s-4.5-4.4-4.5-8a4.5 4.5 0 0 1 9 0c0 3.6-4.5 8-4.5 8z"/><circle cx="8" cy="6.5" r="1.5"/>',
    text: '<path d="M3 3.5h10M8 3.5v9.5M6 13h4"/>',
  });
  const S2 = { mode: "", dirs: [], cards: {}, audits: {}, critic: true, slider: 50, timeline: localStorage.getItem("blaze.timeline") === "1" };
  window.STUDIO = S2;
  const call = (fn, ...a) => (API()[fn] ? API()[fn](...a) : Promise.resolve(null));
  const note = (t, kind) => (typeof toast === "function" ? toast(t, kind) : toastLine(t));

  // ---------- toolbar ----------
  const _designView = designView;
  designView = function (el) {
    _designView(el);
    const bar = $(".dbar", el); if (!bar) return;
    const anchor = $("#dselect", bar);
    const mk = (id, ic, label, title) => { const b = h("button", "btn ghost sm", `${icon(ic, "i sm")}<span class="btxt">${label}</span>`); b.id = id; b.title = title; b.dataset.pop = "1"; return b; };
    const states = mk("dstates", "grid", "States", "States board: hover, focus, empty, loading, error and dark at once");
    const copy = mk("dcopy", "window", "Copy", "Rebuild any window or screenshot as code");
    const brand = mk("dbrand", "drop", "Brand", "Brand kit and taste");
    const phone = mk("dphone", "qr", "Phone", "Preview on your phone");
    const ship = mk("dship", "rocket", "Ship", "Publish to Cloudflare Pages or GitHub Pages");
    const score = h("button", "btn ghost sm crit", ""); score.id = "dscore"; score.title = "Design critic"; score.dataset.pop = "1"; score.style.display = "none";
    bar.insertBefore(score, anchor);
    [states, copy, brand].forEach(b => bar.insertBefore(b, $("#ddirs", bar)));
    bar.insertBefore(phone, $("#dopen", bar)); bar.insertBefore(ship, $("#dopen", bar));
    states.onclick = () => { S2.mode = S2.mode === "states" ? "" : "states"; drawStage(); };
    copy.onclick = e => { e.stopPropagation(); copyPop(copy); };
    brand.onclick = e => { e.stopPropagation(); brandPop(brand); };
    phone.onclick = e => { e.stopPropagation(); phonePop(phone); };
    ship.onclick = e => { e.stopPropagation(); shipPop(ship); };
    score.onclick = e => { e.stopPropagation(); criticPop(score); };
    $("#ddirs", bar).dataset.pop = "1";
    $("#ddirs", bar).onclick = e => { e.stopPropagation(); directionsPop($("#ddirs")); };
    // Timeline toggle in the versions strip.
    const head = $(".dstriphead", el);
    if (head) {
      const tl = h("label", "toggle", `<input type="checkbox" id="dtl"${S2.timeline ? " checked" : ""}><span class="label">Timeline</span>`);
      head.insertBefore(tl, $("#dcmp", head).closest("label"));
      $("input", tl).onchange = e => { S2.timeline = e.target.checked; localStorage.setItem("blaze.timeline", S2.timeline ? "1" : "0"); drawVersions(); };
    }
    // Drop a screenshot anywhere on the stage to copy it.
    const st = $("#dstage", el);
    st.addEventListener("dragover", e => { if ([...e.dataTransfer.items].some(i => i.type.startsWith("image/"))) { e.preventDefault(); st.classList.add("dropok"); } });
    st.addEventListener("dragleave", () => st.classList.remove("dropok"));
    st.addEventListener("drop", e => { const f = [...e.dataTransfer.files].find(f => f.type.startsWith("image/")); st.classList.remove("dropok"); if (!f) return; e.preventDefault(); copyFile(f, ""); });
    refreshDirs();
  };

  // ---------- the stage: states board, directions board, before/after slider ----------
  const _drawStage = drawStage;
  drawStage = function () {
    $$("#dstates").forEach(b => b.classList.toggle("on", S2.mode === "states"));
    const live = S2.dirs.length && S2.mode !== "states" && S2.mode !== "live";
    if (live) return drawDirections();
    if (S2.mode === "states" && D && D.target && D.target.kind) return drawStates();
    _drawStage();
    // The studio (dstudio.js) compares side by side at one zoom unless the slider is chosen.
    if (compare.length === 2 && D && D.target && D.target.kind === "file" && (!window.DSTUDIO || DSTUDIO.cmpView === "slider")) drawSlider();
    drawScore();
  };

  function stateURL(st) { return frameURL(viewing) + "&__state=" + st; }
  function drawStates() {
    const st = $("#dstage"); if (!st) return;
    st.innerHTML = "";
    const grid = h("div", "sgrid " + device);
    [["default", "Default"], ["hover", "Hover"], ["focus", "Focus"], ["active", "Pressed"], ["empty", "Empty"], ["loading", "Loading"], ["error", "Error"], ["dark", "Dark mode"]].forEach(([k, l]) => {
      const cell = h("div", "scell", `<div class="label">${l}</div>`);
      const f = h("iframe", "sframe"); f.src = k === "default" ? frameURL(viewing) : stateURL(k); f.tabIndex = -1; f.loading = "lazy";
      cell.append(f); grid.append(cell);
    });
    const head = h("div", "viewing", `<span class="label acc">States board</span><span class="sub">Every state at once, so nothing ships half-designed. States the page doesn't design get a stand-in.</span><span class="grow"></span>`);
    const ask = h("button", "btn line xs", "Design missing states"); ask.onclick = () => { S2.mode = ""; drawStage(); sendText(`Design the missing UI states for ${D.target.path || D.target.url}: hover, focus (visible ring), pressed, empty (lists/grids with nothing in them), loading (skeletons), error (form fields and messages) and dark mode (prefers-color-scheme). Keep the current look; add only what's missing.`, []); };
    const x = h("button", "btn ghost xs", "Close"); x.onclick = () => { S2.mode = ""; drawStage(); };
    head.append(ask, x);
    st.append(head, grid);
  }

  function drawSlider() {
    const frames = $("#dstage .frames"); if (!frames) return;
    const [a, b] = compare;
    const box = h("div", "fbox slider " + device);
    const fa = h("iframe", "pframe"); fa.src = frameURL(a);
    const fb = h("iframe", "pframe top"); fb.src = frameURL(b);
    const handle = h("div", "shandle", `<span class="label">${a ? "v" + pad(a) : "Live"}</span><span class="knob"></span><span class="label">${b ? "v" + pad(b) : "Live"}</span>`);
    const set = v => { S2.slider = Math.max(0, Math.min(100, v)); fb.style.clipPath = `inset(0 0 0 ${S2.slider}%)`; handle.style.left = S2.slider + "%"; };
    handle.onpointerdown = e => {
      e.preventDefault(); [fa, fb].forEach(f => f.style.pointerEvents = "none");
      const r = box.getBoundingClientRect();
      const mv = ev => set((ev.clientX - r.left) / r.width * 100);
      const up = () => { removeEventListener("pointermove", mv); removeEventListener("pointerup", up); [fa, fb].forEach(f => f.style.pointerEvents = ""); };
      addEventListener("pointermove", mv); addEventListener("pointerup", up);
    };
    box.append(fa, fb, handle); set(S2.slider);
    frames.innerHTML = ""; frames.append(box);
  }

  // ---------- versions: score badges and the branching timeline ----------
  const _drawVersions = drawVersions;
  drawVersions = function () {
    if (S2.timeline && D && (D.versions || []).length) return drawTimeline();
    const bx = $("#dversions"); if (bx) bx.classList.remove("tlmode");
    _drawVersions();
    const vs = (D && D.versions || []).slice().reverse();
    $$("#dversions .vtile").forEach((t, i) => {
      const v = vs[i]; if (!v) return;
      const c = v.card || S2.cards[v.n];
      if (c && c.score) t.querySelector(".vthumb").append(h("span", "vscore " + grade(c.score), String(c.score)));
      if (v.kind && v.kind !== "chat") t.querySelector(".vcap").prepend(h("span", "vkind", v.kind));
      if (v.n === D.head) t.classList.add("head");
    });
  };
  const grade = s => s >= 90 ? "good" : s >= 75 ? "ok" : "bad";

  function drawTimeline() {
    const box = $("#dversions"); if (!box) return;
    box.innerHTML = ""; box.classList.add("tlmode");
    const vs = (D.versions || []).map((v, i, all) => Object.assign({}, v, { parent: v.parent || (i ? all[i - 1].n : 0) }));
    const by = {}; vs.forEach(v => by[v.n] = v);
    // Lanes: a version continues its parent's lane when it's the parent's first child.
    const lane = {}, used = {}; let lanes = 0;
    vs.forEach(v => {
      const p = by[v.parent];
      if (p && !used[p.n]) { lane[v.n] = lane[p.n]; used[p.n] = true; }
      else { lane[v.n] = p ? ++lanes : (lanes ? ++lanes : 0); if (p) used[p.n] = true; }
    });
    const X = 46, Y = 22, W = Math.max(1, vs.length) * X + 40, H = (lanes + 1) * Y + 24;
    const xs = {}; vs.forEach((v, i) => xs[v.n] = 24 + i * X);
    let svg = `<svg class="tl" width="${W}" height="${H}" viewBox="0 0 ${W} ${H}">`;
    vs.forEach(v => { const p = by[v.parent]; if (p) svg += `<path d="M${xs[p.n]} ${14 + lane[p.n] * Y} C ${xs[p.n] + X / 2} ${14 + lane[p.n] * Y}, ${xs[v.n] - X / 2} ${14 + lane[v.n] * Y}, ${xs[v.n]} ${14 + lane[v.n] * Y}"/>`; });
    vs.forEach(v => {
      const c = v.card || S2.cards[v.n];
      const cls = ["n", v.n === D.head ? "head" : "", viewing === v.n ? "on" : "", compare.includes(v.n) ? "on" : "", c && c.score ? grade(c.score) : ""].join(" ");
      svg += `<g class="${cls}" data-n="${v.n}" transform="translate(${xs[v.n]} ${14 + lane[v.n] * Y})"><title>v${pad(v.n)} · ${esc(v.summary || "")}${c && c.score ? " · score " + c.score : ""}</title><rect x="-13" y="-9" width="26" height="18"/><text y="4">${pad(v.n)}</text></g>`;
    });
    svg += `</svg>`;
    const wrap = h("div", "tlwrap", svg);
    const cap = h("div", "tlcap", `<span class="label weak">Click to view · Compare on: pick two for the before/after slider · Restoring an old version starts a new branch</span>`);
    box.append(wrap, cap);
    $$("g.n", wrap).forEach(g => g.onclick = () => {
      const n = +g.dataset.n;
      if ($("#dcmp").checked) compare = compare.includes(n) ? compare.filter(x => x !== n) : compare.concat(n).slice(-2);
      else viewing = D.target.kind === "url" ? 0 : n;
      drawVersions(); drawStage();
    });
    wrap.scrollLeft = wrap.scrollWidth;
  }

  // ---------- selection bar: write-back, retype, swap colour, voice, restore one element ----------
  const _drawSelBar = drawSelBar;
  drawSelBar = function () {
    _drawSelBar();
    const bar = $("#dstage .selbar"); if (!bar || !selection) return;
    const items = selection.items, one = items[items.length - 1];
    const frame = $("#dstage .pframe");
    const post = m => { try { frame && frame.contentWindow.postMessage(m, "*"); } catch (e) {} };
    const acts = $(".sbacts", bar);
    if (items.every(it => it.part)) return parts3d(bar, items, one, post);
    if (one.leaf && one.text) { const rt = h("button", "", "Retype"); rt.title = "Edit the text right in the page (or double-click it)"; rt.onclick = () => post({ blaze: "retype" }); acts.prepend(rt); }
    // Voice while pointing: hold the mic and say what to change.
    const mic = h("button", "micbtn", `${icon("mic", "i sm")}<span>Hold to talk</span>`);
    mic.title = "Hold, say what to change (\"bigger, warmer, less busy\"), let go";
    mic.onpointerdown = e => { e.preventDefault(); voiceStart(mic); };
    mic.onpointerup = mic.onpointerleave = () => voiceStop(mic);
    acts.append(mic);
    // Write-back for the style controls: Apply edits the file directly.
    const tweak = $(".sbtweak", bar), changed = {};
    $$("input", tweak).forEach(inp => inp.addEventListener("input", () => { changed[inp.dataset.p] = inp.value + (inp.dataset.u || ""); }));
    const apply = $(".apply", tweak);
    if (apply) apply.onclick = async () => {
      const text = ($(".sbform input", bar) || {}).value || "";
      if (!Object.keys(changed).length && text) return designEdit("Restyle", text);
      for (const it of items) await writeBack({ kind: "style", target: it.target, props: changed }, it, text);
      selection = null; drawStage();
    };
    // Swap a colour everywhere it's written.
    const sw = h("div", "sbswap", `<span class="label">Swap colour</span><input type="color" class="from" title="This colour"><span class="label weak">→</span><input type="color" class="to" title="New colour"><button class="btn line xs">Everywhere</button>`);
    const from = $(".from", sw), to = $(".to", sw);
    from.value = rgbHex(one.styles && (one.styles.background && !/rgba\(0, 0, 0, 0\)/.test(one.styles.background) ? one.styles.background : one.styles.color));
    to.value = from.value;
    $("button", sw).onclick = async () => {
      if (from.value === to.value) return;
      const r = await call("SwapColor", from.value, to.value);
      editDone(r, "Swapped " + from.value + " for " + to.value);
    };
    tweak.append(sw);
    // Restore just this element from an older version.
    const vs = (D && D.versions || []).filter(v => v.n !== D.head);
    if (vs.length && D.target.kind === "file" && !viewing && items.length === 1) {
      const rs = h("div", "sbrestore", `<span class="label">Restore this element from</span><select class="field">${vs.slice().reverse().map(v => `<option value="${v.n}">v${pad(v.n)} · ${esc((v.summary || "").slice(0, 40))}</option>`).join("")}</select><button class="btn line xs">Restore</button>`);
      $("button", rs).onclick = async () => { const r = await call("RestoreElement", +$("select", rs).value, one.target); editDone(r, "Restored the element"); };
      bar.append(rs);
    }
  };

  // ---------- 1. edits that write back ----------
  async function writeBack(edit, it, why) {
    const r = await call("ApplyEdit", { file: "", edit });
    if (r && r.ok) { editDone(r); return true; }
    // Not writable by hand (framework expression, ambiguous match): Blaze does it.
    const vals = Object.entries(edit.props || {}).map(([k, v]) => `${k}: ${v}`).join("; ");
    const req = edit.kind === "text" ? `Replace its text with: ${edit.new_text}` : edit.kind === "style" ? (why ? why + "\n" : "") + "Set these CSS values in the source: " + vals : edit.kind === "part" ? (why ? why + "\n" : "") + "Set these values on this part in the blaze-parts block: " + vals : "Remove it";
    const t = D.target.kind === "url" ? `dev server ${D.target.url} (find the source file)` : D.target.path;
    const what = edit.kind === "part" ? `Part: ${edit.target.id}\n\`\`\`json\n${(it && it.html) || ""}\n\`\`\`` : `Element: ${(it && it.selector) || ""}\n\`\`\`html\n${(it && it.html) || ""}\n\`\`\``;
    sendText(`[Design edit] ${edit.kind === "text" ? "Replace text" : edit.kind === "part" ? "Adjust part" : "Restyle"}\nFile: ${t}\n${what}\nRequest: ${req}`, []);
    note("Blaze is making that change" + (r && r.error ? ` (${r.error})` : ""));
    return false;
  }
  function editDone(r, label) {
    if (!r) return;
    if (!r.ok) { note(r.error || "Couldn't make that change", "error"); return; }
    drawDesign();
    let box = $("#toasts"); if (!box) { box = h("div", ""); box.id = "toasts"; document.body.append(box); }
    const t = h("div", "toast wb", `${icon("check", "i sm")}<span>${esc(label || "Saved")} → <span class="mono">${esc(r.file)}</span> <span class="dstat">+${r.add} −${r.del}</span></span><button class="btn ghost xs d">Diff</button><button class="btn ghost xs u">${icon("undo", "i sm")}Undo</button>`);
    $(".u", t).onclick = async () => { t.remove(); const m = await call("UndoEdit"); if (m !== "ok") note(m); viewing = 0; drawDesign(); };
    $(".d", t).onclick = () => { const pre = $(".diffbox", t); if (pre) { pre.remove(); return; } t.append(h("pre", "diffbox", (r.diff || "").split("\n").map(l => `<span class="${l[0] === "+" && !l.startsWith("+++") ? "a" : l[0] === "-" && !l.startsWith("---") ? "r" : ""}">${esc(l)}</span>`).join("\n"))); clearTimeout(t._k); };
    box.append(t);
    t._k = setTimeout(() => { t.classList.add("out"); setTimeout(() => t.remove(), 300); }, 9000);
    t.onmouseenter = () => clearTimeout(t._k);
  }
  // 3D parts (three-d-design skill): colour, scale and turn write straight to the part's entry
  // in the page's blaze-parts JSON; arrow keys in the preview nudge it. No text or CSS tools.
  function parts3d(bar, items, one, post) {
    const acts = $(".sbacts", bar);
    $$("button", acts).forEach(b => { if (b.dataset.a === "Replace text") b.remove(); if (b.dataset.a === "Restyle") b.textContent = "Adjust"; });
    const mic = h("button", "micbtn", `${icon("mic", "i sm")}<span>Hold to talk</span>`);
    mic.title = "Hold, say what to change (\"taller, oak, round the corners\"), let go";
    mic.onpointerdown = e => { e.preventDefault(); voiceStart(mic); };
    mic.onpointerup = mic.onpointerleave = () => voiceStop(mic);
    acts.append(mic);
    const tweak = $(".sbtweak", bar), p = one.part || {};
    tweak.innerHTML = `<label><span class="label">Colour</span><input type="color" data-p="color"></label>
      <label><span class="label">Scale</span><input type="range" min="10" max="300" value="100" data-p="scale" data-u="%"></label>
      <label><span class="label">Turn</span><input type="range" min="-180" max="180" value="0" data-p="rotate-y" data-u="°"></label>
      <button class="btn accent sm apply">Apply</button>
      <div class="hknote">Arrow keys move the part in the preview${p.units ? " (" + esc(p.units) + ")" : ""}, Shift for bigger steps, Page Up/Down to raise or lower it.</div>`;
    const changed = {};
    $$("input", tweak).forEach(inp => {
      if (inp.dataset.p === "color") inp.value = rgbHex(one.styles && one.styles.color);
      inp.oninput = () => { changed[inp.dataset.p] = inp.value; post({ blaze: "style3d", prop: inp.dataset.p, value: inp.value }); };
    });
    $(".apply", tweak).onclick = async () => {
      const text = ($(".sbform input", bar) || {}).value || "";
      if (!Object.keys(changed).length) { if (text) designEdit("Adjust", text); return; }
      for (const it of items) {
        const q = it.part || {}, props = {};
        if (changed.color) props.color = JSON.stringify(changed.color);
        if (changed.scale) { const k = +changed.scale / 100, b = q.scale || [1, 1, 1]; props.scale = JSON.stringify(b.map(v => Math.round(v * k * 1000) / 1000)); }
        if (changed["rotate-y"]) { const b = (q.rotation || [0, 0, 0]).slice(); b[1] = Math.round(((b[1] + +changed["rotate-y"] + 540) % 360 - 180) * 10) / 10; props.rotation = JSON.stringify(b); }
        await writeBack({ kind: "part", target: it.target, props }, it, text);
      }
      selection = null; drawStage();
    };
  }

  addEventListener("message", e => {
    const m = e.data || {};
    if (m.blaze === "edit" && m.edit && view === "design") {
      if (viewing) { note("You're viewing an old version. Go back to live to edit."); return; }
      writeBack(m.edit, { selector: m.selector, html: m.html });
    }
    if (m.blaze === "audit" && m.id && S2.audits[m.id]) S2.audits[m.id](m.report);
  });

  // ---------- 5. the critic ----------
  let criticRun = 0;
  async function critique(n) {
    if (!S2.critic || !D || !D.target || !D.target.kind || !n) return;
    const run = ++criticRun;
    const host = h("div", "crithost");
    document.body.append(host);
    const widths = [390, 820, 1280];
    const audits = await Promise.all(widths.map(w => new Promise(res => {
      const f = h("iframe"); f.style.cssText = `position:fixed;left:-${w + 4000}px;top:0;width:${w}px;height:900px;border:0;visibility:hidden`;
      const id = "a" + Math.random().toString(36).slice(2);
      const done = r => { delete S2.audits[id]; res(r); };
      S2.audits[id] = done;
      const t = setTimeout(() => done(null), 12000);
      f.onload = () => setTimeout(() => { try { f.contentWindow.postMessage({ blaze: "audit", id }, "*"); } catch (e) { clearTimeout(t); done(null); } }, 700);
      f.src = D.target.kind === "file" ? frameURL(n) : frameURL(0);
      host.append(f);
    })));
    host.remove();
    if (run !== criticRun) return;
    const got = audits.filter(Boolean);
    if (!got.length) return;
    const r = await call("SaveCritique", n, got);
    if (!r) return;
    S2.cards[n] = r.card;
    drawScore(); drawVersions();
    if (r.fix && !busy) { await call("MarkFixRequested", n); note(`Critic: ${r.card.score}/100, fixing ${r.card.issues.length} issue${r.card.issues.length > 1 ? "s" : ""}`); sendText(r.fix, []); }
    else if (r.fix) { setTimeout(async () => { if (!busy) { await call("MarkFixRequested", n); sendText(r.fix, []); } }, 4000); }
  }
  function headCard() {
    if (!D) return null;
    const n = viewing || D.head || ((D.versions || []).slice(-1)[0] || {}).n;
    const v = (D.versions || []).find(x => x.n === n);
    return (v && v.card && v.card.score) ? v.card : S2.cards[n] || null;
  }
  function drawScore() {
    const b = $("#dscore"); if (!b) return;
    const c = headCard();
    if (!c || !c.score) { b.style.display = "none"; return; }
    b.style.display = "";
    b.className = "btn ghost sm crit " + grade(c.score);
    b.innerHTML = `<span class="cs">${c.score}</span><span class="btxt">Critic</span>`;
  }
  function criticPop(anchor) {
    const c = headCard(); if (!c) return;
    openPop(anchor, p => criticBody(p, c), { width: 360 });
  }
  // The critic's card, in a popover or the studio's Critic tab.
  S2.criticInto = el => { const c = headCard(); if (!c) return false; el.classList.add("critpane"); criticBody(el, c); return true; };
  function criticBody(p, c) {
    p.innerHTML = `<div class="phd"><span class="label">Critic · version ${pad(c.version)}</span><span class="grow"></span><span class="cs big ${grade(c.score)}">${c.score}</span></div>
      <div class="pbody"><div class="cbars">${[["Accessibility", c.a11y], ["Layout", c.layout], ["Speed", c.speed]].map(([l, v]) => `<div class="cbar"><span class="label">${l}</span><span class="track"><span style="width:${v}%"></span></span><span class="mono">${v}</span></div>`).join("")}</div>
      ${c.issues && c.issues.length ? `<ul class="clist">${c.issues.map(i => `<li>${esc(i)}</li>`).join("")}</ul>` : `<div class="pnote">Checked at phone, tablet and desktop widths. Nothing to fix.</div>`}
      ${c.fix_of ? `<div class="pnote">This version is the critic's fix of v${pad(c.fix_of)}.</div>` : ""}</div>
      <div class="pft" style="display:flex;gap:8px;align-items:center"><label class="toggle"><input type="checkbox" class="af"><span class="label">Fix automatically</span></label><span class="grow"></span><button class="btn accent sm fx">Fix now</button></div>`;
    call("StudioPrefs").then(pr => { $(".af", p).checked = !pr || pr.auto_fix !== false; });
    $(".af", p).onchange = async e => { const pr = (await call("StudioPrefs")) || {}; pr.auto_fix = e.target.checked; call("SetStudioPrefs", pr); };
    const fx = $(".fx", p);
    if (!c.issues || !c.issues.length) fx.style.display = "none";
    fx.onclick = () => { closePop(); call("MarkFixRequested", c.version); sendText(`[Critic fix] Version ${c.version} of ${D.target.path || D.target.url} scored ${c.score}/100. Fix these in the source, keep the design otherwise identical, then say in one line what you fixed:\n` + c.issues.map(i => "- " + i).join("\n"), []); };
  }

  // ---------- design events ----------
  on("design", d => {
    if (d && d.edit) return; // direct edits: no critic run per nudge, and no "design ready" card
    setTimeout(async () => {
      try { const dv = await API().Design(); const last = (dv.versions || []).slice(-1)[0]; if (last && !(last.card && last.card.score)) { D = D || dv; critique(last.n); } } catch (e) {}
    }, 300);
  });
  on("comment", c => { note("New comment from your phone: " + (c.text || "").slice(0, 80)); });

  // ---------- 6. talk while you point ----------
  let vrec = null;
  async function voiceStart(btn) {
    if (vrec) return;
    btn.classList.add("rec"); $("span", btn).textContent = "Listening… let go to send";
    const md = navigator.mediaDevices;
    if (md && md.getUserMedia) {
      try {
        const stream = await md.getUserMedia({ audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true } });
        const AC = window.AudioContext || window.webkitAudioContext, ctx = new AC();
        const src = ctx.createMediaStreamSource(stream), node = ctx.createScriptProcessor(4096, 1, 1), chunks = [];
        node.onaudioprocess = ev => chunks.push(new Float32Array(ev.inputBuffer.getChannelData(0)));
        src.connect(node); node.connect(ctx.destination);
        vrec = { kind: "web", stream, ctx, node, src, chunks, rate: ctx.sampleRate, t0: Date.now(), btn };
        return;
      } catch (e) {}
    }
    const err = await call("RecStart");
    if (err) { btn.classList.remove("rec"); $("span", btn).textContent = "Hold to talk"; note(err, "error"); return; }
    vrec = { kind: "native", t0: Date.now(), btn };
  }
  async function voiceStop(btn) {
    const r = vrec; if (!r) return;
    vrec = null; btn.classList.remove("rec"); $("span", btn).textContent = "Transcribing…";
    let t = null;
    try {
      if (r.kind === "web") {
        r.node.disconnect(); r.src.disconnect(); r.stream.getTracks().forEach(x => x.stop()); r.ctx.close();
        if (Date.now() - r.t0 < 500) { $("span", btn).textContent = "Hold to talk"; return; }
        t = await API().Transcribe(wavB64(r.chunks, r.rate));
      } else t = await API().RecStop(false);
    } catch (e) { t = { error: String(e) }; }
    $("span", btn).textContent = "Hold to talk";
    if (!t || t.error) { note((t && t.error) || "Voice didn't work", "error"); return; }
    if (!t.text) { note("Didn't catch any words"); return; }
    designEdit("Change", t.text);
  }

  // ---------- 2. directions as live branches ----------
  async function refreshDirs() {
    const ds = (await call("Directions")) || [];
    const had = S2.dirs.length;
    S2.dirs = ds;
    if (view === "design" && (had || ds.length)) drawStage();
  }
  on("directions", ds => { S2.dirs = ds || []; if (view === "design") drawStage(); });
  on("agent", v => { if (v && v.handle && v.handle.startsWith("direction-")) { clearTimeout(refreshDirs.t); refreshDirs.t = setTimeout(refreshDirs, 250); } });

  function directionsPop(anchor) {
    if (S2.dirs.length) { S2.mode = S2.mode === "live" ? "" : "live"; drawStage(); return; }
    openPop(anchor, p => {
      const t = D && D.target && D.target.kind ? (D.target.path || D.target.url) : "a page for this project";
      p.innerHTML = `<div class="phd"><span class="label">Directions</span></div><div class="pbody"><div class="pnote" style="padding:0 0 8px">Three specialists each take ${esc(t)} somewhere different, on their own git worktree (or copy), side by side. Then pick one, or mix them: “header from B, colours from C”.</div><textarea class="field" rows="3" placeholder="What should the page do or feel like? (optional)"></textarea></div><div class="pft" style="display:flex;gap:8px"><button class="btn line sm old">Quick options as files</button><span class="grow"></span><button class="btn accent sm go">${icon("branch", "i sm")}Start 3 branches</button></div>`;
      $(".old", p).onclick = () => { closePop(); directions(); };
      $(".go", p).onclick = async () => { const brief = $("textarea", p).value; closePop(); const r = await call("StartDirections", brief); if (r !== "ok") note(r, "error"); else refreshDirs(); };
      setTimeout(() => $("textarea", p).focus(), 0);
    }, { width: 380 });
  }

  function drawDirections() {
    const st = $("#dstage"); if (!st) return;
    const ds = S2.dirs, running = ds.filter(d => agents_active(d.status)).length;
    st.innerHTML = "";
    const head = h("div", "viewing", `<span class="label acc">Directions</span><span class="sub">${running ? `${running} of ${ds.length} still working` : "Pick one, or mix parts from each"}</span><span class="grow"></span>`);
    const x = h("button", "btn ghost xs", "Discard all"); x.onclick = async () => { if (x.dataset.sure) { await call("DiscardDirections"); S2.dirs = []; drawStage(); } else { x.dataset.sure = 1; x.textContent = "Discard all three? Click again"; } };
    const lv = h("button", "btn line xs", "Show my page"); lv.title = "Hide the branches for now (Directions brings them back)"; lv.onclick = () => { S2.mode = "live"; drawStage(); };
    head.append(lv, x);
    const row = h("div", "dirrow");
    ds.forEach(d => {
      const col = h("div", "dircol");
      const cap = h("div", "dircap", `<b>${esc(d.letter)}</b><span class="label">${esc(d.angle)}</span><span class="grow"></span>`);
      if (!agents_active(d.status) && d.status === "done") { const pk = h("button", "btn line xs", `Pick ${esc(d.letter)}`); pk.onclick = async () => { pk.disabled = true; const r = await call("PickDirection", d.handle); if (r !== "ok") { note(r, "error"); pk.disabled = false; } else { S2.dirs = []; viewing = 0; drawDesign(); } }; cap.append(pk); }
      col.append(cap);
      const box = h("div", "dirframe");
      if (d.url && !agents_active(d.status)) { const f = h("iframe", "pframe"); f.src = d.url + "?t=" + Date.now(); box.append(f); }
      else if (d.url && d.status === "running") { const f = h("iframe", "pframe dim"); f.src = d.url + "?t=" + Date.now(); box.append(f, h("div", "dirnow", `<span class="sq"></span>${esc(d.now || "Working…")}`)); }
      else box.append(h("div", "dirnow", `<span class="sq"></span>${esc(d.status === "queued" ? "Waiting for a free slot" : d.status === "error" || d.status === "failed" ? "Couldn't finish" : d.now || "Starting…")}`));
      col.append(box); row.append(col);
    });
    const mix = h("div", "dirmix", `<input class="field" placeholder="Mix them: header from B, colours from C, footer from A"><button class="btn accent sm">Mix</button>`);
    const go = async () => { const v = $("input", mix).value.trim(); if (!v) return; const r = await call("MixDirections", v); if (r !== "ok") note(r, "error"); else note("Blaze is mixing the directions into your files"); };
    $("button", mix).onclick = go; $("input", mix).onkeydown = e => { if (e.key === "Enter") go(); };
    if (running) $("button", mix).disabled = true;
    st.append(head, row, mix);
  }
  const agents_active = s => s === "queued" || s === "running" || s === "waiting";

  // ---------- 3. brand kit + taste ----------
  function swatches(b) {
    const cs = [b.paper, b.ink].concat(b.accents || []).filter(Boolean);
    return cs.map(c => `<span class="sw" style="background:${esc(c)}" title="${esc(c)}"></span>`).join("");
  }
  async function brandPop(anchor) {
    const data = await Promise.all([call("BrandKit"), call("Taste"), call("StudioPrefs")]);
    openPop(anchor, p => brandBody(p, data), { width: 360 });
  }
  // The brand kit and taste, in a popover or the studio's Brand tab.
  S2.brandInto = async el => { el.classList.add("brandpane"); brandBody(el, await Promise.all([call("BrandKit"), call("Taste"), call("StudioPrefs")])); };
  function brandBody(p, [b, tv, pr]) {
    const draw = (b) => {
      p.innerHTML = `<div class="phd"><span class="label">Brand kit</span><span class="grow"></span>${b && (b.accents || []).length ? `<button class="btn ghost xs clr">Clear</button>` : ""}</div>
        <div class="pbody">${b && (b.paper || (b.accents || []).length || (b.fonts || []).length) ? `<div class="bk"><div class="sws">${swatches(b)}</div><div class="bkline"><span class="label">Fonts</span>${esc((b.fonts || []).join(", ") || "—")}</div><div class="bkline"><span class="label">Radius</span>${esc(b.radius || "—")}</div>${b.source ? `<div class="bkline"><span class="label">From</span><span class="mono">${esc(b.source)}</span></div>` : ""}<textarea class="field notes" rows="2" placeholder="Notes Blaze should always follow (voice, dos and don'ts)">${esc(b.notes || "")}</textarea></div>` : `<div class="pnote" style="padding:0 0 8px">Pull a brand from a site or a logo. Every design then follows it without being told.</div>`}
        <div class="urlform"><input class="field mono" placeholder="yourbrand.com"><button class="btn accent sm u">Pull</button></div>
        <label class="btn line sm logo" style="margin-top:8px;display:inline-flex">${icon("drop", "i sm")}From a logo image…<input type="file" accept="image/png,image/jpeg,image/gif" hidden></label></div>
        <div class="pft" style="display:block"><div class="label">Taste</div><div class="pnote" style="padding:4px 0">${tv && tv.prompt ? esc(tv.prompt.split("\n").slice(1).join(" ")) : "Blaze learns your fonts, colours and spacing from what you keep and undo."}</div>
        <div style="display:flex;gap:8px;align-items:center"><label class="toggle"><input type="checkbox" class="tl"${pr && pr.no_taste ? "" : " checked"}><span class="label">Learn my taste</span></label><span class="grow"></span><button class="btn ghost xs fg">Forget</button></div></div>`;
      const u = $(".u", p), inp = $(".urlform input", p);
      const pull = async () => { if (!inp.value.trim()) return; u.textContent = "Reading…"; u.disabled = true; const r = await call("BrandFromURL", inp.value.trim()); if (r.error) { note(r.error, "error"); u.textContent = "Pull"; u.disabled = false; return; } draw(r.brand); };
      u.onclick = pull; inp.onkeydown = e => { if (e.key === "Enter") pull(); };
      $(".logo input", p).onchange = e => { const f = e.target.files[0]; if (!f) return; const rd = new FileReader(); rd.onload = async () => { const r = await call("BrandFromImageData", rd.result, f.name); if (r.error) note(r.error, "error"); else draw(r.brand); }; rd.readAsDataURL(f); };
      const clr = $(".clr", p); if (clr) clr.onclick = async () => draw(await call("ClearBrand"));
      const nt = $(".notes", p); if (nt) nt.onchange = async () => { const cur = await call("BrandKit"); cur.notes = nt.value; call("SetBrand", cur); };
      $(".tl", p).onchange = async e => { const cur = (await call("StudioPrefs")) || {}; cur.no_taste = !e.target.checked; call("SetStudioPrefs", cur); };
      $(".fg", p).onclick = async () => { await call("ForgetTaste"); note("Taste forgotten"); closePop(); };
    };
    draw(b);
  }

  // ---------- 4. copy any window ----------
  function copyFile(f, extra) {
    const rd = new FileReader();
    rd.onload = async () => { const r = await call("CopyFromImage", rd.result, f.name, extra || ""); if (r !== "ok") note(r, "error"); else { show("design"); } };
    rd.readAsDataURL(f);
  }
  function copyPop(anchor) {
    openPop(anchor, p => {
      p.innerHTML = `<div class="phd"><span class="label">Copy any window</span></div><div class="pbody"><div class="pnote" style="padding:0 0 8px">Blaze rebuilds it as editable code in this project's stack. Bring the app or site you like to the front, then:</div>
        <input class="field" placeholder="Anything to change while copying? (optional)">
        <div style="display:flex;gap:8px;margin-top:10px"><button class="btn accent sm win">${icon("window", "i sm")}Copy the window behind Blaze</button></div>
        <label class="dropzone">Or drop a screenshot here, or click to choose one<input type="file" accept="image/*" hidden></label></div>`;
      const extra = () => $("input.field", p).value;
      $(".win", p).onclick = async () => { const x = extra(); closePop(); note("Taking a screenshot of the window behind Blaze…"); const r = await call("CopyWindow", x); if (r !== "ok") note(r, "error"); };
      const dz = $(".dropzone", p);
      $("input[type=file]", dz).onchange = e => { const f = e.target.files[0]; if (f) { closePop(); copyFile(f, extra()); } };
      dz.ondragover = e => { e.preventDefault(); dz.classList.add("over"); };
      dz.ondragleave = () => dz.classList.remove("over");
      dz.ondrop = e => { e.preventDefault(); const f = [...e.dataTransfer.files].find(f => f.type.startsWith("image/")); if (f) { const x = extra(); closePop(); copyFile(f, x); } };
    }, { width: 360 });
  }
  // Paste a screenshot while in Design (and not typing): copy it.
  addEventListener("paste", e => {
    if (view !== "design" || /^(INPUT|TEXTAREA)$/.test((document.activeElement || {}).tagName || "")) return;
    const f = [...(e.clipboardData || {}).files || []].find(f => f.type.startsWith("image/"));
    if (f) { e.preventDefault(); copyFile(f, ""); }
  });

  // ---------- 9. preview on your phone ----------
  async function phonePop(anchor) {
    const pv = await call("PhonePreview", true);
    const comments = (await call("Comments")) || [];
    openPop(anchor, p => {
      if (!pv || pv.error) { p.innerHTML = `<div class="phd"><span class="label">Phone preview</span></div><div class="pbody"><div class="pnote">${esc((pv && pv.error) || "Not available.")}</div></div>`; return; }
      p.innerHTML = `<div class="phd"><span class="label">Phone preview</span><span class="grow"></span><button class="btn ghost xs off">Turn off</button></div>
        <div class="pbody qrbody"><img class="qr" src="${pv.qr}" alt="QR code for the phone preview"><div><div class="pnote" style="padding:0 0 6px">Scan with your phone on the same Wi-Fi. It reloads on every change; long-press anywhere to leave a comment.</div><div class="mono small qrurl">${esc(pv.url)}</div><button class="btn line xs cp" style="margin-top:6px">${icon("copy", "i sm")}Copy link</button></div></div>
        <div class="pft" style="display:block"><div class="label">Comments ${comments.filter(c => !c.resolved).length ? `<span class="pcount">${comments.filter(c => !c.resolved).length}</span>` : ""}</div><div class="clist2"></div></div>`;
      $(".off", p).onclick = async () => { await call("PhonePreview", false); closePop(); note("Phone preview off: the link no longer works"); };
      $(".cp", p).onclick = () => { navigator.clipboard && navigator.clipboard.writeText(pv.url); $(".cp", p).textContent = "Copied"; };
      const list = $(".clist2", p);
      const draw = cs => {
        list.innerHTML = "";
        const open = cs.filter(c => !c.resolved);
        if (!open.length) { list.append(h("div", "pnote", "No comments yet.")); return; }
        open.slice(0, 12).forEach(c => {
          const r = h("div", "cmt", `${icon("pin", "i sm")}<div class="ct"><div>${esc(c.text)}</div><div class="label weak">${esc(c.from === "share" ? (c.who || "Shared link") : "Phone")} · ${esc(c.when)} · <span class="mono">${esc((c.sel || "").slice(0, 40))}</span></div></div>`);
          const fix = h("button", "btn line xs", "Fix"); fix.onclick = () => { closePop(); sendText(`[Design edit] Change\nFile: ${D.target.path || D.target.url}\nElement: ${c.sel}\nRequest: A reviewer commented: "${c.text}". Make that change.`, []); if (c.from !== "share") call("ResolveComment", c.id); };
          const ok = h("button", "btn ghost xs", icon("check", "i sm")); ok.title = "Resolve"; ok.onclick = async () => draw((await call("ResolveComment", c.id)) || []);
          if (c.from === "share") ok.style.display = "none";
          r.append(fix, ok); list.append(r);
        });
      };
      draw(comments);
    }, { width: 400 });
  }

  // ---------- 10. one-click ship ----------
  let shipLog = [], shipURL = "";
  on("ship", ev => {
    shipLog.push(ev);
    if (ev.stage === "done") { shipURL = ev.url; note(ev.line, "ok"); }
    if (ev.stage === "error") note("Publishing failed: " + ev.line.split("\n")[0], "error");
    const p = popEl && popEl.dataset.kind === "ship" ? popEl : null;
    if (p) drawShipLog(p);
  });
  function drawShipLog(p) {
    const box = $(".shiplog", p); if (!box) return;
    box.innerHTML = shipLog.map(e => `<div class="sl ${e.stage}">${e.stage === "done" ? icon("check", "i sm") : e.stage === "error" ? icon("x", "i sm") : '<span class="sq"></span>'}<span>${esc(e.line)}</span></div>`).join("");
    if (shipURL) { const a = h("button", "btn accent sm", `${icon("open", "i sm")}Open ${esc(shipURL.replace(/^https:\/\//, ""))}`); a.onclick = () => API().OpenURL(shipURL); box.append(a); }
  }
  async function shipPop(anchor) {
    const info = (await call("ShipInfo")) || {};
    openPop(anchor, p => {
      p.dataset.kind = "ship";
      p.innerHTML = `<div class="phd"><span class="label">Ship it</span><span class="grow"></span><span class="label weak">${esc(info.stack || "")}</span></div>
        <div class="pbody"><div class="shipbtns">
          <button class="opt cf"><span class="col2"><span class="om" style="color:var(--ink)">Cloudflare Pages</span><span class="od">Free · project.pages.dev · signs in with your Cloudflare account${info.node ? "" : " · needs Node.js (npx)"}</span></span></button>
          <button class="opt gh"${info.github ? "" : " disabled"}><span class="col2"><span class="om" style="color:var(--ink)">GitHub Pages</span><span class="od">${info.github ? "Free · pushes a gh-pages branch to " + esc(info.repo) : "Needs a GitHub remote on this folder"}</span></span></button>
        </div>
        <label class="toggle" style="margin-top:8px"><input type="checkbox" class="cm"${info.comments ? " checked" : ""}><span class="label">Friends can leave pinned comments on the shared page</span></label>
        <div class="shiplog"></div></div>
        ${(info.shipped || []).length ? `<div class="pft" style="display:block"><div class="label">Published</div>${info.shipped.slice(0, 4).map(s => `<button class="opt sh" data-u="${esc(s.url)}"><span class="col2"><span class="om mono" style="color:var(--ink)">${esc(s.url.replace(/^https:\/\//, ""))}</span><span class="od">${esc(s.provider)} · ${esc(s.when)}${s.version ? " · v" + pad(s.version) : ""}</span></span></button>`).join("")}</div>` : ""}`;
      const go = async prov => { shipLog = []; shipURL = ""; drawShipLog(p); const r = await call("Ship", prov); if (r !== "ok") { shipLog.push({ stage: "error", line: r }); drawShipLog(p); } };
      $(".cf", p).onclick = () => go("cloudflare");
      $(".gh", p).onclick = () => go("github");
      $(".cm", p).onchange = async e => { const pr = (await call("StudioPrefs")) || {}; pr.comments = e.target.checked; call("SetStudioPrefs", pr); };
      $$(".sh", p).forEach(b => b.onclick = () => API().OpenURL(b.dataset.u));
      drawShipLog(p);
    }, { width: 380 });
  }
})();
