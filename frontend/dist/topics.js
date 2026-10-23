// v0.19 topic-aware chat: a subtle "Topic · Pizza" chip above the composer. Blaze sends
// the model only the active topic; the chip shows which one and lists the others.
// Picking one starts the message with "Back to the <topic>: " so Blaze switches back.
// Self-mounting (no app.js edits): it finds #composer, follows the "topic" event, and
// refreshes when a chat loads or a turn ends.
(() => {
  const api = () => (window.go && window.go.main && window.go.main.App) || {};
  const esc = s => String(s ?? "").replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
  let view = { current: "", id: 0, topics: [] };
  let open = false;

  function row() {
    const wrap = document.querySelector("#composer .cwrap");
    if (!wrap) return null;
    let r = wrap.querySelector(".topicrow");
    if (!r) {
      r = document.createElement("div");
      r.className = "topicrow";
      r.innerHTML = `<button class="topicchip" type="button" aria-haspopup="true" title="Blaze answers with this topic's context only"><span class="tk">Topic</span><span class="tt"></span><span class="tn"></span></button><div class="topicpop" role="menu" hidden></div>`;
      wrap.insertBefore(r, wrap.querySelector(".inbox") || wrap.firstChild);
      r.querySelector(".topicchip").onclick = e => { e.stopPropagation(); toggle(!open); };
    }
    return r;
  }

  function toggle(on) {
    const r = row(); if (!r) return;
    open = on && view.topics.length > 1;
    const pop = r.querySelector(".topicpop");
    pop.hidden = !open;
    r.querySelector(".topicchip").classList.toggle("on", open);
    if (!open) return;
    pop.innerHTML = `<div class="th">Topics in this chat</div>` + view.topics.map(t =>
      `<button class="topt${t.active ? " cur" : ""}" type="button" data-t="${esc(t.title)}"><span class="d"></span><span class="n">${esc(t.title)}</span><span class="c">${t.turns}</span></button>`).join("");
    pop.querySelectorAll(".topt").forEach(b => b.onclick = e => {
      e.stopPropagation();
      toggle(false);
      if (b.classList.contains("cur")) return;
      const input = document.getElementById("input");
      if (!input) return;
      input.value = `Back to the ${b.dataset.t}: ` + input.value.replace(/^Back to the [^:]*:\s*/, "");
      input.focus();
      input.dispatchEvent(new Event("input"));
    });
  }

  function draw() {
    const r = row(); if (!r) return;
    const show = !!view.current;
    r.classList.toggle("show", show);
    r.querySelector(".tt").textContent = view.current || "";
    const others = Math.max(0, (view.topics || []).length - 1);
    r.querySelector(".tn").textContent = others ? `+${others}` : "";
    if (!show) toggle(false);
  }

  function take(v) { view = v || view; if (!view.topics) view.topics = []; draw(); }

  async function refresh() {
    try { if (api().ChatTopics) take(await api().ChatTopics()); else draw(); } catch (_) { draw(); /* older backend */ }
  }
  window.BlazeTopics = { refresh, set: take };

  function hook() {
    const rt = window.runtime;
    if (rt && rt.EventsOn) {
      rt.EventsOn("topic", take);
      rt.EventsOn("done", () => setTimeout(refresh, 50));
    }
    // A chat loads: show its topics (wraps the global loadConv; app.js is untouched).
    if (typeof window.loadConv === "function" && !window.loadConv.__topics) {
      const orig = window.loadConv;
      window.loadConv = function (...a) { const r = orig.apply(this, a); setTimeout(refresh, 0); return r; };
      window.loadConv.__topics = true;
    }
    document.addEventListener("click", () => open && toggle(false));
    document.addEventListener("keydown", e => { if (e.key === "Escape" && open) toggle(false); });
    // The chat pane moves between Work and Design (and may be rebuilt): keep the chip in it.
    new MutationObserver(() => { const w = document.querySelector("#composer .cwrap"); if (w && !w.querySelector(".topicrow")) draw(); })
      .observe(document.body, { childList: true, subtree: true });
    refresh();
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", hook); else hook();
})();
