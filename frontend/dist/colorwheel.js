// v0.20 Design studio: the colour wheel. Every colour in Design (the inspector, the
// selection bar's restyle, swap colour, 3D parts, the brand kit) picks from a full HSV
// wheel with a brightness slider, an optional opacity slider, a hex field, the system
// picker, the brand kit and page swatches and the recent colours.
//   BlazeColor.picker(host, { value, alpha, swatches, onChange, onCommit, size })
//   BlazeColor.pop(anchor, { value, alpha, onChange, onCommit })   floating wheel
//   BlazeColor.field({ value, alpha, label, onChange, onCommit })  swatch + hex input
//   BlazeColor.upgrade(input[type=color])                          wheel for a native input
(() => {
  const CU = {
    hex2rgb(h) { h = h.trim().replace("#", ""); if (h.length === 3 || h.length === 4) h = h.split("").map(c => c + c).join(""); if (h.length === 6) h += "ff"; const n = parseInt(h, 16); return { r: (n >>> 24) & 255, g: (n >>> 16) & 255, b: (n >>> 8) & 255, a: (n & 255) / 255 }; },
    rgb2hex({ r, g, b, a = 1 }) { const t = x => Math.max(0, Math.min(255, Math.round(x))).toString(16).padStart(2, "0"); return "#" + t(r) + t(g) + t(b) + (a < 0.999 ? t(a * 255) : ""); },
    rgb2hsv({ r, g, b }) { r /= 255; g /= 255; b /= 255; const M = Math.max(r, g, b), m = Math.min(r, g, b), d = M - m; let h = 0; if (d) { if (M === r) h = ((g - b) / d) % 6; else if (M === g) h = (b - r) / d + 2; else h = (r - g) / d + 4; h *= 60; if (h < 0) h += 360; } return { h, s: M ? d / M : 0, v: M }; },
    hsv2rgb(h, s, v) { const f = n => { const k = (n + h / 60) % 6; return v - v * s * Math.max(0, Math.min(k, 4 - k, 1)); }; return { r: f(5) * 255, g: f(3) * 255, b: f(1) * 255 }; },
    css2hex(c) { const m = (c || "").match(/[\d.]+/g); if (!m || m.length < 3) return "#000000"; return CU.rgb2hex({ r: +m[0], g: +m[1], b: +m[2], a: m[3] !== undefined ? +m[3] : 1 }); },
    valid(h) { return /^#?([0-9a-f]{3,4}|[0-9a-f]{6}|[0-9a-f]{8})$/i.test((h || "").trim()); },
    norm(h) { h = (h || "").trim(); if (/^rgba?\(/i.test(h)) return CU.css2hex(h); return h.startsWith("#") ? h : "#" + h; },
    solid(h) { return CU.rgb2hex({ ...CU.hex2rgb(CU.norm(h)), a: 1 }); },
  };
  const RECENT = (() => { try { return JSON.parse(localStorage.getItem("blaze.recentColors") || "[]").slice(0, 9); } catch (e) { return []; } })();
  const saveRecent = () => { try { localStorage.setItem("blaze.recentColors", JSON.stringify(RECENT)); } catch (e) {} };
  let palette = null, paletteAt = 0;
  async function getPalette() {
    if (palette && Date.now() - paletteAt < 4000) return palette;
    try { const a = window.go && window.go.main && window.go.main.App; palette = (a && a.DesignPalette && await a.DesignPalette()) || { brand: [], page: [] }; } catch (e) { palette = { brand: [], page: [] }; }
    paletteAt = Date.now();
    return palette;
  }

  function picker(host, { value = "#4DA8FF", alpha = true, swatches = null, onChange = () => {}, onCommit = () => {}, size = 160 } = {}) {
    host.classList.add("cw");
    host.innerHTML = `<div class="cw-top"><div class="cw-wheel"><canvas width="${size * 2}" height="${size * 2}" style="width:${size}px;height:${size}px"></canvas><i class="cw-knob"></i></div>
      <div class="cw-side"><div class="cw-prev"><i></i></div><label class="cw-hexl">HEX<input class="cw-hex" spellcheck="false" maxlength="9" aria-label="Hex colour"></label>
      <label class="cw-sys" title="Open the system colour picker"><input type="color" aria-label="System colour picker"><span>System</span></label></div></div>
      <div class="cw-sl"><span>Brightness</span><input type="range" class="cw-v" min="0" max="100" aria-label="Brightness"></div>
      <div class="cw-sl cw-al"${alpha ? "" : " hidden"}><span>Opacity</span><input type="range" class="cw-a" min="0" max="100" aria-label="Opacity"></div>
      <div class="cw-sw"></div><div class="cw-recent"></div>`;
    const q = s => host.querySelector(s);
    const cv = q("canvas"), ctx = cv.getContext && cv.getContext("2d"), knob = q(".cw-knob"), hexI = q(".cw-hex"), vI = q(".cw-v"), aI = q(".cw-a"), sys = q("input[type=color]"), prev = q(".cw-prev i"), rc = q(".cw-recent"), sw = q(".cw-sw");
    let h = 0, s = 0, v = 1, a = 1, drawnV = -1;
    function drawWheel() {
      if (!ctx || Math.abs(drawnV - v) < 0.002) return; drawnV = v;
      const W = cv.width, R = W / 2, img = ctx.createImageData(W, W), d8 = img.data;
      for (let y = 0; y < W; y++) for (let x = 0; x < W; x++) {
        const dx = x - R, dy = y - R, d = Math.hypot(dx, dy), i = (y * W + x) * 4; if (d > R) continue;
        const c = CU.hsv2rgb((Math.atan2(dy, dx) * 57.29578 + 360) % 360, d / R, v);
        d8[i] = c.r; d8[i + 1] = c.g; d8[i + 2] = c.b; d8[i + 3] = d > R - 2 ? Math.max(0, (R - d) / 2 * 255) : 255;
      }
      ctx.putImageData(img, 0, 0);
    }
    const hex = () => CU.rgb2hex({ ...CU.hsv2rgb(h, s, v), a: alpha ? a : 1 });
    function sync(skipHex) {
      const rgb = CU.hsv2rgb(h, s, v), solid = CU.rgb2hex(rgb), full = CU.rgb2hex(CU.hsv2rgb(h, s, 1)), hx = hex();
      if (!skipHex) hexI.value = hx.toUpperCase();
      sys.value = solid; vI.value = Math.round(v * 100); aI.value = Math.round(a * 100); prev.style.background = hx;
      const R = size / 2, rad = h * Math.PI / 180;
      knob.style.left = (R + Math.cos(rad) * s * R) + "px"; knob.style.top = (R + Math.sin(rad) * s * R) + "px"; knob.style.background = solid;
      vI.style.background = `linear-gradient(90deg,#000,${full})`;
      aI.style.background = `linear-gradient(90deg,transparent,${solid}),repeating-conic-gradient(#999 0 25%,#ddd 0 50%) 0 0/8px 8px`;
    }
    function emit(skipHex) { drawWheel(); sync(skipHex); onChange(hex()); }
    function from(hx) { const c = CU.hex2rgb(CU.norm(hx)), o = CU.rgb2hsv(c); if (o.s > 0.001) h = o.h; s = o.s; v = o.v; a = c.a; }
    function commit() {
      const hx = hex().toUpperCase(), i = RECENT.indexOf(hx);
      if (i !== 0) { if (i > 0) RECENT.splice(i, 1); RECENT.unshift(hx); RECENT.splice(9); saveRecent(); drawRecent(); }
      onCommit(hex());
    }
    function set(hx, silent) { if (!CU.valid(CU.norm(hx))) return; from(hx); drawWheel(); sync(); if (!silent) onChange(hex()); }
    const chip = (c, title) => { const b = document.createElement("button"); b.type = "button"; b.style.background = c; b.title = title || c; b.setAttribute("aria-label", c); b.onclick = () => { set(c); commit(); }; return b; };
    function drawRecent() { rc.innerHTML = RECENT.length ? "<span>Recent</span>" : ""; RECENT.forEach(c => rc.append(chip(c))); }
    function drawSwatches(p) {
      sw.innerHTML = "";
      const brand = (p && p.brand) || [], page = ((p && p.page) || []).slice(0, 10);
      if (brand.length) { sw.insertAdjacentHTML("beforeend", "<span>Brand</span>"); brand.forEach(c => sw.append(chip(c, "Brand " + c))); }
      if (page.length) { sw.insertAdjacentHTML("beforeend", `<span${brand.length ? ' class="gap"' : ""}>Page</span>`); page.forEach(c => sw.append(chip(c, "On this page " + c))); }
    }
    const pick = e => {
      const r = cv.getBoundingClientRect(), k = r.width / size || 1, R = size / 2, dx = (e.clientX - r.left) / k - R, dy = (e.clientY - r.top) / k - R;
      h = (Math.atan2(dy, dx) * 57.29578 + 360) % 360; s = Math.min(1, Math.hypot(dx, dy) / R); if (v < 0.05) v = 1; emit();
    };
    cv.addEventListener("pointerdown", e => { e.preventDefault(); e.stopPropagation(); try { cv.setPointerCapture(e.pointerId); } catch (x) {} pick(e); cv.onpointermove = pick; });
    cv.addEventListener("pointerup", () => { cv.onpointermove = null; commit(); });
    vI.oninput = () => { v = vI.value / 100; emit(); }; vI.onchange = commit;
    aI.oninput = () => { a = aI.value / 100; emit(); }; aI.onchange = commit;
    hexI.oninput = () => { const t = CU.norm(hexI.value); if (CU.valid(t)) { from(t); if (!alpha) a = 1; emit(true); } };
    hexI.onkeydown = e => { e.stopPropagation(); if (e.key === "Enter") { e.preventDefault(); hexI.blur(); } };
    hexI.onblur = () => { sync(); commit(); };
    sys.oninput = () => { const keepA = a; from(sys.value); a = keepA; emit(); }; sys.onchange = commit;
    host.addEventListener("keydown", e => e.stopPropagation()); // arrows in the fields never nudge the selection
    drawRecent(); from(value); if (!alpha) a = 1; drawWheel(); sync();
    if (swatches) drawSwatches(swatches); else getPalette().then(p => { if (host.isConnected) drawSwatches(p); });
    return { set, get: hex, commit, el: host };
  }

  // A floating wheel next to an anchor; closes on Escape or a press outside.
  let popEl = null;
  function closePop() { if (popEl) { popEl.remove(); popEl = null; } }
  function pop(anchor, opts = {}) {
    if (popEl && popEl._anchor === anchor) { closePop(); return null; }
    closePop();
    const p = document.createElement("div"); p.className = "cwpop"; p._anchor = anchor; popEl = p;
    if (opts.title) p.innerHTML = `<div class="cwpop-hd">${opts.title}</div>`;
    const host = document.createElement("div"); p.append(host);
    document.body.append(p);
    const pk = picker(host, opts);
    const r = anchor.getBoundingClientRect(), w = p.offsetWidth || 300, ht = p.offsetHeight || 330;
    let left = Math.min(Math.max(8, r.left), innerWidth - w - 8), top = r.bottom + 6;
    if (top + ht > innerHeight - 8) top = Math.max(8, r.top - ht - 6);
    p.style.left = left + "px"; p.style.top = top + "px";
    return pk;
  }
  addEventListener("pointerdown", e => { if (popEl && !popEl.contains(e.target) && e.target !== popEl._anchor && !(popEl._anchor && popEl._anchor.contains(e.target))) closePop(); }, true);
  addEventListener("keydown", e => { if (e.key === "Escape" && popEl) { e.stopPropagation(); closePop(); } }, true);

  // A swatch button plus a hex field; the swatch opens the wheel.
  function field({ value = "#000000", alpha = true, label = "", onChange = () => {}, onCommit = () => {} } = {}) {
    const wrap = document.createElement("div"); wrap.className = "cwf";
    wrap.innerHTML = `${label ? `<span class="cwf-l">${label}</span>` : ""}<button type="button" class="cwf-sw" title="Open the colour wheel"><i></i></button><input class="cwf-hex" spellcheck="false" maxlength="9" aria-label="${label || "Colour"} hex">`;
    const sw = wrap.querySelector(".cwf-sw"), dot = sw.querySelector("i"), inp = wrap.querySelector(".cwf-hex");
    let cur = CU.norm(value);
    const show = c => { cur = c; dot.style.background = c; inp.value = c.toUpperCase(); };
    show(cur);
    sw.onclick = e => { e.stopPropagation(); pop(sw, { value: cur, alpha, title: label, onChange: c => { show(c); onChange(c); }, onCommit: c => { show(c); onCommit(c); } }); };
    inp.onkeydown = e => { e.stopPropagation(); if (e.key === "Enter") { e.preventDefault(); inp.blur(); } };
    inp.onchange = () => { const t = CU.norm(inp.value); if (CU.valid(t)) { show(t); onChange(t); onCommit(t); } else inp.value = cur.toUpperCase(); };
    return { el: wrap, set: show, get: () => cur };
  }

  // Give a native <input type=color> the wheel: the input stays (its value and events drive
  // the existing code), a swatch opens the wheel and writes back into it.
  function upgrade(input) {
    if (!input || input._cw || input.type !== "color") return;
    input._cw = true;
    const sw = document.createElement("button"); sw.type = "button"; sw.className = "cwf-sw cwup"; sw.title = input.title || "Open the colour wheel";
    const dot = document.createElement("i"); sw.append(dot);
    const paint = () => { dot.style.background = input.value; };
    paint(); input.addEventListener("input", paint);
    input.classList.add("cw-native"); input.after(sw);
    sw.onclick = e => {
      e.preventDefault(); e.stopPropagation();
      pop(sw, {
        value: input.value, alpha: false,
        onChange: c => { input.value = CU.solid(c); paint(); input.dispatchEvent(new Event("input", { bubbles: true })); },
        onCommit: () => input.dispatchEvent(new Event("change", { bubbles: true })),
      });
    };
    // Keep the swatch in step when code sets input.value directly.
    const d = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value");
    if (d && d.set) Object.defineProperty(input, "value", { configurable: true, get() { return d.get.call(this); }, set(v) { d.set.call(this, v); paint(); } });
  }
  function upgradeAll(root) { (root || document).querySelectorAll('input[type="color"]:not(.cw-native)').forEach(i => { if (!i.closest(".cw")) upgrade(i); }); }

  window.BlazeColor = { picker, pop, field, upgrade, upgradeAll, close: closePop, util: CU, palette: getPalette };
})();
