// v0.18.1 EdgeParticles, ported from Blaze Mobile (BlazeGlass.kt EdgePath + EdgeParticles): ~40 round dots drifting
// along the window's edge (every 3rd the other way, a sin wobble), faint links between neighbours closer than 60 px,
// ~24 fps. Shown only while a run is active (thinking, streaming, tools) and fading in/out over 320 ms. Zero frames
// when idle, when the window is hidden, or under reduced motion. The canvas sits behind the app (z-index -1 in the
// body's stacking context) and never takes pointer events.
(() => {
  /** A point u (0..1) around the perimeter of a w×h rectangle, inset by inset (pure; same as EdgePath.at). */
  const EdgePath = {
    at(u, w, h, inset) {
      const W = Math.max(w - inset * 2, 1), H = Math.max(h - inset * 2, 1);
      const per = 2 * (W + H);
      let d = (((u % 1) + 1) % 1) * per;
      if (d < W) return [inset + d, inset]; d -= W;
      if (d < H) return [inset + W, inset + d]; d -= H;
      if (d < W) return [inset + W - d, inset + H]; d -= W;
      return [inset, inset + H - d];
    },
  };

  // Seeded like the Kotlin (Random(23)): stable dot layout between runs.
  let seed = 23;
  const rnd = () => { seed = (seed * 1664525 + 1013904223) >>> 0; return seed / 4294967296; };
  const N = 40;
  const dots = Array.from({ length: N }, () => [rnd(), .012 + rnd() * .02, rnd(), rnd() * 6.283]);

  const cv = document.createElement("canvas");
  cv.id = "edge-particles";
  cv.setAttribute("aria-hidden", "true");
  Object.assign(cv.style, { position: "fixed", inset: "0", width: "100vw", height: "100vh", pointerEvents: "none", zIndex: "-1", opacity: "0", display: "none" });
  document.body.prepend(cv);

  const motion = () => !!(window.BlazeMotion ? window.BlazeMotion.enabled : !matchMedia("(prefers-reduced-motion: reduce)").matches);
  const FADE = 320, FPS_MS = 1000 / 24;
  let active = false, vis = 0, raf = 0, last = 0, lastTs = 0, time = 0, frozen = false;

  function palette() {
    const dark = document.documentElement.dataset.theme === "dark";
    // dark: white, #E6E6EA every 4th, #BDBDC2 every 7th · light: ink-based
    return dark ? { main: "255,255,255", alt4: "230,230,234", alt7: "189,189,194", link: "255,255,255" }
                : { main: "17,17,18", alt4: "58,58,64", alt7: "110,110,118", link: "17,17,18" };
  }

  function size() {
    const k = window.devicePixelRatio || 1, w = innerWidth, h = innerHeight;
    if (cv.width !== Math.round(w * k) || cv.height !== Math.round(h * k)) { cv.width = Math.round(w * k); cv.height = Math.round(h * k); }
    return { k, w, h };
  }

  function draw(energy = 1) {
    const ctx = cv.getContext("2d"); if (!ctx) return;
    const { k, w, h } = size();
    ctx.setTransform(k, 0, 0, k, 0, 0);
    ctx.clearRect(0, 0, w, h);
    const col = palette();
    const pts = dots.map((q, i) => {
      const dir = i % 3 === 0 ? -1 : 1;
      const u = q[0] + dir * q[1] * time * (.6 + energy * .6);
      const wob = Math.sin(time * 1.7 + q[3]) * 5;
      return EdgePath.at(u, w, h, 6 + q[2] * 10 + wob);
    });
    ctx.lineWidth = 1;
    for (let i = 0; i < pts.length; i++) {
      const a = pts[i], b = pts[(i + 1) % pts.length];
      const dist = Math.hypot(a[0] - b[0], a[1] - b[1]);
      if (dist < 60) {
        ctx.strokeStyle = `rgba(${col.link},${.25 * (1 - dist / 60) * energy})`;
        ctx.beginPath(); ctx.moveTo(a[0], a[1]); ctx.lineTo(b[0], b[1]); ctx.stroke();
      }
    }
    pts.forEach((p, i) => {
      const c = i % 7 === 0 ? col.alt7 : i % 4 === 0 ? col.alt4 : col.main;
      const s = 2.2 + (i % 3);
      const alpha = (.45 + .35 * ((Math.sin(time * 2 + dots[i][3]) + 1) / 2)) * energy;
      ctx.fillStyle = `rgba(${c},${alpha})`;
      ctx.beginPath(); ctx.arc(p[0], p[1], s / 2, 0, Math.PI * 2); ctx.fill();   // round dots, no squares
    });
  }

  function tick(ts) {
    raf = 0;
    if (frozen) return;
    if (!motion() || document.hidden) { stopNow(); return; }
    const dt = lastTs ? ts - lastTs : 16; lastTs = ts;
    vis = Math.max(0, Math.min(1, vis + (active ? 1 : -1) * dt / FADE));
    cv.style.opacity = vis.toFixed(3);
    if (ts - last >= FPS_MS) { time += (last ? Math.min(ts - last, 200) : FPS_MS) / 1000; last = ts; draw(); }
    if (!active && vis <= 0) { stopNow(); return; }
    raf = requestAnimationFrame(tick);
  }
  function stopNow() {
    if (raf) cancelAnimationFrame(raf);
    raf = 0; last = 0; lastTs = 0;
    if (!active || !motion() || document.hidden) { vis = 0; cv.style.opacity = "0"; cv.style.display = "none"; }
  }
  function kick() {
    if (raf || frozen) return;
    if (!active && vis <= 0) return;
    if (!motion() || document.hidden) { stopNow(); return; }
    cv.style.display = "";
    raf = requestAnimationFrame(tick);
  }
  function set(on) {
    on = !!on;
    if (on === active) return;
    active = on;
    kick();
  }

  // Run state: the same moments that toggle thinking() and the run's end in app.js.
  const run = () => set(true);
  const idle = () => set(false);
  ["thinking", "status", "delta", "tool", "confirm"].forEach(ev => on(ev, () => { if (typeof current !== "undefined" && current) run(); }));
  ["done", "error", "stopped"].forEach(ev => on(ev, idle));
  // setBusy is app.js's single switch for "a turn is running" (send → busy, done/error/stopped → idle).
  if (typeof setBusy === "function") {
    const orig = setBusy;
    setBusy = function (b) { orig.apply(this, arguments); set(!!b); };
  }
  document.addEventListener("visibilitychange", () => document.hidden ? stopNow() : kick());
  window.addEventListener("blazemotion", () => motion() ? kick() : stopNow());
  window.addEventListener("resize", () => { if (raf) draw(); });

  window.EdgeParticles = {
    EdgePath, set, get active() { return active; },
    // shots: draw one frame at full strength at time t and hold it
    frame(t = 2.5) { frozen = true; if (raf) cancelAnimationFrame(raf); raf = 0; time = t; vis = 1; cv.style.display = ""; cv.style.opacity = "1"; draw(); },
    thaw() { frozen = false; kick(); },
  };
})();
