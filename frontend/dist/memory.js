// Blaze Memory screen (v0.5): memories, skills and projects as a force graph on one canvas.
// Square particle-dot nodes, ink + one blue accent, mono uppercase labels.
// Cheap by design: Barnes-Hut repulsion, the simulation cools and stops, only what is on
// screen is drawn, labels only when zoomed in, nothing runs while the screen is hidden.
(() => {
  const RM = window.BlazeMotion.query;   // OS reduced motion or Settings › Reduce motion
  const SORT_AT = 0.78;          // replay: share of the timeline spent adding memories
  const MAX_PARTICLES = 700;
  let M = null;                  // the live screen (one at a time)

  const MI = {
    graph: '<rect x="1.5" y="2" width="3.5" height="3.5"/><rect x="11" y="3.5" width="3.5" height="3.5"/><rect x="5.5" y="10.5" width="3.5" height="3.5"/><path d="M5 4.2 11 5.1M11.6 7 8.4 10.5M3.6 5.5l2.8 5"/>',
    play: '<path d="M4.5 3v10l8-5z"/>', pause: '<path d="M5 3.5v9M11 3.5v9"/>',
    minus: '<path d="M3.5 8h9"/>', plus: '<path d="M8 3.5v9M3.5 8h9"/>', fit: '<path d="M2.5 6V2.5H6M10 2.5h3.5V6M13.5 10v3.5H10M6 13.5H2.5V10"/>',
    search: '<rect x="2.5" y="2.5" width="8" height="8"/><path d="m10.5 10.5 3 3"/>', x: '<path d="m4 4 8 8M12 4l-8 8"/>', chev: '<path d="m6 4 4 4-4 4"/>',
    sparks: '<path d="M8 1.5v4M8 10.5v4M1.5 8h4M10.5 8h4"/>', open: '<path d="M9 2.5h4.5V7M13.5 2.5 7.5 8.5M11.5 9.5v4h-9v-9h4"/>',
  };
  const ic = (n, cls = "i sm") => `<svg class="${cls}" viewBox="0 0 16 16">${MI[n]}</svg>`;
  const fmtDate = ms => ms ? new Date(ms).toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" }) : "";
  const fmtFull = ms => ms ? new Date(ms).toLocaleString(undefined, { month: "short", day: "numeric", year: "numeric", hour: "2-digit", minute: "2-digit" }) : "Unknown";
  const KIND = { memory: "Memory", skill: "Skill", project: "Project" };
  const smooth = x => x <= 0 ? 0 : x >= 1 ? 1 : x * x * (3 - 2 * x);

  // ---------------- Barnes-Hut quadtree in pooled typed arrays (no garbage per tick) ----------------
  const Q = { cap: 0, n: 0 };
  function qGrow(cap) {
    const f = (T, o, n) => { const a = new T(n); if (o) a.set(o); return a; };
    for (const k of ["cx", "cy", "m", "x0", "y0", "s"]) Q[k] = f(Float64Array, Q[k], cap);
    Q.body = f(Int32Array, Q.body, cap); Q.ch = f(Int32Array, Q.ch, cap * 4); Q.cap = cap;
  }
  function qNode(x0, y0, s) {
    if (Q.n >= Q.cap) qGrow(Q.cap * 2);
    const q = Q.n++;
    Q.x0[q] = x0; Q.y0[q] = y0; Q.s[q] = s; Q.m[q] = 0; Q.cx[q] = 0; Q.cy[q] = 0; Q.body[q] = -1;
    Q.ch[q * 4] = Q.ch[q * 4 + 1] = Q.ch[q * 4 + 2] = Q.ch[q * 4 + 3] = -1;
    return q;
  }
  function qChild(q, x, y) {
    const h = Q.s[q] / 2, r = x >= Q.x0[q] + h ? 1 : 0, b = y >= Q.y0[q] + h ? 1 : 0, k = r + b * 2;
    let c = Q.ch[q * 4 + k];
    if (c < 0) { c = qNode(Q.x0[q] + r * h, Q.y0[q] + b * h, h); Q.ch[q * 4 + k] = c; }
    return c;
  }
  function qInsert(b, x, y, X, Y) {
    let q = 0;
    for (let depth = 0; ; depth++) {
      if (Q.m[q] === 0) { Q.body[q] = b; Q.m[q] = 1; Q.cx[q] = x; Q.cy[q] = y; return; }
      if (Q.body[q] >= 0) {
        if (depth > 32) { const m = Q.m[q]; Q.cx[q] = (Q.cx[q] * m + x) / (m + 1); Q.cy[q] = (Q.cy[q] * m + y) / (m + 1); Q.m[q] = m + 1; return; }
        const o = Q.body[q]; Q.body[q] = -2;
        const oq = qChild(q, X[o], Y[o]); Q.body[oq] = o; Q.m[oq] = 1; Q.cx[oq] = X[o]; Q.cy[oq] = Y[o];
      }
      const m = Q.m[q]; Q.cx[q] = (Q.cx[q] * m + x) / (m + 1); Q.cy[q] = (Q.cy[q] * m + y) / (m + 1); Q.m[q] = m + 1;
      q = qChild(q, x, y);
    }
  }
  qGrow(1024);
  const qStack = new Int32Array(4096);

  // ---------------- view ----------------
  window.memoryView = async function memoryView(el, arg) {
    if (M) M.dispose();
    const root = h("div", "mem"); el.append(root);
    root.innerHTML = `<div class="mem-load"><span class="label">Loading memory…</span></div>`;
    let g;
    try { g = await API().MemoryGraph(); } catch (e) { g = null; }
    if (!root.isConnected) return;
    if (g && g.notice) return updateState(root, g.notice);
    if (!g || !(g.nodes || []).length) return emptyState(root);
    M = new MemoryScreen(root, g, arg);
  };

  // blaze.db was written by a newer Blaze: memory is off until this one is updated.
  function updateState(root, notice) {
    root.className = "mem mem-empty";
    root.innerHTML = `<div class="label acc">Memory</div><h1>Please update Blaze</h1>
      <p class="lead"></p>
      <div class="mem-eacts"><button class="btn line sm" data-a="update">Update Blaze</button></div>`;
    $(".lead", root).textContent = notice;
    $("[data-a=update]", root).onclick = () => { show("work"); if (typeof SelfUpdate !== "undefined") SelfUpdate.run(); };
  }

  function emptyState(root) {
    root.className = "mem mem-empty";
    root.innerHTML = `<div class="mem-orb"><canvas width="240" height="240"></canvas></div>
      <div class="label acc">Memory</div><h1>Nothing remembered yet</h1>
      <p class="lead">As you work, Blaze saves lasting facts here: how you like things, your setup, your projects. Try “remember that I use pnpm”.</p>
      <div class="mem-eacts"><button class="btn line sm" data-a="chat">Start a chat</button><button class="btn ghost sm" data-a="edit">Write memories by hand</button></div>`;
    const cv = $("canvas", root); cv.width = 240 * devicePixelRatio; cv.height = 240 * devicePixelRatio; cv.style.width = cv.style.height = "120px";
    if (typeof particleOrb === "function") particleOrb(cv, 120);
    $("[data-a=chat]", root).onclick = () => show("work");
    $("[data-a=edit]", root).onclick = () => show("settings", "memory");
  }

  class MemoryScreen {
    constructor(root, g, arg) {
      this.root = root; root.innerHTML = "";
      this.cam = { x: 0, y: 0, z: 1 }; this.camT = null;
      this.hover = -1; this.sel = -1; this.query = ""; this.match = null; this.catHi = -1;
      this.alpha = 1; this.autoFit = true; this.particles = []; this.raf = 0; this.idleAt = performance.now(); this.lastDraw = 0;
      this.R = { on: false, playing: false, t: 1, speed: 1, last: 0, shown: 0 };
      this.collapsed = new Set();
      this.build();
      this.resize();
      this.load(g, true);
      this.listen();
      if (arg) { const i = this.nodes.findIndex(n => n.id === arg); if (i >= 0) this.select(i, true); }
    }

    // ---------- DOM ----------
    build() {
      const r = this.root;
      r.innerHTML = `
        <aside class="mlist">
          <div class="mhead"><div class="label acc">Memory</div><div class="mcount"></div></div>
          <label class="msearch">${ic("search")}<input placeholder="Search memories" spellcheck="false"><button class="mclr" title="Clear">${ic("x")}</button></label>
          <div class="mgroups"></div>
          <div class="mfoot"></div>
        </aside>
        <div class="mstage">
          <canvas class="mcv"></canvas>
          <div class="mtl label weak">Graph</div>
          <div class="mtools"><button class="mt" data-z="out" title="Zoom out">${ic("minus")}</button><button class="mt" data-z="in" title="Zoom in">${ic("plus")}</button><button class="mt" data-z="fit" title="Fit (double-click)">${ic("fit")}</button></div>
          <div class="mlegend"><span><i class="lg mem"></i>Memory</span><span><i class="lg skill"></i>Skill</span><span><i class="lg proj"></i>Project</span></div>
          <div class="mtime">
            <button class="mplay" title="Replay how Blaze's memory grew">${ic("play")}</button>
            <div class="mscrub"><canvas class="mspark"></canvas><input type="range" min="0" max="1000" value="1000" aria-label="Timeline"></div>
            <div class="mwhen"><span class="mdate label ink"></span><span class="mnum label weak"></span></div>
            <div class="seg mspeed"><button data-s="1" class="on">1×</button><button data-s="4">4×</button><button data-s="16">16×</button></div>
          </div>
        </div>
        <aside class="mpanel" hidden></aside>`;
      this.cv = $(".mcv", r); this.ctx = this.cv.getContext("2d");
      this.stage = $(".mstage", r); this.panel = $(".mpanel", r); this.groups = $(".mgroups", r);
      this.input = $(".msearch input", r); this.range = $(".mscrub input", r); this.spark = $(".mspark", r);
      this.readColors();
    }

    readColors() {
      const cs = getComputedStyle(document.documentElement), v = k => cs.getPropertyValue(k).trim();
      this.col = { ink: v("--ink") || "#111110", acc: v("--accent") || "#E6E6EA", paper: v("--paper") || "#fbfbfa", weak: v("--weak"), sub: v("--sub"), mono: v("--mono") || "monospace", dark: document.documentElement.dataset.theme === "dark" };
      this.labelW = new Map();
    }

    // ---------- data ----------
    load(g, first) {
      const old = new Map((this.nodes || []).map(n => [n.id, n]));
      this.g = g; this.cats = g.cats || [];
      const nodes = this.nodes = (g.nodes || []).map((n, i) => ({ ...n, i, x: 0, y: 0, vx: 0, vy: 0, deg: 0, nb: [], vis: true, born: 0, fx: null }));
      this.edges = (g.edges || []).filter(e => nodes[e.a] && nodes[e.b]).map(e => ({ ...e, born: 0 }));
      this.edges.forEach(e => { nodes[e.a].deg++; nodes[e.b].deg++; nodes[e.a].nb.push(e.b); nodes[e.b].nb.push(e.a); });
      nodes.forEach(n => { n.size = n.kind === "project" ? 9 + Math.sqrt(n.deg) * 1.2 : n.kind === "skill" ? 6 + Math.sqrt(n.deg) : 4.5 + Math.sqrt(n.deg) * 1.5; });
      this.order = nodes.map(n => n.i).sort((a, b) => (nodes[a].created || 0) - (nodes[b].created || 0) || a - b);
      const mems = nodes.filter(n => n.kind === "memory");
      this.newest = mems.length ? mems.reduce((a, b) => (b.created || 0) > (a.created || 0) ? b : a).i : -1;
      this.layoutAnchors();
      nodes.forEach((n, k) => {
        const o = old.get(n.id) || (this.carry && this.carry.id === n.id ? this.carry : null);
        if (o) { n.x = o.x; n.y = o.y; return; }
        const a = this.anchor[n.cat], j = k * 2.39996, rr = 6 * Math.sqrt(k % 97 + 1);
        n.x = a.x + Math.cos(j) * rr; n.y = a.y + Math.sin(j) * rr;
      });
      this.carry = null;
      this.alpha = first ? 1 : Math.max(this.alpha, .35);
      if (first && RM.matches) this.settle(400);
      this.drawList(); this.drawFoot(); this.drawSpark(); this.updateTime();
      $(".mcount", this.root).textContent = this.countText();
      if (first) this.fit(true);
      this.kick();
    }

    countText() {
      const c = { memory: 0, skill: 0, project: 0 };
      this.nodes.forEach(n => c[n.kind]++);
      return [c.memory + (c.memory === 1 ? " memory" : " memories"), c.skill && c.skill + (c.skill === 1 ? " skill" : " skills"), c.project && c.project + (c.project === 1 ? " project" : " projects")].filter(Boolean).join(" · ");
    }

    // Category anchors on a ring sized by how many nodes there are; big categories get more room.
    layoutAnchors() {
      const C = this.cats.length || 1, N = this.nodes.length;
      const R = C === 1 ? 0 : 90 + Math.sqrt(N) * 30;
      const total = this.cats.reduce((s, c) => s + Math.sqrt(c.count), 0) || 1;
      let acc = -Math.PI / 2;
      this.anchor = this.cats.map(c => {
        const span = Math.sqrt(c.count) / total * Math.PI * 2, a = acc + span / 2; acc += span;
        return { x: Math.cos(a) * R, y: Math.sin(a) * R * .78 };
      });
      if (!this.anchor.length) this.anchor = [{ x: 0, y: 0 }];
    }

    async reload() {
      let g; try { g = await API().MemoryGraph(); } catch (e) { return; }
      if (!this.root.isConnected) return;
      if (!(g.nodes || []).length) { this.dispose(); M = null; return emptyState(this.root); }
      const selId = this.sel >= 0 ? this.nodes[this.sel].id : null;
      this.sel = this.hover = -1;
      this.load(g, false);
      const i = selId ? this.nodes.findIndex(n => n.id === selId) : -1;
      if (i >= 0) this.select(i, false); else this.closePanel();
      $(".mcount", this.root).textContent = this.countText();
    }

    // ---------- simulation ----------
    tick() {
      const ns = this.nodes, N = ns.length, a = this.alpha;
      const X = this._X && this._X.length >= N ? this._X : (this._X = new Float64Array(N * 2 + 16)), Y = this._Y && this._Y.length >= N ? this._Y : (this._Y = new Float64Array(N * 2 + 16));
      let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity, nv = 0;
      for (const n of ns) { X[n.i] = n.x; Y[n.i] = n.y; if (!n.vis) continue; nv++; if (n.x < x0) x0 = n.x; if (n.y < y0) y0 = n.y; if (n.x > x1) x1 = n.x; if (n.y > y1) y1 = n.y; }
      if (!nv) return;
      Q.n = 0; const s = Math.max(x1 - x0, y1 - y0) + 2; qNode(x0 - 1, y0 - 1, s);
      for (const n of ns) if (n.vis) qInsert(n.i, n.x, n.y, X, Y);
      // Repulsion (Barnes-Hut, theta 0.9, capped range so clusters don't push each other away forever)
      const strength = -38, theta2 = .81, dmax2 = 520 * 520;
      for (const n of ns) {
        if (!n.vis) continue;
        let sp = 0; qStack[sp++] = 0;
        const xi = n.x, yi = n.y;
        while (sp) {
          const q = qStack[--sp]; if (Q.m[q] === 0) continue;
          let dx = Q.cx[q] - xi, dy = Q.cy[q] - yi, d2 = dx * dx + dy * dy;
          const leaf = Q.body[q] >= 0;
          if (leaf || Q.s[q] * Q.s[q] < theta2 * d2) {
            if (leaf && Q.body[q] === n.i && Q.m[q] === 1) continue;
            if (d2 > dmax2) continue;
            if (d2 < 1) { dx = (n.i % 7 - 3) * .1 || .1; dy = (n.i % 5 - 2) * .1 || .1; d2 = 1; }
            const f = strength * Q.m[q] * a / d2;
            n.vx += dx * f; n.vy += dy * f;
          } else if (sp < 4092) {
            const c = q * 4; for (let k = 0; k < 4; k++) if (Q.ch[c + k] >= 0) qStack[sp++] = Q.ch[c + k];
          }
        }
      }
      // Links pull together, more for strong links, less for busy nodes; links across
      // categories give way as the categories gather.
      const pull = this.catPull(), cross = 1 - .75 * pull;
      for (const e of this.edges) {
        const A = ns[e.a], B = ns[e.b]; if (!A.vis || !B.vis) continue;
        let dx = B.x + B.vx - A.x - A.vx, dy = B.y + B.vy - A.y - A.vy, l = Math.sqrt(dx * dx + dy * dy) || 1;
        const k = (l - 34) / l * a * (.12 + Math.min(e.w, 5) * .03) / Math.sqrt(Math.min(A.deg, B.deg) || 1) * (A.cat === B.cat ? 1 : cross);
        dx *= k; dy *= k;
        const bias = A.deg / (A.deg + B.deg);
        B.vx -= dx * (1 - bias); B.vy -= dy * (1 - bias); A.vx += dx * bias; A.vy += dy * bias;
      }
      // Categories gather around their anchors (off at the start of a replay, then eased in).
      const kc = pull * .16 * a, kg = (.004 + (1 - pull) * .02) * a;
      for (const n of ns) {
        if (!n.vis) continue;
        const an = this.anchor[n.cat] || this.anchor[0];
        n.vx += (an.x - n.x) * kc - n.x * kg; n.vy += (an.y - n.y) * kc - n.y * kg;
        if (n.fx != null) { n.x = n.fx; n.y = n.fy; n.vx = n.vy = 0; continue; }
        n.vx *= .58; n.vy *= .58; n.x += n.vx; n.y += n.vy;
      }
      this.alpha += ((this.R.playing ? .3 : 0) - this.alpha) * .028;
    }
    catPull() { return this.R.on ? smooth((this.R.t - SORT_AT) / (1 - SORT_AT)) : 1; }
    settle(max) { const keep = this.R.playing; for (let k = 0; k < max && this.alpha > .004; k++) this.tick(); this.R.playing = keep; this.alpha = 0; }

    // ---------- camera ----------
    resize() {
      const r = this.stage.getBoundingClientRect(), d = devicePixelRatio || 1;
      this.W = Math.max(1, r.width); this.H = Math.max(1, r.height); this.dpr = d;
      this.cv.width = Math.round(this.W * d); this.cv.height = Math.round(this.H * d);
      this.cv.style.width = this.W + "px"; this.cv.style.height = this.H + "px";
      const sp = this.spark, sr = sp.parentElement.getBoundingClientRect();
      sp.width = Math.max(1, Math.round(sr.width * d)); sp.height = Math.round(18 * d); this.drawSpark();
      this.kick();
    }
    toWorld(px, py) { const c = this.cam; return [(px - this.W / 2) / c.z + c.x, (py - this.H / 2) / c.z + c.y]; }
    zoomAt(px, py, f) {
      this.autoFit = false; this.replayCam = false;
      const c = this.cam, [wx, wy] = this.toWorld(px, py), z = Math.min(6, Math.max(.06, c.z * f));
      c.x = wx - (px - this.W / 2) / z; c.y = wy - (py - this.H / 2) / z; c.z = z; this.camT = null; this.poke();
    }
    fit(now) {
      let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
      for (const n of this.nodes) { if (!n.vis) continue; x0 = Math.min(x0, n.x); y0 = Math.min(y0, n.y); x1 = Math.max(x1, n.x); y1 = Math.max(y1, n.y); }
      if (x0 === Infinity) { x0 = y0 = -100; x1 = y1 = 100; }
      // Fit the settled layout, not the first frame of it.
      if (now && !RM.matches && this.alpha > .5) { const R = 90 + Math.sqrt(this.nodes.length) * 30 + 70; x0 = Math.min(x0, -R); x1 = Math.max(x1, R); y0 = Math.min(y0, -R * .8); y1 = Math.max(y1, R * .8); }
      const pad = 90, z = Math.min(2.2, Math.max(.06, Math.min((this.W - pad * 2) / (x1 - x0 || 1), (this.H - pad * 2 - 40) / (y1 - y0 || 1))));
      this.flyTo((x0 + x1) / 2, (y0 + y1) / 2 + 14 / z, z, now);
    }
    flyTo(x, y, z, now) {
      if (now || RM.matches) { this.cam = { x, y, z }; this.camT = null; } else this.camT = { x, y, z };
      this.poke();
    }

    // ---------- events ----------
    listen() {
      const cv = this.cv, ptr = new Map();
      let drag = null;
      this.ro = new ResizeObserver(() => this.resize()); this.ro.observe(this.stage);
      this.onVis = () => { if (!document.hidden) this.kick(); };
      document.addEventListener("visibilitychange", this.onVis);
      this.mo = new MutationObserver(() => { this.readColors(); this.drawSpark(); this.kick(); });
      this.mo.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
      this.onKey = e => {
        if (view !== "memory") return;
        if (e.key === "Escape" && document.activeElement !== this.input) { if (this.sel >= 0) this.closePanel(); else if (this.R.on) this.stopReplay(); }
        if (e.key === " " && e.target === document.body) { e.preventDefault(); this.togglePlay(); }
      };
      document.addEventListener("keydown", this.onKey);
      const pos = e => { const r = cv.getBoundingClientRect(); return [e.clientX - r.left, e.clientY - r.top]; };
      cv.addEventListener("pointerdown", e => {
        cv.setPointerCapture(e.pointerId); const [px, py] = pos(e); ptr.set(e.pointerId, [px, py]);
        if (ptr.size === 2) { const [a, b] = [...ptr.values()]; drag = { pinch: Math.hypot(a[0] - b[0], a[1] - b[1]) }; return; }
        const i = this.hit(px, py);
        drag = { px, py, sx: px, sy: py, node: i, moved: false };
        if (i >= 0) { const n = this.nodes[i]; n.fx = n.x; n.fy = n.y; }
        this.poke();
      });
      cv.addEventListener("pointermove", e => {
        const [px, py] = pos(e);
        if (ptr.has(e.pointerId)) ptr.set(e.pointerId, [px, py]);
        if (drag && drag.pinch && ptr.size === 2) {
          const [a, b] = [...ptr.values()], d = Math.hypot(a[0] - b[0], a[1] - b[1]);
          this.zoomAt((a[0] + b[0]) / 2, (a[1] + b[1]) / 2, d / drag.pinch); drag.pinch = d; return;
        }
        if (drag && !drag.pinch) {
          if (Math.hypot(px - drag.sx, py - drag.sy) > 4) drag.moved = true;
          if (drag.node >= 0) {
            if (drag.moved) { const [wx, wy] = this.toWorld(px, py), n = this.nodes[drag.node]; n.fx = wx; n.fy = wy; n.x = wx; n.y = wy; this.alpha = Math.max(this.alpha, .22); }
          } else { this.autoFit = false; this.replayCam = false; this.cam.x -= (px - drag.px) / this.cam.z; this.cam.y -= (py - drag.py) / this.cam.z; this.camT = null; cv.style.cursor = "grabbing"; }
          drag.px = px; drag.py = py; this.poke(); return;
        }
        const i = this.hit(px, py);
        if (i !== this.hover) { this.hover = i; cv.style.cursor = i >= 0 ? "pointer" : "grab"; this.poke(); }
      });
      const up = e => {
        ptr.delete(e.pointerId);
        if (!drag) return;
        if (drag.pinch) { if (!ptr.size) drag = null; return; }
        if (drag.node >= 0) { const n = this.nodes[drag.node]; n.fx = n.fy = null; }
        if (!drag.moved) { if (drag.node >= 0) this.select(drag.node, false); else if (this.sel >= 0) this.closePanel(); }
        cv.style.cursor = this.hover >= 0 ? "pointer" : "grab";
        drag = null; this.poke();
      };
      cv.addEventListener("pointerup", up); cv.addEventListener("pointercancel", up);
      cv.addEventListener("pointerleave", () => { if (!drag && this.hover >= 0) { this.hover = -1; this.poke(); } });
      cv.addEventListener("wheel", e => {
        e.preventDefault(); const [px, py] = pos(e);
        if (!e.ctrlKey && Math.abs(e.deltaX) > Math.abs(e.deltaY) * 1.5) { this.cam.x += e.deltaX / this.cam.z; this.camT = null; this.poke(); return; }
        this.zoomAt(px, py, Math.exp(-e.deltaY * (e.ctrlKey ? .012 : .0016) * (e.deltaMode === 1 ? 16 : 1)));
      }, { passive: false });
      cv.addEventListener("dblclick", e => { const [px, py] = pos(e); if (this.hit(px, py) < 0) this.fit(false); });
      $$(".mtools .mt", this.root).forEach(b => b.onclick = () => { const z = b.dataset.z; if (z === "fit") this.fit(false); else this.zoomAt(this.W / 2, this.H / 2, z === "in" ? 1.35 : 1 / 1.35); });
      this.input.oninput = () => this.search(this.input.value);
      this.input.onkeydown = e => { if (e.key === "Escape") { this.input.value = ""; this.search(""); this.input.blur(); } if (e.key === "Enter" && this.match && this.match.size) this.select([...this.match][0], true); };
      $(".mclr", this.root).onclick = () => { this.input.value = ""; this.search(""); };
      $(".mplay", this.root).onclick = () => this.togglePlay();
      this.range.oninput = () => { if (!this.R.on) this.startReplay(false); this.R.playing = false; this.setPlayIcon(); this.seek(this.range.value / 1000, false); };
      $$(".mspeed button", this.root).forEach(b => b.onclick = () => { this.R.speed = +b.dataset.s; $$(".mspeed button", this.root).forEach(x => x.classList.toggle("on", x === b)); });
      this.stage.addEventListener("pointermove", () => { this.idleAt = performance.now(); });
    }

    dispose() {
      cancelAnimationFrame(this.raf); this.raf = 0; clearTimeout(this.timer);
      if (this.ro) this.ro.disconnect(); if (this.mo) this.mo.disconnect();
      document.removeEventListener("visibilitychange", this.onVis); document.removeEventListener("keydown", this.onKey);
      this.dead = true;
    }

    hit(px, py) {
      const [wx, wy] = this.toWorld(px, py), z = this.cam.z;
      let best = -1, bd = Infinity;
      for (const n of this.nodes) {
        if (!n.vis) continue;
        const r = Math.max(n.size * .75, 7 / z), dx = Math.abs(n.x - wx), dy = Math.abs(n.y - wy);
        if (dx <= r && dy <= r) { const d = dx * dx + dy * dy; if (d < bd) { bd = d; best = n.i; } }
      }
      return best;
    }

    // ---------- loop ----------
    poke() { this.idleAt = performance.now(); this.kick(); }
    kick() { if (!this.raf && !this.dead) { clearTimeout(this.timer); this.timer = 0; this.raf = requestAnimationFrame(t => this.frame(t)); } }
    frame(now) {
      this.raf = 0;
      if (this.dead) return;
      if (!this.cv.isConnected) { this.dispose(); if (M === this) M = null; return; }
      if (document.hidden) return; // visibilitychange restarts us
      const dt = Math.min(64, now - (this.lastT || now)); this.lastT = now;
      if (this.R.on && this.R.playing) { this.advance(dt); if (this.replayCam && !RM.matches && (this.frameN = (this.frameN || 0) + 1) % 6 === 0) this.followCam(); }
      const simming = this.alpha > .004;
      if (simming && !(RM.matches && this.R.on)) this.tick(); else if (this.alpha <= .004) this.alpha = 0;
      if (this.camT) {
        const c = this.cam, t = this.camT, k = .16;
        c.x += (t.x - c.x) * k; c.y += (t.y - c.y) * k; c.z *= Math.pow(t.z / c.z, k);
        if (Math.abs(t.x - c.x) * c.z < .3 && Math.abs(t.y - c.y) * c.z < .3 && Math.abs(t.z / c.z - 1) < .002) { this.cam = { ...t }; this.camT = null; }
      }
      if (this.autoFit && this.alpha < .03 && !this.R.on) { this.autoFit = false; this.fit(false); }
      this.stepParticles(dt);
      this.draw(now);
      // Keep going while something moves; then a slow ambient breath for 20s after the last
      // interaction; then stop completely until the next event.
      const busyNow = this.alpha > 0 || this.camT || this.particles.length || (this.R.on && this.R.playing) || this.edgesGrowing(now);
      if (busyNow) this.kick();
      else if (!RM.matches && now - this.idleAt < 20000) { this.timer = setTimeout(() => this.kick(), 90); }
    }
    edgesGrowing(now) { return this.R.on && now - (this.R.lastBorn || 0) < 900; }

    // ---------- replay ----------
    duration() { return Math.min(45000, Math.max(9000, this.nodes.length * 230)); }
    togglePlay() {
      if (!this.R.on || this.R.t >= 1) this.startReplay(true);
      else { this.R.playing = !this.R.playing; this.R.lastT = 0; }
      this.setPlayIcon(); this.poke();
    }
    setPlayIcon() { const b = $(".mplay", this.root); b.innerHTML = ic(this.R.playing ? "pause" : "play"); b.classList.toggle("on", this.R.on); this.root.classList.toggle("replaying", this.R.on); }
    startReplay(play) {
      // Remember the settled layout; reduced motion replays straight into it.
      if (this.alpha > .004) this.settle(500);
      this.nodes.forEach(n => { n.hx = n.x; n.hy = n.y; n.vis = false; });
      this.R.on = true; this.R.playing = play; this.R.t = 0; this.R.shown = 0; this.replayCam = true;
      this.particles = []; this.closePanel();
      if (RM.matches) this.fitHome(); else this.cam = { x: 0, y: 0, z: 1.4 };
      this.setPlayIcon(); this.updateTime(); this.kick();
    }
    fitHome() {
      let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
      for (const n of this.nodes) { x0 = Math.min(x0, n.hx); y0 = Math.min(y0, n.hy); x1 = Math.max(x1, n.hx); y1 = Math.max(y1, n.hy); }
      const pad = 90, z = Math.min(2.2, Math.max(.06, Math.min((this.W - pad * 2) / (x1 - x0 || 1), (this.H - pad * 2 - 40) / (y1 - y0 || 1))));
      this.flyTo((x0 + x1) / 2, (y0 + y1) / 2 + 14 / z, z, false);
    }
    stopReplay() {
      this.nodes.forEach(n => n.vis = true);
      this.R.on = false; this.R.playing = false; this.R.t = 1; this.range.value = 1000; this.range.style.setProperty("--p", "100%");
      this.alpha = Math.max(this.alpha, .3); this.autoFit = true; this.setPlayIcon(); this.updateTime(); this.poke();
    }
    advance(dt) {
      const t = Math.min(1, this.R.t + dt * this.R.speed / this.duration());
      this.seek(t, true);
      if (t >= 1) { this.R.playing = false; setTimeout(() => { if (this.R.on && !this.R.playing && this.R.t >= 1) this.stopReplay(); }, RM.matches ? 0 : 1600); this.setPlayIcon(); }
    }
    // seek moves the replay to t (0..1): memories appear in creation order, then sort into clusters.
    seek(t, live) {
      const R = this.R, N = this.order.length, now = performance.now();
      R.t = t; this.range.value = Math.round(t * 1000); this.range.style.setProperty("--p", t * 100 + "%");
      const want = Math.min(N, Math.ceil(Math.min(1, t / SORT_AT) * N));
      const jump = want - R.shown;
      if (jump > 0) {
        for (let k = R.shown; k < want; k++) {
          const fresh = jump < 12 || want - k <= 3;
          this.appear(this.nodes[this.order[k]], fresh ? now : now - 5000, live && fresh);
          R.shown = k + 1;
        }
        R.lastBorn = now;
        if (jump >= 12 && !RM.matches) { R.t = t; this.alpha = .6; for (let k = 0; k < 90; k++) this.tick(); }
      } else if (want < R.shown) {
        for (let k = want; k < R.shown; k++) this.nodes[this.order[k]].vis = false;
      }
      R.shown = want;
      if (R.on && this.replayCam && !RM.matches) this.followCam();
      if (RM.matches) this.nodes.forEach(n => { if (n.vis) { n.x = n.hx; n.y = n.hy; } });
      else this.alpha = Math.max(this.alpha, t >= SORT_AT ? .28 : .2);
      this.updateTime(); this.kick();
    }
    // While replaying, the camera keeps the memories that exist so far in view.
    followCam() {
      let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
      for (const n of this.nodes) { if (!n.vis) continue; x0 = Math.min(x0, n.x); y0 = Math.min(y0, n.y); x1 = Math.max(x1, n.x); y1 = Math.max(y1, n.y); }
      if (x0 === Infinity) return;
      const pad = 100, z = Math.min(2, Math.max(.06, Math.min((this.W - pad * 2) / (x1 - x0 + 90 || 1), (this.H - pad * 2 - 40) / (y1 - y0 + 90 || 1))));
      this.camT = { x: (x0 + x1) / 2, y: (y0 + y1) / 2 + 14 / z, z };
    }
    appear(n, now, burst) {
      n.vis = true; n.born = now;
      if (RM.matches) { n.x = n.hx; n.y = n.hy; return; }
      // Arrive next to something it links to, else on a slow spiral around the middle.
      const nb = n.nb.map(j => this.nodes[j]).filter(m => m.vis && m !== n);
      const k = this.R.shown + 1;
      if (nb.length) { const m = nb[0], a = k * 2.39996; n.x = m.x + Math.cos(a) * 16; n.y = m.y + Math.sin(a) * 16; }
      else { const a = k * 2.39996, r = 20 * Math.sqrt(k); n.x = Math.cos(a) * r; n.y = Math.sin(a) * r; }
      n.vx = n.vy = 0;
      if (burst) this.burst(n);
      for (const e of this.edges) if ((e.a === n.i && this.nodes[e.b].vis) || (e.b === n.i && this.nodes[e.a].vis)) e.born = now;
    }
    burst(n) {
      if (RM.matches) return;
      const cnt = n.kind === "memory" ? 18 : 26;
      for (let k = 0; k < cnt && this.particles.length < MAX_PARTICLES; k++) {
        const a = Math.random() * Math.PI * 2, sp = .6 + Math.random() * 1.8;
        this.particles.push({ x: n.x, y: n.y, vx: Math.cos(a) * sp, vy: Math.sin(a) * sp, life: 0, max: 500 + Math.random() * 500, s: Math.random() < .3 ? 3 : 2, acc: Math.random() < .6 });
      }
    }
    stepParticles(dt) {
      if (!this.particles.length) return;
      const f = dt / 16.7;
      this.particles = this.particles.filter(p => { p.life += dt; p.x += p.vx * f; p.y += p.vy * f; p.vx *= Math.pow(.94, f); p.vy *= Math.pow(.94, f); return p.life < p.max; });
    }
    updateTime() {
      const R = this.R, N = this.order.length;
      const shown = R.on ? R.shown : N, last = shown ? this.nodes[this.order[shown - 1]] : null;
      $(".mdate", this.root).textContent = last && last.created ? fmtDate(last.created) : R.on ? "Start" : "Today";
      $(".mnum", this.root).textContent = R.on ? (R.t >= SORT_AT ? "Sorting · " : "") + `${shown} / ${N}` : `${N} nodes`;
    }
    drawSpark() {
      const cv = this.spark; if (!cv || !this.nodes) return;
      const c = cv.getContext("2d"), w = cv.width, hh = cv.height, d = this.dpr || 1, N = this.order.length;
      c.clearRect(0, 0, w, hh); if (!N) return;
      // One dot per node at its place in the timeline (by order, which is what the scrubber moves through).
      const cols = Math.max(1, Math.floor(w / (3 * d))), heights = new Array(cols).fill(0);
      c.fillStyle = this.col.dark ? "rgba(242,242,240,.28)" : "rgba(17,17,16,.22)";
      const s = Math.max(1, Math.round(1.5 * d));
      for (let k = 0; k < N; k++) {
        const col = Math.min(cols - 1, Math.floor(k / N * SORT_AT * cols)), hgt = heights[col]++;
        if (hgt > 3) continue;
        c.fillRect(Math.round(col * 3 * d), hh - (hgt + 1) * (s + d), s, s);
      }
      c.fillStyle = this.col.acc; c.globalAlpha = .5;
      for (let x = Math.floor(SORT_AT * cols); x < cols; x += 2) c.fillRect(Math.round(x * 3 * d), hh - s - d, s, s);
      c.globalAlpha = 1;
    }

    // ---------- selection, search, list ----------
    select(i, focus) {
      if (i < 0) return this.closePanel();
      this.sel = i; this.autoFit = false; this.poke();
      const n = this.nodes[i];
      if (focus || Math.abs(n.x - this.cam.x) * this.cam.z > this.W / 2 - 60 || Math.abs(n.y - this.cam.y) * this.cam.z > this.H / 2 - 60) this.flyTo(n.x, n.y, Math.max(this.cam.z, 1.1), false);
      this.drawPanel(n);
      $$(".mitem", this.groups).forEach(b => b.classList.toggle("on", +b.dataset.i === i));
      const it = $(`.mitem[data-i="${i}"]`, this.groups); if (it) it.scrollIntoView({ block: "nearest" });
    }
    closePanel() {
      this.sel = -1; this.panel.hidden = true; this.panel.innerHTML = ""; this.root.classList.remove("open");
      $$(".mitem.on", this.groups).forEach(b => b.classList.remove("on")); this.poke();
    }
    drawPanel(n) {
      const p = this.panel; p.hidden = false; this.root.classList.add("open");
      const cat = this.cats[n.cat] ? this.cats[n.cat].name : "";
      const links = this.edges.filter(e => e.a === n.i || e.b === n.i).sort((a, b) => b.w - a.w);
      p.innerHTML = `
        <div class="mp-head"><span class="mp-mark ${n.kind}"></span><span class="label acc">${KIND[n.kind]}</span><span class="label weak">·</span><span class="label">${esc(cat)}</span><span class="grow"></span><button class="btn ghost icon xs mp-x" title="Close (Esc)">${ic("x")}</button></div>
        <div class="mp-body">
          ${n.kind === "memory" ? `<textarea class="field mp-text" spellcheck="true"></textarea>` : `<h2 class="mp-title">${esc(n.label)}</h2><p class="mp-desc">${esc(n.text || (n.kind === "project" ? "No standing instructions." : ""))}</p>`}
          <div class="mp-meta">
            <div><span class="label weak">${n.kind === "memory" ? "Saved" : n.kind === "skill" ? "Written" : "Created"}</span><span>${esc(fmtFull(n.created))}</span></div>
            ${n.kind === "memory" ? `<div><span class="label weak">Category</span><span>${esc(cat)} <span class="weak">${n.ai ? "· sorted by AI" : "· guessed from keywords"}</span></span></div>` : ""}
            ${n.kind === "memory" ? `<div><span class="label weak">In prompts</span><span>${n.pinned ? "Every chat (pinned)" : "When a chat is about it"}${n.uses ? ` <span class="weak">· used ${n.uses}×</span>` : ""}</span></div>` : ""}
            ${n.project_name ? `<div><span class="label weak">Project</span><button class="lnk" data-a="proj">${esc(n.project_name)}</button></div>` : ""}
            ${n.chat ? `<div><span class="label weak">Chat</span><button class="lnk" data-a="chat">${esc(n.chat_title || "Open the chat")}</button></div>` : ""}
            ${n.path ? `<div><span class="label weak">${n.kind === "project" ? "Folder" : "File"}</span><span class="mono small mp-path" title="${esc(n.path)}">${esc(n.path)}</span></div>` : ""}
          </div>
          <div class="mp-acts"></div>
          <div class="mp-links"><div class="label">Linked · ${pad(links.length)}</div>${links.length ? "" : `<p class="weak small">No links yet. Memories link when they share names, words, a chat or a project.</p>`}</div>
        </div>`;
      $(".mp-x", p).onclick = () => this.closePanel();
      const acts = $(".mp-acts", p);
      if (n.kind === "memory") {
        const ta = $(".mp-text", p); ta.value = n.text;
        const fitTa = () => { ta.style.height = "auto"; ta.style.height = Math.min(260, ta.scrollHeight + 2) + "px"; };
        setTimeout(fitTa, 0); ta.oninput = () => { fitTa(); save.disabled = ta.value.trim() === n.text; };
        const save = h("button", "btn accent sm", "Save"); save.disabled = true;
        const forget = h("button", "btn line sm danger", "Forget");
        const note = h("span", "mp-note weak small");
        save.onclick = async () => {
          save.disabled = true; save.textContent = "Saving…";
          const r = await API().EditMemory(n.id, ta.value);
          if (r && r.error) { note.textContent = r.error; save.textContent = "Save"; save.disabled = false; return; }
          this.carry = { id: r && r.id, x: n.x, y: n.y };
          await this.reload();
          const j = this.nodes.findIndex(m => m.id === (r && r.id)); if (j >= 0) this.select(j, false);
        };
        let armed = false;
        forget.onclick = async () => {
          if (!armed) { armed = true; forget.textContent = "Forget for good?"; forget.classList.add("armed"); setTimeout(() => { armed = false; forget.textContent = "Forget"; forget.classList.remove("armed"); }, 3500); return; }
          const err = await API().ForgetMemory(n.id);
          if (err) { note.textContent = err; return; }
          this.burst(n); this.closePanel(); await this.reload();
        };
        const pin = h("button", "btn ghost sm", n.pinned ? "Unpin" : "Pin");
        pin.title = n.pinned ? "Stop sending this with every chat" : "Send this with every chat, whatever it's about";
        pin.onclick = async () => {
          const err = API().PinMemory ? await API().PinMemory(n.id, !n.pinned) : "";
          if (err) { note.textContent = err; return; }
          n.pinned = !n.pinned; this.select(n.i, false);
        };
        acts.append(save, forget, pin, note);
      } else if (n.kind === "project") {
        const b = h("button", "btn line sm", `${ic("open")}Open project`); b.onclick = () => show("project", n.project); acts.append(b);
      }
      const pj = $("[data-a=proj]", p); if (pj) pj.onclick = () => show("project", n.project);
      const ch = $("[data-a=chat]", p); if (ch) ch.onclick = () => openChat(n.chat);
      const box = $(".mp-links", p);
      links.slice(0, 40).forEach(e => {
        const m = this.nodes[e.a === n.i ? e.b : e.a];
        const r = h("button", "mp-link", `<span class="mp-mark ${m.kind}"></span><span class="t">${esc(m.label)}</span><span class="why">${esc(e.why)}</span>`);
        r.onclick = () => this.select(m.i, true);
        r.onmouseenter = () => { this.hover = m.i; this.poke(); }; r.onmouseleave = () => { this.hover = -1; this.poke(); };
        box.append(r);
      });
    }
    search(q) {
      this.query = q.trim().toLowerCase();
      this.root.classList.toggle("searching", !!this.query);
      if (!this.query) this.match = null;
      else {
        this.match = new Set();
        this.nodes.forEach(n => { const c = this.cats[n.cat]; if ((n.text + " " + n.label + " " + (c ? c.name : "")).toLowerCase().includes(this.query)) this.match.add(n.i); });
        // v0.5.1: the local database's full-text search adds word-form matches ("deploying" finds "deploys").
        const q = this.query, api = API();
        if (api.SearchMemory) Promise.resolve(api.SearchMemory(q)).then(ids => {
          if (this.query !== q || !this.match || !ids || !ids.length) return;
          const at = new Map(this.nodes.map(n => [n.id, n.i])); let more = false;
          ids.forEach(id => { const i = at.get(id); if (i !== undefined && !this.match.has(i)) { this.match.add(i); more = true; } });
          if (more) { this.drawList(); this.poke(); }
        }).catch(() => {});
      }
      this.drawList(); this.poke();
    }
    drawList() {
      const g = this.groups; g.innerHTML = "";
      const by = this.cats.map((c, ci) => ({ c, ci, items: [] }));
      this.nodes.forEach(n => { if (!this.match || this.match.has(n.i)) (by[n.cat] || by[0]).items.push(n); });
      const rank = x => x.c.name === "Skills" ? 1 : x.c.name === "Projects" ? 2 : 0;
      by.sort((a, b) => rank(a) - rank(b) || b.items.length - a.items.length || a.c.name.localeCompare(b.c.name));
      let any = false;
      for (const grp of by) {
        if (!grp.items.length) continue; any = true;
        grp.items.sort((a, b) => (b.created || 0) - (a.created || 0));
        const shut = this.collapsed.has(grp.c.name) && !this.match;
        const head = h("button", "mghead" + (shut ? " shut" : ""), `${ic("chev", "i sm chev")}<span class="mgname">${esc(grp.c.name)}</span><span class="mgn">${pad(grp.items.length)}</span>`);
        head.onclick = () => { this.collapsed.has(grp.c.name) ? this.collapsed.delete(grp.c.name) : this.collapsed.add(grp.c.name); this.drawList(); };
        head.onmouseenter = () => { this.catHi = grp.ci; this.poke(); }; head.onmouseleave = () => { this.catHi = -1; this.poke(); };
        const box = h("div", "mgitems"); if (shut) box.hidden = true;
        const frag = document.createDocumentFragment();
        for (const n of grp.items.slice(0, this.match ? 400 : 1500)) {
          const b = h("button", "mitem " + n.kind + (n.i === this.sel ? " on" : ""), `<span class="mp-mark ${n.kind}"></span><span class="t">${esc(n.kind === "memory" ? n.text : n.label)}</span>`);
          b.dataset.i = n.i; b.title = n.text || n.label;
          frag.append(b);
        }
        box.append(frag);
        g.append(head, box);
      }
      if (!any) g.append(h("div", "mnone", `<b>No matches</b>Nothing in memory mentions “${esc(this.query)}”.`));
      g.onclick = e => { const b = e.target.closest(".mitem"); if (b) this.select(+b.dataset.i, true); };
      g.onmouseover = e => { const b = e.target.closest(".mitem"); const i = b ? +b.dataset.i : -1; if (i !== this.hover) { this.hover = i; this.poke(); } };
      g.onmouseleave = () => { if (this.hover >= 0) { this.hover = -1; this.poke(); } };
    }
    drawFoot() {
      const f = $(".mfoot", this.root), g = this.g;
      f.innerHTML = `<label class="mtoggle"><input type="checkbox" ${g.ai_sort ? "checked" : ""} ${g.can_sort ? "" : "disabled"}><span class="box"></span><span>Sort new memories with AI</span></label>
        <div class="msort"><span class="weak small">${g.sorting ? "Sorting…" : !g.can_sort ? "Add a model to sort with AI" : g.unsorted ? `${g.unsorted} guessed from keywords` : "All sorted"}</span><button class="btn ghost xs" ${g.can_sort && !g.sorting ? "" : "disabled"}>${ic("sparks")}Re-sort</button></div>`;
      $("input", f).onchange = e => { API().SetMemoryAISort(e.target.checked); g.ai_sort = e.target.checked; };
      $(".msort button", f).onclick = async e => { const b = e.currentTarget; b.disabled = true; const err = await API().SortMemories(); $(".msort span", f).textContent = err || "Sorting…"; };
    }

    // ---------- drawing ----------
    draw(now) {
      const c = this.ctx, d = this.dpr, W = this.W, H = this.H, cam = this.cam, z = cam.z, col = this.col;
      const ns = this.nodes, ink = col.ink, acc = col.acc;
      c.setTransform(d, 0, 0, d, 0, 0); c.clearRect(0, 0, W, H);
      // Dot grid that pans and zooms with the graph (CSS background, free to draw).
      let gs = 26 * z; while (gs < 12) gs *= 2; while (gs > 52) gs /= 2;
      this.stage.style.backgroundSize = `${gs}px ${gs}px`;
      this.stage.style.backgroundPosition = `${(W / 2 - cam.x * z) % gs}px ${(H / 2 - cam.y * z) % gs}px`;
      const sx = x => (x - cam.x) * z + W / 2, sy = y => (y - cam.y) * z + H / 2;
      const m = 40 / z, vx0 = cam.x - W / 2 / z - m, vx1 = cam.x + W / 2 / z + m, vy0 = cam.y - H / 2 / z - m, vy1 = cam.y + H / 2 / z + m;
      const inView = n => n.x > vx0 && n.x < vx1 && n.y > vy0 && n.y < vy1;
      // Focus: the selected or hovered node and its neighbours; everything else dims.
      const focus = this.hover >= 0 ? this.hover : this.sel;
      let near = null;
      if (focus >= 0) { near = new Set(ns[focus].nb); near.add(focus); if (this.sel >= 0 && this.hover >= 0) { near.add(this.sel); } }
      const dimmed = n => (near && !near.has(n.i)) || (this.match && !this.match.has(n.i)) || (this.catHi >= 0 && n.cat !== this.catHi);
      const anyDim = !!(near || this.match || this.catHi >= 0);
      const breathe = !RM.matches && now - this.idleAt < 20000;

      // Edges
      c.lineWidth = 1; c.strokeStyle = ink;
      const ea = Math.min(1, Math.max(.35, Math.sqrt(500 / (this.edges.length || 1))));
      c.globalAlpha = (anyDim ? (col.dark ? .06 : .05) : (col.dark ? .16 : .13)) * ea;
      c.beginPath();
      const hot = [], growing = [];
      for (const e of this.edges) {
        const A = ns[e.a], B = ns[e.b];
        if (!A.vis || !B.vis) continue;
        if (Math.max(A.x, B.x) < vx0 || Math.min(A.x, B.x) > vx1 || Math.max(A.y, B.y) < vy0 || Math.min(A.y, B.y) > vy1) continue;
        if (this.R.on && now - e.born < 700) { growing.push(e); continue; }
        if (focus >= 0 && (e.a === focus || e.b === focus)) { hot.push(e); continue; }
        c.moveTo(sx(A.x), sy(A.y)); c.lineTo(sx(B.x), sy(B.y));
      }
      c.stroke();
      if (hot.length) {
        c.strokeStyle = acc; c.globalAlpha = .75; c.lineWidth = 1.25; c.beginPath();
        for (const e of hot) { const A = ns[e.a], B = ns[e.b]; c.moveTo(sx(A.x), sy(A.y)); c.lineTo(sx(B.x), sy(B.y)); }
        c.stroke();
      }
      // Replay: new links draw in from the older node, a particle riding the tip.
      if (growing.length) {
        c.strokeStyle = acc; c.lineWidth = 1.2;
        for (const e of growing) {
          const p = smooth((now - e.born) / 700); let A = ns[e.a], B = ns[e.b];
          if ((A.born || 0) > (B.born || 0)) [A, B] = [B, A];
          const tx = A.x + (B.x - A.x) * p, ty = A.y + (B.y - A.y) * p;
          c.globalAlpha = .7; c.beginPath(); c.moveTo(sx(A.x), sy(A.y)); c.lineTo(sx(tx), sy(ty)); c.stroke();
          c.globalAlpha = 1; c.fillStyle = acc; c.fillRect(sx(tx) - 1.5, sy(ty) - 1.5, 3, 3);
        }
      }

      // Cluster labels (fade in as categories gather during a replay)
      const pull = this.catPull(), placed = [];
      if (pull > .05 && this.cats.length > 1) {
        const cx = new Float64Array(this.cats.length), cy = new Float64Array(this.cats.length), cn = new Float64Array(this.cats.length), top = new Float64Array(this.cats.length).fill(Infinity);
        const minx = new Float64Array(this.cats.length).fill(Infinity), maxx = new Float64Array(this.cats.length).fill(-Infinity), maxy = new Float64Array(this.cats.length).fill(-Infinity);
        for (const n of ns) { if (!n.vis) continue; cx[n.cat] += n.x; cy[n.cat] += n.y; cn[n.cat]++; }
        for (const n of ns) { if (!n.vis) continue; const k = n.cat, mx = cx[k] / cn[k], my = cy[k] / cn[k]; if (Math.hypot(n.x - mx, n.y - my) < 160 + Math.sqrt(cn[k]) * 12) { top[k] = Math.min(top[k], n.y); minx[k] = Math.min(minx[k], n.x); maxx[k] = Math.max(maxx[k], n.x); maxy[k] = Math.max(maxy[k], n.y); } }
        c.font = `500 ${10.5}px ${col.mono}`; c.textAlign = "center"; c.textBaseline = "alphabetic";
        if ("letterSpacing" in c) c.letterSpacing = "1.6px";
        for (let k = 0; k < this.cats.length; k++) {
          if (!cn[k] || top[k] === Infinity) continue;
          const hi = this.catHi === k, X = sx(cx[k] / cn[k]), Y = sy(top[k]) - 18;
          if (X < -200 || X > W + 200 || Y < -40 || Y > H + 60) continue;
          c.globalAlpha = pull * (hi ? 1 : anyDim ? .35 : .7);
          c.fillStyle = hi ? acc : col.sub;
          const ct = `${this.cats[k].name.toUpperCase()} · ${pad(cn[k])}`, cw = c.measureText(ct).width;
          c.fillText(ct, X, Y); placed.push([X - cw / 2 - 6, Y - 12, X + cw / 2 + 6, Y + 4]);
          // Hairline corner brackets around the cluster
          if (z > .25 && cn[k] > 1) {
            const x0 = sx(minx[k]) - 16, x1 = sx(maxx[k]) + 16, y0 = sy(top[k]) - 10, y1 = sy(maxy[k]) + 16, L = 8;
            c.globalAlpha = pull * (hi ? .9 : .28); c.strokeStyle = hi ? acc : ink; c.lineWidth = 1; c.beginPath();
            c.moveTo(x0, y0 + L); c.lineTo(x0, y0); c.lineTo(x0 + L, y0); c.moveTo(x1 - L, y0); c.lineTo(x1, y0); c.lineTo(x1, y0 + L);
            c.moveTo(x1, y1 - L); c.lineTo(x1, y1); c.lineTo(x1 - L, y1); c.moveTo(x0 + L, y1); c.lineTo(x0, y1); c.lineTo(x0, y1 - L); c.stroke();
          }
        }
        if ("letterSpacing" in c) c.letterSpacing = "0px";
      }

      // Nodes: square particle dots. Big on screen → a 3×3 grid of dots that breathes; small → one square.
      const labels = [];
      for (const n of ns) {
        if (!n.vis || !inView(n)) continue;
        const X = sx(n.x), Y = sy(n.y), dim = dimmed(n), isSel = n.i === this.sel, isHov = n.i === this.hover;
        let s = Math.max(2.5, n.size * z);
        const age = this.R.on ? now - n.born : 1e9;
        if (age < 420) s *= .4 + smooth(age / 420) * .6 + Math.sin(age / 420 * Math.PI) * .5;
        const b = breathe ? 1 + Math.sin(now / 1300 + n.i * 1.7) * .07 : 1;
        c.globalAlpha = dim ? (col.dark ? .2 : .16) : 1;
        const hotNode = isSel || isHov || (this.match && this.match.has(n.i)) || (age < 900 && !RM.matches);
        c.fillStyle = hotNode ? acc : ink; c.strokeStyle = hotNode ? acc : ink;
        if (n.kind === "memory") {
          if (s >= 9) {
            const g = s / 3, ds = g * .62 * b, o = (g - ds) / 2;
            for (let yy = 0; yy < 3; yy++) for (let xx = 0; xx < 3; xx++) {
              if ((xx + yy) % 2 && !hotNode) c.globalAlpha = (dim ? .1 : .55);
              else c.globalAlpha = dim ? (col.dark ? .2 : .16) : 1;
              c.fillRect(X - s / 2 + xx * g + o, Y - s / 2 + yy * g + o, ds, ds);
            }
            c.globalAlpha = dim ? .16 : 1;
          } else c.fillRect(X - s * b / 2, Y - s * b / 2, s * b, s * b);
        } else if (n.kind === "skill") {
          c.lineWidth = 1.25; c.strokeRect(Math.round(X - s / 2) + .5, Math.round(Y - s / 2) + .5, Math.round(s), Math.round(s));
          const ds = Math.max(2, s * .34 * b); c.fillRect(X - ds / 2, Y - ds / 2, ds, ds);
        } else {
          c.lineWidth = 1.5; c.strokeRect(Math.round(X - s / 2) + .5, Math.round(Y - s / 2) + .5, Math.round(s), Math.round(s));
          const ds = Math.max(2, s * .2); [[-1, -1], [1, -1], [-1, 1], [1, 1]].forEach(([u, v]) => c.fillRect(X + u * s * .24 * b - ds / 2, Y + v * s * .24 * b - ds / 2, ds, ds));
        }
        if (isSel) { c.globalAlpha = 1; c.strokeStyle = acc; c.lineWidth = 1; const r = s / 2 + 5; c.strokeRect(Math.round(X - r) + .5, Math.round(Y - r) + .5, Math.round(r * 2), Math.round(r * 2)); }
        // The newest memory sends out a slow blue pulse.
        if (n.i === this.newest && !this.R.on && !RM.matches && breathe) {
          const ph = (now % 2600) / 2600, r = s / 2 + 3 + ph * 18;
          c.globalAlpha = (1 - ph) * .6; c.strokeStyle = acc; c.lineWidth = 1; c.strokeRect(X - r, Y - r, r * 2, r * 2);
        }
        labels.push(n);
      }
      c.globalAlpha = 1;

      // Particles (replay bursts, forget)
      if (this.particles.length) {
        // Streaks first (one path), then the square heads.
        c.strokeStyle = acc; c.lineWidth = 1; c.globalAlpha = .35; c.beginPath();
        for (const p of this.particles) { const X = sx(p.x), Y = sy(p.y); c.moveTo(X, Y); c.lineTo(X - p.vx * 7 * z, Y - p.vy * 7 * z); }
        c.stroke();
        for (const p of this.particles) {
          const k = 1 - p.life / p.max; c.globalAlpha = k * .95; c.fillStyle = p.acc ? acc : ink;
          const s = p.s * Math.min(1.4, .8 + z * .3); c.fillRect(sx(p.x) - s / 2, sy(p.y) - s / 2, s, s);
        }
      }
      c.globalAlpha = 1;

      // Labels: mono uppercase under the node. Always for the focus and its neighbours;
      // for everything else only above a zoom threshold, most-linked first, never overlapping.
      const N = ns.length, lz = N > 600 ? 1.7 : N > 200 ? 1.15 : .8;
      const fs = 10, mid = z >= lz * .55, top = mid && z < lz ? new Set(labels.slice().sort((a, b) => b.deg - a.deg).slice(0, 14).map(n => n.i)) : null;
      c.font = `500 ${fs}px ${col.mono}`; c.textAlign = "center"; c.textBaseline = "top";
      if ("letterSpacing" in c) c.letterSpacing = ".6px";
      const fresh = n => this.R.on && now - n.born < 1600 && !RM.matches;
      const want = labels.filter(n => n.i === this.sel || n.i === this.hover || (near && near.has(n.i)) || (this.match && this.match.has(n.i) && z > .5) || (n.kind === "project" && z > .35) || fresh(n) ||
        (!this.R.on && (z >= lz || (top && (top.has(n.i) || n.kind !== "memory")))));
      want.sort((a, b) => (b.i === this.hover) - (a.i === this.hover) || (b.i === this.sel) - (a.i === this.sel) || (near ? (near.has(b.i) - near.has(a.i)) : 0) || (b.kind === "project") - (a.kind === "project") || b.deg - a.deg);
      let count = 0;
      for (const n of want) {
        if (count > 260) break;
        const txt = n.label.toUpperCase();
        let w = this.labelW.get(txt); if (w == null) { w = c.measureText(txt).width; this.labelW.set(txt, w); }
        const X = sx(n.x), Y = sy(n.y) + Math.max(2.5, n.size * z) / 2 + 5;
        const r = [X - w / 2 - 6, Y - 2, X + w / 2 + 6, Y + fs + 4];
        const forced = n.i === this.sel || n.i === this.hover;
        if (!forced && placed.some(p => r[0] < p[2] && r[2] > p[0] && r[1] < p[3] && r[3] > p[1])) continue;
        placed.push(r); count++;
        const dim = dimmed(n), hotL = n.i === this.sel || n.i === this.hover;
        if (hotL) { c.globalAlpha = .92; c.fillStyle = col.paper; c.fillRect(r[0], r[1], r[2] - r[0], r[3] - r[1]); }
        const fa = fresh(n) && !hotL ? 1 - smooth((now - n.born - 600) / 1000) : 1;
        c.globalAlpha = (dim ? .25 : hotL ? 1 : .78) * fa;
        c.fillStyle = hotL || (fresh(n) && now - n.born < 900) ? acc : ink;
        c.fillText(txt, X, Y);
      }
      if ("letterSpacing" in c) c.letterSpacing = "0px";
      c.globalAlpha = 1;
    }
  }

  // Shots and tests drive the screen through this.
  window.MemoryUI = {
    get live() { return M; },
    settle() { if (M) { M.settle(600); M.fit(true); M.kick(); } },
    select(idOrIndex) { if (!M) return; const i = typeof idOrIndex === "number" ? idOrIndex : M.nodes.findIndex(n => n.id === idOrIndex); M.select(i, true); M.camT && (M.cam = { ...M.camT }, M.camT = null); M.kick(); },
    seek(t) { if (!M) return; if (!M.R.on) M.startReplay(false); M.camT && (M.cam = { ...M.camT }, M.camT = null); M.R.playing = false; M.setPlayIcon(); M.seek(t, true); },
    play(speed) { if (!M) return; if (speed) M.R.speed = speed; M.togglePlay(); },
    stats() { return M ? { nodes: M.nodes.length, edges: M.edges.length, cats: M.cats.length, alpha: M.alpha, running: !!M.raf || !!M.timer } : null; },
  };
  on("memory", () => { if (M && !M.dead) M.reload(); });
})();
