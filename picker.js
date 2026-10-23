// Injected into design previews by Blaze: hover outlines, click to select, live style tweaks,
// and (v0.8.0) direct edits that write back to the source (retype, drag, resize; v0.20: eight
// handles, Shift for proportions, arrow-key nudges, exact values from the inspector), the
// critic's in-page audit, the states board (?__state=…), hot reload and phone comments.
// 3D mockups (three-d-design skill) can also pick, tweak and nudge their parts.
(function () {
  if (window.__blazePicker) return;
  window.__blazePicker = true;
  const Q = new URLSearchParams(location.search);
  const inFrame = window.parent !== window;
  let on = false, hoverBox, boxes = [], picked = [], handles = null, editing = null;
  const css = `position:fixed;pointer-events:none;z-index:2147483647;box-sizing:border-box;`;
  const post = m => { try { parent.postMessage(m, "*"); } catch (e) {} };
  function mk(color, fill) {
    const d = document.createElement("div");
    d.setAttribute("data-blaze-ui", "");
    d.style.cssText = css + `border:1.5px solid ${color};background:${fill};display:none`;
    document.documentElement.appendChild(d); return d;
  }
  function place(d, el) {
    const r = el.getBoundingClientRect();
    Object.assign(d.style, { display: "block", left: r.left + "px", top: r.top + "px", width: r.width + "px", height: r.height + "px" });
  }
  const isUI = el => !!(el && el.closest && el.closest("[data-blaze-ui]"));
  const goodClass = c => !/^(hover|active|focus|is-|has-|ng-|v-|svelte-|css-|jsx-|sc-|_)/.test(c) && !/^[a-z]+-[0-9a-z]{5,}$/i.test(c);
  function selector(el) {
    if (el.id) return "#" + CSS.escape(el.id);
    const parts = [];
    while (el && el.nodeType === 1 && el !== document.documentElement) {
      if (el.id) { parts.unshift("#" + CSS.escape(el.id)); break; }
      let s = el.tagName.toLowerCase();
      const cls = [...el.classList].filter(c => !/^(hover|active|focus)/.test(c)).slice(0, 2);
      if (cls.length) s += "." + cls.map(c => CSS.escape(c)).join(".");
      const sib = el.parentElement ? [...el.parentElement.children].filter(x => x.tagName === el.tagName) : [];
      if (sib.length > 1) s += `:nth-of-type(${sib.indexOf(el) + 1})`;
      parts.unshift(s); el = el.parentElement;
    }
    return parts.join(" > ");
  }
  // target: how Blaze finds this element in the source file (tag, id, classes, text, and
  // its position among elements with the same tag and classes).
  function target(el) {
    const tag = el.tagName.toLowerCase();
    const classes = [...el.classList].filter(goodClass);
    let index = 0;
    try {
      const q = tag + classes.map(c => "." + CSS.escape(c)).join("");
      index = [...document.querySelectorAll(q)].filter(x => !isUI(x)).indexOf(el);
    } catch (e) {}
    return { tag: el.tagName.toLowerCase() === el.tagName ? el.tagName : tag, id: el.id || "", classes, text: (el.innerText || el.textContent || "").trim().replace(/\s+/g, " ").slice(0, 120), index: Math.max(0, index) };
  }
  function info(el) {
    const cs = getComputedStyle(el);
    let html = el.outerHTML; if (html.length > 3000) html = html.slice(0, 3000) + "…";
    return { selector: selector(el), tag: el.tagName.toLowerCase(), text: (el.innerText || el.textContent || "").trim().slice(0, 200), html, target: target(el),
      leaf: !el.querySelector("*") || [...el.childNodes].every(n => n.nodeType === 3 || /^(B|I|EM|STRONG|SPAN|BR|SMALL|A)$/.test(n.nodeName)),
      box: (() => { const b = boxOf(el); return { x: b.x, y: b.y, w: b.w, h: b.h }; })(),
      styles: { color: cs.color, background: cs.backgroundColor, fontSize: cs.fontSize, padding: cs.padding, borderRadius: cs.borderRadius, gap: cs.gap, fontFamily: cs.fontFamily, width: cs.width, height: cs.height } };
  }
  function send() {
    boxes.forEach(b => b.remove()); boxes = [];
    if (parts.length) { // 3D parts: the outline lives in the scene, the bar sits under the canvas
      const r = scene3d().renderer.domElement.getBoundingClientRect();
      post({ blaze: "selected", items: picked.map(info).concat(parts.map(info3d)), rect: { left: r.left, top: r.top, bottom: r.bottom, width: r.width } });
      return;
    }
    picked.forEach(el => { const b = mk("#1F4BD8", "rgba(31,75,216,.10)"); place(b, el); boxes.push(b); });
    drawHandles();
    const r = picked.length ? picked[picked.length - 1].getBoundingClientRect() : null;
    post({ blaze: "selected", items: picked.map(info), rect: r && { left: r.left, top: r.top, bottom: r.bottom, width: r.width } });
  }

  // ---- direct manipulation (v0.20 studio): a selection frame with eight handles. Drag the
  // element (or its tag) to move it, a side to resize one way, a corner to resize both (Shift
  // keeps the proportions). Arrow keys nudge it 1px (Shift: 10px). Every finished move or
  // resize is written back to the element's source (studio_app.go ApplyEdit). ----
  const ACC = "#4DA8FF";
  const KNOBS = ["nw", "n", "ne", "e", "se", "s", "sw", "w"];
  const CURSOR = { nw: "nwse-resize", se: "nwse-resize", ne: "nesw-resize", sw: "nesw-resize", n: "ns-resize", s: "ns-resize", e: "ew-resize", w: "ew-resize" };
  let dragging = null, suppressClick = false, nudge = null;
  function ui(tag) { const d = document.createElement(tag || "div"); d.setAttribute("data-blaze-ui", ""); document.documentElement.appendChild(d); return d; }
  function px(v) { return Math.round(v) + "px"; }
  function boxOf(el) {
    const cs = getComputedStyle(el), r = el.getBoundingClientRect();
    const t = (cs.translate && cs.translate !== "none" ? cs.translate : "0px 0px").split(" ").map(parseFloat);
    return { x: Math.round(t[0] || 0), y: Math.round(t[1] || 0), w: Math.round(r.width), h: Math.round(r.height), left: r.left, top: r.top };
  }
  function drawHandles() {
    if (handles) { handles.forEach(h => h.remove()); handles = null; }
    if (!on || picked.length !== 1 || editing || parts.length) return;
    const el = picked[0], r = el.getBoundingClientRect();
    const frame = ui(), grip = ui(), size = ui();
    frame.style.cssText = `${css}left:${r.left}px;top:${r.top}px;width:${r.width}px;height:${r.height}px;outline:1.5px solid ${ACC};outline-offset:0`;
    grip.title = "Drag to move";
    grip.textContent = el.tagName.toLowerCase() + ([...el.classList].filter(goodClass)[0] ? "." + [...el.classList].filter(goodClass)[0] : "");
    grip.style.cssText = `position:fixed;z-index:2147483647;left:${r.left - 1}px;top:${Math.max(0, r.top - 20)}px;height:18px;max-width:220px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;padding:0 7px;border-radius:5px 5px 0 0;font:600 10.5px/18px system-ui;color:#03101f;background:${ACC};cursor:move;user-select:none`;
    size.style.cssText = `${css}left:${r.left + r.width / 2}px;top:${r.bottom + 7}px;transform:translateX(-50%);padding:2px 6px;border-radius:5px;font:600 10px/1.3 ui-monospace,monospace;color:#03101f;background:${ACC};white-space:nowrap`;
    size.textContent = Math.round(r.width) + " × " + Math.round(r.height);
    handles = [frame, grip, size];
    KNOBS.forEach(k => {
      const kb = ui();
      const x = k.includes("w") ? r.left : k.includes("e") ? r.right : r.left + r.width / 2;
      const y = k.includes("n") ? r.top : k.includes("s") ? r.bottom : r.top + r.height / 2;
      kb.dataset.h = k; kb.title = "Drag to resize";
      kb.style.cssText = `position:fixed;z-index:2147483647;left:${x - 5}px;top:${y - 5}px;width:10px;height:10px;box-sizing:border-box;background:#fff;border:1.5px solid ${ACC};border-radius:3px;cursor:${CURSOR[k]};box-shadow:0 1px 3px rgba(0,0,0,.25)`;
      kb.onpointerdown = e => drag(e, k);
      handles.push(kb);
    });
    grip.onpointerdown = e => drag(e, "move");
  }
  function report(el) { const b = boxOf(el); post({ blaze: "box", box: { x: b.x, y: b.y, w: b.w, h: b.h } }); }
  function writeBox(el, props, html) {
    post({ blaze: "edit", edit: { kind: "style", target: target(el), props }, html: html || el.outerHTML.slice(0, 3000), selector: selector(el) });
  }
  function drag(e, mode) {
    e.preventDefault(); e.stopPropagation();
    const el = picked[0]; if (!el) return;
    const b0 = boxOf(el), x0 = e.clientX, y0 = e.clientY, ratio = b0.w / Math.max(1, b0.h);
    const html0 = el.outerHTML.slice(0, 3000);
    if (mode !== "move" && getComputedStyle(el).display === "inline") el.style.display = "inline-block";
    let moved = false;
    dragging = mode;
    const move = ev => {
      const dx = ev.clientX - x0, dy = ev.clientY - y0;
      if (!moved && Math.abs(dx) + Math.abs(dy) < 3) return;
      moved = true;
      let tx = b0.x, ty = b0.y, w = b0.w, h = b0.h;
      if (mode === "move") { tx += dx; ty += dy; }
      else {
        if (mode.includes("e")) w = b0.w + dx;
        if (mode.includes("w")) { w = b0.w - dx; tx = b0.x + dx; }
        if (mode.includes("s")) h = b0.h + dy;
        if (mode.includes("n")) { h = b0.h - dy; ty = b0.y + dy; }
        if (ev.shiftKey && mode.length === 2) { // keep the proportions from a corner
          if (Math.abs(w - b0.w) / ratio > Math.abs(h - b0.h)) { const nh = w / ratio; if (mode.includes("n")) ty = b0.y + (b0.h - nh); h = nh; }
          else { const nw = h * ratio; if (mode.includes("w")) tx = b0.x + (b0.w - nw); w = nw; }
        }
        w = Math.max(8, w); h = Math.max(8, h);
        if (mode !== "n" && mode !== "s") { el.style.width = px(w); el.style.maxWidth = "none"; }
        if (mode !== "e" && mode !== "w") el.style.height = px(h);
      }
      el.style.translate = px(tx) + " " + px(ty);
      boxes.forEach((b, i) => place(b, picked[i])); drawHandles(); report(el);
    };
    const up = () => {
      removeEventListener("pointermove", move, true); removeEventListener("pointerup", up, true);
      dragging = null;
      if (!moved) return; // a plain click: the click handler selects what's under it
      suppressClick = true; setTimeout(() => { suppressClick = false; }, 0);
      const b = boxOf(el), props = {};
      if (b.x !== b0.x || b.y !== b0.y) props.translate = b.x + "px " + b.y + "px";
      if (mode !== "move") {
        if (mode !== "n" && mode !== "s") props.width = b.w + "px";
        if (mode !== "e" && mode !== "w") props.height = b.h + "px";
      }
      if (Object.keys(props).length) writeBox(el, props, html0);
      send();
    };
    addEventListener("pointermove", move, true); addEventListener("pointerup", up, true);
  }
  // Dragging the selected element itself moves it; a click without a drag still selects
  // whatever is under the pointer (a child, a neighbour).
  addEventListener("pointerdown", e => {
    if (!on || editing || picked.length !== 1 || isUI(e.target) || e.button !== 0 || e.shiftKey) return;
    if (!picked[0].contains(e.target) || onCanvas(e.target)) return;
    drag(e, "move");
  }, true);
  // window capture runs before the selection click handler (document capture), so a drag's
  // trailing click never re-selects.
  addEventListener("click", e => { if (suppressClick) { e.preventDefault(); e.stopPropagation(); suppressClick = false; } }, true);
  // Arrow keys nudge (in the preview, or forwarded from the studio); the write waits for a pause.
  function nudgeBy(dx, dy) {
    const el = picked[0]; if (!el || picked.length !== 1 || editing || parts.length) return;
    const b = boxOf(el);
    if (!nudge || nudge.el !== el) nudge = { el, html: el.outerHTML.slice(0, 3000) };
    el.style.translate = px(b.x + dx) + " " + px(b.y + dy);
    boxes.forEach((bx, i) => place(bx, picked[i])); drawHandles(); report(el);
    clearTimeout(nudge.t);
    nudge.t = setTimeout(() => { const n = nudge; nudge = null; const nb = boxOf(n.el); writeBox(n.el, { translate: nb.x + "px " + nb.y + "px" }, n.html); }, 600);
  }
  addEventListener("keydown", e => {
    if (!on || editing || parts.length || picked.length !== 1) return;
    const s = e.shiftKey ? 10 : 1;
    const d = { ArrowLeft: [-s, 0], ArrowRight: [s, 0], ArrowUp: [0, -s], ArrowDown: [0, s] }[e.key];
    if (!d) return;
    e.preventDefault(); nudgeBy(d[0], d[1]);
  }, true);
  // Exact values from the studio's inspector: { x, y, w, h } (any of them).
  function setBox(v) {
    const el = picked[0]; if (!el || picked.length !== 1) return;
    const b = boxOf(el), props = {};
    if (v.x !== undefined || v.y !== undefined) {
      const x = v.x !== undefined ? +v.x : b.x, y = v.y !== undefined ? +v.y : b.y;
      el.style.translate = px(x) + " " + px(y); props.translate = Math.round(x) + "px " + Math.round(y) + "px";
    }
    if (v.w !== undefined) { if (getComputedStyle(el).display === "inline") el.style.display = "inline-block"; el.style.width = px(Math.max(8, +v.w)); el.style.maxWidth = "none"; props.width = Math.round(Math.max(8, +v.w)) + "px"; }
    if (v.h !== undefined) { el.style.height = px(Math.max(8, +v.h)); props.height = Math.round(Math.max(8, +v.h)) + "px"; }
    boxes.forEach((bx, i) => place(bx, picked[i])); drawHandles(); report(el);
    if (Object.keys(props).length) writeBox(el, props);
  }
  function retype(el) {
    if (editing) return;
    const before = (el.innerText || "").trim();
    editing = el; drawHandles();
    el.contentEditable = "true"; el.focus();
    try { const s = getSelection(), r = document.createRange(); r.selectNodeContents(el); s.removeAllRanges(); s.addRange(r); } catch (e) {}
    const done = keep => {
      el.removeEventListener("keydown", key, true); el.removeEventListener("blur", blur, true);
      el.contentEditable = "false"; el.removeAttribute("contenteditable"); editing = null;
      const after = (el.innerText || "").trim();
      if (!keep) { el.innerText = before; drawHandles(); return; }
      if (after !== before) post({ blaze: "edit", edit: { kind: "text", target: Object.assign(target(el), { text: before.slice(0, 120) }), old_text: before, new_text: after }, html: el.outerHTML.slice(0, 3000), selector: selector(el) });
      send();
    };
    const key = ev => { if (ev.key === "Enter" && !ev.shiftKey) { ev.preventDefault(); done(true); } if (ev.key === "Escape") { ev.preventDefault(); ev.stopPropagation(); done(false); } };
    const blur = () => done(true);
    el.addEventListener("keydown", key, true); el.addEventListener("blur", blur, true);
  }

  // ---- 3D parts (three-d-design skill) ----
  // A mockup exposes window.__blazeScene = { THREE, scene, camera, renderer, parts, spec }, and
  // every part's root object carries userData.part = its id in the page's blaze-parts JSON.
  // Clicks on the canvas are raycast to a part; orbit drags are left alone.
  let parts = [], hoverPart = null, helpers = [], hoverHelper = null, down = null, nudged = null;
  const scene3d = () => { const z = window.__blazeScene; return z && z.THREE && z.scene && z.camera && z.renderer ? z : null; };
  const onCanvas = el => { const z = scene3d(); return !!(z && el === z.renderer.domElement); };
  function partAt(e) {
    const z = scene3d(); if (!z) return null;
    const r = z.renderer.domElement.getBoundingClientRect();
    const ray = new z.THREE.Raycaster();
    ray.setFromCamera(new z.THREE.Vector2(((e.clientX - r.left) / r.width) * 2 - 1, -((e.clientY - r.top) / r.height) * 2 + 1), z.camera);
    for (const hit of ray.intersectObjects(z.scene.children, true)) {
      let o = hit.object;
      if (!o.visible || o.userData.blazeUI) continue;
      while (o && !(o.userData && o.userData.part)) o = o.parent;
      if (o) return o;
    }
    return null;
  }
  function spec3d(o) {
    const z = scene3d(), id = o.userData.part;
    return ((z.spec && z.spec.parts) || []).find(p => p.id === id) || null;
  }
  function hex3d(o) {
    let c = null;
    o.traverse(m => { if (!c && m.material && m.material.color) c = m.material.color; });
    return c ? "rgb(" + [c.r, c.g, c.b].map(v => Math.round(v * 255)).join(", ") + ")" : "";
  }
  function info3d(o) {
    const sp = spec3d(o) || { id: o.userData.part };
    const label = sp.label || o.userData.label || o.name || sp.id;
    return { selector: "part#" + sp.id, tag: "part", text: label, html: JSON.stringify(sp, null, 1), leaf: false,
      part: { id: sp.id, label, position: sp.position || null, rotation: sp.rotation || null, scale: sp.scale || null, units: (scene3d().spec || {}).units || "" },
      target: { tag: "part", id: sp.id, classes: [], text: label, index: 0 },
      styles: { color: hex3d(o), background: "", fontSize: "", padding: "", borderRadius: "", gap: "", fontFamily: "", width: "", height: "" } };
  }
  function helper(o, color) {
    const z = scene3d(), h = new z.THREE.BoxHelper(o, color);
    h.userData.blazeUI = true; h.raycast = () => {}; z.scene.add(h); return h;
  }
  function drawParts() {
    const z = scene3d(); if (!z) return;
    helpers.forEach(h => { z.scene.remove(h); h.geometry.dispose(); }); helpers = parts.map(o => helper(o, 0x1F4BD8));
  }
  (function follow() { // keep outlines on parts that move or animate
    helpers.forEach(h => h.update()); if (hoverHelper) hoverHelper.update();
    requestAnimationFrame(follow);
  })();
  function hover3d(o) {
    const z = scene3d(); if (!z || o === hoverPart) return;
    if (hoverHelper) { z.scene.remove(hoverHelper); hoverHelper.geometry.dispose(); hoverHelper = null; }
    hoverPart = o;
    if (o && !parts.includes(o)) hoverHelper = helper(o, 0x6B8CFF);
    z.renderer.domElement.style.cursor = o ? "pointer" : "";
  }
  function clear3d() { parts = []; drawParts(); hover3d(null); }
  // Live tweaks from the selection bar (colour, scale %, rotation), shown before they're saved.
  function tweak3d(prop, value) {
    parts.forEach(o => {
      const sp = spec3d(o) || {};
      if (prop === "color") o.traverse(m => { if (m.material && m.material.color) m.material.color.set(value); });
      if (prop === "scale") { const k = (+value || 100) / 100, b = sp.scale || [1, 1, 1]; o.scale.set(b[0] * k, b[1] * k, b[2] * k); }
      if (prop === "rotate-y") { const b = sp.rotation || [0, 0, 0]; o.rotation.y = (b[1] + (+value || 0)) * Math.PI / 180; }
    });
  }
  // Arrow keys nudge the picked part in the mockup's units (Shift ×10); PageUp/PageDown raise and
  // lower it. The new position is saved to the blaze-parts block once the keys go quiet.
  addEventListener("keydown", e => {
    if (!on || parts.length !== 1 || editing) return;
    const step = (e.shiftKey ? 10 : 1) * ((scene3d().spec || {}).nudge || 10);
    const d = { ArrowLeft: [-step, 0, 0], ArrowRight: [step, 0, 0], ArrowUp: [0, 0, -step], ArrowDown: [0, 0, step], PageUp: [0, step, 0], PageDown: [0, -step, 0] }[e.key];
    if (!d) return;
    e.preventDefault();
    const o = parts[0], sp = spec3d(o); if (!sp) return;
    const p0 = nudged ? nudged.pos : (sp.position || [0, 0, 0]).slice();
    const pos = p0.map((v, i) => Math.round((v + d[i]) * 1000) / 1000);
    o.position.set(pos[0], pos[1], pos[2]); // part roots sit in mockup units under the scaled model
    clearTimeout(nudged && nudged.t);
    nudged = { pos, t: setTimeout(() => {
      sp.position = pos; nudged = null;
      post({ blaze: "edit", edit: { kind: "part", target: { tag: "part", id: sp.id, classes: [], text: sp.label || sp.id, index: 0 }, props: { position: JSON.stringify(pos) } }, html: JSON.stringify(sp, null, 1), selector: "part#" + sp.id });
      send();
    }, 600) };
  }, true);

  document.addEventListener("mousemove", e => {
    if (!on || editing) return;
    if (onCanvas(e.target)) { if (hoverBox) hoverBox.style.display = "none"; if (!e.buttons) hover3d(partAt(e)); return; }
    if (hoverPart) hover3d(null);
    hoverBox = hoverBox || mk("#6B8CFF", "rgba(107,140,255,.08)");
    const el = e.target; if (el === document.documentElement || el === document.body || isUI(el)) { hoverBox.style.display = "none"; return; }
    place(hoverBox, el);
  }, true);
  addEventListener("pointerdown", e => { down = { x: e.clientX, y: e.clientY }; }, true);
  document.addEventListener("click", e => {
    if (!on || isUI(e.target)) return;
    if (editing) { if (editing.contains(e.target)) return; }
    if (onCanvas(e.target)) {
      if (down && Math.hypot(e.clientX - down.x, e.clientY - down.y) > 4) return; // that was an orbit drag
      e.preventDefault(); e.stopPropagation();
      const o = partAt(e);
      if (e.shiftKey && o) { const i = parts.indexOf(o); i >= 0 ? parts.splice(i, 1) : parts.push(o); }
      else { parts = o ? [o] : []; picked = []; }
      hover3d(null); drawParts(); send();
      return;
    }
    if (parts.length && !e.shiftKey) clear3d();
    e.preventDefault(); e.stopPropagation();
    const el = e.target;
    if (e.shiftKey) { const i = picked.indexOf(el); i >= 0 ? picked.splice(i, 1) : picked.push(el); }
    else picked = [el];
    send();
  }, true);
  document.addEventListener("dblclick", e => {
    if (!on || isUI(e.target) || onCanvas(e.target)) return;
    e.preventDefault(); e.stopPropagation();
    const el = e.target; picked = [el]; send();
    if (info(el).leaf && (el.innerText || "").trim()) retype(el);
  }, true);
  addEventListener("scroll", () => { if (picked.length) { boxes.forEach((b, i) => place(b, picked[i])); drawHandles(); } }, true);
  addEventListener("message", e => {
    const m = e.data || {};
    if (m.blaze === "mode") {
      on = !!m.on;
      if (!on) { picked = []; boxes.forEach(b => b.remove()); boxes = []; if (hoverBox) hoverBox.style.display = "none"; clear3d(); drawHandles(); }
      document.documentElement.style.cursor = on ? "crosshair" : "";
    }
    if (m.blaze === "clear") { picked = []; clear3d(); send(); }
    if (m.blaze === "style3d") tweak3d(m.prop, m.value);
    if (m.blaze === "style") { picked.forEach(el => el.style.setProperty(m.prop, m.value)); boxes.forEach((b, i) => place(b, picked[i])); drawHandles(); }
    if (m.blaze === "retype") { if (picked[0]) retype(picked[0]); }
    if (m.blaze === "nudge") nudgeBy(+m.dx || 0, +m.dy || 0);
    if (m.blaze === "setbox" && m.box) setBox(m.box);
    if (m.blaze === "theme") { document.documentElement.style.colorScheme = m.value; }
    if (m.blaze === "audit") { audit().then(report => post({ blaze: "audit", id: m.id, report })); }
  });
  // A press anywhere in the page tells Blaze, so open menus and pickers outside the
  // preview close (clicks inside an iframe never reach the app's own document).
  addEventListener("pointerdown", e => { if (!isUI(e.target)) post({ blaze: "down" }); }, true);

  // ---- the critic's audit (contrast, alt text, labels, tap targets, overflow, weight) ----
  function rgb(c) { const m = (c || "").match(/[\d.]+/g); return m ? { r: +m[0], g: +m[1], b: +m[2], a: m[3] === undefined ? 1 : +m[3] } : null; }
  function lum(c) { const f = v => { v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); }; return 0.2126 * f(c.r) + 0.7152 * f(c.g) + 0.0722 * f(c.b); }
  function ratio(a, b) { const x = lum(a), y = lum(b); return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05); }
  function bgOf(el) {
    let c = { r: 255, g: 255, b: 255, a: 1 }, layers = [];
    for (let n = el; n && n.nodeType === 1; n = n.parentElement) {
      const cs = getComputedStyle(n);
      if (cs.backgroundImage && cs.backgroundImage !== "none") return null; // over an image: can't judge
      const b = rgb(cs.backgroundColor); if (b && b.a > 0) { layers.push(b); if (b.a >= 1) break; }
    }
    if (!layers.length) {
      const dark = getComputedStyle(document.documentElement).colorScheme === "dark";
      if (dark) c = { r: 18, g: 18, b: 18, a: 1 };
    }
    for (let i = layers.length - 1; i >= 0; i--) { const l = layers[i]; c = { r: l.r * l.a + c.r * (1 - l.a), g: l.g * l.a + c.g * (1 - l.a), b: l.b * l.a + c.b * (1 - l.a), a: 1 }; }
    return c;
  }
  const hex = c => "#" + [c.r, c.g, c.b].map(v => Math.round(v).toString(16).padStart(2, "0")).join("");
  function visible(el) { const r = el.getBoundingClientRect(), cs = getComputedStyle(el); return r.width > 0 && r.height > 0 && cs.visibility !== "hidden" && cs.display !== "none" && +cs.opacity > 0.05; }
  async function audit() {
    const out = { width: innerWidth, contrast: [], no_alt: [], unlabeled: [], small_taps: [], overflow_px: 0, nodes: 0, img_bytes: 0, load_ms: 0 };
    const all = [...document.body.querySelectorAll("*")].filter(el => !isUI(el));
    out.nodes = all.length;
    const seen = new Set();
    for (const el of all) {
      if (out.contrast.length >= 25) break;
      if (!visible(el)) continue;
      const own = [...el.childNodes].some(n => n.nodeType === 3 && n.textContent.trim().length > 1);
      if (!own) continue;
      const cs = getComputedStyle(el), fg = rgb(cs.color); if (!fg || fg.a === 0) continue;
      const bg = bgOf(el); if (!bg) continue;
      const size = parseFloat(cs.fontSize), bold = +cs.fontWeight >= 700;
      const need = size >= 24 || (size >= 18.66 && bold) ? 3 : 4.5;
      const r = ratio(fg, bg);
      const sel = selector(el);
      if (r < need && !seen.has(sel)) { seen.add(sel); out.contrast.push({ sel, text: el.innerText.trim().slice(0, 40), fg: hex(fg), bg: hex(bg), ratio: Math.round(r * 100) / 100, need }); }
    }
    document.querySelectorAll("img").forEach(img => { if (!isUI(img) && !img.hasAttribute("alt") && visible(img)) out.no_alt.push({ sel: selector(img) }); });
    document.querySelectorAll("input:not([type=hidden]),select,textarea,button,a[href],[role=button]").forEach(el => {
      if (isUI(el) || !visible(el)) return;
      const name = (el.getAttribute("aria-label") || el.getAttribute("aria-labelledby") || el.getAttribute("title") || el.getAttribute("placeholder") || (el.id && document.querySelector(`label[for="${CSS.escape(el.id)}"]`) ? "l" : "") || (el.closest("label") ? "l" : "") || (el.innerText || "").trim() || (el.querySelector("img[alt]") ? "i" : "") || (el.value && el.type === "submit" ? el.value : ""));
      if (!name) out.unlabeled.push({ sel: selector(el) });
      if (innerWidth <= 480 && !/^(INPUT|SELECT|TEXTAREA)$/.test(el.tagName)) {
        const r = el.getBoundingClientRect();
        const inline = el.tagName === "A" && getComputedStyle(el).display === "inline" && el.closest("p,li");
        if (!inline && (r.width < 44 || r.height < 44) && (r.width < 24 || r.height < 24 || r.width * r.height < 44 * 30)) out.small_taps.push({ sel: selector(el), w: Math.round(r.width), h: Math.round(r.height) });
      }
    });
    out.small_taps = out.small_taps.slice(0, 20);
    out.overflow_px = Math.max(0, document.documentElement.scrollWidth - document.documentElement.clientWidth);
    try {
      performance.getEntriesByType("resource").forEach(e => { if (e.initiatorType === "img" || /\.(png|jpe?g|gif|webp|avif|svg)(\?|$)/i.test(e.name)) out.img_bytes += e.transferSize || e.encodedBodySize || 0; });
      const nav = performance.getEntriesByType("navigation")[0]; if (nav) out.load_ms = Math.round(nav.loadEventEnd || nav.domContentLoadedEventEnd || 0);
    } catch (e) {}
    return out;
  }

  // ---- states board: ?__state=hover|focus|active|empty|loading|error|dark ----
  function forceStates(st) {
    const pseudo = { hover: ":hover", focus: ":focus", active: ":active" }[st];
    const extra = [];
    const walk = rules => { for (const r of rules) {
      if (r.cssRules && r.media) { if (st === "dark" && /prefers-color-scheme:\s*dark/.test(r.media.mediaText)) for (const x of r.cssRules) extra.push(x.cssText); else walk(r.cssRules); continue; }
      if (pseudo && r.selectorText && r.selectorText.includes(pseudo)) {
        const sel = r.selectorText.split(",").filter(s => s.includes(pseudo)).map(s => s.replace(/:focus-(visible|within)/g, ":focus").split(pseudo).join(`[data-bz-${st}]`)).join(",");
        extra.push(sel + "{" + r.style.cssText + "}");
      }
    } };
    for (const s of document.styleSheets) { try { walk(s.cssRules); } catch (e) {} }
    const style = document.createElement("style"); style.setAttribute("data-blaze-ui", "");
    let base = "";
    const ctl = "a,button,input,select,textarea,[role=button],[tabindex],.btn,.button";
    if (pseudo) {
      document.querySelectorAll(ctl).forEach(el => el.setAttribute("data-bz-" + st, ""));
      if (st === "focus") base += `[data-bz-focus]{outline:2px solid Highlight;outline-offset:2px}`;
    }
    if (st === "dark") {
      document.documentElement.style.colorScheme = "dark";
      document.documentElement.classList.add("dark"); document.documentElement.setAttribute("data-theme", "dark");
      if (!extra.length && !document.querySelector("[data-theme],.dark *")) base += `html{filter:invert(.92) hue-rotate(180deg)}img,video,picture,svg image{filter:invert(1) hue-rotate(180deg)}`;
    }
    if (st === "loading") {
      const marked = document.querySelectorAll("[data-loading],.loading,.skeleton,[aria-busy]");
      if (marked.length) marked.forEach(el => { el.hidden = false; el.style.display = el.style.display === "none" ? "" : el.style.display; el.setAttribute("aria-busy", "true"); });
      else base += `main h1,main h2,main h3,main p,main li,main img,main button,section h1,section h2,section p,section img,section button,article *{color:transparent!important;background:linear-gradient(90deg,#e8e8e8 25%,#f4f4f4 50%,#e8e8e8 75%)!important;background-size:200% 100%!important;animation:bzsk 1.2s infinite linear;border-color:transparent!important;border-radius:4px}main img,section img{opacity:.0}@keyframes bzsk{to{background-position:-200% 0}}`;
    }
    if (st === "empty") {
      const marked = document.querySelectorAll("[data-empty],.empty,.empty-state");
      if (marked.length) marked.forEach(el => { el.hidden = false; if (getComputedStyle(el).display === "none") el.style.display = "block"; });
      document.querySelectorAll("ul,ol,tbody,[role=list],.list,.grid,.cards").forEach(list => {
        if (list.closest("nav,header,footer") || isUI(list) || list.children.length < 2) return;
        [...list.children].forEach(c => c.style.display = "none");
        if (!marked.length) { const n = document.createElement(list.tagName === "TBODY" ? "tr" : list.tagName === "UL" || list.tagName === "OL" ? "li" : "div"); n.setAttribute("data-blaze-ui", ""); n.style.cssText = "display:block;padding:24px;text-align:center;color:#888;font:14px system-ui;border:1px dashed #ccc;list-style:none"; n.textContent = "Nothing here yet (no empty state designed)"; list.appendChild(n); }
      });
    }
    if (st === "error") {
      document.querySelectorAll("input:not([type=hidden]):not([type=checkbox]):not([type=radio]),select,textarea").forEach(el => { el.setAttribute("aria-invalid", "true"); el.classList.add("error", "is-invalid", "invalid"); });
      const marked = document.querySelectorAll("[data-error],.error-message,.field-error,.invalid-feedback,[role=alert]");
      marked.forEach(el => { el.hidden = false; if (getComputedStyle(el).display === "none") el.style.display = "block"; });
      base += `[aria-invalid=true]{outline:0;border-color:#d93025!important;box-shadow:0 0 0 1px #d93025}`;
    }
    style.textContent = base + extra.join("\n");
    document.head.appendChild(style);
  }
  const state = Q.get("__state");
  if (state) { if (document.readyState === "loading") addEventListener("DOMContentLoaded", () => forceStates(state)); else forceStates(state); }

  // ---- hot reload + phone comments (when not inside Blaze's own frame) ----
  if (!inFrame && window.EventSource) {
    try { const es = new EventSource("/__blaze/live"); es.onmessage = e => { if (e.data === "reload") location.reload(); }; } catch (e) {}
  }
  if (!inFrame && /(?:^|; )bzphone=1/.test(document.cookie)) {
    const tip = document.createElement("div"); tip.setAttribute("data-blaze-ui", "");
    tip.style.cssText = "position:fixed;left:50%;bottom:14px;transform:translateX(-50%);z-index:2147483647;background:#111;color:#fff;font:600 12px/1 system-ui;padding:9px 12px;border-radius:999px;opacity:.88;pointer-events:none;transition:opacity .4s";
    tip.textContent = "Blaze preview · long-press to comment"; document.documentElement.appendChild(tip);
    setTimeout(() => tip.style.opacity = "0", 3500);
    const pins = () => fetch("/__blaze/comment").then(r => r.json()).then(cs => {
      document.querySelectorAll("[data-bz-pin]").forEach(p => p.remove());
      (cs || []).filter(c => !c.resolved && (!c.path || c.path === location.pathname)).forEach((c, i) => {
        const p = document.createElement("div"); p.setAttribute("data-blaze-ui", ""); p.setAttribute("data-bz-pin", "");
        p.style.cssText = `position:absolute;z-index:2147483646;left:calc(${c.x * 100}% - 11px);top:${c.y - 11}px;width:22px;height:22px;border-radius:50% 50% 50% 0;background:#1F4BD8;color:#fff;font:700 11px/22px system-ui;text-align:center;box-shadow:0 2px 6px rgba(0,0,0,.3)`;
        p.textContent = i + 1; p.title = c.text; p.onclick = () => alert(c.text);
        document.body.appendChild(p);
      });
    }).catch(() => {});
    pins();
    let timer = 0, sx = 0, sy = 0;
    addEventListener("touchstart", e => {
      const t = e.touches[0]; sx = t.pageX; sy = t.pageY; const el = e.target;
      clearTimeout(timer);
      timer = setTimeout(() => {
        if (isUI(el)) return;
        const text = prompt("Comment on this spot:"); if (!text) return;
        fetch("/__blaze/comment", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ sel: selector(el), text, x: sx / document.documentElement.scrollWidth, y: sy, path: location.pathname }) }).then(pins);
      }, 650);
    }, { passive: true });
    ["touchend", "touchmove", "touchcancel"].forEach(ev => addEventListener(ev, e => { if (ev !== "touchmove" || Math.hypot(e.touches[0].pageX - sx, e.touches[0].pageY - sy) > 10) clearTimeout(timer); }, { passive: true }));
  }
  post({ blaze: "ready", title: document.title });
})();
