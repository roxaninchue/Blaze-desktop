// v0.19.14 just-in-time permissions. Setup asks for nothing; Blaze asks for a folder or for
// notifications the first time a task needs one, with a one-line reason, and a "no" gets an
// honest fallback line. Risky steps already ask on their confirm card; this adds the card's
// one-line reason and says plainly that a declined step wasn't done. Go side: permissions.go.
// It wraps submit() and listens to events; it doesn't touch how chat messages render.
const Perms = (() => {
  IC.folder = IC.folder || '<path d="M1.5 3.5h5l1.5 1.5h6.5v8h-13z"/>';
  IC.bell = IC.bell || '<path d="M4 11.5V7a4 4 0 0 1 8 0v4.5l1 1H3zM6.5 14h3"/>';
  const asked = new Set();   // kinds asked this session (an ask never repeats in one session)
  let pending = null, skip = false;

  // The ask card: icon, one-line reason, yes / not now. Resolves true when allowed.
  function card(slot, p) {
    return new Promise(resolve => {
      const c = h("div", "permask", `<span class="pmi">${icon(p.kind === "notify" ? "bell" : "folder", "i sm")}</span><div class="pmt">${esc(p.reason)}</div>
        <div class="pmb"><button class="btn accent xs yes">${esc(p.allow || "Allow")}</button><button class="btn ghost xs no">${esc(p.deny || "Not now")}</button></div>`);
      c.setAttribute("role", "group"); c.setAttribute("aria-label", "Blaze is asking");
      const answer = async allow => {
        $$("button", c).forEach(b => b.disabled = true);
        let r = null;
        try { r = await API().PermAnswer(p.kind, allow); } catch (e) { r = null; }
        if (r && r.folder && S && S.prefs) { S.prefs.folder = r.folder; if (typeof setFolder === "function") setFolder(r.folder); }
        const ok = !!(r && r.allowed);
        line(c, (r && r.line) || (allow ? "" : p.fallback), ok);
        resolve(ok);
      };
      $(".yes", c).onclick = () => answer(true);
      $(".no", c).onclick = () => answer(false);
      (slot || document.body).append(c);
      if (typeof scrollDown === "function" && slot && slot.closest && slot.closest("#log")) scrollDown(true);
      $(".yes", c).focus({ preventScroll: true });
    });
  }

  // After an answer: one plain line in the card's place, gone after a while.
  function line(c, text, ok) {
    if (!text) { c.remove(); return; }
    c.className = "permline" + (ok ? " ok" : "");
    c.innerHTML = `<span class="pmi">${icon(ok ? "check" : "x", "i sm")}</span><div class="pmt">${esc(text)}</div>`;
    setTimeout(() => { c.classList.add("out"); setTimeout(() => c.remove(), 400); }, ok ? 5000 : 9000);
  }

  // gate(kind, text, slot): ask if this task needs it and it wasn't settled; true = go ahead.
  // A "no" still goes ahead: the task runs with the honest fallback.
  async function gate(kind, text, slot) {
    if (asked.has(kind) || !API().PermAsk) return true;
    let p = null;
    try { p = await API().PermAsk(kind, text || ""); } catch (e) { return true; }
    if (!p || !p.ask) { if (kind === "notify") asked.add(kind); return true; } // settled: stop polling
    asked.add(kind);
    pending = card(slot, p);
    try { return await pending; } finally { pending = null; }
  }

  const logSlot = () => (typeof pane !== "undefined" && pane && $("#log .col", pane)) || null;

  // Chat: a message that needs a folder asks for one first (once), then sends as typed.
  if (typeof submit === "function") {
    const _submit = submit;
    submit = async function (...args) {
      const i = $("#input"), t = i ? i.value.trim() : "";
      if (skip) { skip = false; return _submit.apply(this, args); }
      if (pending) return;  // an ask is on screen: answer it first
      if (t && !t.startsWith("/") && !(typeof busy !== "undefined" && busy)) await gate("folder", t, logSlot());
      return _submit.apply(this, args);
    };
  }

  // Notifications: asked at the first task that runs long (20 s), or when a job is scheduled.
  // v0.19.14 background runs: an acked task frees the composer at once, so "runs long" also
  // means a background run still working 20 s after its ack (no final yet). One in-app ask,
  // never a desktop notification and never a status line.
  let since = 0, ackAt = 0;
  if (window.runtime) {
    on("done", d => { if (d && d.ack && !ackAt) ackAt = Date.now(); });
    on("blaze_msg", () => { ackAt = 0; });
  }
  setInterval(() => {
    const now = Date.now(), b = typeof busy !== "undefined" && busy;
    since = b ? (since || now) : 0;
    const long = (since && now - since > 20000) || (ackAt && now - ackAt > 20000);
    if (long && !asked.has("notify") && !pending) gate("notify", "", logSlot());
  }, 2000);

  if (window.runtime) on("confirm", c => {
    // Runs after app.js's handler drew the card: add the one-line reason, and make a decline honest.
    setTimeout(() => {
      const cards = $$(".card.confirm:not([data-why])", logSlot() || document);
      const el = cards[cards.length - 1]; if (!el) return;
      el.dataset.why = "1";
      if (c.why) { const d = $(".detail", el); (d || $(".title", el)).after(h("div", "permwhy", esc(c.why))); }
      const no = $(".no", el);
      if (no && c.declined) no.addEventListener("click", () => { const l = $(".actions .label", el); if (l) l.textContent = "Declined · " + c.declined; });
      if (/^schedule/i.test(c.title || "") && !asked.has("notify")) { const yes = $(".yes", el); if (yes) yes.addEventListener("click", () => setTimeout(() => gate("notify", "", logSlot()), 300)); }
    }, 0);
  });

  return { gate, skipNext: () => { skip = true; }, get asked() { return [...asked]; } };
})();
window.Perms = Perms;
