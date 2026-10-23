// v0.18.1 the Blaze emblem, ported 1:1 from Blaze Mobile (DuoGlow.kt + StreamOrb.kt):
// drawWaterOrb (Blaze, the glass drop), drawHaloRing (Halo, the gold ring), drawSplash and drawDuoEmblem.
// Everything draws on a 2D canvas context in CSS pixels; t is the clock in seconds (0 = a still picture).
// <blaze-emblem size="22" [live] [dim] [bare]> renders the emblem; it only moves while it is live, on screen, the
// window is shown and motion is on (one shared requestAnimationFrame loop for every live emblem, ~30 fps).
(() => {
  const TAU = Math.PI * 2;
  const COLORS = { blazeBlue: "#4DA8FF", blazeIce: "#BFE3FF", haloGold: "#FFC94D", haloCream: "#FFF0C2", haloDeep: "#E0A21F" };
  const W = a => `rgba(255,255,255,${a})`;
  const GOLD = a => `rgba(255,201,77,${a})`;

  // Compose's radialGradient(colors) spreads the stops evenly (0, .5, 1). Transparent stops keep the colour's
  // own RGB so canvas interpolation does not darken towards black.
  function radial(ctx, x, y, r, stops) {
    const g = ctx.createRadialGradient(x, y, 0, x, y, Math.max(r, .01));
    stops.forEach(([o, c]) => g.addColorStop(Math.min(1, Math.max(0, o)), c));
    return g;
  }

  /** The droplet: wobbling outline, frosted body, refraction, rim, caustic, specular. stretch > 1 falls, < 1 squashes. */
  function drawWaterOrb(ctx, cx, cy, r, t = 0, stretch = 1, alpha = 1) {
    if (alpha <= 0 || r <= 0) return;
    ctx.save();
    // glow
    ctx.fillStyle = radial(ctx, cx, cy, r * 2.4, [[0, W(.16 * alpha)], [1, W(0)]]);
    ctx.beginPath(); ctx.arc(cx, cy, r * 2.4, 0, TAU); ctx.fill();
    const n = 28;
    const sx = 1 / Math.sqrt(Math.max(stretch, .2)), sy = stretch;
    const path = new Path2D();
    for (let i = 0; i <= n; i++) {
      const a = (i / n) * TAU;
      const k = 1 + .055 * Math.sin(3 * a + t * 2.2) + .03 * Math.sin(5 * a - t * 3.1) + .02 * Math.sin(2 * a + t * 1.3);
      const heavy = 1 + .06 * ((Math.sin(a) + 1) / 2);   // a drop is a little heavier at the bottom
      const x = cx + Math.cos(a) * r * k * sx;
      const y = cy + Math.sin(a) * r * k * sy * heavy;
      if (i === 0) path.moveTo(x, y); else path.lineTo(x, y);
    }
    path.closePath();
    // body: frosted, brighter at the top-left
    ctx.fillStyle = radial(ctx, cx - r * .35, cy - r * .4, r * 1.6, [[0, W(.62 * alpha)], [.5, W(.20 * alpha)], [1, `rgba(154,154,160,${.18 * alpha})`]]);
    ctx.fill(path);
    // refraction: the ground seen through the drop, darker at the bottom-right
    ctx.fillStyle = radial(ctx, cx - r * .2, cy - r * .25, r * 1.25, [[0, "rgba(0,0,0,0)"], [1, `rgba(0,0,0,${.32 * alpha})`]]);
    ctx.fill(path);
    // rim
    const rim = ctx.createLinearGradient(cx - r, cy - r, cx + r, cy + r);
    rim.addColorStop(0, W(.75 * alpha)); rim.addColorStop(1, W(.10 * alpha));
    ctx.strokeStyle = rim; ctx.lineWidth = Math.max(r * .11, 1);
    ctx.stroke(path);
    // caustic: light focused through the drop onto its lower edge (start 35°, sweep 70°, oval 1.24 r)
    ctx.strokeStyle = W(.38 * alpha); ctx.lineWidth = r * .14;
    ctx.beginPath();
    ctx.ellipse(cx, cy + r * .07 * sy, r * .62 * sx, r * .62 * sy, 0, 35 * Math.PI / 180, 105 * Math.PI / 180);
    ctx.stroke();
    // specular highlight (oval .42 r × .26 r, top-left) and its small dot
    ctx.fillStyle = W(.92 * alpha);
    ctx.beginPath();
    ctx.ellipse(cx - r * .58 * sx + r * .21, cy - r * .62 * sy + r * .13, r * .21, r * .13, 0, 0, TAU);
    ctx.fill();
    ctx.fillStyle = W(.7 * alpha);
    ctx.beginPath(); ctx.arc(cx + r * .30 * sx, cy - r * .42 * sy, r * .07, 0, TAU); ctx.fill();
    ctx.restore();
  }

  /** The halo: a glowing gold ring of radius ≈ 1.75 r, breathing gently with the clock. */
  function drawHaloRing(ctx, cx, cy, r, t = 0, alpha = 1) {
    if (alpha <= .01 || r <= 0) return;
    ctx.save();
    const ringR = r * 1.75 * (1 + .03 * Math.sin(t * 1.7));
    const w = Math.max(r * .2, 1.2);
    const outer = ringR + w * 3.2;
    // soft glow on both sides of the ring
    ctx.fillStyle = radial(ctx, cx, cy, outer, [[0, GOLD(0)], [Math.max(0, (ringR - w * 3) / outer), GOLD(0)], [ringR / outer, GOLD(.42 * alpha)], [1, GOLD(0)]]);
    ctx.beginPath(); ctx.arc(cx, cy, outer, 0, TAU); ctx.fill();
    // the ring itself, brighter at the top-left like the drop
    const g = ctx.createLinearGradient(cx - ringR, cy - ringR, cx + ringR, cy + ringR);
    g.addColorStop(0, `rgba(255,240,194,${alpha})`); g.addColorStop(.5, GOLD(.95 * alpha)); g.addColorStop(1, `rgba(224,162,31,${.9 * alpha})`);
    ctx.strokeStyle = g; ctx.lineWidth = w;
    ctx.beginPath(); ctx.arc(cx, cy, ringR, 0, TAU); ctx.stroke();
    ctx.strokeStyle = W(.55 * alpha); ctx.lineWidth = w * .32;
    ctx.beginPath(); ctx.arc(cx, cy, ringR, 0, TAU); ctx.stroke();
    ctx.restore();
  }

  /** A ripple where the drop landed: two thin expanding ellipses and two splash beads (p 0 → 1). */
  function drawSplash(ctx, x, y, r, p) {
    if (p <= 0 || p >= 1) return;
    ctx.save();
    const fade = 1 - p;
    for (let k = 0; k <= 1; k++) {
      const q = Math.min(1, Math.max(0, p - k * .18));
      if (q <= 0) continue;
      const w = r * (1.2 + q * 6), h = w * .28;
      ctx.strokeStyle = W(.45 * fade * (1 - k * .4)); ctx.lineWidth = Math.max(r * .12, 1);
      ctx.beginPath(); ctx.ellipse(x, y + r * .6, w, h, 0, 0, TAU); ctx.stroke();
    }
    const up = Math.sin(p * Math.PI) * r * 2.2;
    ctx.fillStyle = W(.7 * fade);
    for (const s of [-1, 1]) { ctx.beginPath(); ctx.arc(x + s * r * (.8 + p * 2.2), y - up, r * .16, 0, TAU); ctx.fill(); }
    ctx.restore();
  }

  /** The Blaze icon: the drop inside its gold halo, centred in a square of side w (r = .19 w). */
  function drawDuoEmblem(ctx, cx, cy, w, t = 0, alpha = 1) {
    const r = w * .19;
    drawHaloRing(ctx, cx, cy, r, t, alpha);
    drawWaterOrb(ctx, cx, cy, r, t, 1, alpha);
  }

  // Desktop only: on a light ground the white glass needs something to refract. A small dark lens inside the ring,
  // and a deeper gold under the ring so the halo does not wash out on white.
  function drawLightBacking(ctx, cx, cy, r, t = 0, alpha = 1, lens = true) {
    ctx.save();
    if (lens) {
    ctx.fillStyle = radial(ctx, cx, cy, r * 1.5, [[0, `rgba(44,44,50,${.92 * alpha})`], [.72, `rgba(20,20,24,${.9 * alpha})`], [1, "rgba(20,20,24,0)"]]);
    ctx.beginPath(); ctx.arc(cx, cy, r * 1.5, 0, TAU); ctx.fill();
    }
    const ringR = r * 1.75 * (1 + .03 * Math.sin(t * 1.7));
    ctx.strokeStyle = `rgba(176,120,10,${.6 * alpha})`; ctx.lineWidth = Math.max(r * .2, 1.2) * 1.4;
    ctx.beginPath(); ctx.arc(cx, cy, ringR, 0, TAU); ctx.stroke();
    ctx.restore();
  }

  // ---------------- the shared clock ----------------
  const t0 = performance.now();
  const now = () => (performance.now() - t0) / 1000;
  const motion = () => !!(window.BlazeMotion ? window.BlazeMotion.enabled : !matchMedia("(prefers-reduced-motion: reduce)").matches);
  const isLight = () => document.documentElement.dataset.theme !== "dark";

  const live = new Set();       // emblems that want frames
  let raf = 0, last = 0;
  function loop(ts) {
    raf = 0;
    if (!motion() || document.hidden) return;
    let any = false;
    const step = ts - last >= 1000 / 30;
    if (step) last = ts;
    live.forEach(e => {
      if (!e.isConnected) { live.delete(e); return; }
      if (!e._visible) return;
      any = true;
      if (step) e.draw(now());
    });
    if (any) raf = requestAnimationFrame(loop);
  }
  const kick = () => { if (!raf && live.size && motion() && !document.hidden) raf = requestAnimationFrame(loop); };

  const io = typeof IntersectionObserver === "function" ? new IntersectionObserver(es => {
    es.forEach(en => { en.target._visible = en.isIntersecting; });
    kick();
  }) : null;

  class BlazeEmblem extends HTMLElement {
    static get observedAttributes() { return ["live", "animate", "size", "dim", "halo-only"]; }
    constructor() {
      super();
      this.canvas = document.createElement("canvas");
      this.canvas.setAttribute("aria-hidden", "true");
      this.canvas.style.display = "block";
      this.attachShadow({ mode: "open" }).appendChild(this.canvas);
      this._visible = !io;
      this._t = 0;
    }
    get size() { return Number(this.getAttribute("size")) || 22; }
    get live() { return this.hasAttribute("live") || this.hasAttribute("animate"); }
    set live(v) { this.toggleAttribute("live", !!v); }
    connectedCallback() {
      this.layout();
      if (io) io.observe(this);
      this.sync();
    }
    disconnectedCallback() { live.delete(this); if (io) io.unobserve(this); }
    attributeChangedCallback(name) { if (!this.isConnected) return; if (name === "size") this.layout(); this.sync(); }
    layout() {
      const s = this.size, dpr = window.devicePixelRatio || 1;
      this.canvas.width = Math.round(s * dpr); this.canvas.height = Math.round(s * dpr);
      this.canvas.style.width = this.canvas.style.height = s + "px";
      this.draw(this._t);
    }
    // Live: joins the shared loop. Not live (or motion off): one still frame.
    sync() {
      if (this.live && motion()) { live.add(this); kick(); }
      else { live.delete(this); this.draw(motion() ? this._t : 0); }
    }
    draw(t = 0) {
      this._t = t;
      const ctx = this.canvas.getContext("2d"); if (!ctx) return;
      const s = this.size, dpr = this.canvas.width / s;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      ctx.clearRect(0, 0, s, s);
      const a = this.hasAttribute("dim") ? .45 : 1;
      // halo-only: Blaze's drop is out riding a streaming reply (stream.js); the label keeps just the ring.
      const haloOnly = this.hasAttribute("halo-only");
      if (isLight() && !this.hasAttribute("bare")) drawLightBacking(ctx, s / 2, s / 2, s * .19, t, a, !haloOnly);
      if (haloOnly) drawHaloRing(ctx, s / 2, s / 2, s * .19, t, a);
      else drawDuoEmblem(ctx, s / 2, s / 2, s, t, a);
    }
  }
  customElements.define("blaze-emblem", BlazeEmblem);

  const redrawAll = () => document.querySelectorAll("blaze-emblem").forEach(e => { e.layout(); e.sync(); });
  new MutationObserver(redrawAll).observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
  window.addEventListener("blazemotion", redrawAll);
  matchMedia("(prefers-reduced-motion: reduce)").addEventListener?.("change", () => setTimeout(redrawAll, 0));
  document.addEventListener("visibilitychange", kick);

  window.Duo = { drawWaterOrb, drawHaloRing, drawSplash, drawDuoEmblem, drawLightBacking, COLORS, now, motion, isLight, kick };
})();

