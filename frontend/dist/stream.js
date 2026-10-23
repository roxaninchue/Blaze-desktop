// v0.18.1 StreamOrb + duoShimmer, ported from Blaze Mobile (StreamOrb.kt OrbLayer, DuoGlow.kt duoShimmer).
// While a reply streams, Blaze's glass drop leaves its halo (the halo stays on the reply's Blaze label) and rides
// the end of the newest text line. When a code block, table, card or image appears it falls onto that block's top
// edge (gravity ease, stretched while falling, squash + splash on landing), then follows the text below. When the
// reply ends it glides home and the pair rests, dimmed, beside the newest reply only.
// The newest words glow Blaze blue with an ice glint; on stream end one diagonal sweep crosses the reply and fades.
// Draw-only overlays: one drop canvas moved by transform, one shimmer canvas blended over the text. No layout
// change, text stays selectable (pointer-events: none). Reduced motion: the drop just sits at the end and fades;
// no fall, glow or sweep.
(() => {
  const D = () => window.Duo;
  const motion = () => !!(window.BlazeMotion ? window.BlazeMotion.enabled : true);
  const R = 5.5;                    // drop radius in CSS px (desktop text is 14 px)
  const BOX = Math.ceil(R * 10);    // drop canvas side: room for glow, stretch and splash
  const GAP = 9;                    // between the last character and the drop's centre
  const REACH = 150, BAND = 22, SWEEP_HALF = 90;
  const RICH = "pre, .code, table, .acard, img, svg.mermaid, .mermaid, .card";
  const FALL_PX = 28;
  const fallMs = dy => Math.min(560, Math.max(220, Math.sqrt(Math.max(0, dy)) * 38));
  const easeStd = t => { // cubic-bezier(.4,0,.2,1), solved numerically
    let lo = 0, hi = 1, x = 0;
    for (let i = 0; i < 18; i++) { x = (lo + hi) / 2; const bx = 3 * (1 - x) * (1 - x) * x * .4 + 3 * (1 - x) * x * x * .2 + x * x * x; if (bx < t) lo = x; else hi = x; }
    return 3 * (1 - x) * (1 - x) * x * 0 + 3 * (1 - x) * x * x * 1 + x * x * x;
  };
  const dpr = () => window.devicePixelRatio || 1;
  const blueFor = () => D().isLight() ? { main: "31,111,209", light: "120,180,240", blend: "lighten" } : { main: "77,168,255", light: "191,227,255", blend: "darken" };

  let S = null;        // the live (or homing) stream state
  let frozen = false;  // shots: hold the current frame
  let raf = 0;

  // ---------- measuring (reads only; nothing here writes layout) ----------
  function lastTextNode(root) {
    const w = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
      acceptNode: n => (!n.nodeValue.trim() || n.parentElement.closest(RICH + ", .afade, .amore, .ahead")) ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT,
    });
    let last = null, n;
    while ((n = w.nextNode())) last = n;
    return last;
  }
  function lastRich(root) {
    const all = root.querySelectorAll(RICH);
    for (let i = all.length - 1; i >= 0; i--) { const e = all[i]; if (!e.parentElement.closest(RICH) && e.getBoundingClientRect().height > 4) return e; }
    return null;
  }
  // Where the drop should be, in the reply element's own coordinates, and whether it is the top of a block.
  function aim(s) {
    const host = s.turn.el.getBoundingClientRect();
    const md = s.turn.mdEl;
    const tn = lastTextNode(md), rich = lastRich(md);
    const richAfter = rich && (!tn || (tn.compareDocumentPosition(rich) & Node.DOCUMENT_POSITION_FOLLOWING));
    if (richAfter) {
      const r = rich.getBoundingClientRect();
      return { x: r.left - host.left + 22, y: r.top - host.top - R * .95, card: true, line: null };
    }
    if (!tn) return null;
    const rg = document.createRange();
    rg.selectNodeContents(tn);
    const rects = (typeof rg.getClientRects === "function" ? [...rg.getClientRects()] : []).filter(r => r.width > 0 || r.height > 0);
    const r = rects[rects.length - 1];
    if (!r) return null;
    return { x: r.right - host.left + GAP, y: r.top - host.top + r.height / 2, card: false,
      line: { right: r.right - host.left, top: r.top - host.top, bottom: r.bottom - host.top } };
  }
  function homeOf(s) {
    const mk = s.turn.el.querySelector(".who .mk");
    if (!mk) return { x: 7, y: 9 };
    const host = s.turn.el.getBoundingClientRect(), r = mk.getBoundingClientRect();
    return { x: r.left - host.left + r.width / 2, y: r.top - host.top + r.height / 2 };
  }
  const whoEmblem = el => el && el.querySelector(".who .mk blaze-emblem");

  // ---------- overlays ----------
  function makeOverlay(turn) {
    turn.el.classList.add("streaming-host");
    const drop = document.createElement("canvas");
    drop.className = "sdrop"; drop.setAttribute("aria-hidden", "true");
    drop.width = drop.height = Math.round(BOX * dpr());
    drop.style.width = drop.style.height = BOX + "px";
    const shim = document.createElement("canvas");
    shim.className = "sshim"; shim.setAttribute("aria-hidden", "true");
    turn.el.append(shim, drop);
    return { drop, shim };
  }

  function start(turn) {
    if (S && S.turn === turn) return S;
    if (S) finishNow(S);
    const o = makeOverlay(turn);
    S = { turn, ...o, pos: null, vel: { x: 0, y: 0 }, target: null, card: false, stretch: 1, splash: 1,
      fall: null, squash: null, glow: 0, sweep: -1, homing: null, live: true, line: null, t: 0, alpha: 1 };
    // the drop leaves the halo: the label keeps only the ring while the drop travels
    restNewest(turn.el, true);
    kick();
    return S;
  }

  function finishNow(s) {
    s.drop.remove(); s.shim.remove(); s.turn.el.classList.remove("streaming-host");
    if (S === s) S = null;
  }

  // ---------- the frame ----------
  function frame(ts) {
    raf = 0;
    const s = S; if (!s) return;
    if (!s.turn.el.isConnected) { finishNow(s); return; }
    const now = performance.now();
    const dt = Math.min(.05, s.last ? (now - s.last) / 1000 : 1 / 60); s.last = now;
    const mo = motion();
    if (!frozen) s.t = D().now();

    // where to go
    if (s.live) {
      const em = whoEmblem(s.turn.el);   // the label mark can attach after the first delta
      if (em && (!em.hasAttribute("halo-only") || em.hasAttribute("dim"))) restNewest(s.turn.el, true);
      const a = aim(s);
      if (a) {
        if (!s.pos) {
          const h = homeOf(s);
          s.pos = mo ? { x: h.x, y: h.y } : { x: a.x, y: a.y };
        }
        const prev = s.target;
        s.target = a; s.line = a.line;
        if (mo && prev && a.y - s.pos.y > FALL_PX && !s.fall) {
          s.fall = { from: { ...s.pos }, to: { x: a.x, y: a.y }, t0: now, ms: fallMs(a.y - s.pos.y), card: a.card };
        } else if (s.fall) { s.fall.to = { x: a.x, y: a.y }; }
      }
    }
    // move
    if (s.pos && s.target) {
      if (!mo) { s.pos = { x: s.target.x, y: s.target.y }; s.vel = { x: 0, y: 0 }; s.stretch = 1; }
      else if (s.fall) {
        const p = Math.min(1, (now - s.fall.t0) / s.fall.ms), e = p * p;   // gravity: quadratic ease-in
        s.pos = { x: s.fall.from.x + (s.fall.to.x - s.fall.from.x) * e, y: s.fall.from.y + (s.fall.to.y - s.fall.from.y) * e };
        s.stretch = 1 + .22 * Math.min(1, p * 2);
        if (p >= 1) {
          s.squash = { t0: now }; s.vel = { x: 0, y: 0 };
          if (s.fall.card) s.splash = 0;
          s.fall = null;
        }
      } else {
        // spring (damping ≈ .8, medium-low stiffness), moved by transform only
        const k = 120, c = 2 * Math.sqrt(k) * .8;
        for (const ax of ["x", "y"]) {
          const f = (s.target[ax] - s.pos[ax]) * k - s.vel[ax] * c;
          s.vel[ax] += f * dt; s.pos[ax] += s.vel[ax] * dt;
        }
      }
      if (s.squash) {
        // landing: squash to .72 then a bouncy spring back to 1
        const q = (now - s.squash.t0) / 1000;
        s.stretch = 1 - .28 * Math.exp(-q * 9) * Math.cos(q * 26);
        if (q > .7) { s.stretch = 1; s.squash = null; }
      }
      if (s.splash < 1) s.splash = Math.min(1, s.splash + dt / .56);
    }
    // homing: glide to the halo, then hand over to the resting pair
    if (s.homing) {
      const p = mo ? Math.min(1, (now - s.homing.t0) / s.homing.ms) : 1;
      const e = easeStd(p);
      if (s.pos && mo) s.pos = { x: s.homing.from.x + (s.homing.to.x - s.homing.from.x) * e, y: s.homing.from.y + (s.homing.to.y - s.homing.from.y) * e };
      s.alpha = mo ? 1 : Math.max(0, 1 - (now - s.homing.t0) / 220);
      if ((mo && p >= 1) || (!mo && s.alpha <= 0)) {
        s.pos = null; // the drop is home
        restNewest(s.turn.el, false);
      }
    }
    // shimmer: glow while live, one sweep after
    if (mo) {
      if (s.live) s.glow = Math.min(1, s.glow + dt / .24);
      else s.glow = Math.max(0, s.glow - dt / .7);
      if (s.sweep >= 0) { s.sweep += dt * 1000 / 1200; if (s.sweep > 1) s.sweep = -2; }
    } else { s.glow = 0; s.sweep = -2; }

    drawDrop(s);
    drawShimmer(s);

    const busy = s.live || s.pos || s.glow > .01 || (s.sweep >= 0 && s.sweep <= 1);
    if (!busy) { finishNow(s); return; }
    // reduced motion: a still drop needs no frames until the next delta, scroll or resize
    if (!frozen && !document.hidden && (mo || !s.live)) raf = requestAnimationFrame(frame);
  }
  const kick = () => { if (!raf && S && !frozen) raf = requestAnimationFrame(frame); };

  function drawDrop(s) {
    const cv = s.drop, ctx = cv.getContext("2d"), k = dpr();
    if (!ctx) return;
    if (!s.pos) { cv.style.opacity = "0"; return; }
    cv.style.opacity = "1";
    const half = BOX / 2;
    cv.style.transform = `translate3d(${(s.pos.x - half).toFixed(2)}px, ${(s.pos.y - half).toFixed(2)}px, 0)`;
    ctx.setTransform(k, 0, 0, k, 0, 0);
    ctx.clearRect(0, 0, BOX, BOX);
    const mo = motion();
    if (D().isLight()) {
      // the same small dark lens the emblem uses on light grounds, so the white glass reads
      const g = ctx.createRadialGradient(half, half, 0, half, half, R * 1.55);
      g.addColorStop(0, `rgba(44,44,50,${.9 * s.alpha})`); g.addColorStop(.7, `rgba(20,20,24,${.85 * s.alpha})`); g.addColorStop(1, "rgba(20,20,24,0)");
      ctx.save(); ctx.translate(half, half); ctx.scale(1 / Math.sqrt(s.stretch), s.stretch); ctx.translate(-half, -half);
      ctx.fillStyle = g; ctx.beginPath(); ctx.arc(half, half, R * 1.55, 0, Math.PI * 2); ctx.fill(); ctx.restore();
    }
    D().drawSplash(ctx, half, half, R, s.splash);
    D().drawWaterOrb(ctx, half, half, R, mo ? s.t : 0, s.stretch, s.alpha);
  }

  function drawShimmer(s) {
    const cv = s.shim, md = s.turn.mdEl;
    const g = s.glow, sw = s.sweep;
    const on = g > .01 || (sw >= 0 && sw <= 1);
    if (!on) { if (cv.width) { cv.width = 0; cv.height = 0; } cv.style.display = "none"; return; }
    cv.style.display = "";
    const host = s.turn.el.getBoundingClientRect(), r = md.getBoundingClientRect();
    const k = dpr();
    // only the part of the reply on screen (long replies stay cheap)
    const top = Math.max(r.top, 0) - host.top, bottom = Math.min(r.bottom, innerHeight) - host.top;
    const w = Math.ceil(r.width), hgt = Math.max(0, Math.ceil(bottom - top));
    const left = r.left - host.left;
    if (cv.width !== Math.round(w * k) || cv.height !== Math.round(hgt * k)) { cv.width = Math.round(w * k); cv.height = Math.round(hgt * k); }
    cv.style.width = w + "px"; cv.style.height = hgt + "px";
    cv.style.transform = `translate3d(${left.toFixed(1)}px, ${top.toFixed(1)}px, 0)`;
    const b = blueFor();
    cv.style.mixBlendMode = b.blend;
    const ctx = cv.getContext("2d");
    if (!ctx) return;
    ctx.setTransform(k, 0, 0, k, -left * k, -top * k);   // draw in reply coordinates
    ctx.clearRect(left, top, w, hgt);
    if (g > .01 && s.line) {
      const L = s.line, cx = L.right, cy = (L.top + L.bottom) / 2;
      const rg = ctx.createRadialGradient(cx, cy, 0, cx, cy, REACH);
      rg.addColorStop(0, `rgba(${b.main},${.95 * g})`); rg.addColorStop(.5, `rgba(${b.main},${.55 * g})`); rg.addColorStop(1, `rgba(${b.main},0)`);
      ctx.fillStyle = rg; ctx.fillRect(cx - REACH, cy - REACH, REACH * 2, REACH * 2);
      // a glint that runs back over the last line, again and again
      const bx = cx - REACH * ((s.t * .85) % 1);
      const lg = ctx.createLinearGradient(bx - BAND, 0, bx + BAND, 0);
      lg.addColorStop(0, `rgba(${b.light},0)`); lg.addColorStop(.5, `rgba(${b.light},${.85 * g})`); lg.addColorStop(1, `rgba(${b.light},0)`);
      ctx.fillStyle = lg; ctx.fillRect(Math.max(left, bx - BAND), L.top, BAND * 2, L.bottom - L.top);
    }
    if (sw >= 0 && sw <= 1) {
      // the closing sweep: a diagonal band crossing the block, fading as it goes
      const e = easeStd(sw);
      const W = r.width, H = r.height, top0 = r.top - host.top;
      const x = left - W * .35 + W * 1.7 * e;
      const a = (1 - e) * (e < .1 ? e / .1 : 1);
      const lg = ctx.createLinearGradient(x - SWEEP_HALF, top0, x + SWEEP_HALF, top0 + Math.min(H, SWEEP_HALF));
      lg.addColorStop(0, `rgba(${b.main},0)`); lg.addColorStop(.25, `rgba(${b.main},${.9 * a})`); lg.addColorStop(.5, `rgba(${b.light},${.9 * a})`);
      lg.addColorStop(.75, `rgba(${b.main},${.9 * a})`); lg.addColorStop(1, `rgba(${b.main},0)`);
      ctx.fillStyle = lg; ctx.fillRect(left, top, w, hgt);
    }
  }

  // ---------- the resting pair: beside the newest reply only ----------
  // traveling = the newest reply's drop is out riding the text, so its label shows only the halo.
  function restNewest(newest, traveling) {
    const all = [...document.querySelectorAll("#log .msg.blaze")];
    const last = newest || all[all.length - 1];
    all.forEach(el => {
      const em = whoEmblem(el); if (!em) return;
      // awake (a turn is running, e.g. tools before any text): the pair stays bright; idle: it rests dimmed
      const awake = typeof busy !== "undefined" && busy;
      if (el === last) { em.toggleAttribute("halo-only", !!traveling); em.toggleAttribute("dim", !traveling && !awake); }
      else { em.setAttribute("halo-only", ""); em.removeAttribute("dim"); }
    });
  }
  let restQueued = 0;
  if (typeof setBusy === "function") { const ob = setBusy; setBusy = function () { ob.apply(this, arguments); queueRest(); }; }
  const queueRest = () => { if (restQueued || (S && S.live)) return; restQueued = requestAnimationFrame(() => { restQueued = 0; if (!(S && S.live)) restNewest(null, false); }); };
  new MutationObserver(ms => { if (ms.some(m => m.target.nodeType === 1 && (m.target.id === "log" || m.target.classList.contains("col") || m.target.classList.contains("mk")))) queueRest(); })
    .observe(document.body, { childList: true, subtree: true });

  // ---------- events (registered after app.js, so the text is already rendered) ----------
  on("delta", () => {
    if (typeof current === "undefined" || !current || !current.el) return;
    const s = start(current);
    s.live = true; s.homing = null; s.alpha = 1;
    kick();
  });
  function end() {
    const s = S; if (!s || !s.live) return;
    s.live = false;
    s.sweep = motion() ? 0 : -2;
    const to = homeOf(s);
    s.fall = null; s.squash = null;
    s.homing = { from: s.pos ? { ...s.pos } : to, to, t0: performance.now(), ms: 700 };
    if (!s.pos) restNewest(s.turn.el, false);
    kick();
  }
  on("done", end); on("error", end); on("stopped", end);
  // A tool call mid-reply: the text pauses, the drop stays where it is (the work card shows progress).
  window.addEventListener("resize", kick);
  window.addEventListener("blazemotion", kick);
  document.addEventListener("visibilitychange", kick);
  document.addEventListener("scroll", kick, true);

  window.StreamOrb = {
    // shots: run the loop for ms then hold that frame
    freeze(ms = 0) { frozen = false; kick(); return new Promise(r => setTimeout(() => { frozen = true; if (raf) cancelAnimationFrame(raf); raf = 0; r(S ? { pos: S.pos, glow: S.glow, card: S.target && S.target.card } : null); }, ms)); },
    thaw() { frozen = false; kick(); },
    state: () => S && { live: S.live, pos: S.pos, target: S.target, glow: S.glow, sweep: S.sweep },
    restNewest,
  };
})();
