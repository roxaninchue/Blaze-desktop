// v0.17.1 messaging-style thread (port of Blaze-agent v0.24.0 ui/Bubbles.kt).
// Consecutive messages from one side form a group (a pause of 10 minutes or anything else in
// the thread, such as an agent card, starts a new one). Each message gets g-single / g-first /
// g-mid / g-last, which picks its corners in chat.css; the last one carries the group's meta
// line: one quiet mono timestamp, then Copy · Quote · Retry. Pure helpers (positions, time)
// are unit-tested in ci/design.test.js. Loaded before app.js; uses its helpers at call time.
const Bubbles = (() => {
  const GAP_MS = 10 * 60000;

  // Group positions for sides (strings, null = not a message) and times (0 = unknown, never splits).
  function positions(sides, ats, gap = GAP_MS) {
    const n = sides.length;
    const joins = i => {
      if (i <= 0 || i >= n || !sides[i] || sides[i] !== sides[i - 1]) return false;
      const a = ats[i - 1] || 0, b = ats[i] || 0;
      return a <= 0 || b <= 0 || b - a < gap;
    };
    return sides.map((s, i) => {
      if (!s) return "";
      const p = joins(i), nx = joins(i + 1);
      return !p && !nx ? "single" : !p ? "first" : nx ? "mid" : "last";
    });
  }

  // "14:02" today, "Yesterday 14:02", "Mon 14:02" this week, else "12 Oct 14:02". "" when unknown.
  function time(at, now = Date.now()) {
    if (!at || at <= 0) return "";
    const d = new Date(at), n = new Date(now);
    const hm = String(d.getHours()).padStart(2, "0") + ":" + String(d.getMinutes()).padStart(2, "0");
    const day = x => Math.floor(new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime() / 864e5);
    const days = day(n) - day(d);
    if (days <= 0) return hm;
    if (days === 1) return "Yesterday " + hm;
    if (days < 7) return ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"][d.getDay()] + " " + hm;
    return d.getDate() + " " + ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"][d.getMonth()] + " " + hm;
  }

  const sideOf = el => !el.classList || !el.classList.contains("msg") ? null : el.classList.contains("user") ? "me" : el.classList.contains("blaze") ? "blaze" : null;
  const textOf = el => el.__turn ? MD.splitFollowups(el.__turn.text || "", true).text : (el.querySelector(".bubble") || el).innerText || (el.querySelector(".bubble") || el).textContent || "";

  function quote(text) {
    const i = document.querySelector("#input"); if (!i) return;
    const q = String(text || "").replace(/\s+/g, " ").trim();
    const short = q.length > 160 ? q.slice(0, 159) + "…" : q;
    i.value = "> " + short + "\n\n" + i.value.replace(/^> .*\n\n/, "");
    i.focus(); i.setSelectionRange(i.value.length, i.value.length);
    i.dispatchEvent(new Event("input"));
  }

  function gmFor(el, side, isLastBlaze) {
    const gm = document.createElement("span"); gm.className = "gm";
    const t = time(+el.dataset.at || 0);
    const parts = [];
    if (t) parts.push(`<span class="gt">${t}</span>`);
    const acts = [["copy", "Copy"]];
    if (side === "blaze") { acts.push(["quote", "Quote"]); if (isLastBlaze) acts.push(["retry", "Retry"]); }
    acts.forEach(([k, l]) => parts.push(`<button class="ga" data-a="${k}" title="${l}">${l}</button>`));
    gm.innerHTML = parts.join('<span class="gd"></span>');
    gm.dataset.key = t + "|" + acts.map(a => a[0]).join(",");
    return gm;
  }

  // Walk the thread and set group classes and the closing meta line. Idempotent.
  function regroup(col) {
    if (!col) return;
    const kids = [...col.children];
    const sides = kids.map(sideOf);
    const pos = positions(sides, kids.map(k => +((k.dataset && k.dataset.at) || 0)));
    const blazes = kids.filter((k, i) => sides[i] === "blaze");
    const lastBlaze = blazes[blazes.length - 1];
    kids.forEach((el, i) => {
      if (!sides[i]) return;
      const want = "g-" + pos[i];
      ["g-single", "g-first", "g-mid", "g-last"].forEach(c => { if (c !== want && el.classList.contains(c)) el.classList.remove(c); });
      if (!el.classList.contains(want)) el.classList.add(want);
      const newest = el === lastBlaze;
      if (el.classList.contains("newest") !== newest) el.classList.toggle("newest", newest);
      const ends = pos[i] === "single" || pos[i] === "last";
      const metas = [...el.children].filter(c => c.classList.contains("meta"));
      let gm = el.querySelector(":scope > .meta > .gm");
      if (!ends) {
        if (gm) gm.remove();
        metas.filter(m => m.classList.contains("gmeta") && !m.children.length).forEach(m => m.remove());
        return;
      }
      const fresh = gmFor(el, sides[i], newest);
      // Prefer the last real meta (route, Stopped); otherwise our own line at the end.
      let target = metas.filter(m => !m.classList.contains("gmeta")).pop();
      if (!target) {
        target = metas.find(m => m.classList.contains("gmeta"));
        if (!target) { target = document.createElement("div"); target.className = "meta gmeta"; el.append(target); }
        else if (el.lastElementChild !== target && !el.lastElementChild.classList.contains("fups")) el.append(target);
      }
      if (!gm || gm.dataset.key !== fresh.dataset.key || gm.parentElement !== target) { if (gm) gm.remove(); target.prepend(fresh); }
      metas.filter(m => m !== target && m.classList.contains("gmeta")).forEach(m => m.remove());
      const fu = el.querySelector(":scope > .fups"); if (fu && fu !== el.lastElementChild) el.append(fu);
    });
  }

  let mo = null, watched = null;
  function watch(col) {
    if (typeof MutationObserver !== "function" || !col) return;
    if (!mo) mo = new MutationObserver(() => { if (!watched) return; mo.disconnect(); try { regroup(watched); } finally { mo.observe(watched, { childList: true, subtree: true }); } });
    if (watched === col) return;
    mo.disconnect(); watched = col; regroup(col);
    mo.observe(col, { childList: true, subtree: true });
  }

  // Follow-up pills under an answer (only the newest shows them, and never while busy).
  function followups(turn, list) {
    if (!turn || !turn.el) return;
    let box = turn.el.querySelector(":scope > .fups");
    if (!list || !list.length) { if (box) box.remove(); return; }
    if (!box) { box = document.createElement("div"); box.className = "fups"; turn.el.append(box); }
    const key = list.join("|");
    if (box.dataset.key === key) return;
    box.dataset.key = key; box.innerHTML = "";
    list.forEach(t => {
      const b = document.createElement("button"); b.className = "fup"; b.textContent = t;
      b.onclick = () => { const i = document.querySelector("#input"); if (!i) return; i.value = t; if (typeof submit === "function") submit(); };
      box.append(b);
    });
  }

  // The round jump-to-newest button.
  function attach(p) {
    const log = p.querySelector("#log"); if (!log || p.querySelector(".jump")) return;
    const j = document.createElement("button"); j.className = "jump"; j.title = "Jump to the newest message"; j.setAttribute("aria-label", "Jump to the newest message");
    j.innerHTML = '<svg class="i" viewBox="0 0 16 16"><path d="M8 3v10M3.5 8.5 8 13l4.5-4.5"/></svg>';
    j.onclick = () => { log.scrollTo ? log.scrollTo({ top: log.scrollHeight, behavior: "smooth" }) : (log.scrollTop = log.scrollHeight); };
    p.append(j);
    const upd = () => j.classList.toggle("on", log.scrollHeight - log.scrollTop - log.clientHeight > 320);
    log.addEventListener("scroll", upd, { passive: true });
    const col = log.querySelector(".col"); watch(col);
  }

  // Thread actions and the picture deck, by delegation.
  document.addEventListener("click", e => {
    const a = e.target.closest && e.target.closest(".gm .ga");
    if (a) {
      const el = a.closest(".msg"), k = a.dataset.a;
      if (k === "copy") { try { navigator.clipboard.writeText(textOf(el)); } catch (x) {} a.textContent = "Copied"; setTimeout(() => a.textContent = "Copy", 1200); }
      if (k === "quote") quote(textOf(el));
      if (k === "retry" && typeof retry === "function") retry();
      return;
    }
    const nav = e.target.closest && e.target.closest(".deck .dnav");
    if (nav) deckMove(nav.closest(".deck"), nav.classList.contains("next") ? 1 : -1);
    const src = e.target.closest && e.target.closest(".code.diagram .dsrc");
    if (src) { const c = src.closest(".code"); src.textContent = c.classList.toggle("src") ? "Picture" : "Source"; }
  });

  function deckMove(deck, step) {
    const cards = [...deck.querySelectorAll(".dcard")], n = cards.length;
    const i = ((+deck.dataset.i || 0) + step + n) % n;
    deck.dataset.i = i;
    cards.forEach((c, k) => { c.classList.remove("flip"); c.dataset.o = Math.min((k - i + n) % n, 3); });
    const cnt = deck.querySelector(".dcount"); if (cnt) cnt.textContent = (i + 1) + " / " + n;
  }

  return { positions, time, regroup, watch, attach, followups, deckMove, GAP_MS };
})();
window.Bubbles = Bubbles;
