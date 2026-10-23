// 0.19.16 working pill + delivery status (the UI half; the logic is runEvents.js).
//
// Working pill: one subtle line under the pending Blaze reply with what Blaze is doing right now
// ("Reading your CSV", "Searching the web", "Bringing in a specialist"), replaced in place as the
// work moves. Nothing for the first ~2 s; gone when the reply (or the failure) lands. A background
// run (the ack-and-background path) keeps its pill under its "got it" bubble until its final lands.
// Steps never notify and never touch the status line's history.
//
// Delivery status: under the user's latest message only, small dots + Sending → Sent → Read.
// Offline it stays at Sending (Go retries with the same client_msg_id).
(() => {
  const R = window.RunEvents;
  if (!R || typeof on !== "function") return;
  const col = () => document.querySelector("#log .col");
  const now = () => Date.now();

  // ---------- delivery status ----------
  let msg = null, msgEl = null;
  function drawStatus() {
    if (!msgEl || !msg) return;
    let el = msgEl.querySelector(":scope > .dstat");
    if (!el) {
      el = document.createElement("div");
      el.className = "dstat"; el.setAttribute("role", "status"); el.setAttribute("aria-live", "polite");
      el.innerHTML = `<span class="dd" aria-hidden="true"><i></i><i></i><i></i></span><span class="dt"></span>`;
      msgEl.append(el);
    }
    el.dataset.state = msg.state;
    el.classList.toggle("offline", !!msg.offline);
    el.querySelector(".dt").textContent = R.statusText(msg);
    el.title = msg.offline ? "Waiting for a connection. It will send by itself." : "";
  }
  function track(el) {
    document.querySelectorAll("#log .msg.user > .dstat").forEach(x => x.remove()); // latest only
    msgEl = el; msg = R.message(); drawStatus();
  }
  function feed(ev, d) {
    if (msg && msg.apply(ev, d)) drawStatus();
  }

  // ---------- working pill ----------
  // host: the element the pill sits under; for the live turn it is current.el, for a background
  // run its ack bubble. One pill per host.
  const pills = new Map(); // key (turn | run_id) -> { host, p, el, timer }
  function pillFor(key, host, started) {
    let x = pills.get(key);
    if (!x) { x = { host, p: R.pill(started || now()), el: null, timer: 0 }; pills.set(key, x); }
    if (host) x.host = host;
    return x;
  }
  function paint(x) {
    if (!x.host || !x.host.isConnected || !x.p.text) return;
    const wait = R.PILL_DELAY_MS - (now() - x.p.started);
    if (wait > 0) { clearTimeout(x.timer); x.timer = setTimeout(() => paint(x), wait + 20); return; }
    if (!x.el) {
      x.el = document.createElement("div");
      x.el.className = "wpill"; x.el.setAttribute("role", "status"); x.el.setAttribute("aria-live", "polite");
      x.el.innerHTML = `<span class="wdot" aria-hidden="true"></span><span class="wt"></span>`;
      x.host.append(x.el);
    }
    x.host.classList.add("haspill"); // the live "Working" line drops its copy of the step (CSS)
    const t = x.el.querySelector(".wt");
    if (t.textContent !== x.p.text) { t.textContent = x.p.text; x.el.classList.remove("swap"); void x.el.offsetWidth; x.el.classList.add("swap"); }
  }
  function unpaint(x) {
    if (x.el) x.el.remove(); x.el = null;
    if (x.host) x.host.classList.remove("haspill");
  }
  function drop(key) {
    const x = pills.get(key); if (!x) return;
    clearTimeout(x.timer); unpaint(x); pills.delete(key);
  }
  function step(key, host, text, started) {
    if (!text) return;
    const x = pillFor(key, host, started);
    if (x.p.set(text)) paint(x);
  }

  // ---------- wiring ----------
  let turnKey = null, turnStarted = 0, request = ""; // request: the message this turn answers
  const liveTurn = () => (typeof current !== "undefined" && current) ? current : null;
  const startTurn = () => { if (turnKey) drop(turnKey); turnKey = "t" + now(); turnStarted = now(); };

  if (typeof sendText === "function") {
    const orig = sendText;
    sendText = async function (text, files, att) {
      startTurn(); request = String(text || "");
      const p = orig.apply(this, arguments);
      const c = col(); const last = c && [...c.querySelectorAll(".msg.user")].pop();
      if (last && !last.classList.contains("cont")) track(last);
      return p;
    };
  }
  on("turn", t => { if (!t || t.kind !== "agent") { if (!turnKey) startTurn(); } });

  let lastRun = null;                 // the run the latest message started (ack bubble host)
  const runHost = {};                 // run_id -> ack bubble
  on("run_event", d => {
    if (!d) return;
    if (d.conv && typeof convId !== "undefined" && convId && d.conv !== convId) return;
    feed("run_event", d);
    if (d.type === "message_received" && d.run_id) lastRun = d.run_id;
    const text = R.stepOf("run_event", d, request);
    if (!text) return;
    const t = liveTurn();
    if (d.run_id && runHost[d.run_id]) step(d.run_id, runHost[d.run_id], text, turnStarted);
    else if (t && turnKey) step(turnKey, t.el, text, turnStarted);
  });
  ["thinking", "status", "tool", "delta", "route", "confirm"].forEach(ev => on(ev, d => {
    feed(ev, d);
    // The reply's words are the progress now (contract: hide the pill when the reply starts
    // streaming); a later tool step brings it back.
    if (ev === "delta" && turnKey) { const x = pills.get(turnKey); if (x) { unpaint(x); x.p.text = ""; } return; }
    const t = liveTurn(); if (!t || !turnKey) return;
    const text = R.stepOf(ev, d, request);
    if (text) step(turnKey, t.el, text, turnStarted);
  }));
  on("done", d => {
    feed("done", d);
    if (turnKey) {
      // An ack: the run keeps working in the background, its pill moves under the ack bubble.
      if (d && d.ack && lastRun && window.__lastReply) {
        runHost[lastRun] = window.__lastReply;
        const x = pills.get(turnKey);
        if (x) { pills.delete(turnKey); unpaint(x); x.host = window.__lastReply; pills.set(lastRun, x); paint(x); }
      } else drop(turnKey);
      turnKey = null;
    }
  });
  on("error", d => { feed("error", d); if (turnKey) { drop(turnKey); turnKey = null; } });
  on("stopped", () => { if (turnKey) { drop(turnKey); turnKey = null; } });
  // A background final lands: that chat's background pills go.
  on("blaze_msg", m => {
    if (m && m.run_id && pills.has(m.run_id)) { drop(m.run_id); delete runHost[m.run_id]; return; }
    for (const [k, x] of [...pills]) if (k !== turnKey && (!x.host || !x.host.isConnected || !m || !m.conv || typeof convId === "undefined" || m.conv === convId)) drop(k);
  });

  window.WorkPills = { state: () => ({ status: msg && { state: msg.state, offline: msg.offline }, pills: [...pills].map(([k, x]) => ({ key: k, text: x.p.text, shown: !!x.el })) }) };
})();
